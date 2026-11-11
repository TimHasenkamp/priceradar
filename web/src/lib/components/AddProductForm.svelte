<script lang="ts">
	import { createProduct, errorText, type Product } from '$lib/api';
	import { parseEuroToCents } from '$lib/format';

	let { oncreated }: { oncreated: (p: Product) => void } = $props();

	let url = $state('');
	let target = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);
	let success = $state<string | null>(null);
	let elapsed = $state(0);

	const targetCents = $derived(target.trim() ? parseEuroToCents(target) : null);
	const targetInvalid = $derived(target.trim() !== '' && targetCents === null);

	function validUrl(u: string): boolean {
		try {
			const parsed = new URL(u);
			return parsed.protocol === 'http:' || parsed.protocol === 'https:';
		} catch {
			return false;
		}
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (busy) return;
		error = null;
		success = null;

		const u = url.trim();
		if (!validUrl(u)) {
			error = 'Bitte eine vollständige URL eingeben (https://www.otto.de/p/…).';
			return;
		}
		if (targetInvalid) {
			error = 'Zielpreis ist keine gültige Zahl (z. B. 449,99).';
			return;
		}

		busy = true;
		elapsed = 0;
		const started = Date.now();
		const timer = setInterval(() => (elapsed = Math.floor((Date.now() - started) / 1000)), 1000);
		try {
			const p = await createProduct({
				url: u,
				...(targetCents != null ? { target_price_cents: targetCents } : {})
			});
			oncreated(p);
			success = `„${p.name}“ wurde hinzugefügt.`;
			url = '';
			target = '';
		} catch (err) {
			error = errorText(err);
		} finally {
			clearInterval(timer);
			busy = false;
		}
	}
</script>

<form class="card add" onsubmit={submit} aria-busy={busy} novalidate>
	<h2>Produkt hinzufügen</h2>
	<div class="row">
		<label class="field url">
			Produkt-URL (otto.de)
			<input
				class="input"
				type="url"
				inputmode="url"
				autocomplete="off"
				name="url"
				placeholder="https://www.otto.de/p/…"
				bind:value={url}
				required
				disabled={busy}
				aria-invalid={error && !validUrl(url.trim()) ? 'true' : undefined}
			/>
		</label>
		<label class="field target">
			Zielpreis (optional, €)
			<input
				class="input tabular"
				type="text"
				inputmode="decimal"
				autocomplete="off"
				name="target_price"
				placeholder="z. B. 449,99"
				bind:value={target}
				disabled={busy}
				aria-invalid={targetInvalid ? 'true' : undefined}
			/>
		</label>
		<button class="btn btn-primary submit" type="submit" disabled={busy || !url.trim()}>
			{#if busy}
				<span class="spinner" aria-hidden="true"></span> Seite wird abgerufen…
			{:else}
				Hinzufügen
			{/if}
		</button>
	</div>

	<div aria-live="polite" class="status">
		{#if busy}
			<p class="alert alert-info">
				Seite wird abgerufen… Das kann 10–60 Sekunden dauern.
				<span class="tabular">({elapsed} s)</span>
			</p>
		{:else if error}
			<p class="alert alert-error" role="alert">{error}</p>
		{:else if success}
			<p class="ok">{success}</p>
		{/if}
	</div>
</form>

<style>
	.add {
		padding: 1.1rem 1.2rem;
		display: grid;
		gap: 0.85rem;
	}
	.row {
		display: grid;
		grid-template-columns: 1fr 12rem auto;
		gap: 0.75rem;
		align-items: end;
	}
	.submit {
		min-width: 12.5rem;
	}
	.status:empty {
		display: none;
	}
	.ok {
		color: var(--good);
		font-size: 0.9rem;
		font-weight: 600;
	}
	@media (max-width: 760px) {
		.row {
			grid-template-columns: 1fr 1fr;
		}
		.url {
			grid-column: 1 / -1;
		}
		.submit {
			min-width: 0;
		}
	}
	@media (max-width: 420px) {
		.row {
			grid-template-columns: 1fr;
		}
	}
</style>
