import { LitElement, html, css, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { Vehicle } from '../types/vehicle.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { SignalLatest, TimeSeriesBucket } from '../types/telemetry.ts';
import { PrefsService } from '../services/prefs-service.ts';
import {
    formatDistance,
    formatPercent,
    formatSpeed,
    formatTemperature,
    formatVoltage,
} from '../utils/units.ts';

interface ChartBar {
    height: number;    // 0..100, normalized to the max in the series
    value: number | null;
    label: string;     // day-of-week, short
    tooltip: string;
}

@customElement('vehicle-details-view')
export class VehicleDetailsView extends LitElement {
    @property({ type: String }) tokenId: string = '';
    @state() private vehicle: Vehicle | null = null;
    @state() private loading = true;

    @state() private latestSignals: Record<string, SignalLatest> = {};
    @state() private speedBuckets: TimeSeriesBucket[] = [];
    @state() private distanceBuckets: TimeSeriesBucket[] = [];
    @state() private telemetryPermissionsRequired = false;
    @state() private telemetryDevLicense = '';

    private unsubscribePrefs: (() => void) | null = null;

    private get vehicleTitle(): string {
        if (!this.vehicle) return `Vehicle #${this.tokenId}`;
        const d = this.vehicle.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : `Vehicle #${this.tokenId}`;
    }

    private async loadAll() {
        this.loading = true;
        this.telemetryPermissionsRequired = false;
        this.latestSignals = {};
        this.speedBuckets = [];
        this.distanceBuckets = [];

        // Identity (typed vehicle)
        try {
            this.vehicle = await ApiService.getInstance().get<Vehicle>(`/vehicles/${this.tokenId}`);
        } catch (e) {
            console.error('Failed to load vehicle', e);
        }

        // Telemetry. We parallelize latest + 7-day speed/distance to keep TTFP low.
        const tokenIdNum = Number(this.tokenId);
        const to = new Date();
        const from = new Date(to.getTime() - 7 * 24 * 60 * 60 * 1000);
        const fromIso = from.toISOString();
        const toIso = to.toISOString();

        const [latestRes, speedRes, distRes] = await Promise.allSettled([
            TelemetryService.getInstance().latest(tokenIdNum),
            TelemetryService.getInstance().timeSeries(tokenIdNum, 'speed', fromIso, toIso, '1d'),
            TelemetryService.getInstance().timeSeries(
                tokenIdNum,
                'powertrainTransmissionTravelledDistance',
                fromIso, toIso, '1d',
            ),
        ]);

        if (latestRes.status === 'fulfilled') {
            this.latestSignals = latestRes.value.signals || {};
            this.telemetryPermissionsRequired = !!latestRes.value.permissionsRequired;
            this.telemetryDevLicense = latestRes.value.devLicense || '';
        } else {
            console.warn('latest telemetry failed', latestRes.reason);
        }
        if (speedRes.status === 'fulfilled') this.speedBuckets = speedRes.value.buckets || [];
        if (distRes.status === 'fulfilled')  this.distanceBuckets = distRes.value.buckets || [];

        this.loading = false;
    }

    connectedCallback() {
        super.connectedCallback();
        this.loadAll();
        this.unsubscribePrefs = PrefsService.getInstance().subscribe(() => this.requestUpdate());
    }

    disconnectedCallback() {
        this.unsubscribePrefs?.();
        super.disconnectedCallback();
    }

    willUpdate(changed: Map<string, unknown>) {
        if (changed.has('tokenId') && this.tokenId && !this.loading) {
            this.loadAll();
        }
    }

    private signalValue(name: string): number | undefined {
        const v = this.latestSignals[name]?.value;
        if (typeof v === 'number') return v;
        if (typeof v === 'string') {
            const n = Number(v);
            return Number.isFinite(n) ? n : undefined;
        }
        return undefined;
    }

    /** Pick top + avg speed from the daily buckets — telemetry-api already aggregated. */
    private speedSummary(): { top?: number; avg?: number } {
        if (this.speedBuckets.length === 0) return {};
        let top = -Infinity, sum = 0, n = 0;
        for (const b of this.speedBuckets) {
            if (b.max > top) top = b.max;
            if (Number.isFinite(b.avg)) { sum += b.avg; n += 1; }
        }
        return {
            top: top === -Infinity ? undefined : top,
            avg: n > 0 ? sum / n : undefined,
        };
    }

    /** Distance over last 7 days as the delta between first and last odometer reading. */
    private distance7d(): number | undefined {
        if (this.distanceBuckets.length < 2) return undefined;
        const first = this.distanceBuckets.find((b) => Number.isFinite(b.last));
        const last = [...this.distanceBuckets].reverse().find((b) => Number.isFinite(b.last));
        if (!first || !last) return undefined;
        const delta = last.last - first.last;
        return delta >= 0 ? delta : undefined;
    }

    /**
     * Build chart bars from time-series buckets. Caller picks how to read
     * the value out of each bucket (e.g. `b => b.max` for Speed, daily-delta
     * for Distance). Returns one bar per non-null source bucket, each with
     * normalized height (5–100), the raw value (for tooltips), and a
     * day-of-week label.
     */
    private chartBars(
        buckets: TimeSeriesBucket[],
        getValue: (b: TimeSeriesBucket, i: number, all: TimeSeriesBucket[]) => number | undefined,
        formatValue: (v: number) => string,
    ): ChartBar[] {
        const rawValues: Array<number | undefined> = buckets.map((b, i, all) => getValue(b, i, all));
        const finiteValues = rawValues.filter((v): v is number => typeof v === 'number' && Number.isFinite(v));
        if (finiteValues.length === 0) return [];
        const max = Math.max(...finiteValues);
        return buckets.map((b, i) => {
            const v = rawValues[i];
            const height = typeof v === 'number' && Number.isFinite(v) && max > 0
                ? Math.max(5, (v / max) * 100)
                : 0;
            return {
                height,
                value: typeof v === 'number' ? v : null,
                label: this.dayLabel(b.timestamp),
                tooltip: typeof v === 'number' ? `${this.weekdayLong(b.timestamp)}: ${formatValue(v)}` : 'No data',
            };
        });
    }

    private dayLabel(iso: string): string {
        if (!iso) return '';
        const d = new Date(iso);
        if (Number.isNaN(d.getTime())) return '';
        return d.toLocaleDateString(undefined, { weekday: 'short' }).slice(0, 3);
    }

    private weekdayLong(iso: string): string {
        if (!iso) return '';
        const d = new Date(iso);
        if (Number.isNaN(d.getTime())) return iso;
        return d.toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });
    }

    /** Render a bar chart with day-of-week labels and per-bar tooltips. */
    private renderBarChart(bars: ChartBar[], color: 'orange' | 'blue' | 'green', density: 'wide' | 'narrow' = 'wide') {
        if (this.loading) {
            return html`<div class="chart ${color} ${density === 'narrow' ? 'narrow' : ''}">
                ${Array.from({ length: density === 'narrow' ? 7 : 8 }).map(() => html`
                    <div class="bar-col">
                        <div class="bar ghost shimmer"></div>
                        <div class="bar-label">&nbsp;</div>
                    </div>
                `)}
            </div>`;
        }
        if (bars.length === 0) {
            return html`<div class="chart ${color} ${density === 'narrow' ? 'narrow' : ''} empty">
                ${Array.from({ length: density === 'narrow' ? 7 : 8 }).map(() => html`
                    <div class="bar-col">
                        <div class="bar ghost"></div>
                        <div class="bar-label">&nbsp;</div>
                    </div>
                `)}
                <div class="chart-empty-overlay">No data</div>
            </div>`;
        }
        return html`<div class="chart ${color} ${density === 'narrow' ? 'narrow' : ''}">
            ${bars.map((b) => html`
                <div class="bar-col" title=${b.tooltip}>
                    <div class="bar ${color} ${b.value == null ? 'missing' : ''}" style="height: ${b.height}%;"></div>
                    <div class="bar-label">${b.label}</div>
                </div>
            `)}
        </div>`;
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: 100%;
                height: 100%;
                overflow-y: auto;
                background: var(--background);
            }

            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 10;
                height: 80px;
                width: 100%;
                background: rgba(19, 19, 19, 0.8);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--margin-desktop);
                border-bottom: 1px solid var(--outline-variant);
            }
            header.top-bar .left { display: flex; align-items: center; gap: 32px; }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            header.top-bar nav { display: flex; gap: 24px; }
            header.top-bar nav a {
                text-decoration: none;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                padding-bottom: 4px;
            }
            header.top-bar nav a.active {
                color: var(--primary);
                border-bottom: 2px solid var(--primary);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }
            .live-tracking {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 8px 16px;
                border-radius: var(--radius-full);
                border: 1px solid var(--outline-variant);
                color: var(--primary);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                background: none;
            }
            .live-tracking:hover { background: var(--surface-container-high); }
            .status-dot {
                width: 8px;
                height: 8px;
                border-radius: var(--radius-full);
                background: var(--tertiary-fixed-dim);
                position: relative;
            }
            .status-dot::after {
                content: '';
                position: absolute;
                inset: -4px;
                background: inherit;
                border-radius: var(--radius-full);
                filter: blur(4px);
                opacity: 0.5;
            }
            .icon-btn {
                color: var(--on-surface-variant);
                background: none;
                border: none;
                padding: 4px;
            }

            .canvas {
                flex: 1;
                padding: var(--margin-desktop);
                max-width: var(--container-max-width);
                margin: 0 auto;
                width: 100%;
            }

            .hero-status {
                display: flex;
                align-items: center;
                gap: 16px;
                margin-bottom: 32px;
            }
            .hero-status .chip {
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
                padding: 4px 12px;
                border-radius: var(--radius-full);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                display: flex;
                align-items: center;
                gap: 8px;
            }
            .hero-status .chip .material-symbols-outlined { font-size: 16px; }
            .hero-status .meta {
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                gap: 8px;
            }
            .hero-status .meta .dot {
                width: 4px;
                height: 4px;
                border-radius: var(--radius-full);
                background: var(--outline-variant);
            }

            .grid {
                display: grid;
                grid-template-columns: repeat(12, 1fr);
                gap: var(--gutter);
                margin-bottom: 48px;
            }
            @media (max-width: 768px) {
                .grid { grid-template-columns: 1fr; }
            }

            .data-card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: var(--gutter);
                transition: background 0.2s ease;
            }
            .data-card:hover { background: var(--surface-container-high); }
            .data-card h4 {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .data-card-head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-bottom: 24px;
            }
            .data-card-head .material-symbols-outlined { color: var(--on-surface-variant); }

            .col-12 { grid-column: span 12; }
            .col-6  { grid-column: span 6; }
            .col-4  { grid-column: span 4; }
            .col-3  { grid-column: span 3; }
            @media (max-width: 768px) {
                .col-12, .col-6, .col-4, .col-3 { grid-column: span 1; }
            }

            .trips-card {
                position: relative;
                grid-column: span 12;
                min-height: 300px;
                border-radius: var(--radius-lg);
                overflow: hidden;
                border: 1px solid var(--outline-variant);
            }
            .trips-bg {
                position: absolute;
                inset: 0;
                background-image: url('https://lh3.googleusercontent.com/aida-public/AB6AXuCUEePP8X7lhZsYuKcY97xNfG4loiGb-LmVCARxTGWO0EQyT5v5ozen4q9d9KfdN_MhsqKS0yFqeFrR4zLYzT3iSl-4nksBYrRgd-AmsnlMKA5U_gZdRNrM2SMq9628-jevQ3MFdQU9cgKBV2CYDD_lPR0fB_w2H7VYjJVZeS-LoqYp4-saTHHer7ouvx0RezGPbPVUFTY0dr4jOcrTMFxanTNy6M4NMo2-wY0-hZ3RsjhKviJ625RW8FKjzcHSbFnHxX1xCCoOT5E');
                background-size: cover;
                background-position: center;
            }
            .trips-content {
                position: absolute;
                inset: 0;
                background: linear-gradient(180deg, rgba(19, 19, 19, 0.2) 0%, rgba(19, 19, 19, 1) 100%);
                padding: var(--gutter);
                display: flex;
                flex-direction: column;
                justify-content: space-between;
            }
            .trips-top {
                display: flex;
                justify-content: space-between;
                align-items: flex-start;
            }
            .trips-top h3 { font: var(--type-headline-md); color: var(--primary); }
            .trips-top .chip {
                background: rgba(53, 53, 52, 0.8);
                backdrop-filter: blur(8px);
                color: var(--primary);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                padding: 4px 12px;
                border-radius: var(--radius-full);
                border: 1px solid var(--outline-variant);
            }
            .trips-bottom .from {
                font: var(--type-body-lg);
                font-weight: 500;
                color: var(--primary);
                margin-bottom: 4px;
            }
            .trips-bottom .to {
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                gap: 8px;
                margin-bottom: 16px;
            }
            .trips-bottom .to .material-symbols-outlined { font-size: 16px; }
            .trips-bottom .stats {
                display: flex;
                gap: 24px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }

            .section-label {
                grid-column: span 12;
                margin-top: 16px;
                font: var(--type-body-lg);
                font-weight: 500;
                color: var(--primary);
            }
            .section-headline {
                grid-column: span 12;
                margin-top: 16px;
                font: var(--type-headline-md);
                color: var(--primary);
                font-weight: 500;
            }

            .stat-row {
                display: flex;
                gap: 32px;
                flex: 1;
                height: 100%;
            }
            .stat-col {
                display: flex;
                flex-direction: column;
                justify-content: space-between;
            }
            .stat-label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .stat-value-lg {
                display: flex;
                align-items: baseline;
                gap: 4px;
            }
            .stat-value-lg .num {
                font: var(--type-data-display);
                letter-spacing: -0.03em;
                color: var(--primary);
            }
            .stat-value-lg .unit {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .stat-value-md {
                display: flex;
                align-items: baseline;
                gap: 4px;
            }
            .stat-value-md .num {
                font: var(--type-headline-lg);
                letter-spacing: -0.01em;
                color: var(--primary);
            }
            .stat-value-md .unit {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }

            .chart {
                flex: 1;
                display: flex;
                align-items: flex-end;
                justify-content: space-between;
                gap: 6px;
                height: 100%;
                padding-bottom: 4px;
                position: relative;
            }
            .chart.narrow { gap: 4px; }
            .chart.empty { opacity: 0.6; }

            .bar-col {
                flex: 1;
                display: flex;
                flex-direction: column;
                align-items: stretch;
                justify-content: flex-end;
                height: 100%;
                min-width: 0;
                position: relative;
            }
            .bar-col[title] { cursor: help; }

            .bar {
                width: 100%;
                border-radius: 3px 3px 0 0;
                min-height: 2px;
                transition: height 0.45s cubic-bezier(0.16, 1, 0.3, 1), opacity 0.2s ease;
            }
            .bar.orange { background: linear-gradient(to top, transparent 0%, rgba(234, 107, 24, 0.35) 30%, var(--secondary-container) 100%); }
            .bar.green  { background: linear-gradient(to top, transparent 0%, rgba(105, 219, 173, 0.3) 30%, var(--tertiary-fixed-dim) 100%); }
            .bar.blue   { background: linear-gradient(to top, transparent 0%, rgba(59, 130, 246, 0.3) 30%, #3b82f6 100%); }
            .bar.missing { opacity: 0; }

            .bar.ghost {
                background: var(--surface-container-high);
                opacity: 0.5;
                height: 40%;
            }
            .bar.ghost.shimmer {
                background: linear-gradient(
                    90deg,
                    var(--surface-container-high) 0%,
                    var(--surface-container-highest) 50%,
                    var(--surface-container-high) 100%
                );
                background-size: 200% 100%;
                animation: shimmer 1.4s ease-in-out infinite;
            }
            @keyframes shimmer {
                0%   { background-position: 200% 0; }
                100% { background-position: -200% 0; }
            }

            .bar-label {
                font-family: var(--font-mono);
                font-size: 9px;
                letter-spacing: 0.08em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                text-align: center;
                margin-top: 6px;
                opacity: 0.7;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            .chart.empty .bar-label { color: transparent; }

            .chart-empty-overlay {
                position: absolute;
                inset: 0;
                display: flex;
                align-items: center;
                justify-content: center;
                font: var(--type-label-caps);
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                pointer-events: none;
            }
            .chart.narrow .chart-empty-overlay { font-size: 9px; }

            /* Animate the value swap on units toggle (subtle) */
            .num { transition: opacity 0.18s ease; }

            .card-tall  { height: 280px; display: flex; flex-direction: column; }
            .card-mid   { height: 200px; display: flex; flex-direction: column; justify-content: space-between; }
            .card-short { height: 180px; display: flex; flex-direction: column; justify-content: space-between; }

            .fuel-bar {
                width: 100%;
                height: 32px;
                border-radius: var(--radius-sm);
                background: var(--surface-container-highest);
                position: relative;
                overflow: hidden;
            }
            .fuel-bar-fill {
                position: absolute;
                inset: 0 auto 0 0;
                background: linear-gradient(to top, var(--secondary-container), rgba(234, 107, 24, 0.4));
                transition: width 0.5s ease;
            }

            .perms-banner {
                grid-column: span 12;
                display: flex;
                align-items: center;
                gap: 16px;
                padding: 16px;
                background: rgba(255, 182, 145, 0.06);
                border: 1px solid rgba(255, 182, 145, 0.2);
                border-radius: var(--radius-md);
                margin-bottom: 16px;
            }
            .perms-banner strong { color: var(--secondary); font: var(--type-body-md); font-weight: 600; }
            .perms-banner p { font: var(--type-body-sm); color: var(--on-surface-variant); margin-top: 4px; line-height: 1.5; }
            .perms-banner code { font-family: var(--font-mono); font-size: 11px; color: var(--secondary); }
            .perms-banner a.grant {
                flex-shrink: 0;
                background: var(--primary);
                color: var(--on-primary);
                padding: 10px 16px;
                border-radius: var(--radius-md);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                text-decoration: none;
                display: inline-flex;
                align-items: center;
                gap: 4px;
            }

            .data-card.placeholder { opacity: 0.55; }
            .placeholder-body p { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 4px; }
            .placeholder-body p.small {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
            }

            .pill-normal {
                padding: 4px 8px;
                border-radius: var(--radius-sm);
                background: rgba(105, 219, 173, 0.1);
                border: 1px solid rgba(105, 219, 173, 0.2);
                color: var(--tertiary-fixed-dim);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
            }

            .distance-row {
                display: flex;
                align-items: flex-end;
                justify-content: space-between;
                height: 100%;
                margin-top: 16px;
            }
            .distance-row .chart { flex: 1; margin-left: 16px; max-width: 60%; }

            .quick-actions {
                max-width: 480px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                overflow: hidden;
                margin-bottom: 96px;
            }
            .quick-actions button {
                width: 100%;
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px;
                background: none;
                border: none;
                border-bottom: 1px solid var(--outline-variant);
                text-align: left;
                color: inherit;
                transition: background 0.15s ease;
            }
            .quick-actions button:last-child { border-bottom: none; }
            .quick-actions button:hover { background: var(--surface-container-high); }
            .quick-actions button .left-group { display: flex; align-items: center; gap: 16px; }
            .quick-actions button .label {
                font: var(--type-body-md);
                color: var(--primary);
            }
            .quick-actions button .material-symbols-outlined.muted { color: var(--on-surface-variant); }

            .err-engineering {
                position: absolute;
                bottom: 16px;
                right: 16px;
                font-size: 48px;
                color: var(--outline-variant);
                opacity: 0.3;
                pointer-events: none;
            }
            .relative { position: relative; overflow: hidden; }
        `,
    ];

    private renderSpeedCard() {
        const { top, avg } = this.speedSummary();
        const topFmt = formatSpeed(top);
        const avgFmt = formatSpeed(avg);
        const bars = this.chartBars(
            this.speedBuckets,
            (b) => b.max,
            (v) => {
                const f = formatSpeed(v);
                return `${f.value} ${f.unit}`;
            },
        );
        return html`
            <div class="data-card col-6 card-tall">
                <div class="data-card-head">
                    <h4>Speed</h4>
                    <span class="material-symbols-outlined">chevron_right</span>
                </div>
                <div class="stat-row">
                    <div class="stat-col">
                        <div>
                            <p class="stat-label">Top</p>
                            <div class="stat-value-lg"><span class="num">${topFmt.value}</span><span class="unit">${topFmt.unit}</span></div>
                        </div>
                        <div>
                            <p class="stat-label">Average</p>
                            <div class="stat-value-md"><span class="num">${avgFmt.value}</span><span class="unit">${avgFmt.unit}</span></div>
                        </div>
                    </div>
                    ${this.renderBarChart(bars, 'orange')}
                </div>
            </div>
        `;
    }

    private renderUtilizationPlaceholder() {
        // Telemetry-api doesn't directly expose "hours driven"; would need to
        // derive from speed-based ignition segments. Defer to a future phase.
        return html`
            <div class="data-card col-6 card-tall placeholder">
                <div class="data-card-head">
                    <h4>Utilization</h4>
                    <span class="material-symbols-outlined">hourglass_empty</span>
                </div>
                <div class="placeholder-body">
                    <p>Driving hours aren't exposed directly by telemetry-api.</p>
                    <p class="small">Will be derived from ignition + speed segments in a future phase.</p>
                </div>
            </div>
        `;
    }

    private renderFuelCard() {
        const pct = this.signalValue('powertrainFuelSystemRelativeLevel');
        const fmt = formatPercent(pct);
        const widthPct = typeof pct === 'number' ? Math.max(0, Math.min(100, pct)) : 0;
        return html`
            <div class="data-card col-4 card-mid">
                <div class="data-card-head">
                    <h4>Fuel level</h4>
                    <span class="material-symbols-outlined">local_gas_station</span>
                </div>
                <div>
                    <div class="stat-value-lg" style="margin-bottom: 16px;">
                        <span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span>
                    </div>
                    <div class="fuel-bar"><div class="fuel-bar-fill" style="width: ${widthPct}%;"></div></div>
                </div>
            </div>
        `;
    }

    private renderCoolantCard() {
        const c = this.signalValue('powertrainCombustionEngineECT');
        const fmt = formatTemperature(c);
        const normal = typeof c === 'number' && c >= 70 && c <= 110;
        return html`
            <div class="data-card col-4 card-mid">
                <div class="data-card-head" style="border-bottom: 1px solid var(--outline-variant); padding-bottom: 16px; margin-bottom: 16px;">
                    <h4>Coolant temperature</h4>
                </div>
                <div style="display:flex; justify-content:space-between; align-items:flex-end;">
                    <div class="stat-value-lg"><span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span></div>
                    ${typeof c === 'number'
                        ? html`<span class="pill-normal">${normal ? 'Normal' : 'Check'}</span>`
                        : nothing}
                </div>
            </div>
        `;
    }

    private renderDistanceCard() {
        const km = this.distance7d();
        const fmt = formatDistance(km);
        // Daily delta = today's last odometer reading - yesterday's. First bucket
        // has no predecessor so it's undefined.
        const bars = this.chartBars(
            this.distanceBuckets,
            (b, i, all) => {
                if (i === 0) return undefined;
                const prev = all[i - 1].last;
                if (!Number.isFinite(prev) || !Number.isFinite(b.last)) return undefined;
                const d = b.last - prev;
                return d >= 0 ? d : undefined;
            },
            (v) => {
                const f = formatDistance(v, 1);
                return `${f.value} ${f.unit}`;
            },
        );
        return html`
            <div class="data-card col-4 card-mid">
                <div class="data-card-head">
                    <h4>Distance · 7d</h4>
                    <span class="material-symbols-outlined">chevron_right</span>
                </div>
                <div class="distance-row">
                    <div class="stat-value-md" style="padding-bottom: 8px;">
                        <span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span>
                    </div>
                    ${this.renderBarChart(bars, 'blue', 'narrow')}
                </div>
            </div>
        `;
    }

    private renderBatteryCard() {
        const v = this.signalValue('lowVoltageBatteryCurrentVoltage');
        const fmt = formatVoltage(v);
        return html`
            <div class="data-card col-3 card-short">
                <div class="data-card-head">
                    <h4>Battery voltage</h4>
                    <span class="material-symbols-outlined">battery_full</span>
                </div>
                <div class="stat-value-md"><span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span></div>
            </div>
        `;
    }

    private renderErrorCodesPlaceholder() {
        return html`
            <div class="data-card col-3 card-short relative placeholder">
                <div class="data-card-head">
                    <h4>Error codes</h4>
                    <span class="material-symbols-outlined">hourglass_empty</span>
                </div>
                <div>
                    <div class="stat-value-md" style="margin-bottom: 8px;">
                        <span class="num">—</span><span class="unit">DTCs</span>
                    </div>
                    <p class="stat-label">Not yet wired</p>
                </div>
                <span class="material-symbols-outlined err-engineering">engineering</span>
            </div>
        `;
    }

    private renderOdometerCard() {
        const km = this.signalValue('powertrainTransmissionTravelledDistance');
        const fmt = formatDistance(km);
        return html`
            <div class="data-card col-3 card-short">
                <div class="data-card-head"><h4>Odometer</h4></div>
                <div class="stat-value-md"><span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span></div>
            </div>
        `;
    }

    private renderAdBlueCard() {
        const pct = this.signalValue('powertrainCombustionEngineDieselExhaustFluidLevel');
        const fmt = formatPercent(pct);
        return html`
            <div class="data-card col-3 card-short">
                <div class="data-card-head"><h4>AdBlue</h4></div>
                <div class="stat-value-md"><span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span></div>
            </div>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <div class="left">
                    <h2>${this.vehicleTitle}</h2>
                    <nav>
                        <a href="#" class="active">Overview</a>
                        <a href="#">Diagnostics</a>
                        <a href="#">Trips</a>
                    </nav>
                </div>
                <div class="right">
                    <button class="live-tracking">
                        <span class="status-dot"></span>
                        Live Tracking
                    </button>
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <button class="icon-btn"><span class="material-symbols-outlined">account_circle</span></button>
                </div>
            </header>

            <div class="canvas">
                <div class="hero-status">
                    <div class="chip">
                        <span class="material-symbols-outlined">tag</span>
                        <span>Token #${this.tokenId}</span>
                    </div>
                    ${this.vehicle?.aftermarketDevice
                        ? html`<div class="meta">
                            <span class="dot"></span>
                            <span>Aftermarket device #${this.vehicle.aftermarketDevice.tokenId}</span>
                        </div>`
                        : this.vehicle?.syntheticDevice && this.vehicle.syntheticDevice.tokenId > 0
                            ? html`<div class="meta">
                                <span class="dot"></span>
                                <span>Synthetic device #${this.vehicle.syntheticDevice.tokenId}</span>
                            </div>`
                            : html`<div class="meta">
                                <span class="dot"></span>
                                <span>No DIMO integration yet</span>
                            </div>`
                    }
                </div>

                <div class="grid">
                    <!-- Trips -->
                    <div class="trips-card">
                        <div class="trips-bg"></div>
                        <div class="trips-content">
                            <div class="trips-top">
                                <h3>Trips</h3>
                                <span class="chip">3h ago</span>
                            </div>
                            <div class="trips-bottom">
                                <p class="from">Las Encinas 641, Cerrillos</p>
                                <p class="to">
                                    <span class="material-symbols-outlined">arrow_forward</span>
                                    <span>Avenida Gladys Marín 6096, Estación Central</span>
                                </p>
                                <div class="stats">
                                    <span>14.3 mi</span>
                                    <span>1h 5m</span>
                                    <span>11:31 AM</span>
                                </div>
                            </div>
                        </div>
                    </div>

                    <div class="section-label">Last 7 days</div>

                    ${this.telemetryPermissionsRequired ? html`
                        <div class="perms-banner">
                            <div>
                                <strong>Grant DIMO permissions to see live telemetry on this vehicle.</strong>
                                <p>
                                    The fleet-lite dev license <code>${this.telemetryDevLicense}</code>
                                    needs SACD permissions on this vehicle before we can read signals from
                                    telemetry-api. Charts below are placeholders until permissions are granted.
                                </p>
                            </div>
                            <a class="grant" href="https://console.dimo.org" target="_blank" rel="noopener">
                                Open DIMO console
                                <span class="material-symbols-outlined" style="font-size:14px;">open_in_new</span>
                            </a>
                        </div>
                    ` : nothing}

                    ${this.renderSpeedCard()}
                    ${this.renderUtilizationPlaceholder()}
                    ${this.renderFuelCard()}
                    ${this.renderCoolantCard()}
                    ${this.renderDistanceCard()}

                    <div class="section-headline">Vehicle status</div>

                    ${this.renderBatteryCard()}
                    ${this.renderErrorCodesPlaceholder()}
                    ${this.renderOdometerCard()}
                    ${this.renderAdBlueCard()}
                </div>

                <div class="quick-actions">
                    <button>
                        <div class="left-group">
                            <span class="material-symbols-outlined muted">star</span>
                            <span class="label">Make favorite</span>
                        </div>
                    </button>
                    <button>
                        <div class="left-group">
                            <span class="material-symbols-outlined muted">wifi</span>
                            <span class="label">Data sources</span>
                        </div>
                        <span class="material-symbols-outlined muted">chevron_right</span>
                    </button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'vehicle-details-view': VehicleDetailsView;
    }
}
