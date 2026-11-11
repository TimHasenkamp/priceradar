// Typed client for the PriceRadar API (see docs/API.md).
// All money values are integer cents, all timestamps RFC 3339 UTC strings.

export type Availability = 'in_stock' | 'out_of_stock' | 'preorder' | 'limited' | 'unknown';

export type ErrorKind =
	| 'blocked'
	| 'layout_changed'
	| 'not_found'
	| 'transient'
	| 'robots_disallowed'
	| 'internal';

export type ApiErrorCode =
	| 'invalid_request'
	| 'not_found'
	| 'duplicate'
	| 'unsupported_shop'
	| 'scrape_failed'
	| 'timeout'
	| 'internal'
	| 'backend_unreachable'
	| 'network';

export interface ProductStats {
	current_cents: number | null;
	previous_cents: number | null;
	avg30_cents: number | null;
	min_cents: number | null;
	max_cents: number | null;
	old_price_cents: number | null;
	rrp_cents: number | null;
	availability: Availability | null;
}

export interface Product {
	id: number;
	shop: string;
	url: string;
	sku: string | null;
	name: string;
	original_name: string;
	custom_name: string | null;
	image_url: string | null;
	currency: string;
	target_price_cents: number | null;
	target_reached: boolean;
	paused: boolean;
	created_at: string;
	last_checked_at: string | null;
	last_success_at: string | null;
	consecutive_failures: number;
	last_error_kind: ErrorKind | null;
	last_error: string | null;
	stats: ProductStats;
}

export interface PricePoint {
	checked_at: string;
	price_cents: number;
	old_price_cents: number | null;
	rrp_cents: number | null;
	availability: Availability | null;
}

export interface Check {
	id: number;
	checked_at: string;
	success: boolean;
	price_cents: number | null;
	error_kind: ErrorKind | null;
	error_message: string | null;
	duration_ms: number | null;
}

export interface RefreshResult {
	product: Product;
	check: Check;
}

export interface CreateProductInput {
	url: string;
	target_price_cents?: number;
}

export interface PatchProductInput {
	custom_name?: string | null;
	paused?: boolean;
	target_price_cents?: number | null;
}

export class ApiError extends Error {
	readonly code: string;
	readonly status: number;

	constructor(code: string, message: string, status: number) {
		super(message);
		this.name = 'ApiError';
		this.code = code;
		this.status = status;
	}
}

export type Fetch = typeof fetch;

const BASE = '/api';

async function request<T>(
	f: Fetch,
	method: string,
	path: string,
	body?: unknown
): Promise<T> {
	const init: RequestInit = { method, headers: { Accept: 'application/json' } };
	if (body !== undefined) {
		init.body = JSON.stringify(body);
		(init.headers as Record<string, string>)['Content-Type'] = 'application/json';
	}

	let res: Response;
	try {
		res = await f(BASE + path, init);
	} catch (e) {
		throw new ApiError('network', 'Server nicht erreichbar', 0);
	}

	if (res.status === 204) return undefined as T;

	const text = await res.text();
	let data: unknown = null;
	if (text) {
		try {
			data = JSON.parse(text);
		} catch {
			data = null;
		}
	}

	if (!res.ok) {
		const err = (data as { error?: { code?: unknown; message?: unknown } } | null)?.error;
		const code = typeof err?.code === 'string' ? err.code : 'internal';
		const message =
			typeof err?.message === 'string' && err.message
				? err.message
				: `HTTP ${res.status} ${res.statusText}`.trim();
		throw new ApiError(code, message, res.status);
	}

	return data as T;
}

const enc = encodeURIComponent;

export function listProducts(f: Fetch = fetch): Promise<Product[]> {
	return request<Product[]>(f, 'GET', '/products');
}

export function getProduct(id: number, f: Fetch = fetch): Promise<Product> {
	return request<Product>(f, 'GET', `/products/${enc(String(id))}`);
}

export function createProduct(input: CreateProductInput, f: Fetch = fetch): Promise<Product> {
	return request<Product>(f, 'POST', '/products', input);
}

export function patchProduct(id: number, patch: PatchProductInput, f: Fetch = fetch): Promise<Product> {
	return request<Product>(f, 'PATCH', `/products/${enc(String(id))}`, patch);
}

export function deleteProduct(id: number, f: Fetch = fetch): Promise<void> {
	return request<void>(f, 'DELETE', `/products/${enc(String(id))}`);
}

export function getPrices(
	id: number,
	opts: { from?: string | Date; to?: string | Date } = {},
	f: Fetch = fetch
): Promise<PricePoint[]> {
	const q = new URLSearchParams();
	const iso = (d: string | Date) => (typeof d === 'string' ? d : d.toISOString());
	if (opts.from) q.set('from', iso(opts.from));
	if (opts.to) q.set('to', iso(opts.to));
	const qs = q.toString();
	return request<PricePoint[]>(f, 'GET', `/products/${enc(String(id))}/prices${qs ? '?' + qs : ''}`);
}

export function getChecks(id: number, limit = 50, f: Fetch = fetch): Promise<Check[]> {
	return request<Check[]>(f, 'GET', `/products/${enc(String(id))}/checks?limit=${limit}`);
}

export function refreshProduct(id: number, f: Fetch = fetch): Promise<RefreshResult> {
	return request<RefreshResult>(f, 'POST', `/products/${enc(String(id))}/refresh`);
}

/** Maps API error codes to German UI messages. */
export function errorText(e: unknown): string {
	if (e instanceof ApiError) {
		switch (e.code) {
			case 'duplicate':
				return 'Produkt ist bereits vorhanden';
			case 'unsupported_shop':
				return 'Shop wird nicht unterstützt';
			case 'scrape_failed':
				return e.message;
			case 'timeout':
				return 'Zeitüberschreitung – bitte später erneut versuchen';
			case 'not_found':
				return 'Produkt nicht gefunden';
			case 'invalid_request':
				return `Ungültige Anfrage: ${e.message}`;
			case 'backend_unreachable':
			case 'network':
				return 'Backend nicht erreichbar';
			default:
				return e.message || 'Unbekannter Fehler';
		}
	}
	return e instanceof Error ? e.message : 'Unbekannter Fehler';
}
