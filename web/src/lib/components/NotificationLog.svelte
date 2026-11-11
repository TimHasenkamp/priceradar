<script lang="ts">
	import type { Notification } from '$lib/api';
	import {
		formatDateTime,
		notificationKind,
		notificationStatus,
		notificationStatusTitle
	} from '$lib/format';

	let { notifications, error = null }: { notifications: Notification[]; error?: string | null } = $props();
</script>

{#if error}
	<p class="alert alert-error" role="alert">Benachrichtigungen konnten nicht geladen werden: {error}</p>
{:else if notifications.length === 0}
	<p class="muted">Noch keine Benachrichtigungen für dieses Produkt.</p>
{:else}
	<ul class="notif-list">
		{#each notifications as n (n.id)}
			{@const k = notificationKind(n.kind)}
			{@const st = notificationStatus(n.status)}
			<li class="notif">
				<span class="kind-icon kind-{n.kind}" aria-hidden="true">{k.icon}</span>
				<div class="main">
					<div class="top">
						<span class="kind-label">{k.label}</span>
						<time class="muted tabular" datetime={n.created_at}>{formatDateTime(n.created_at)}</time>
						<span
							class="badge {st.badge}"
							title={notificationStatusTitle(n)}
							aria-label="Status: {st.label}{n.error ? ` – ${n.error}` : ''}"
						>
							{st.label}
						</span>
					</div>
					<p class="title">{n.title}</p>
					<details>
						<summary>Nachricht anzeigen</summary>
						<pre>{n.message}</pre>
					</details>
				</div>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.notif-list {
		list-style: none;
		margin: 0;
		padding: 0;
		max-height: 32rem;
		overflow-y: auto;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.notif {
		display: grid;
		grid-template-columns: 2rem minmax(0, 1fr);
		gap: 0.6rem;
		padding: 0.65rem 0.75rem;
		border-bottom: 1px solid var(--border);
	}
	.notif:last-child {
		border-bottom: 0;
	}
	.kind-icon {
		display: inline-grid;
		place-items: center;
		width: 2rem;
		height: 2rem;
		border-radius: 50%;
		background: var(--surface-2);
		color: var(--text-muted);
		font-weight: 700;
	}
	.kind-target_reached,
	.kind-new_low {
		background: var(--accent-soft);
		color: var(--accent);
	}
	.kind-price_drop,
	.kind-trend_down,
	.kind-back_in_stock {
		background: var(--good-soft);
		color: var(--good);
	}
	.kind-price_rise,
	.kind-trend_up,
	.kind-out_of_stock {
		background: var(--bad-soft);
		color: var(--bad);
	}
	.kind-scrape_failing {
		background: var(--warn-soft);
		color: var(--warn);
	}
	.main {
		display: grid;
		gap: 0.2rem;
		min-width: 0;
	}
	.top {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.6rem;
		font-size: 0.825rem;
	}
	.kind-label {
		font-weight: 650;
	}
	.badge[title] {
		cursor: help;
	}
	.title {
		font-size: 0.9rem;
		overflow-wrap: anywhere;
	}
	details {
		font-size: 0.8rem;
	}
	summary {
		cursor: pointer;
		color: var(--text-muted);
	}
	pre {
		margin: 0.35rem 0 0;
		padding: 0.5rem 0.65rem;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		font-family: var(--mono);
		font-size: 0.775rem;
		background: var(--surface-2);
		border-radius: 6px;
	}
</style>
