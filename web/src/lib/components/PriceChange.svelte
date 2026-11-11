<script lang="ts">
	import { formatPercent, formatSignedCents, priceChange } from '$lib/format';

	let {
		current,
		reference,
		label,
		currency = 'EUR'
	}: {
		current: number | null;
		reference: number | null;
		label: string;
		currency?: string;
	} = $props();

	const change = $derived(priceChange(current, reference));
	const arrow = $derived(change?.direction === 'down' ? '↓' : change?.direction === 'up' ? '↑' : '→');
	const word = $derived(
		change?.direction === 'down' ? 'gesunken' : change?.direction === 'up' ? 'gestiegen' : 'unverändert'
	);
</script>

{#if change}
	<span class="change {change.direction}" title="{label}: {word}">
		<span class="arrow" aria-hidden="true">{arrow}</span>
		<span class="tabular">{formatSignedCents(change.diffCents, currency)}</span>
		<span class="pct tabular">({formatPercent(change.fraction)})</span>
		<span class="label">{label}</span>
		<span class="visually-hidden">– Preis {word}</span>
	</span>
{/if}

<style>
	.change {
		display: inline-flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.3rem;
		font-size: 0.85rem;
		font-weight: 600;
	}
	.arrow {
		font-weight: 800;
	}
	.down {
		color: var(--good);
	}
	.up {
		color: var(--bad);
	}
	.same {
		color: var(--text-muted);
	}
	.pct {
		font-weight: 500;
	}
	.label {
		color: var(--text-muted);
		font-weight: 500;
	}
</style>
