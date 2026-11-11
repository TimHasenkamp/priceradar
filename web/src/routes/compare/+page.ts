import { errorText, listProducts, type Product } from '$lib/api';
import { DEFAULT_RANGE, isRangeKey } from '$lib/ranges';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url }) => {
	const r = url.searchParams.get('range');
	const range = isRangeKey(r) ? r : DEFAULT_RANGE;
	const idsParam = url.searchParams.get('ids');
	const ids = idsParam
		? idsParam
				.split(',')
				.map(Number)
				.filter((n) => Number.isInteger(n) && n > 0)
		: null;
	try {
		return { products: await listProducts(fetch), range, ids, loadError: null as string | null };
	} catch (e) {
		return { products: [] as Product[], range, ids, loadError: errorText(e) };
	}
};
