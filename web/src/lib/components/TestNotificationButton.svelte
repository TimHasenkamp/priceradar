<script lang="ts">
	import { errorText, sendTestNotification } from '$lib/api';

	let busy = $state(false);
	let result = $state<{ kind: 'ok' | 'error'; text: string } | null>(null);

	async function send() {
		if (busy) return;
		busy = true;
		result = null;
		try {
			await sendTestNotification();
			result = { kind: 'ok', text: 'Testbenachrichtigung gesendet.' };
		} catch (e) {
			result = { kind: 'error', text: errorText(e) };
		} finally {
			busy = false;
		}
	}
</script>

<span class="test-notify">
	<button type="button" class="btn btn-ghost btn-sm" onclick={send} disabled={busy}>
		{#if busy}<span class="spinner" aria-hidden="true"></span>{/if}
		Testbenachrichtigung senden
	</button>
	<span class="result {result?.kind ?? ''}" aria-live="polite">{result?.text ?? ''}</span>
</span>

<style>
	.test-notify {
		display: inline-flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.25rem 0.5rem;
	}
	.btn {
		color: var(--text-muted);
		font-size: 0.8rem;
		min-height: 1.75rem;
		padding: 0.2rem 0.5rem;
	}
	.result:empty {
		display: none;
	}
	.result.ok {
		color: var(--good);
	}
	.result.error {
		color: var(--bad);
	}
</style>
