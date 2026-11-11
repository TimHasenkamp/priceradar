<script lang="ts">
	import { errorText, refreshProduct, type Product } from '$lib/api';
	import { clock } from '$lib/clock.svelte';
	import { errorKindLabel, formatCents, formatDateTime, formatRelative } from '$lib/format';
	import BellOffIcon from './BellOffIcon.svelte';
	import PriceChange from './PriceChange.svelte';
	import ProductImage from './ProductImage.svelte';
	import StatusBadges from './StatusBadges.svelte';

	let { product, onupdate }: { product: Product; onupdate: (p: Product) => void } = $props();

	let refreshing = $state(false);
	let flash = $state<{ kind: 'ok' | 'error'; text: string } | null>(null);
	let flashTimer: ReturnType<typeof setTimeout> | undefined;

	const s = $derived(product.stats);
	const cur = $derived(product.currency);

	function showFlash(kind: 'ok' | 'error', text: string, ms = 6000) {
		clearTimeout(flashTimer);
		flash = { kind, text };
		flashTimer = setTimeout(() => (flash = null), ms);
	}

	$effect(() => () => clearTimeout(flashTimer));

	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		flash = null;
		try {
			const { product: updated, check } = await refreshProduct(product.id);
			onupdate(updated);
			if (check.success) {
				showFlash('ok', 'Aktualisiert', 2500);
			} else {
				showFlash(
					'error',
					`${errorKindLabel(check.error_kind)}${check.error_message ? `: ${check.error_message}` : ''}`
				);
			}
		} catch (e) {
			showFlash('error', errorText(e));
		} finally {
			refreshing = false;
		}
	}
</script>

<article class="card product" class:paused={product.paused} aria-busy={refreshing}>
	<a class="image-link" href="/products/{product.id}" tabindex="-1" aria-hidden="true">
		<ProductImage src={product.image_url} alt="" />
	</a>

	<div class="body">
		<div class="name-row">
			<h2 class="name">
				<a href="/products/{product.id}">{product.name}</a>
			</h2>
			{#if !product.notify}
				<BellOffIcon size={15} />
			{/if}
		</div>

		<StatusBadges {product} />

		<div class="price-row">
			<span class="price tabular">{formatCents(s.current_cents, cur)}</span>
			{#if s.old_price_cents != null}
				<s class="ref tabular" title="Streichpreis">{formatCents(s.old_price_cents, cur)}</s>
			{/if}
			{#if s.rrp_cents != null}
				<span class="ref" title="Unverbindliche Preisempfehlung">
					UVP <s class="tabular">{formatCents(s.rrp_cents, cur)}</s>
				</span>
			{/if}
		</div>

		<div class="changes">
			<PriceChange current={s.current_cents} reference={s.previous_cents} label="ggü. vorher" currency={cur} />
			<PriceChange current={s.current_cents} reference={s.avg30_cents} label="ggü. Ø 30 T" currency={cur} />
		</div>

		<dl class="minmax">
			<div>
				<dt>Tiefst</dt>
				<dd class="tabular">{formatCents(s.min_cents, cur)}</dd>
			</div>
			<div>
				<dt>Höchst</dt>
				<dd class="tabular">{formatCents(s.max_cents, cur)}</dd>
			</div>
			{#if product.target_price_cents != null}
				<div>
					<dt>Ziel</dt>
					<dd class="tabular">{formatCents(product.target_price_cents, cur)}</dd>
				</div>
			{/if}
		</dl>

		<div class="footer">
			<span
				class="checked"
				title={product.last_checked_at ? formatDateTime(product.last_checked_at) : undefined}
			>
				Zuletzt geprüft: {formatRelative(product.last_checked_at, clock.now)}
			</span>
			<button type="button" class="btn btn-sm" onclick={refresh} disabled={refreshing}>
				{#if refreshing}
					<span class="spinner" aria-hidden="true"></span> Wird aktualisiert…
				{:else}
					↻ Jetzt aktualisieren
				{/if}
			</button>
		</div>

		<div class="flash-slot" aria-live="polite">
			{#if flash}
				<p class="flash {flash.kind}">{flash.text}</p>
			{/if}
		</div>
	</div>
</article>

<style>
	.product {
		display: grid;
		grid-template-columns: 104px 1fr;
		gap: 1rem;
		padding: 1rem;
		align-items: start;
	}
	.product.paused .price {
		color: var(--text-muted);
	}
	.image-link {
		display: block;
	}
	.body {
		display: grid;
		gap: 0.55rem;
		min-width: 0;
	}
	.name-row {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.5rem;
	}
	.name-row :global(.bell-off) {
		margin-top: 0.2rem;
	}
	.name {
		min-width: 0;
		font-size: 0.975rem;
		font-weight: 650;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}
	.name a {
		color: var(--text);
		text-decoration: none;
	}
	.name a:hover {
		color: var(--accent);
		text-decoration: underline;
	}
	.price-row {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.25rem 0.6rem;
	}
	.price {
		font-size: 1.6rem;
		font-weight: 750;
		letter-spacing: -0.02em;
	}
	.ref {
		color: var(--text-muted);
		font-size: 0.875rem;
	}
	.changes {
		display: grid;
		gap: 0.15rem;
	}
	.changes:empty {
		display: none;
	}
	.minmax {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1.25rem;
		margin: 0;
		font-size: 0.85rem;
	}
	.minmax div {
		display: flex;
		gap: 0.35rem;
	}
	.minmax dt {
		color: var(--text-muted);
	}
	.minmax dd {
		margin: 0;
		font-weight: 600;
	}
	.footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem;
		padding-top: 0.55rem;
		border-top: 1px solid var(--border);
	}
	.checked {
		font-size: 0.8rem;
		color: var(--text-muted);
	}
	.flash-slot:empty {
		display: none;
	}
	.flash {
		font-size: 0.85rem;
		padding: 0.35rem 0.6rem;
		border-radius: 6px;
	}
	.flash.ok {
		background: var(--good-soft);
		color: var(--good);
	}
	.flash.error {
		background: var(--bad-soft);
		color: var(--bad);
	}

	@media (max-width: 420px) {
		.product {
			grid-template-columns: 72px 1fr;
			gap: 0.75rem;
			padding: 0.85rem;
		}
		.price {
			font-size: 1.4rem;
		}
	}
</style>
