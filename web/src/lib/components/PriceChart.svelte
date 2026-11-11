<script lang="ts" module>
	export interface ChartPoint {
		/** epoch milliseconds */
		x: number;
		y: number;
	}

	export interface ChartSeries {
		label: string;
		points: ChartPoint[];
		/** CSS custom property holding the series color, e.g. "--chart-line" */
		colorVar: string;
		dashed?: boolean;
		stepped?: false | 'before' | 'after';
		fill?: boolean;
		/** decorative series (e.g. target line): no points, no hover */
		decorative?: boolean;
	}
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import type { Chart as ChartType, ChartConfiguration, ChartDataset } from 'chart.js';
	import { de } from 'date-fns/locale';
	import { cssVar, theme } from '$lib/theme.svelte';
	import { formatDateTime } from '$lib/format';

	let {
		series,
		xMin,
		xMax,
		formatY,
		formatTick = formatY,
		ariaLabel,
		height = '340px',
		legend = false,
		emptyText = 'Keine Daten im gewählten Zeitraum.'
	}: {
		series: ChartSeries[];
		xMin?: number;
		xMax?: number;
		formatY: (v: number) => string;
		formatTick?: (v: number) => string;
		ariaLabel: string;
		height?: string;
		legend?: boolean;
		emptyText?: string;
	} = $props();

	let canvas: HTMLCanvasElement | undefined = $state();
	let ChartCtor: typeof ChartType | null = $state(null);
	let chart: ChartType<'line', ChartPoint[]> | null = null;
	let loadError = $state(false);

	const DAY = 86_400_000;

	const dataPoints = $derived(
		series.filter((s) => !s.decorative).reduce((n, s) => n + s.points.length, 0)
	);

	onMount(() => {
		let cancelled = false;
		(async () => {
			try {
				const m = await import('chart.js');
				await import('chartjs-adapter-date-fns');
				m.Chart.register(
					m.LineController,
					m.LineElement,
					m.PointElement,
					m.LinearScale,
					m.TimeScale,
					m.Tooltip,
					m.Legend,
					m.Filler
				);
				if (!cancelled) ChartCtor = m.Chart;
			} catch (e) {
				console.error('chart.js konnte nicht geladen werden', e);
				loadError = true;
			}
		})();
		return () => {
			cancelled = true;
			chart?.destroy();
			chart = null;
		};
	});

	function computeRange(): { min: number | undefined; max: number | undefined } {
		const xs = series.filter((s) => !s.decorative).flatMap((s) => s.points.map((p) => p.x));
		let min = xMin ?? (xs.length ? Math.min(...xs) : undefined);
		let max = xMax ?? (xs.length ? Math.max(...xs) : undefined);
		if (min !== undefined && max !== undefined && max - min < DAY) {
			// a single point (or a very short range): pad so the point is not glued to the edge
			const mid = (min + max) / 2;
			min = mid - DAY / 2;
			max = mid + DAY / 2;
		}
		return { min, max };
	}

	function buildConfig(): ChartConfiguration<'line', ChartPoint[]> {
		const text = cssVar('--chart-text', '#586274');
		const grid = cssVar('--chart-grid', 'rgba(0,0,0,0.08)');
		const surface = cssVar('--surface', '#fff');
		const border = cssVar('--border', '#ddd');
		const textStrong = cssVar('--text', '#111');
		const font = cssVar('--font', 'system-ui');

		const datasets: ChartDataset<'line', ChartPoint[]>[] = series.map((s) => {
			const color = cssVar(s.colorVar, '#0f766e');
			const pts = s.points.map((p) => ({ x: p.x, y: p.y })); // plain copies: Chart.js patches data arrays
			const few = pts.length <= 2;
			return {
				label: s.label,
				data: pts,
				borderColor: color,
				backgroundColor: s.fill ? cssVar('--chart-fill', 'transparent') : color,
				fill: s.fill ? 'start' : false,
				borderWidth: s.decorative ? 1.5 : 2,
				borderDash: s.dashed ? [6, 5] : undefined,
				stepped: s.stepped ?? false,
				tension: 0,
				pointRadius: s.decorative ? 0 : few ? 4 : pts.length < 40 ? 2 : 0,
				pointHoverRadius: s.decorative ? 0 : 5,
				pointHitRadius: s.decorative ? 0 : 6,
				pointBackgroundColor: color,
				spanGaps: true
			};
		});

		const { min, max } = computeRange();

		return {
			type: 'line',
			data: { datasets },
			options: {
				responsive: true,
				maintainAspectRatio: false,
				animation: { duration: 250 },
				parsing: false,
				normalized: true,
				interaction: { mode: 'nearest', axis: 'x', intersect: false },
				font: { family: font },
				scales: {
					x: {
						type: 'time',
						min,
						max,
						adapters: { date: { locale: de } },
						time: {
							minUnit: 'day',
							tooltipFormat: 'Pp',
							displayFormats: {
								hour: 'HH:mm',
								day: 'd. MMM',
								week: 'd. MMM',
								month: 'MMM yyyy',
								quarter: 'QQQ yyyy',
								year: 'yyyy'
							}
						},
						grid: { color: grid },
						border: { color: border },
						ticks: { color: text, maxRotation: 0, autoSkipPadding: 16 }
					},
					y: {
						grace: '8%',
						grid: { color: grid },
						border: { display: false },
						ticks: {
							color: text,
							callback: (v) => formatTick(Number(v))
						}
					}
				},
				plugins: {
					legend: {
						display: legend,
						position: 'bottom',
						labels: {
							color: textStrong,
							usePointStyle: true,
							pointStyle: 'line',
							filter: (item, data) => {
								const s = series[item.datasetIndex ?? -1];
								return !!s && (data.datasets.length > 1 || !s.decorative);
							}
						}
					},
					tooltip: {
						backgroundColor: surface,
						titleColor: textStrong,
						bodyColor: textStrong,
						borderColor: border,
						borderWidth: 1,
						padding: 10,
						displayColors: series.length > 1,
						filter: (item) => !series[item.datasetIndex]?.decorative,
						callbacks: {
							title: (items) => (items[0] ? formatDateTime(items[0].parsed.x ?? 0) : ''),
							label: (item) => {
								const y = item.parsed.y ?? 0;
								return series.length > 1 ? `${item.dataset.label}: ${formatY(y)}` : formatY(y);
							}
						}
					}
				}
			}
		};
	}

	$effect(() => {
		if (!ChartCtor || !canvas) return;
		// Dependencies: series/xMin/xMax (read inside buildConfig) and the theme.
		void theme.effective;
		const cfg = buildConfig();
		if (!chart) {
			chart = new ChartCtor(canvas, cfg) as ChartType<'line', ChartPoint[]>;
		} else {
			chart.data = cfg.data;
			chart.options = cfg.options ?? {};
			chart.update();
		}
	});
</script>

<div class="chart" style:height role="img" aria-label={ariaLabel}>
	<canvas bind:this={canvas}></canvas>
	{#if loadError}
		<p class="overlay">Diagramm konnte nicht geladen werden.</p>
	{:else if dataPoints === 0}
		<p class="overlay">{emptyText}</p>
	{:else if !ChartCtor}
		<p class="overlay"><span class="spinner" aria-hidden="true"></span></p>
	{/if}
</div>

<style>
	.chart {
		position: relative;
		width: 100%;
	}
	canvas {
		display: block;
	}
	.overlay {
		position: absolute;
		inset: 0;
		display: grid;
		place-items: center;
		color: var(--text-muted);
		font-size: 0.925rem;
		text-align: center;
		padding: 1rem;
		pointer-events: none;
	}
</style>
