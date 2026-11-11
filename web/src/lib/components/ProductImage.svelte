<script lang="ts">
	let {
		src,
		alt = '',
		size = '100%',
		eager = false
	}: { src: string | null; alt?: string; size?: string; eager?: boolean } = $props();

	let failed = $state(false);
	// Reset the error state when the source changes.
	$effect(() => {
		void src;
		failed = false;
	});
</script>

<div class="frame" style:--size={size}>
	{#if src && !failed}
		<img
			{src}
			{alt}
			loading={eager ? 'eager' : 'lazy'}
			decoding="async"
			referrerpolicy="no-referrer"
			onerror={() => (failed = true)}
		/>
	{:else}
		<svg class="placeholder" viewBox="0 0 48 48" aria-hidden="true">
			<rect x="8" y="10" width="32" height="28" rx="4" fill="none" stroke="currentColor" stroke-width="2" />
			<circle cx="18" cy="20" r="3" fill="currentColor" />
			<path d="M10 34l9-9 6 6 5-4 8 7" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" />
		</svg>
		{#if alt}<span class="visually-hidden">Kein Bild für {alt}</span>{/if}
	{/if}
</div>

<style>
	.frame {
		width: var(--size);
		aspect-ratio: 1 / 1;
		display: grid;
		place-items: center;
		background: #fff;
		border-radius: var(--radius-sm);
		overflow: hidden;
		border: 1px solid var(--border);
	}
	img {
		width: 100%;
		height: 100%;
		object-fit: contain;
		padding: 6%;
	}
	.placeholder {
		width: 40%;
		height: 40%;
		color: #9aa3b2;
	}
</style>
