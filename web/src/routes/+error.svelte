<script lang="ts">
	import { page } from '$app/state';
</script>

<svelte:head><title>Fehler – PriceRadar</title></svelte:head>

<section class="card error">
	<p class="code tabular">{page.status}</p>
	<h1>{page.status === 404 ? 'Nicht gefunden' : 'Etwas ist schiefgelaufen'}</h1>
	{#if page.error?.message}
		<p class="muted">{page.error.message}</p>
	{/if}
	<p><a class="btn" href="/">Zum Dashboard</a></p>
</section>

<style>
	.error {
		max-width: 32rem;
		margin: 3rem auto;
		padding: 2rem;
		display: grid;
		gap: 0.75rem;
		text-align: center;
		justify-items: center;
	}
	.code {
		font-size: 2.5rem;
		font-weight: 800;
		color: var(--text-faint);
	}
</style>
