import { errorText, listProducts, type Product } from '$lib/api';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, depends }) => {
	depends('app:products');
	try {
		return { products: await listProducts(fetch), loadError: null as string | null };
	} catch (e) {
		return { products: [] as Product[], loadError: errorText(e) };
	}
};
