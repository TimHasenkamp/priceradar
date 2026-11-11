import type { Handle } from '@sveltejs/kit';
import { env } from '$env/dynamic/private';

// Hop-by-hop headers (RFC 9110 §7.6.1) plus headers that must not be forwarded verbatim.
const STRIP_REQUEST = new Set([
	'host',
	'connection',
	'keep-alive',
	'proxy-connection',
	'transfer-encoding',
	'upgrade',
	'te',
	'trailer',
	'content-length',
	'accept-encoding'
]);

const STRIP_RESPONSE = new Set([
	'connection',
	'keep-alive',
	'transfer-encoding',
	'upgrade',
	'trailer',
	// fetch() transparently decodes the body, so the original encoding/length no longer apply
	'content-encoding',
	'content-length'
]);

function backendBase(): string {
	return (env.BACKEND_URL || 'http://localhost:8080').replace(/\/+$/, '');
}

/** undici wraps connection errors (often an AggregateError with an empty message) in `cause`. */
function describeFetchError(e: unknown): string {
	const cause = e instanceof Error ? (e.cause as { code?: unknown; message?: unknown } | undefined) : undefined;
	if (cause && typeof cause.code === 'string') return cause.code;
	if (cause && typeof cause.message === 'string' && cause.message) return cause.message;
	if (e instanceof Error && e.message) return e.message;
	return 'unbekannter Fehler';
}

async function proxy(request: Request, url: URL): Promise<Response> {
	const target = backendBase() + url.pathname + url.search;

	const headers = new Headers();
	request.headers.forEach((value, key) => {
		if (!STRIP_REQUEST.has(key.toLowerCase())) headers.set(key, value);
	});

	const hasBody = request.method !== 'GET' && request.method !== 'HEAD';
	const init: RequestInit & { duplex?: 'half' } = {
		method: request.method,
		headers,
		redirect: 'manual',
		signal: request.signal
	};
	if (hasBody && request.body) {
		init.body = request.body;
		init.duplex = 'half';
	}

	let upstream: Response;
	try {
		upstream = await fetch(target, init);
	} catch (e) {
		if (request.signal.aborted) {
			return new Response(null, { status: 499 });
		}
		const message = describeFetchError(e);
		console.error(`[proxy] ${request.method} ${target} failed: ${message}`);
		return new Response(
			JSON.stringify({
				error: { code: 'backend_unreachable', message: `Backend nicht erreichbar (${message})` }
			}),
			{ status: 502, headers: { 'content-type': 'application/json; charset=utf-8' } }
		);
	}

	const resHeaders = new Headers();
	upstream.headers.forEach((value, key) => {
		if (!STRIP_RESPONSE.has(key.toLowerCase())) resHeaders.set(key, value);
	});
	resHeaders.set('cache-control', upstream.headers.get('cache-control') ?? 'no-store');

	const noBody = upstream.status === 204 || upstream.status === 304 || request.method === 'HEAD';
	return new Response(noBody ? null : upstream.body, {
		status: upstream.status,
		statusText: upstream.statusText,
		headers: resHeaders
	});
}

export const handle: Handle = async ({ event, resolve }) => {
	const { pathname } = event.url;
	if (pathname === '/api' || pathname.startsWith('/api/')) {
		return proxy(event.request, event.url);
	}
	return resolve(event);
};
