import type { Availability, ErrorKind } from './api';

const currencyFormatters = new Map<string, Intl.NumberFormat>();

function currencyFormatter(currency: string): Intl.NumberFormat {
	let f = currencyFormatters.get(currency);
	if (!f) {
		try {
			f = new Intl.NumberFormat('de-DE', { style: 'currency', currency });
		} catch {
			f = new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' });
		}
		currencyFormatters.set(currency, f);
	}
	return f;
}

/** 49360 → "493,60 €". null/undefined → "–". */
export function formatCents(cents: number | null | undefined, currency = 'EUR'): string {
	if (cents === null || cents === undefined || !Number.isFinite(cents)) return '–';
	return currencyFormatter(currency).format(cents / 100);
}

const percentFormatter = new Intl.NumberFormat('de-DE', {
	style: 'percent',
	minimumFractionDigits: 1,
	maximumFractionDigits: 1,
	signDisplay: 'exceptZero'
});

/** Formats a fraction (0.051 → "+5,1 %"). */
export function formatPercent(fraction: number): string {
	// typographic minus, consistent with formatSignedCents
	return percentFormatter.format(fraction).replace(/^-/, '−');
}

export interface Change {
	/** current - reference, in cents */
	diffCents: number;
	/** relative change as fraction (diff / reference) */
	fraction: number;
	direction: 'down' | 'up' | 'same';
}

/** Change of `current` relative to `reference`. Returns null if either is missing or reference is 0. */
export function priceChange(
	current: number | null | undefined,
	reference: number | null | undefined
): Change | null {
	if (current == null || reference == null || reference === 0) return null;
	const diffCents = current - reference;
	return {
		diffCents,
		fraction: diffCents / reference,
		direction: diffCents < 0 ? 'down' : diffCents > 0 ? 'up' : 'same'
	};
}

/** Absolute diff with explicit sign, e.g. "−26,30 €" / "+3,00 €". */
export function formatSignedCents(cents: number, currency = 'EUR'): string {
	const abs = formatCents(Math.abs(cents), currency);
	if (cents < 0) return `−${abs}`;
	if (cents > 0) return `+${abs}`;
	return abs;
}

/** Parses a German/English euro input ("449,99", "449.99", "1.299,00") to cents. */
export function parseEuroToCents(input: string): number | null {
	let s = input.trim().replace(/\s|€/g, '');
	if (!s) return null;
	if (s.includes(',')) {
		s = s.replace(/\./g, '').replace(',', '.');
	} else if (/^\d{1,3}(\.\d{3})+$/.test(s)) {
		s = s.replace(/\./g, '');
	}
	if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
	return Math.round(parseFloat(s) * 100);
}

/** Cents to an editable input string, e.g. 45000 → "450,00". */
export function centsToInput(cents: number | null | undefined): string {
	if (cents == null) return '';
	return (cents / 100).toFixed(2).replace('.', ',');
}

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

/** Relative time in German: "gerade eben", "vor 5 Min.", "vor 3 Std.", "vor 2 Tagen". */
export function formatRelative(
	iso: string | null | undefined,
	now: Date | number = Date.now()
): string {
	if (!iso) return 'nie';
	const t = new Date(iso).getTime();
	if (Number.isNaN(t)) return '–';
	const nowMs = typeof now === 'number' ? now : now.getTime();
	const diff = nowMs - t;
	if (diff < 0) {
		return diff > -MIN ? 'gerade eben' : formatDateTime(iso);
	}
	if (diff < MIN) return 'gerade eben';
	if (diff < HOUR) return `vor ${Math.floor(diff / MIN)} Min.`;
	if (diff < DAY) return `vor ${Math.floor(diff / HOUR)} Std.`;
	const days = Math.floor(diff / DAY);
	if (days === 1) return 'vor 1 Tag';
	if (days < 30) return `vor ${days} Tagen`;
	return formatDate(iso);
}

const dateFormatter = new Intl.DateTimeFormat('de-DE', { dateStyle: 'medium' });
const dateTimeFormatter = new Intl.DateTimeFormat('de-DE', {
	dateStyle: 'medium',
	timeStyle: 'short'
});

export function formatDate(iso: string | Date | number): string {
	return dateFormatter.format(new Date(iso));
}

export function formatDateTime(iso: string | Date | number): string {
	return dateTimeFormatter.format(new Date(iso));
}

export function formatDuration(ms: number | null | undefined): string {
	if (ms == null) return '–';
	if (ms < 1000) return `${ms} ms`;
	return `${(ms / 1000).toLocaleString('de-DE', { maximumFractionDigits: 1 })} s`;
}

export const errorKindLabels: Record<ErrorKind, string> = {
	blocked: 'Blockiert (Bot-Schutz)',
	layout_changed: 'Seitenlayout geändert?',
	not_found: 'Nicht gefunden',
	transient: 'Temporärer Fehler',
	robots_disallowed: 'robots.txt verbietet Abruf',
	internal: 'Interner Fehler'
};

export function errorKindLabel(kind: string | null | undefined): string {
	if (!kind) return 'Fehler';
	return (errorKindLabels as Record<string, string>)[kind] ?? kind;
}

export const availabilityLabels: Record<Availability, string> = {
	in_stock: 'Lieferbar',
	out_of_stock: 'Ausverkauft',
	preorder: 'Vorbestellbar',
	limited: 'Begrenzt verfügbar',
	unknown: 'Verfügbarkeit unbekannt'
};

export function availabilityLabel(a: string | null | undefined): string {
	if (!a) return availabilityLabels.unknown;
	return (availabilityLabels as Record<string, string>)[a] ?? a;
}
