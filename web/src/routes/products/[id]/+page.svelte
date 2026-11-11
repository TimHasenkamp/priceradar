<script lang="ts">
	import { tick } from 'svelte';
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import {
		deleteProduct,
		errorText,
		getNotifications,
		getPrices,
		patchProduct,
		refreshProduct,
		type Check,
		type Notification,
		type PatchProductInput,
		type PricePoint,
		type Product
	} from '$lib/api';
	import { clock } from '$lib/clock.svelte';
	import {
		availabilityLabel,
		centsToInput,
		errorKindLabel,
		formatCents,
		formatDate,
		formatDateTime,
		formatDuration,
		formatRelative,
		parseEuroToCents
	} from '$lib/format';
	import { RANGES, rangeFrom, type RangeKey } from '$lib/ranges';
	import PriceChange from '$lib/components/PriceChange.svelte';
	import PriceChart, { type ChartSeries } from '$lib/components/PriceChart.svelte';
	import NotificationLog from '$lib/components/NotificationLog.svelte';
	import ProductImage from '$lib/components/ProductImage.svelte';
	import StatusBadges from '$lib/components/StatusBadges.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	let product = $derived<Product>(data.product);
	let prices = $derived<PricePoint[]>(data.prices);
	let checks = $derived<Check[]>(data.checks);
	let notifications = $derived<Notification[]>(data.notifications);
	let notificationsError = $derived<string | null>(data.notificationsError);
	let range = $derived<RangeKey>(data.range);
	let rangeTo = $derived<number>(data.rangeTo);

	let pricesLoading = $state(false);
	let pricesError = $state<string | null>(null);

	type Busy = 'rename' | 'target' | 'pause' | 'notify' | 'refresh' | 'delete' | null;
	let busy = $state<Busy>(null);
	let actionError = $state<string | null>(null);
	let actionInfo = $state<string | null>(null);

	// --- rename ---
	let editingName = $state(false);
	let nameInput = $state('');
	let nameEl: HTMLInputElement | undefined = $state();

	// --- target price ---
	// Writable derived: follows the product, but the user can edit it.
	let targetInput = $derived(centsToInput(product.target_price_cents));
	const targetCents = $derived(targetInput.trim() ? parseEuroToCents(targetInput) : null);
	const targetInvalid = $derived(targetInput.trim() !== '' && targetCents === null);

	let dialog: HTMLDialogElement | undefined = $state();

	const cur = $derived(product.currency);
	const s = $derived(product.stats);

	// --- chart ---
	const fromMs = $derived(rangeFrom(range, rangeTo)?.getTime());
	const xMin = $derived(fromMs ?? (prices.length ? new Date(prices[0].checked_at).getTime() : undefined));
	const xMax = $derived(Math.max(rangeTo, prices.length ? new Date(prices[prices.length - 1].checked_at).getTime() : 0));

	const series = $derived.by<ChartSeries[]>(() => {
		const list: ChartSeries[] = [
			{
				label: 'Preis',
				colorVar: '--chart-line',
				stepped: 'after',
				fill: true,
				points: prices.map((p) => ({ x: new Date(p.checked_at).getTime(), y: p.price_cents }))
			}
		];
		const t = product.target_price_cents;
		if (t != null && xMin !== undefined) {
			list.push({
				label: 'Zielpreis',
				colorVar: '--chart-target',
				dashed: true,
				decorative: true,
				points: [
					{ x: xMin, y: t },
					{ x: xMax, y: t }
				]
			});
		}
		return list;
	});

	const fmtY = (v: number) => formatCents(Math.round(v), cur);
	const fmtTick = (v: number) =>
		new Intl.NumberFormat('de-DE', {
			style: 'currency',
			currency: cur,
			maximumFractionDigits: v % 100 === 0 ? 0 : 2
		}).format(v / 100);

	async function setRange(key: RangeKey) {
		if (key === range && !pricesError) return;
		pricesLoading = true;
		pricesError = null;
		const now = Date.now();
		try {
			const res = await getPrices(product.id, { from: rangeFrom(key, now) });
			prices = res;
			range = key;
			rangeTo = now;
			const u = new URL(page.url);
			u.searchParams.set('range', key);
			replaceState(u, page.state);
		} catch (e) {
			pricesError = errorText(e);
		} finally {
			pricesLoading = false;
		}
	}

	async function reloadPrices() {
		try {
			const now = Date.now();
			prices = await getPrices(product.id, { from: rangeFrom(range, now) });
			rangeTo = now;
		} catch {
			/* keep the old series; the error is not critical here */
		}
	}

	async function patch(kind: Exclude<Busy, null>, body: PatchProductInput, info?: string) {
		busy = kind;
		actionError = null;
		actionInfo = null;
		try {
			product = await patchProduct(product.id, body);
			if (info) actionInfo = info;
			return true;
		} catch (e) {
			actionError = errorText(e);
			return false;
		} finally {
			busy = null;
		}
	}

	async function startRename() {
		nameInput = product.custom_name ?? product.name;
		editingName = true;
		await tick();
		nameEl?.focus();
		nameEl?.select();
	}

	async function saveName(e?: SubmitEvent) {
		e?.preventDefault();
		const v = nameInput.trim();
		const ok = await patch('rename', { custom_name: v === '' ? null : v }, v === '' ? 'Name auf Original zurückgesetzt.' : 'Name gespeichert.');
		if (ok) editingName = false;
	}

	function nameKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			editingName = false;
		}
	}

	async function saveTarget(e: SubmitEvent) {
		e.preventDefault();
		if (targetInvalid) {
			actionError = 'Zielpreis ist keine gültige Zahl (z. B. 449,99).';
			return;
		}
		await patch('target', { target_price_cents: targetCents }, targetCents == null ? 'Zielpreis entfernt.' : 'Zielpreis gespeichert.');
	}

	async function removeTarget() {
		await patch('target', { target_price_cents: null }, 'Zielpreis entfernt.');
	}

	async function togglePause() {
		await patch('pause', { paused: !product.paused }, product.paused ? 'Überwachung fortgesetzt.' : 'Überwachung pausiert.');
	}

	async function toggleNotify() {
		await patch(
			'notify',
			{ notify: !product.notify },
			product.notify ? 'Benachrichtigungen ausgeschaltet.' : 'Benachrichtigungen eingeschaltet.'
		);
	}

	async function reloadNotifications() {
		try {
			notifications = await getNotifications(product.id, 50);
			notificationsError = null;
		} catch (e) {
			notificationsError = errorText(e);
		}
	}

	async function refresh() {
		busy = 'refresh';
		actionError = null;
		actionInfo = null;
		try {
			const res = await refreshProduct(product.id);
			product = res.product;
			checks = [res.check, ...checks.filter((c) => c.id !== res.check.id)].slice(0, 50);
			// the check may have triggered notifications
			void reloadNotifications();
			if (res.check.success) {
				actionInfo = `Aktualisiert: ${formatCents(res.check.price_cents, cur)}`;
				await reloadPrices();
			} else {
				actionError = `Abruf fehlgeschlagen – ${errorKindLabel(res.check.error_kind)}${res.check.error_message ? `: ${res.check.error_message}` : ''}`;
			}
		} catch (e) {
			actionError = errorText(e);
		} finally {
			busy = null;
		}
	}

	async function confirmDelete() {
		busy = 'delete';
		actionError = null;
		try {
			await deleteProduct(product.id);
			dialog?.close();
			await goto('/', { invalidate: ['app:products'] });
		} catch (e) {
			dialog?.close();
			actionError = errorText(e);
		} finally {
			busy = null;
		}
	}
</script>

<svelte:head>
	<title>{product.name} – PriceRadar</title>
</svelte:head>

<p class="back"><a href="/">← Zurück zum Dashboard</a></p>

<section class="card header">
	<div class="img">
		<ProductImage src={product.image_url} alt={product.name} eager />
	</div>

	<div class="info">
		{#if editingName}
			<form class="rename" onsubmit={saveName}>
				<label class="visually-hidden" for="name-input">Anzeigename</label>
				<input
					id="name-input"
					name="custom_name"
					class="input"
					bind:this={nameEl}
					bind:value={nameInput}
					onkeydown={nameKeydown}
					placeholder={product.original_name}
					disabled={busy === 'rename'}
				/>
				<button class="btn btn-primary btn-sm" type="submit" disabled={busy === 'rename'}>
					{#if busy === 'rename'}<span class="spinner" aria-hidden="true"></span>{/if} Speichern
				</button>
				<button class="btn btn-sm" type="button" onclick={() => (editingName = false)}>Abbrechen</button>
				<p class="hint muted">Leer lassen, um den Originalnamen wiederherzustellen.</p>
			</form>
		{:else}
			<div class="title-row">
				<h1>{product.name}</h1>
				<button class="btn btn-ghost btn-sm" type="button" onclick={startRename} aria-label="Produkt umbenennen">
					✎ Umbenennen
				</button>
			</div>
			{#if product.custom_name}
				<p class="muted small">Original: {product.original_name}</p>
			{/if}
		{/if}

		<p class="small">
			<a href={product.url} target="_blank" rel="noopener noreferrer">Im Shop ansehen ({product.shop}) ↗</a>
			{#if product.sku}<span class="muted"> · Art.-Nr. {product.sku}</span>{/if}
		</p>

		<StatusBadges {product} />

		<div class="price-row">
			<span class="price tabular">{formatCents(s.current_cents, cur)}</span>
			{#if s.old_price_cents != null}
				<s class="ref tabular" title="Streichpreis">{formatCents(s.old_price_cents, cur)}</s>
			{/if}
			{#if s.rrp_cents != null}
				<span class="ref" title="Unverbindliche Preisempfehlung">UVP <s class="tabular">{formatCents(s.rrp_cents, cur)}</s></span>
			{/if}
		</div>
		<div class="changes">
			<PriceChange current={s.current_cents} reference={s.previous_cents} label="ggü. vorherigem Preis" currency={cur} />
			<PriceChange current={s.current_cents} reference={s.avg30_cents} label="ggü. Ø 30 Tage" currency={cur} />
		</div>

		{#if product.consecutive_failures >= 2 && product.last_error}
			<p class="alert alert-error small">
				{product.consecutive_failures} Fehlversuche in Folge – {errorKindLabel(product.last_error_kind)}: {product.last_error}
			</p>
		{/if}
	</div>

	<dl class="stats">
		<div><dt>Ø 30 Tage</dt><dd class="tabular">{formatCents(s.avg30_cents, cur)}</dd></div>
		<div><dt>Tiefstpreis</dt><dd class="tabular">{formatCents(s.min_cents, cur)}</dd></div>
		<div><dt>Höchstpreis</dt><dd class="tabular">{formatCents(s.max_cents, cur)}</dd></div>
		<div><dt>Vorheriger Preis</dt><dd class="tabular">{formatCents(s.previous_cents, cur)}</dd></div>
		<div><dt>Zielpreis</dt><dd class="tabular">{formatCents(product.target_price_cents, cur)}</dd></div>
		<div><dt>Verfügbarkeit</dt><dd>{availabilityLabel(s.availability)}</dd></div>
		<div>
			<dt>Zuletzt geprüft</dt>
			<dd title={product.last_checked_at ? formatDateTime(product.last_checked_at) : undefined}>
				{formatRelative(product.last_checked_at, clock.now)}
			</dd>
		</div>
		<div>
			<dt>Letzter Erfolg</dt>
			<dd title={product.last_success_at ? formatDateTime(product.last_success_at) : undefined}>
				{formatRelative(product.last_success_at, clock.now)}
			</dd>
		</div>
		<div><dt>Hinzugefügt</dt><dd>{formatDate(product.created_at)}</dd></div>
	</dl>
</section>

<section class="card actions" aria-label="Aktionen">
	<form class="target-form" onsubmit={saveTarget}>
		<label class="field">
			Zielpreis (€)
			<input
				class="input tabular"
				type="text"
				inputmode="decimal"
				name="target_price"
				placeholder="z. B. 449,99"
				bind:value={targetInput}
				aria-invalid={targetInvalid ? 'true' : undefined}
				disabled={busy === 'target'}
			/>
		</label>
		<button class="btn" type="submit" disabled={busy !== null || targetInvalid}>
			{#if busy === 'target'}<span class="spinner" aria-hidden="true"></span>{/if} Zielpreis setzen
		</button>
		{#if product.target_price_cents != null}
			<button class="btn btn-ghost" type="button" onclick={removeTarget} disabled={busy !== null}>Entfernen</button>
		{/if}
	</form>

	<div class="buttons">
		<button class="btn btn-primary" type="button" onclick={refresh} disabled={busy !== null}>
			{#if busy === 'refresh'}<span class="spinner" aria-hidden="true"></span> Wird aktualisiert…{:else}↻ Jetzt aktualisieren{/if}
		</button>
		<button class="btn" type="button" onclick={togglePause} disabled={busy !== null} aria-pressed={product.paused}>
			{#if busy === 'pause'}<span class="spinner" aria-hidden="true"></span>{/if}
			{product.paused ? '▶ Fortsetzen' : '⏸ Pausieren'}
		</button>
		<button
			class="btn notify-toggle"
			type="button"
			role="switch"
			aria-checked={product.notify}
			onclick={toggleNotify}
			disabled={busy !== null}
			title={product.paused ? 'Pausierte Produkte senden keine Benachrichtigungen' : undefined}
		>
			{#if busy === 'notify'}<span class="spinner" aria-hidden="true"></span>{:else}<span class="switch" aria-hidden="true"></span>{/if}
			Benachrichtigungen
		</button>
		<button class="btn btn-danger" type="button" onclick={() => dialog?.showModal()} disabled={busy !== null}>
			Löschen
		</button>
	</div>

	<div class="messages" aria-live="polite">
		{#if actionError}
			<p class="alert alert-error" role="alert">{actionError}</p>
		{:else if actionInfo}
			<p class="ok">{actionInfo}</p>
		{/if}
	</div>
</section>

<section class="card chart-card">
	<div class="section-head">
		<h2>Preisverlauf</h2>
		<div class="segmented" role="group" aria-label="Zeitraum">
			{#each RANGES as r (r.key)}
				<button
					type="button"
					aria-pressed={range === r.key}
					title={r.title}
					onclick={() => setRange(r.key)}
					disabled={pricesLoading}
				>
					{r.label}
				</button>
			{/each}
		</div>
	</div>
	{#if pricesError}
		<p class="alert alert-error" role="alert">Preisverlauf konnte nicht geladen werden: {pricesError}</p>
	{/if}
	<div class="chart-wrap" class:loading={pricesLoading}>
		<PriceChart
			{series}
			{xMin}
			{xMax}
			formatY={fmtY}
			formatTick={fmtTick}
			ariaLabel="Preisverlauf von {product.name}, {prices.length} Messpunkte"
			emptyText={prices.length === 0 ? 'Noch keine Preise in diesem Zeitraum.' : undefined}
		/>
	</div>
	{#if prices.length === 1}
		<p class="muted small">Bisher nur ein Messpunkt – der Verlauf entsteht mit weiteren Abrufen.</p>
	{/if}
</section>

<section class="card log">
	<div class="section-head">
		<h2>Abruf-Protokoll</h2>
		<span class="muted small">letzte {checks.length} Abrufe</span>
	</div>
	{#if checks.length === 0}
		<p class="muted">Noch keine Abrufe.</p>
	{:else}
		<div class="table-wrap log-scroll">
			<table class="table">
				<thead>
					<tr>
						<th scope="col">Zeit</th>
						<th scope="col">Status</th>
						<th scope="col">Ergebnis</th>
						<th scope="col" class="num">Dauer</th>
					</tr>
				</thead>
				<tbody>
					{#each checks as c (c.id)}
						<tr>
							<td class="nowrap tabular">{formatDateTime(c.checked_at)}</td>
							<td>
								{#if c.success}
									<span class="ok-mark" aria-label="Erfolgreich" title="Erfolgreich">✓</span>
								{:else}
									<span class="fail-mark" aria-label="Fehlgeschlagen" title="Fehlgeschlagen">✗</span>
								{/if}
							</td>
							<td>
								{#if c.success}
									<span class="tabular">{formatCents(c.price_cents, cur)}</span>
								{:else}
									<strong class="err-kind">{errorKindLabel(c.error_kind)}</strong>
									{#if c.error_message}<span class="muted err-msg">{c.error_message}</span>{/if}
								{/if}
							</td>
							<td class="num">{formatDuration(c.duration_ms)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</section>

<section class="card log">
	<div class="section-head">
		<h2>Benachrichtigungen</h2>
		<span class="muted small">
			{#if !product.notify}
				für dieses Produkt ausgeschaltet
			{:else if product.paused}
				pausiert – keine neuen Benachrichtigungen
			{:else}
				letzte {notifications.length}
			{/if}
		</span>
	</div>
	<NotificationLog {notifications} error={notificationsError} />
</section>

<dialog bind:this={dialog} class="card confirm" aria-labelledby="del-title">
	<form method="dialog" class="confirm-body" onsubmit={(e) => { e.preventDefault(); confirmDelete(); }}>
		<h2 id="del-title">Produkt löschen?</h2>
		<p>„{product.name}“ und der gesamte Preisverlauf werden dauerhaft gelöscht.</p>
		<div class="confirm-actions">
			<button class="btn" type="button" onclick={() => dialog?.close()} disabled={busy === 'delete'}>Abbrechen</button>
			<button class="btn btn-danger-solid" type="submit" disabled={busy === 'delete'}>
				{#if busy === 'delete'}<span class="spinner" aria-hidden="true"></span>{/if} Endgültig löschen
			</button>
		</div>
	</form>
</dialog>

<style>
	.back {
		margin-bottom: 1rem;
		font-size: 0.9rem;
	}
	.back a {
		text-decoration: none;
	}
	.card + .card {
		margin-top: 1rem;
	}
	.small {
		font-size: 0.875rem;
	}

	.header {
		padding: 1.25rem;
		display: grid;
		grid-template-columns: 180px minmax(0, 1fr) minmax(220px, 280px);
		gap: 1.5rem;
		align-items: start;
	}
	.info {
		display: grid;
		gap: 0.6rem;
		min-width: 0;
	}
	.title-row {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0.5rem;
	}
	.title-row h1 {
		font-size: 1.35rem;
		overflow-wrap: break-word;
		hyphens: auto;
		min-width: 0;
	}
	.title-row .btn {
		flex: none;
		color: var(--text-muted);
	}
	.rename {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		align-items: center;
	}
	.rename .input {
		flex: 1 1 16rem;
	}
	.hint {
		flex-basis: 100%;
		font-size: 0.8rem;
	}
	.price-row {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
	}
	.price {
		font-size: 2.1rem;
		font-weight: 750;
		letter-spacing: -0.02em;
	}
	.ref {
		color: var(--text-muted);
	}
	.changes {
		display: grid;
		gap: 0.15rem;
	}
	.stats {
		margin: 0;
		display: grid;
		gap: 0.45rem;
		font-size: 0.875rem;
		padding: 0.85rem 1rem;
		background: var(--surface-2);
		border-radius: var(--radius-sm);
	}
	.stats div {
		display: flex;
		justify-content: space-between;
		gap: 1rem;
	}
	.stats dt {
		color: var(--text-muted);
	}
	.stats dd {
		margin: 0;
		font-weight: 600;
		text-align: right;
	}

	.actions {
		padding: 1rem 1.25rem;
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		justify-content: space-between;
		gap: 1rem;
	}
	.target-form {
		display: flex;
		align-items: flex-end;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.target-form .field {
		width: 10rem;
	}
	.buttons {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.switch {
		position: relative;
		flex: none;
		width: 1.9rem;
		height: 1.1rem;
		border-radius: 999px;
		background: var(--border-strong);
		transition: background-color 0.15s;
	}
	.switch::after {
		content: '';
		position: absolute;
		top: 0.15rem;
		left: 0.15rem;
		width: 0.8rem;
		height: 0.8rem;
		border-radius: 50%;
		background: var(--surface);
		transition: transform 0.15s;
	}
	.notify-toggle[aria-checked='true'] .switch {
		background: var(--accent);
	}
	.notify-toggle[aria-checked='true'] .switch::after {
		transform: translateX(0.8rem);
	}
	@media (prefers-reduced-motion: reduce) {
		.switch,
		.switch::after {
			transition: none;
		}
	}
	.messages {
		flex-basis: 100%;
	}
	.messages:empty {
		display: none;
	}
	.ok {
		color: var(--good);
		font-weight: 600;
		font-size: 0.9rem;
	}

	.chart-card,
	.log {
		padding: 1.1rem 1.25rem;
		display: grid;
		gap: 0.85rem;
	}
	.section-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem 1rem;
	}
	.chart-wrap {
		transition: opacity 0.15s;
	}
	.chart-wrap.loading {
		opacity: 0.5;
	}

	.log-scroll {
		max-height: 32rem;
		overflow-y: auto;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.log-scroll :global(th) {
		position: sticky;
		top: 0;
		z-index: 1;
	}
	.nowrap {
		white-space: nowrap;
	}
	.ok-mark {
		color: var(--good);
		font-weight: 800;
	}
	.fail-mark {
		color: var(--bad);
		font-weight: 800;
	}
	.err-kind {
		font-weight: 600;
		margin-right: 0.4rem;
	}
	.err-msg {
		overflow-wrap: anywhere;
	}

	.confirm {
		max-width: min(28rem, calc(100vw - 2rem));
		padding: 0;
		color: var(--text);
		box-shadow: var(--shadow-lg);
	}
	.confirm::backdrop {
		background: rgb(0 0 0 / 0.45);
	}
	.confirm-body {
		padding: 1.25rem;
		display: grid;
		gap: 0.85rem;
	}
	.confirm-actions {
		display: flex;
		justify-content: flex-end;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	@media (max-width: 900px) {
		.header {
			grid-template-columns: 140px minmax(0, 1fr);
		}
		.stats {
			grid-column: 1 / -1;
			grid-template-columns: repeat(auto-fill, minmax(min(100%, 210px), 1fr));
			column-gap: 1.5rem;
		}
	}
	@media (max-width: 560px) {
		.header {
			grid-template-columns: 1fr;
			padding: 1rem;
		}
		.img {
			max-width: 160px;
		}
		.price {
			font-size: 1.8rem;
		}
		.title-row h1 {
			font-size: 1.2rem;
		}
		.actions,
		.chart-card,
		.log {
			padding: 1rem;
		}
		.target-form .field {
			width: 100%;
		}
	}
</style>
