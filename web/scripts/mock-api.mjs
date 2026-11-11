#!/usr/bin/env node
// Zero-dependency in-memory mock of the PriceRadar API (docs/API.md) for frontend development.
// Usage: node scripts/mock-api.mjs   (PORT env var overrides 8080)
// MOCK_GOTIFY=1 simulates a configured Gotify (test message succeeds), MOCK_GOTIFY=fail
// a failing one; unset means "not configured" (409).

import { createServer } from 'node:http';

const PORT = Number(process.env.PORT || 8080);
const GOTIFY = process.env.MOCK_GOTIFY ?? '';
const DAY = 86_400_000;
const HOUR = 3_600_000;

const iso = (ms) => new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Deterministic PRNG so the seed data is stable across restarts.
function rng(seed) {
	let s = seed >>> 0;
	return () => {
		s = (s * 1664525 + 1013904223) >>> 0;
		return s / 2 ** 32;
	};
}

let nextProductId = 1;
let nextCheckId = 1;
/** @type {Map<number, any>} */
const products = new Map();
/** @type {Map<number, any[]>} checks per product, ascending by time */
const checks = new Map();
let nextNotificationId = 1;
/** @type {Map<number, any[]>} notifications per product, ascending by time */
const notifications = new Map();

const fmtEuro = (c) => (c / 100).toLocaleString('de-DE', { style: 'currency', currency: 'EUR' });

function addNotification(productId, kind, title, message, priority, at = Date.now()) {
	const status = GOTIFY === '1' ? 'sent' : GOTIFY === 'fail' ? 'failed' : 'skipped';
	const n = {
		id: nextNotificationId++,
		kind,
		title,
		message,
		priority,
		status,
		error: status === 'failed' ? 'gotify: HTTP 401: Unauthorized: you need to provide a valid access token' : null,
		created_at: iso(at),
		sent_at: status === 'sent' ? iso(at + 800) : null
	};
	if (!notifications.has(productId)) notifications.set(productId, []);
	notifications.get(productId).push(n);
	return n;
}

function genHistory(productId, { days, base, volatility, seed, failTail = 0, endPrice }) {
	const rand = rng(seed);
	const now = Date.now();
	const list = [];
	let price = base;
	for (let t = now - days * DAY; t <= now - HOUR; t += 6 * HOUR) {
		// prices change rarely: ~8% of checks
		if (rand() < 0.08) {
			const delta = Math.round((rand() - 0.55) * volatility);
			price = Math.max(Math.round(base * 0.6), Math.min(Math.round(base * 1.3), price + delta));
			price = Math.round(price / 10) * 10 - 1; // ...,99 / ...,49 style
		}
		const success = rand() > 0.03;
		const jitter = Math.floor(rand() * 40 * 60_000);
		list.push({
			id: nextCheckId++,
			checked_at: iso(t + jitter),
			success,
			price_cents: success ? price : null,
			old_price_cents: success && rand() < 0.3 ? Math.round(price * 1.15) : null,
			rrp_cents: null,
			availability: success ? (rand() < 0.9 ? 'in_stock' : 'limited') : null,
			error_kind: success ? null : 'transient',
			error_message: success ? null : 'upstream returned HTTP 503',
			duration_ms: 3000 + Math.floor(rand() * 9000)
		});
	}
	if (endPrice !== undefined) {
		// make the last few successful checks end at a defined price
		const succ = list.filter((c) => c.success);
		for (const c of succ.slice(-6)) c.price_cents = endPrice;
	}
	for (let i = 0; i < failTail; i++) {
		list.push({
			id: nextCheckId++,
			checked_at: iso(now - (failTail - i) * 20 * 60_000),
			success: false,
			price_cents: null,
			old_price_cents: null,
			rrp_cents: null,
			availability: null,
			error_kind: 'layout_changed',
			error_message: 'no Product JSON-LD and no price block',
			duration_ms: 7000 + i * 500
		});
	}
	checks.set(productId, list);
}

function computeProduct(p) {
	const list = checks.get(p.id) ?? [];
	const succ = list.filter((c) => c.success);
	const latest = succ[succ.length - 1] ?? null;
	const current = latest?.price_cents ?? null;
	let previous = null;
	for (let i = succ.length - 1; i >= 0; i--) {
		if (succ[i].price_cents !== current) {
			previous = succ[i].price_cents;
			break;
		}
	}
	const since = Date.now() - 30 * DAY;
	const last30 = succ.filter((c) => Date.parse(c.checked_at) >= since);
	const avg30 = last30.length
		? Math.round(last30.reduce((a, c) => a + c.price_cents, 0) / last30.length)
		: null;
	const prices = succ.map((c) => c.price_cents);
	let consecutive = 0;
	for (let i = list.length - 1; i >= 0 && !list[i].success; i--) consecutive++;
	const lastCheck = list[list.length - 1] ?? null;
	const lastFail = consecutive > 0 ? lastCheck : null;

	return {
		id: p.id,
		shop: 'otto',
		url: p.url,
		sku: p.sku,
		name: p.custom_name ?? p.original_name,
		original_name: p.original_name,
		custom_name: p.custom_name,
		image_url: p.image_url,
		currency: 'EUR',
		target_price_cents: p.target_price_cents,
		target_reached: p.target_price_cents != null && current != null && current <= p.target_price_cents,
		paused: p.paused,
		notify: p.notify,
		created_at: p.created_at,
		last_checked_at: lastCheck?.checked_at ?? null,
		last_success_at: latest?.checked_at ?? null,
		consecutive_failures: consecutive,
		last_error_kind: lastFail?.error_kind ?? null,
		last_error: lastFail?.error_message ?? null,
		stats: {
			current_cents: current,
			previous_cents: previous,
			avg30_cents: avg30,
			min_cents: prices.length ? Math.min(...prices) : null,
			max_cents: prices.length ? Math.max(...prices) : null,
			old_price_cents: latest?.old_price_cents ?? null,
			rrp_cents: latest?.rrp_cents ?? null,
			availability: latest?.availability ?? null
		}
	};
}

function seed() {
	const now = Date.now();
	const mk = (data) => {
		const id = nextProductId++;
		const p = { id, custom_name: null, paused: false, notify: true, ...data };
		products.set(id, p);
		return p;
	};

	// 1) target reached
	const a = mk({
		url: 'https://www.otto.de/p/bambu-lab-3d-drucker-a1-combo-CS00EO0AU/',
		sku: 'S8Z7V9T27LCL',
		original_name: 'Bambu Lab 3D-Drucker A1 Combo',
		image_url: 'https://placehold.co/400x400/png?text=A1+Combo',
		target_price_cents: 45000,
		created_at: iso(now - 120 * DAY)
	});
	genHistory(a.id, { days: 120, base: 52990, volatility: 3000, seed: 7, endPrice: 43999 });
	// ensure a visible "previous" price
	const la = checks.get(a.id).filter((c) => c.success);
	la[la.length - 7].price_cents = 46999;
	la[la.length - 1].rrp_cents = 59900;

	// 2) failing: layout changed x3
	const b = mk({
		url: 'https://www.otto.de/p/philips-kaffeevollautomat-ep2224-40-C1234567/',
		sku: 'P2224AB',
		original_name: 'Philips Kaffeevollautomat 2200 Serie EP2224/40, mit Milchaufschäumer',
		image_url: 'https://invalid.example/broken-image.jpg',
		target_price_cents: null,
		created_at: iso(now - 118 * DAY)
	});
	genHistory(b.id, { days: 118, base: 32999, volatility: 2500, seed: 42, failTail: 3 });

	// 3) paused
	const c = mk({
		url: 'https://www.otto.de/p/samsung-galaxy-tab-s9-fe-C7654321/',
		sku: 'SMX510',
		original_name: 'Samsung Galaxy Tab S9 FE WiFi 128 GB',
		image_url: null,
		custom_name: 'Tablet fürs Wohnzimmer',
		paused: true,
		target_price_cents: 29900,
		created_at: iso(now - 100 * DAY)
	});
	genHistory(c.id, { days: 100, base: 37900, volatility: 2000, seed: 99 });
	c.notify = false;

	addNotification(a.id, 'price_drop', 'Preis gesunken: Bambu Lab 3D-Drucker A1 Combo (−6,4 %)',
		`**Bambu Lab 3D-Drucker A1 Combo**\n\nDer Preis ist gesunken.\n\n- Aktueller Preis: **${fmtEuro(43999)}**\n- Vorheriger Preis: ${fmtEuro(46999)}`,
		6, now - 3 * DAY);
	addNotification(a.id, 'target_reached', 'Zielpreis erreicht: Bambu Lab 3D-Drucker A1 Combo',
		`**Bambu Lab 3D-Drucker A1 Combo**\n\nDein Zielpreis ist erreicht.\n\n- Aktueller Preis: **${fmtEuro(43999)}**\n- Zielpreis: ${fmtEuro(45000)}`,
		8, now - 3 * DAY);
	addNotification(a.id, 'trend_down', 'Preistrend fallend: Bambu Lab 3D-Drucker A1 Combo',
		'**Bambu Lab 3D-Drucker A1 Combo**\n\nÜber die letzten 7 Tage ist der Preis laut Trendlinie um 9,1 % gefallen.',
		4, now - 2 * DAY);
	const fails = notifications.get(a.id);
	fails[fails.length - 1].status = 'failed';
	fails[fails.length - 1].error = 'gotify: HTTP 503: Service Unavailable';
	fails[fails.length - 1].sent_at = null;
	addNotification(b.id, 'scrape_failing', 'Abruf schlägt fehl: Philips Kaffeevollautomat 2200 Serie EP2224/40',
		'**Philips Kaffeevollautomat**\n\nDie letzten 3 Abrufe sind fehlgeschlagen.\n\n- Fehlerart: **Seitenlayout geändert?**',
		5, now - 20 * 60_000);
}

seed();

// ---------------------------------------------------------------------------

function send(res, status, body) {
	if (status === 204) {
		res.writeHead(204);
		res.end();
		return;
	}
	const data = JSON.stringify(body);
	res.writeHead(status, {
		'Content-Type': 'application/json; charset=utf-8',
		'Content-Length': Buffer.byteLength(data)
	});
	res.end(data);
}

const fail = (res, status, code, message) => send(res, status, { error: { code, message } });

async function readJson(req) {
	const chunks = [];
	for await (const c of req) chunks.push(c);
	const raw = Buffer.concat(chunks).toString('utf8');
	if (!raw) return {};
	return JSON.parse(raw);
}

function isCents(v) {
	return Number.isInteger(v) && v >= 0;
}

function doCheck(p) {
	const list = checks.get(p.id);
	const succ = list.filter((c) => c.success);
	const last = succ[succ.length - 1];
	const ok = Math.random() > 0.25;
	let check;
	if (ok) {
		const base = last?.price_cents ?? 9999;
		const change = Math.random() < 0.4 ? Math.round((Math.random() - 0.5) * base * 0.06) : 0;
		check = {
			id: nextCheckId++,
			checked_at: iso(Date.now()),
			success: true,
			price_cents: Math.max(100, base + change),
			old_price_cents: last?.old_price_cents ?? null,
			rrp_cents: last?.rrp_cents ?? null,
			availability: 'in_stock',
			error_kind: null,
			error_message: null,
			duration_ms: 900 + Math.floor(Math.random() * 300)
		};
	} else {
		check = {
			id: nextCheckId++,
			checked_at: iso(Date.now()),
			success: false,
			price_cents: null,
			old_price_cents: null,
			rrp_cents: null,
			availability: null,
			error_kind: 'blocked',
			error_message: 'HTTP 403 – bot protection challenge',
			duration_ms: 1000
		};
	}
	list.push(check);
	return check;
}

const publicCheck = ({ id, checked_at, success, price_cents, error_kind, error_message, duration_ms }) => ({
	id,
	checked_at,
	success,
	price_cents,
	error_kind,
	error_message,
	duration_ms
});

async function handle(req, res) {
	const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);
	const path = url.pathname.replace(/\/+$/, '') || '/';
	const method = req.method ?? 'GET';
	console.log(`${new Date().toISOString()} ${method} ${url.pathname}${url.search}`);

	if (path === '/api/healthz' && method === 'GET') {
		return send(res, 200, { status: 'ok', db: 'ok', fetcher: 'ok', gotify: GOTIFY ? 'configured' : 'not_configured' });
	}

	if (path === '/api/notifications/test' && method === 'POST') {
		await sleep(500);
		if (!GOTIFY) return fail(res, 409, 'not_configured', 'Gotify is not configured (set PR_GOTIFY_URL and PR_GOTIFY_TOKEN)');
		if (GOTIFY === 'fail') return fail(res, 502, 'notify_failed', 'gotify: HTTP 401: Unauthorized: you need to provide a valid access token');
		return send(res, 200, { status: 'sent' });
	}

	if (path === '/api/products') {
		if (method === 'GET') {
			const list = [...products.values()]
				.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id - a.id)
				.map(computeProduct);
			return send(res, 200, list);
		}
		if (method === 'POST') {
			let body;
			try {
				body = await readJson(req);
			} catch {
				return fail(res, 400, 'invalid_request', 'malformed JSON');
			}
			let u;
			try {
				u = new URL(String(body.url ?? ''));
			} catch {
				return fail(res, 400, 'invalid_request', 'invalid URL');
			}
			if (body.target_price_cents !== undefined && body.target_price_cents !== null && !isCents(body.target_price_cents)) {
				return fail(res, 400, 'invalid_request', 'target_price_cents must be a non-negative integer');
			}
			if (!/(^|\.)otto\.de$/.test(u.hostname)) {
				return fail(res, 422, 'unsupported_shop', `no scraper for host ${u.hostname}`);
			}
			const normalized = u.origin + u.pathname;
			if ([...products.values()].some((p) => p.url === normalized)) {
				return fail(res, 409, 'duplicate', `product with url ${normalized} already exists`);
			}
			await sleep(2000);
			if (u.pathname.includes('fail')) {
				return fail(res, 502, 'scrape_failed', 'layout_changed: no Product JSON-LD and no price block');
			}
			const id = nextProductId++;
			const slug = u.pathname.split('/').filter(Boolean).pop() ?? 'produkt';
			const name = slug
				.replace(/-[A-Z0-9]{6,}$/i, '')
				.split('-')
				.map((w) => w.charAt(0).toUpperCase() + w.slice(1))
				.join(' ');
			const p = {
				id,
				url: normalized,
				sku: 'MOCK' + id,
				original_name: name || `Produkt ${id}`,
				custom_name: null,
				image_url: `https://placehold.co/400x400/png?text=Produkt+${id}`,
				target_price_cents: body.target_price_cents ?? null,
				paused: false,
				notify: true,
				created_at: iso(Date.now())
			};
			products.set(id, p);
			const price = 5000 + Math.floor(Math.random() * 50000);
			checks.set(id, [
				{
					id: nextCheckId++,
					checked_at: iso(Date.now()),
					success: true,
					price_cents: price - (price % 100) + 99,
					old_price_cents: null,
					rrp_cents: null,
					availability: 'in_stock',
					error_kind: null,
					error_message: null,
					duration_ms: 2000
				}
			]);
			return send(res, 201, computeProduct(p));
		}
		return fail(res, 405, 'invalid_request', 'method not allowed');
	}

	const m = path.match(/^\/api\/products\/([^/]+)(?:\/(prices|checks|refresh|notifications))?$/);
	if (m) {
		const id = Number(m[1]);
		if (!Number.isInteger(id) || id <= 0) return fail(res, 400, 'invalid_request', `bad id ${m[1]}`);
		const p = products.get(id);
		if (!p) return fail(res, 404, 'not_found', `product ${id} not found`);
		const sub = m[2];

		if (!sub) {
			if (method === 'GET') return send(res, 200, computeProduct(p));
			if (method === 'DELETE') {
				products.delete(id);
				checks.delete(id);
				notifications.delete(id);
				return send(res, 204);
			}
			if (method === 'PATCH') {
				let body;
				try {
					body = await readJson(req);
				} catch {
					return fail(res, 400, 'invalid_request', 'malformed JSON');
				}
				if ('custom_name' in body) {
					if (body.custom_name !== null && typeof body.custom_name !== 'string')
						return fail(res, 400, 'invalid_request', 'custom_name must be string or null');
					p.custom_name = body.custom_name === null || body.custom_name.trim() === '' ? null : body.custom_name.trim();
				}
				if ('paused' in body) {
					if (typeof body.paused !== 'boolean') return fail(res, 400, 'invalid_request', 'paused must be boolean');
					p.paused = body.paused;
				}
				if ('notify' in body) {
					if (typeof body.notify !== 'boolean') return fail(res, 400, 'invalid_request', 'notify: must be true or false');
					p.notify = body.notify;
				}
				if ('target_price_cents' in body) {
					if (body.target_price_cents !== null && !isCents(body.target_price_cents))
						return fail(res, 400, 'invalid_request', 'target_price_cents must be integer or null');
					p.target_price_cents = body.target_price_cents;
				}
				return send(res, 200, computeProduct(p));
			}
		}

		if (sub === 'prices' && method === 'GET') {
			const from = url.searchParams.get('from');
			const to = url.searchParams.get('to');
			const fromMs = from ? Date.parse(from) : -Infinity;
			const toMs = to ? Date.parse(to) : Infinity;
			if (Number.isNaN(fromMs) || Number.isNaN(toMs)) return fail(res, 400, 'invalid_request', 'bad from/to');
			const pts = checks
				.get(id)
				.filter((c) => c.success)
				.filter((c) => {
					const t = Date.parse(c.checked_at);
					return t >= fromMs && t <= toMs;
				})
				.map(({ checked_at, price_cents, old_price_cents, rrp_cents, availability }) => ({
					checked_at,
					price_cents,
					old_price_cents,
					rrp_cents,
					availability
				}));
			return send(res, 200, pts);
		}

		if (sub === 'checks' && method === 'GET') {
			const limit = Number(url.searchParams.get('limit') ?? 50);
			if (!Number.isInteger(limit) || limit < 1 || limit > 500)
				return fail(res, 400, 'invalid_request', 'limit must be 1..500');
			const list = [...checks.get(id)].reverse().slice(0, limit).map(publicCheck);
			return send(res, 200, list);
		}

		if (sub === 'notifications' && method === 'GET') {
			const limit = Number(url.searchParams.get('limit') ?? 50);
			if (!Number.isInteger(limit) || limit < 1 || limit > 500)
				return fail(res, 400, 'invalid_request', 'limit must be 1..500');
			return send(res, 200, [...(notifications.get(id) ?? [])].reverse().slice(0, limit));
		}

		if (sub === 'refresh' && method === 'POST') {
			await sleep(1000);
			if (!products.has(id)) return fail(res, 404, 'not_found', `product ${id} not found`);
			const before = computeProduct(p).stats.current_cents;
			const check = doCheck(p);
			if (p.notify && !p.paused && check.success && before && check.price_cents !== before) {
				const diff = check.price_cents - before;
				const pct = ((diff / before) * 100).toLocaleString('de-DE', { maximumFractionDigits: 1 });
				if (Math.abs(diff / before) >= 0.02) {
					const down = diff < 0;
					addNotification(id, down ? 'price_drop' : 'price_rise',
						`Preis ${down ? 'gesunken' : 'gestiegen'}: ${p.custom_name ?? p.original_name} (${pct} %)`,
						`- Aktueller Preis: **${fmtEuro(check.price_cents)}**\n- Vorheriger Preis: ${fmtEuro(before)}`,
						down ? 6 : 3);
				}
			}
			return send(res, 200, { product: computeProduct(p), check: publicCheck(check) });
		}

		return fail(res, 405, 'invalid_request', 'method not allowed');
	}

	return fail(res, 404, 'not_found', `no route for ${method} ${url.pathname}`);
}

createServer((req, res) => {
	handle(req, res).catch((e) => {
		console.error(e);
		if (!res.headersSent) fail(res, 500, 'internal', String(e?.message ?? e));
	});
}).listen(PORT, () => {
	console.log(`PriceRadar mock API listening on http://localhost:${PORT}  (${products.size} seeded products)`);
});
