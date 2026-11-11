export type ThemeChoice = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'priceradar-theme';

function readStored(): ThemeChoice {
	try {
		const v = localStorage.getItem(STORAGE_KEY);
		return v === 'light' || v === 'dark' ? v : 'system';
	} catch {
		return 'system';
	}
}

function systemPrefersDark(): boolean {
	return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches;
}

class ThemeState {
	choice = $state<ThemeChoice>('system');
	systemDark = $state(false);
	effective = $derived<'light' | 'dark'>(
		this.choice === 'system' ? (this.systemDark ? 'dark' : 'light') : this.choice
	);

	/** Call once in the browser (root layout). Returns a cleanup function. */
	init(): () => void {
		this.choice = readStored();
		const mq = matchMedia('(prefers-color-scheme: dark)');
		this.systemDark = mq.matches;
		const onChange = (e: MediaQueryListEvent) => (this.systemDark = e.matches);
		mq.addEventListener('change', onChange);
		return () => mq.removeEventListener('change', onChange);
	}

	set(choice: ThemeChoice) {
		this.choice = choice;
		const root = document.documentElement;
		if (choice === 'system') root.removeAttribute('data-theme');
		else root.setAttribute('data-theme', choice);
		try {
			if (choice === 'system') localStorage.removeItem(STORAGE_KEY);
			else localStorage.setItem(STORAGE_KEY, choice);
		} catch {
			/* storage unavailable – override lasts for this page view only */
		}
	}

	/** Toggles the effective theme; returns to "system" when the toggle matches the OS preference. */
	toggle() {
		const next = this.effective === 'dark' ? 'light' : 'dark';
		this.set(next === (systemPrefersDark() ? 'dark' : 'light') ? 'system' : next);
	}
}

export const theme = new ThemeState();

/** Reads a CSS custom property from :root. */
export function cssVar(name: string, fallback = ''): string {
	if (typeof document === 'undefined') return fallback;
	const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
	return v || fallback;
}
