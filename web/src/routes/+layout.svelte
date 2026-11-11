<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { theme } from '$lib/theme.svelte';
	import { clock } from '$lib/clock.svelte';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	let mounted = $state(false);

	onMount(() => {
		const stopTheme = theme.init();
		const stopClock = clock.start();
		mounted = true;
		return () => {
			stopTheme();
			stopClock();
		};
	});

	const nav = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/compare', label: 'Vergleich' }
	];

	function isActive(href: string): boolean {
		const p = page.url.pathname;
		if (href === '/') return p === '/' || p.startsWith('/products');
		return p === href || p.startsWith(href + '/');
	}

	const themeLabel = $derived(
		theme.effective === 'dark' ? 'Zu hellem Design wechseln' : 'Zu dunklem Design wechseln'
	);
</script>

<a class="skip" href="#main">Zum Inhalt springen</a>

<header class="site-header">
	<div class="inner">
		<a class="brand" href="/" aria-label="PriceRadar – Dashboard">
			<svg viewBox="0 0 32 32" width="26" height="26" aria-hidden="true">
				<circle cx="16" cy="16" r="14" fill="var(--accent)" />
				<circle cx="16" cy="16" r="9" fill="none" stroke="var(--accent-contrast)" stroke-width="2" opacity=".5" />
				<path d="M16 16 L26 8" stroke="var(--accent-contrast)" stroke-width="2.5" stroke-linecap="round" />
				<circle cx="16" cy="16" r="2" fill="var(--accent-contrast)" />
			</svg>
			<span>PriceRadar</span>
		</a>

		<nav aria-label="Hauptnavigation">
			<ul>
				{#each nav as item (item.href)}
					<li>
						<a href={item.href} aria-current={isActive(item.href) ? 'page' : undefined}>{item.label}</a>
					</li>
				{/each}
			</ul>
		</nav>

		<button
			type="button"
			class="btn btn-ghost theme-toggle"
			onclick={() => theme.toggle()}
			aria-label={themeLabel}
			title={mounted ? `${themeLabel}${theme.choice === 'system' ? ' (aktuell: Systemeinstellung)' : ''}` : 'Design wechseln'}
		>
			<!-- Both icons are rendered; CSS picks the right one so SSR output never flashes. -->
			<svg class="icon-moon" viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
				<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" />
			</svg>
			<svg class="icon-sun" viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
				<circle cx="12" cy="12" r="4" fill="none" stroke="currentColor" stroke-width="2" />
				<path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
			</svg>
		</button>
	</div>
</header>

<main id="main" class="container">
	{@render children()}
</main>

<footer class="site-footer container">
	<span>PriceRadar · selbst gehostet · Preise von otto.de</span>
</footer>

<style>
	.skip {
		position: absolute;
		left: 0.5rem;
		top: -3rem;
		padding: 0.5rem 0.8rem;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		z-index: 100;
	}
	.skip:focus {
		top: 0.5rem;
	}

	.site-header {
		position: sticky;
		top: 0;
		z-index: 20;
		background: color-mix(in srgb, var(--surface) 88%, transparent);
		backdrop-filter: saturate(1.4) blur(8px);
		border-bottom: 1px solid var(--border);
	}
	.inner {
		max-width: 1200px;
		margin: 0 auto;
		padding: 0.6rem 1rem;
		display: flex;
		align-items: center;
		gap: 1rem;
	}
	.brand {
		display: inline-flex;
		align-items: center;
		gap: 0.55rem;
		font-weight: 750;
		font-size: 1.1rem;
		letter-spacing: -0.01em;
		color: var(--text);
		text-decoration: none;
	}
	.brand:hover {
		color: var(--text);
	}
	nav {
		margin-left: auto;
	}
	nav ul {
		list-style: none;
		display: flex;
		gap: 0.25rem;
		margin: 0;
		padding: 0;
	}
	nav a {
		display: inline-block;
		padding: 0.4rem 0.7rem;
		border-radius: var(--radius-sm);
		color: var(--text-muted);
		font-weight: 600;
		font-size: 0.925rem;
		text-decoration: none;
	}
	nav a:hover {
		color: var(--text);
		background: var(--surface-2);
	}
	nav a[aria-current='page'] {
		color: var(--accent);
		background: var(--accent-soft);
	}
	.theme-toggle {
		min-height: 2.25rem;
		padding: 0.4rem;
		color: var(--text-muted);
	}
	.icon-sun {
		display: none;
	}
	:global(:root[data-theme='dark']) .icon-sun {
		display: block;
	}
	:global(:root[data-theme='dark']) .icon-moon {
		display: none;
	}
	@media (prefers-color-scheme: dark) {
		:global(:root:not([data-theme='light'])) .icon-sun {
			display: block;
		}
		:global(:root:not([data-theme='light'])) .icon-moon {
			display: none;
		}
	}

	.container {
		max-width: 1200px;
		margin: 0 auto;
		padding: 1.5rem 1rem;
	}
	main {
		min-height: calc(100vh - 9rem);
	}
	.site-footer {
		padding-top: 0.5rem;
		padding-bottom: 2rem;
		font-size: 0.8rem;
		color: var(--text-faint);
	}

	@media (max-width: 480px) {
		.inner {
			gap: 0.5rem;
		}
		.brand span {
			font-size: 1rem;
		}
		nav a {
			padding: 0.4rem 0.5rem;
		}
	}
</style>
