import { describe, expect, it } from 'vitest';
import {
	formatCents,
	formatPercent,
	formatRelative,
	formatSignedCents,
	parseEuroToCents,
	priceChange,
	centsToInput,
	errorKindLabel,
	notificationKind,
	notificationStatus,
	notificationStatusTitle
} from './format';

// Intl uses non-breaking spaces; normalize for readable assertions.
const n = (s: string) => s.replace(/[  ]/g, ' ');

describe('formatCents', () => {
	it('formats cents as EUR in de-DE', () => {
		expect(n(formatCents(49360))).toBe('493,60 €');
		expect(n(formatCents(129900))).toBe('1.299,00 €');
		expect(n(formatCents(5))).toBe('0,05 €');
	});
	it('returns a dash for null', () => {
		expect(formatCents(null)).toBe('–');
		expect(formatCents(undefined)).toBe('–');
	});
	it('supports other currencies', () => {
		expect(n(formatCents(1000, 'USD'))).toBe('10,00 $');
	});
});

describe('formatSignedCents', () => {
	it('adds explicit signs', () => {
		expect(n(formatSignedCents(-2630))).toBe('−26,30 €');
		expect(n(formatSignedCents(300))).toBe('+3,00 €');
		expect(n(formatSignedCents(0))).toBe('0,00 €');
	});
});

describe('priceChange / formatPercent', () => {
	it('computes direction and fraction', () => {
		const c = priceChange(49360, 51990)!;
		expect(c.direction).toBe('down');
		expect(c.diffCents).toBe(-2630);
		expect(n(formatPercent(c.fraction))).toBe('−5,1 %');
		expect(priceChange(110, 100)!.direction).toBe('up');
		expect(n(formatPercent(priceChange(110, 100)!.fraction))).toBe('+10,0 %');
		expect(priceChange(100, 100)!.direction).toBe('same');
	});
	it('returns null when data is missing', () => {
		expect(priceChange(null, 100)).toBeNull();
		expect(priceChange(100, null)).toBeNull();
		expect(priceChange(100, 0)).toBeNull();
	});
});

describe('formatRelative', () => {
	const now = new Date('2026-11-11T14:00:00Z');
	it('formats recent times', () => {
		expect(formatRelative('2026-11-11T13:59:40Z', now)).toBe('gerade eben');
		expect(formatRelative('2026-11-11T13:55:00Z', now)).toBe('vor 5 Min.');
		expect(formatRelative('2026-11-11T11:00:00Z', now)).toBe('vor 3 Std.');
		expect(formatRelative('2026-11-10T13:00:00Z', now)).toBe('vor 1 Tag');
		expect(formatRelative('2026-11-08T14:00:00Z', now)).toBe('vor 3 Tagen');
	});
	it('handles null', () => {
		expect(formatRelative(null, now)).toBe('nie');
	});
});

describe('parseEuroToCents', () => {
	it('parses German and English inputs', () => {
		expect(parseEuroToCents('449,99')).toBe(44999);
		expect(parseEuroToCents('449.99')).toBe(44999);
		expect(parseEuroToCents('1.299,00 €')).toBe(129900);
		expect(parseEuroToCents('1.299')).toBe(129900);
		expect(parseEuroToCents('450')).toBe(45000);
		expect(parseEuroToCents('0,1')).toBe(10);
	});
	it('rejects invalid input', () => {
		expect(parseEuroToCents('')).toBeNull();
		expect(parseEuroToCents('abc')).toBeNull();
		expect(parseEuroToCents('-5')).toBeNull();
		expect(parseEuroToCents('1,234')).toBeNull();
	});
	it('round-trips with centsToInput', () => {
		expect(centsToInput(45000)).toBe('450,00');
		expect(parseEuroToCents(centsToInput(12345))).toBe(12345);
	});
});

describe('errorKindLabel', () => {
	it('maps known kinds and passes through unknown', () => {
		expect(errorKindLabel('blocked')).toBe('Blockiert (Bot-Schutz)');
		expect(errorKindLabel('weird')).toBe('weird');
	});
});

describe('notification helpers', () => {
	it('labels kinds with an icon', () => {
		expect(notificationKind('target_reached')).toEqual({ label: 'Zielpreis erreicht', icon: '★' });
		expect(notificationKind('new_low').label).toBe('Neuer Tiefstpreis');
		expect(notificationKind('mystery')).toEqual({ label: 'mystery', icon: '•' });
	});
	it('labels statuses in German', () => {
		expect(notificationStatus('sent').label).toBe('gesendet');
		expect(notificationStatus('failed')).toEqual({ label: 'fehlgeschlagen', badge: 'badge-bad' });
		expect(notificationStatus('skipped').label).toBe('übersprungen');
		expect(notificationStatus('weird')).toEqual({ label: 'weird', badge: '' });
	});
	it('uses the error as tooltip', () => {
		expect(notificationStatusTitle({ status: 'failed', error: 'HTTP 401', sent_at: null })).toBe('HTTP 401');
		expect(notificationStatusTitle({ status: 'failed', error: null, sent_at: null })).toBe('Unbekannter Fehler');
		expect(notificationStatusTitle({ status: 'skipped', error: null, sent_at: null })).toContain('nicht konfiguriert');
		expect(notificationStatusTitle({ status: 'sent', error: null, sent_at: '2026-11-11T14:00:00Z' })).toMatch(/^Gesendet am /);
		expect(notificationStatusTitle({ status: 'pending', error: null, sent_at: null })).toBeUndefined();
	});
});
