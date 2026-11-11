export type RangeKey = '7' | '30' | '90' | 'all';

export const RANGES: { key: RangeKey; label: string; title: string; days: number | null }[] = [
	{ key: '7', label: '7 T', title: 'Letzte 7 Tage', days: 7 },
	{ key: '30', label: '30 T', title: 'Letzte 30 Tage', days: 30 },
	{ key: '90', label: '90 T', title: 'Letzte 90 Tage', days: 90 },
	{ key: 'all', label: 'Alles', title: 'Gesamter Zeitraum', days: null }
];

export const DEFAULT_RANGE: RangeKey = '90';

export function isRangeKey(v: string | null | undefined): v is RangeKey {
	return v === '7' || v === '30' || v === '90' || v === 'all';
}

/** Start of the range, or undefined for "all". */
export function rangeFrom(key: RangeKey, now: number = Date.now()): Date | undefined {
	const r = RANGES.find((x) => x.key === key);
	if (!r || r.days === null) return undefined;
	return new Date(now - r.days * 86_400_000);
}
