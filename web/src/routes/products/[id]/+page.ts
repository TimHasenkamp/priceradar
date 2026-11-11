import { error } from '@sveltejs/kit';
import { ApiError, errorText, getChecks, getPrices, getProduct } from '$lib/api';
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
		const [product, prices, checks] = await Promise.all([
			getProduct(id, fetch),
			getPrices(id, { from }, fetch),
			getChecks(id, 50, fetch)
		]);
		return { product, prices, checks, range, rangeTo: now };
	} catch (e) {
		if (e instanceof ApiError) {
			const status = e.status >= 400 && e.status <= 599 ? e.status : 502;
			error(status, errorText(e));
		}
		throw e;
	}
};
