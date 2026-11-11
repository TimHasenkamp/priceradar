<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Product } from '$lib/api';
	import AddProductForm from '$lib/components/AddProductForm.svelte';
	import ProductCard from '$lib/components/ProductCard.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	// Writable derived: resets whenever load data changes, but can be updated locally.
	let products = $derived<Product[]>(data.products);
	let retrying = $state(false);

	const reached = $derived(products.filter((p) => p.target_reached).length);
	const failing = $derived(products.filter((p) => p.consecutive_failures >= 2).length);

	function added(p: Product) {
		products = [p, ...products.filter((x) => x.id !== p.id)];
	}

	function updated(p: Product) {
		products = products.map((x) => (x.id === p.id ? p : x));
	}

	async function retry() {
		retrying = true;
		try {
			await invalidate('app:products');
		} finally {
			retrying = false;
		}
	}
</script>

<svelte:head>
	<title>Dashboard – PriceRadar</title>
</svelte:head>

<div class="stack">
	<div class="heading">
		<h1>Dashboard</h1>
		{#if products.length > 0}
			<p class="muted summary">
				{products.length}
				{products.length === 1 ? 'Produkt' : 'Produkte'}
				{#if reached > 0}· <span class="hl-good">{reached} mit Zielpreis erreicht</span>{/if}
				{#if failing > 0}· <span class="hl-warn">{failing} mit Abrufproblemen</span>{/if}
			</p>
		{/if}
	</div>

	<AddProductForm oncreated={added} />

	{#if data.loadError}
		<div class="alert alert-error load-error" role="alert">
			<span>Produkte konnten nicht geladen werden: {data.loadError}</span>
			<button class="btn btn-sm" type="button" onclick={retry} disabled={retrying}>
				{#if retrying}<span class="spinner" aria-hidden="true"></span>{/if} Erneut versuchen
			</button>
		</div>
	{:else if products.length === 0}
		<section class="card empty">
			<svg viewBox="0 0 64 64" width="56" height="56" aria-hidden="true">
				<circle cx="32" cy="32" r="26" fill="none" stroke="currentColor" stroke-width="2.5" opacity=".35" />
				<circle cx="32" cy="32" r="15" fill="none" stroke="currentColor" stroke-width="2.5" opacity=".55" />
				<path d="M32 32 L50 16" stroke="currentColor" stroke-width="3" stroke-linecap="round" />
				<circle cx="32" cy="32" r="3" fill="currentColor" />
			</svg>
			<h2>Noch keine Produkte</h2>
			<p class="muted">
				Füge oben die URL eines otto.de-Produkts ein. PriceRadar ruft den Preis regelmäßig ab und
				zeigt dir den Verlauf.
			</p>
		</section>
	{:else}
		<ul class="grid" aria-label="Beobachtete Produkte">
			{#each products as product (product.id)}
				<li><ProductCard {product} onupdate={updated} /></li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.stack {
		display: grid;
		gap: 1.25rem;
	}
	.heading {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
	}
	.summary {
		font-size: 0.9rem;
	}
	.hl-good {
		color: var(--good);
		font-weight: 600;
	}
	.hl-warn {
		color: var(--warn);
		font-weight: 600;
	}
	.grid {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 340px), 1fr));
		gap: var(--gap);
	}
	.grid li {
		display: grid;
	}
	.empty {
		padding: 2.5rem 1.5rem;
		display: grid;
		justify-items: center;
		gap: 0.6rem;
		text-align: center;
		color: var(--accent);
	}
	.empty p {
		max-width: 30rem;
	}
	.load-error {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.75rem;
	}
</style>
