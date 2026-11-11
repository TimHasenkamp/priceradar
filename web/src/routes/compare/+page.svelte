<script lang="ts">
	import { untrack } from 'svelte';
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { errorText, getPrices, type PricePoint } from '$lib/api';
	import { formatCents, formatPercent } from '$lib/format';
	import { RANGES, rangeFrom, type RangeKey } from '$lib/ranges';
	import PriceChart, { type ChartSeries } from '$lib/components/PriceChart.svelte';
	import ProductImage from '$lib/components/ProductImage.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const PALETTE = ['--chart-1', '--chart-2', '--chart-3', '--chart-4', '--chart-5', '--chart-6'];

	const products = $derived(data.products);
	let range = $derived<RangeKey>(data.range);
	let selected = $derived<number[]>(
		(data.ids ?? data.products.slice(0, 3).map((p) => p.id)).filter((id) =>
			data.products.some((p) => p.id === id)
		)
	);

	// prices per "id:range"
	let cache = $state<Record<string, PricePoint[]>>({});
	let errors = $state<Record<string, string>>({});
	let loading = $state(0);
	let rangeTo = $state(Date.now());

	const colorOf = (id: number) => PALETTE[products.findIndex((p) => p.id === id) % PALETTE.length];

	let retryTick = $state(0);

	$effect(() => {
		const key = range;
		const ids = selected;
		void retryTick;
		untrack(() => fetchMissing(ids, key));
	});

	function fetchMissing(ids: number[], key: RangeKey) {
		const now = Date.now();
		for (const id of ids) {
			const k = `${id}:${key}`;
			if (k in cache || k in errors) continue;
			loading++;
			errors[k] = ''; // mark as in-flight
			getPrices(id, { from: rangeFrom(key, now) })
				.then((pts) => {
					cache[k] = pts;
					delete errors[k];
					rangeTo = Math.max(rangeTo, now);
				})
				.catch((e) => {
					errors[k] = errorText(e);
				})
				.finally(() => loading--);
		}
	}

	function syncUrl() {
		const u = new URL(page.url);
		u.searchParams.set('range', range);
		u.searchParams.set('ids', selected.join(','));
		replaceState(u, page.state);
	}

	function toggle(id: number, on: boolean) {
		selected = on ? [...selected, id] : selected.filter((x) => x !== id);
		syncUrl();
	}

	function setRange(key: RangeKey) {
		range = key;
		rangeTo = Date.now();
		syncUrl();
	}

	function retry() {
		for (const k of Object.keys(errors)) if (errors[k]) delete errors[k];
		retryTick++;
	}

	const series = $derived.by<ChartSeries[]>(() =>
		selected
			.map((id) => products.find((p) => p.id === id))
			.filter((p) => p !== undefined)
			.map((p) => {
				const pts = cache[`${p.id}:${range}`] ?? [];
				const base = pts[0]?.price_cents;
				return {
					label: p.name,
					colorVar: colorOf(p.id),
					stepped: 'after' as const,
					points: base
						? pts.map((pt) => ({
								x: new Date(pt.checked_at).getTime(),
								y: (pt.price_cents / base - 1) * 100
							}))
						: []
				};
			})
	);

	const summary = $derived(
		selected
			.map((id) => {
				const p = products.find((x) => x.id === id);
				const pts = cache[`${id}:${range}`];
				if (!p || !pts || pts.length === 0) return null;
				const first = pts[0].price_cents;
				const last = pts[pts.length - 1].price_cents;
				return { p, first, last, change: first ? last / first - 1 : 0 };
			})
			.filter((x) => x !== null)
	);

	const failed = $derived(Object.entries(errors).filter(([, v]) => v));
	const fromMs = $derived(rangeFrom(range, rangeTo)?.getTime());
	const fmtPct = (v: number) => formatPercent(v / 100);
	const fmtTick = (v: number) =>
		new Intl.NumberFormat('de-DE', { maximumFractionDigits: 1, signDisplay: 'exceptZero' }).format(v) + ' %';
</script>

<svelte:head>
	<title>Vergleich – PriceRadar</title>
</svelte:head>

<div class="heading">
	<h1>Vergleich</h1>
	<p class="muted">Relative Preisentwicklung – jede Linie startet beim ersten Preis im Zeitraum bei 0 %.</p>
</div>

{#if data.loadError}
	<p class="alert alert-error" role="alert">Produkte konnten nicht geladen werden: {data.loadError}</p>
{:else if products.length === 0}
	<section class="card empty">
		<p>Noch keine Produkte vorhanden. <a href="/">Zum Dashboard</a></p>
	</section>
{:else}
	<div class="layout">
		<fieldset class="card picker">
			<legend>Produkte</legend>
			<ul>
				{#each products as p (p.id)}
					{@const on = selected.includes(p.id)}
					<li>
						<label class:on>
							<input type="checkbox" name="product" value={p.id} checked={on} onchange={(e) => toggle(p.id, e.currentTarget.checked)} />
							<span class="swatch" style:background="var({colorOf(p.id)})" aria-hidden="true"></span>
							<span class="thumb"><ProductImage src={p.image_url} alt="" size="36px" /></span>
							<span class="pname">{p.name}</span>
						</label>
					</li>
				{/each}
			</ul>
		</fieldset>

		<section class="card chart-card">
			<div class="section-head">
				<h2>Preisentwicklung in %</h2>
				<div class="segmented" role="group" aria-label="Zeitraum">
					{#each RANGES as r (r.key)}
						<button type="button" aria-pressed={range === r.key} title={r.title} onclick={() => setRange(r.key)}>
							{r.label}
						</button>
					{/each}
				</div>
			</div>

			{#if failed.length > 0}
				<div class="alert alert-error" role="alert">
					{failed.length === 1 ? 'Ein Verlauf konnte' : `${failed.length} Verläufe konnten`} nicht geladen werden: {failed[0][1]}
					<button class="btn btn-sm" type="button" onclick={retry}>Erneut versuchen</button>
				</div>
			{/if}

			<div class:loading={loading > 0}>
				<PriceChart
					{series}
					xMin={fromMs}
					xMax={rangeTo}
					formatY={fmtPct}
					formatTick={fmtTick}
					legend
					height="380px"
					ariaLabel="Relative Preisentwicklung von {series.length} Produkten"
					emptyText={selected.length === 0 ? 'Wähle links mindestens ein Produkt aus.' : 'Keine Preise im gewählten Zeitraum.'}
				/>
			</div>

			{#if summary.length > 0}
				<div class="table-wrap">
					<table class="table">
						<thead>
							<tr>
								<th scope="col">Produkt</th>
								<th scope="col" class="num">Start</th>
								<th scope="col" class="num">Aktuell</th>
								<th scope="col" class="num">Änderung</th>
							</tr>
						</thead>
						<tbody>
							{#each summary as row (row.p.id)}
								<tr>
									<td>
										<span class="swatch" style:background="var({colorOf(row.p.id)})" aria-hidden="true"></span>
										<a href="/products/{row.p.id}">{row.p.name}</a>
									</td>
									<td class="num">{formatCents(row.first, row.p.currency)}</td>
									<td class="num">{formatCents(row.last, row.p.currency)}</td>
									<td class="num" class:down={row.change < 0} class:up={row.change > 0}>
										{row.change < 0 ? '↓' : row.change > 0 ? '↑' : ''}
										{formatPercent(row.change)}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</section>
	</div>
{/if}

<style>
	.heading {
		display: grid;
		gap: 0.25rem;
		margin-bottom: 1.25rem;
	}
	.layout {
		display: grid;
		grid-template-columns: minmax(240px, 300px) minmax(0, 1fr);
		gap: 1rem;
		align-items: start;
	}
	.picker {
		margin: 0;
		padding: 0.75rem;
		min-width: 0;
	}
	.picker legend {
		float: left;
		width: 100%;
		padding: 0.25rem 0.4rem 0.5rem;
		font-weight: 650;
	}
	.picker ul {
		clear: both;
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 2px;
	}
	.picker label {
		display: flex;
		align-items: center;
		gap: 0.55rem;
		padding: 0.4rem;
		border-radius: var(--radius-sm);
		cursor: pointer;
		font-size: 0.875rem;
	}
	.picker label:hover {
		background: var(--surface-2);
	}
	.picker label.on {
		background: var(--accent-soft);
	}
	.picker input {
		width: 1.05rem;
		height: 1.05rem;
		accent-color: var(--accent);
		flex: none;
	}
	.thumb {
		flex: none;
	}
	.pname {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
	}
	.swatch {
		display: inline-block;
		width: 0.75rem;
		height: 0.75rem;
		border-radius: 3px;
		flex: none;
		vertical-align: middle;
		margin-right: 0.4rem;
	}
	.picker .swatch {
		margin-right: 0;
	}
	.chart-card {
		padding: 1.1rem 1.25rem;
		display: grid;
		gap: 0.85rem;
		min-width: 0;
	}
	.section-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem 1rem;
	}
	.loading {
		opacity: 0.6;
		transition: opacity 0.15s;
	}
	.down {
		color: var(--good);
		font-weight: 650;
	}
	.up {
		color: var(--bad);
		font-weight: 650;
	}
	.empty {
		padding: 2rem;
		text-align: center;
	}
	.alert {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	@media (max-width: 800px) {
		.layout {
			grid-template-columns: 1fr;
		}
		.chart-card {
			padding: 1rem;
		}
	}
</style>
