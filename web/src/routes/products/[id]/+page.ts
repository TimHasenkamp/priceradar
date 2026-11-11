import { error } from '@sveltejs/kit';
import { ApiError, errorText, getChecks, getNotifications, getPrices, getProduct, type Notification } from '$lib/api';
import { DEFAULT_RANGE, isRangeKey, rangeFrom } from '$lib/ranges';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params, fetch, url }) => {
	const id = Number(params.id);
	if (!Number.isInteger(id) || id <= 0) error(404, 'Ungültige Produkt-ID');

	const r = url.searchParams.get('range');
	const range = isRangeKey(r) ? r : DEFAULT_RANGE;
	const now = Date.now();
	const from = rangeFrom(range, now);

	try {
		// The notification log is secondary: its failure must not break the page.
		const notificationsP = getNotifications(id, 50, fetch).then(
			(list): { list: Notification[]; error: string | null } => ({ list, error: null }),
			(e) => ({ list: [] as Notification[], error: errorText(e) })
		);
		const [product, prices, checks, notifications] = await Promise.all([
			getProduct(id, fetch),
			getPrices(id, { from }, fetch),
			getChecks(id, 50, fetch),
			notificationsP
		]);
		return {
			product,
			prices,
			checks,
			notifications: notifications.list,
			notificationsError: notifications.error,
			range,
			rangeTo: now
		};
	} catch (e) {
		if (e instanceof ApiError) {
			const status = e.status >= 400 && e.status <= 599 ? e.status : 502;
			error(status, errorText(e));
		}
		throw e;
	}
};
