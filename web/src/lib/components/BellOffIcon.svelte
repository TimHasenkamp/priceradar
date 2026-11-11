<script lang="ts">
	let { size = 16, title = 'Benachrichtigungen aus' }: { size?: number; title?: string } = $props();
</script>

<svg class="bell-off" viewBox="0 0 24 24" width={size} height={size} role="img" aria-label={title}>
	<title>{title}</title>
	<path
		d="M13.7 21a2 2 0 0 1-3.4 0M18.6 13.5A9 9 0 0 1 18 10a6 6 0 0 0-9.3-5M6.3 6.3A6 6 0 0 0 6 8c0 7-3 9-3 9h14M3 3l18 18"
		fill="none"
		stroke="currentColor"
		stroke-width="2"
		stroke-linecap="round"
		stroke-linejoin="round"
	/>
</svg>

<style>
	.bell-off {
		flex: none;
		color: var(--text-faint);
	}
</style>
