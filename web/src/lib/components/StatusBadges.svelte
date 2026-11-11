<script lang="ts">
	import type { Product } from '$lib/api';
	import { availabilityLabel, errorKindLabel } from '$lib/format';

	let { product }: { product: Product } = $props();

	const availClass = $derived(
		{
			in_stock: 'badge-good',
			out_of_stock: 'badge-bad',
			preorder: 'badge-info',
			limited: 'badge-warn',
			unknown: ''
		}[product.stats.availability ?? 'unknown'] ?? ''
	);
</script>

<div class="badges">
	{#if product.target_reached}
		<span class="badge badge-accent">★ Zielpreis erreicht</span>
	{/if}
	{#if product.paused}
		<span class="badge">⏸ Pausiert</span>
	{/if}
	{#if product.consecutive_failures >= 2}
		<span
			class="badge badge-warn"
			title={product.last_error ?? undefined}
			aria-label="Warnung: {errorKindLabel(product.last_error_kind)}, {product.consecutive_failures} Fehlversuche in Folge{product.last_error ? ` – ${product.last_error}` : ''}"
		>
			⚠ {errorKindLabel(product.last_error_kind)}
			<span class="count">×{product.consecutive_failures}</span>
		</span>
	{/if}
	{#if product.stats.availability}
		<span class="badge {availClass}">{availabilityLabel(product.stats.availability)}</span>
	{/if}
</div>

<style>
	.badges {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}
	.count {
		font-weight: 500;
		opacity: 0.8;
	}
</style>
