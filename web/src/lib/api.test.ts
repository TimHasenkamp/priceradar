import { describe, expect, it } from 'vitest';
import { ApiError, errorText, sendTestNotification } from './api';

describe('errorText for notifications', () => {
	it('explains missing Gotify configuration', () => {
		expect(errorText(new ApiError('not_configured', 'x', 409))).toBe(
			'Gotify ist nicht konfiguriert (PR_GOTIFY_URL / PR_GOTIFY_TOKEN)'
		);
	});
	it('shows the Gotify failure reason', () => {
		expect(errorText(new ApiError('notify_failed', 'gotify: HTTP 401: Unauthorized', 502))).toBe(
			'Senden fehlgeschlagen: gotify: HTTP 401: Unauthorized'
		);
	});
});

describe('sendTestNotification', () => {
	it('POSTs and maps a 409 to ApiError', async () => {
		let seen = '';
		const f = (async (url: string, init?: RequestInit) => {
			seen = `${init?.method} ${url}`;
			return new Response(JSON.stringify({ error: { code: 'not_configured', message: 'nope' } }), { status: 409 });
		}) as typeof fetch;
		const err = await sendTestNotification(f).catch((e) => e);
		expect(seen).toBe('POST /api/notifications/test');
		expect(err).toBeInstanceOf(ApiError);
		expect(err.status).toBe(409);
		expect(err.code).toBe('not_configured');
	});
});
