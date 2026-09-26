import { LitElement, html, css, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { msg, str } from '@lit/localize';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import { Vehicle } from '../types/vehicle.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { SignalLatest, TimeSeriesBucket, Trip } from '../types/telemetry.ts';
import { PrefsService } from '../services/prefs-service.ts';
import {
    formatDistance,
    formatHours,
    formatPercent,
    formatSpeed,
    formatTemperature,
    formatVoltage,
} from '../utils/units.ts';
import { tripDurationMs } from '../utils/trips.ts';
import { GrantLinkController } from '../utils/grant-link.ts';
import '../elements/vehicle-trips-panel.ts';
import '../elements/vehicle-behavior-panel.ts';

interface ChartBar {
    height: number;    // 0..100, normalized to the max in the series
    value: number | null;
    label: string;     // day-of-week, short
    tooltip: string;
}

@customElement('vehicle-details-view')
export class VehicleDetailsView extends LitElement {
    @property({ type: String }) tenantId = '';
    @property({ type: String }) tokenId: string = '';
    @state() private vehicle: Vehicle | null = null;
    @state() private loading = true;

    @state() private latestSignals: Record<string, SignalLatest> = {};
    @state() private speedBuckets: TimeSeriesBucket[] = [];
    @state() private distanceBuckets: TimeSeriesBucket[] = [];
    @state() private telemetryPermissionsRequired = false;
    @state() private telemetryDevLicense = '';
    // "Grant permissions" in the permissions banner: the fleet license's
    // sharing link, or the setup hint (see GrantLinkController).
    private readonly grant = new GrantLinkController(this);
    @state() private favoriteBusy = false;
    // Detected trips over the last 7 days, for the Utilization (driving time)
    // card. The trips panel fetches its own (selectable) window separately.
    @state() private weekSegments: Trip[] = [];
    @state() private weekSegmentsLoaded = false;
    // Which header tab is highlighted; tabs scroll to in-page sections.
    @state() private activeTab: 'overview' | 'trips' | 'status' = 'overview';

    private goToSection(e: Event, tab: 'overview' | 'trips' | 'status') {
        e.preventDefault();
        this.activeTab = tab;
        if (tab === 'overview') {
            this.renderRoot.querySelector('.canvas')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
            return;
        }
        this.renderRoot.querySelector(`#${tab}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }

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

        const [latestRes, speedRes, distRes, segmentsRes] = await Promise.allSettled([
            TelemetryService.getInstance().latest(tokenIdNum),
            TelemetryService.getInstance().timeSeries(tokenIdNum, 'speed', fromIso, toIso, '24h'),
            TelemetryService.getInstance().timeSeries(
                tokenIdNum,
                'powertrainTransmissionTravelledDistance',
                fromIso, toIso, '24h',
            ),
            TelemetryService.getInstance().segments(tokenIdNum, fromIso, toIso),
        ]);

        if (latestRes.status === 'fulfilled') {
            this.latestSignals = latestRes.value.signals || {};
            this.telemetryPermissionsRequired = !!latestRes.value.permissionsRequired;
            this.telemetryDevLicense = latestRes.value.devLicense || '';
            this.grant.setLicense(this.telemetryDevLicense);
        } else {
            console.warn('latest telemetry failed', latestRes.reason);
        }
        if (speedRes.status === 'fulfilled') this.speedBuckets = speedRes.value.buckets || [];
        if (distRes.status === 'fulfilled')  this.distanceBuckets = distRes.value.buckets || [];
        if (segmentsRes.status === 'fulfilled') {
            this.weekSegments = segmentsRes.value.segments || [];
            this.weekSegmentsLoaded = true;
        }

        this.loading = false;
    }

    /**
     * Toggle favorite status for this vehicle, persisted server-side for the
     * tenant ("account") and shared across its members. Optimistically updates
     * the button and invalidates the fleet map cache so the pinned ordering
     * and star badge reflect the change next time the user views it.
     */
    private async toggleFavorite() {
        if (!this.vehicle || this.favoriteBusy) return;
        const next = !this.vehicle.isFavorite;
        this.favoriteBusy = true;
        const previous = this.vehicle;
        this.vehicle = { ...this.vehicle, isFavorite: next };
        try {
            if (next) {
                await ApiService.getInstance().post(`/vehicles/${this.tokenId}/favorite`, {});
            } else {
                await ApiService.getInstance().delete(`/vehicles/${this.tokenId}/favorite`);
            }
            FleetCache.invalidate();
        } catch (e) {
            console.error('Failed to toggle favorite', e);
            this.vehicle = previous;
        } finally {
            this.favoriteBusy = false;
        }
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
                tooltip: typeof v === 'number' ? `${this.weekdayLong(b.timestamp)}: ${formatValue(v)}` : msg('No data'),
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
                <div class="chart-empty-overlay">${msg('No data')}</div>
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

            /* Page header: 72px, no divider. Sticky, so it carries the sheet's
               own tone (reads as transparent) to keep scrolled content legible. */
            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 10;
                height: var(--top-bar-height);
                flex-shrink: 0;
                background: var(--background);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
            }
            header.top-bar .left { display: flex; align-items: center; gap: 20px; min-width: 0; }
            header.top-bar h2 {
                font: var(--type-headline-md);
                letter-spacing: -0.01em;
                color: var(--primary);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            /* Segmented control: the sections are one choice, not three links. */
            header.top-bar nav {
                display: flex;
                flex-shrink: 0;
                gap: 2px;
                padding: 3px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
            }
            header.top-bar nav a {
                text-decoration: none;
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface-variant);
                padding: 6px 14px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            header.top-bar nav a:hover { color: var(--on-surface); }
            header.top-bar nav a.active {
                color: var(--primary);
                background: var(--surface-bright);
                box-shadow: var(--shadow-sm);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }
            /* Phones: title + tenant switcher on the first row, the section
               control full-width underneath (scrolls sideways if a locale's
               labels run long). */
            @media (max-width: 768px) {
                header.top-bar {
                    height: auto;
                    min-height: var(--top-bar-height);
                    flex-wrap: wrap;
                    gap: 8px 12px;
                    padding-top: 12px;
                    padding-bottom: 12px;
                }
                header.top-bar .left { display: contents; }
                header.top-bar h2 { flex: 1 1 0; min-width: 0; }
                header.top-bar nav {
                    order: 1;
                    flex: 1 0 100%;
                    min-width: 0;
                    overflow-x: auto;
                    scrollbar-width: none;
                }
                header.top-bar nav::-webkit-scrollbar { display: none; }
                header.top-bar nav a { flex: 1 0 auto; text-align: center; white-space: nowrap; }
            }

            .canvas {
                flex: 1;
                padding: 8px var(--margin-desktop) var(--margin-desktop);
                max-width: var(--container-max-width);
                margin: 0 auto;
                width: 100%;
            }
            @media (max-width: 768px) {
                .canvas { padding: 8px var(--margin-mobile) var(--margin-mobile); }
            }

            /* ---- identity row ---- */
            .hero-status {
                display: flex;
                flex-wrap: wrap;
                align-items: center;
                gap: 8px 12px;
                margin-bottom: 24px;
            }
            .hero-status .favorite-btn {
                margin-left: auto;
                display: inline-flex;
                align-items: center;
                gap: 6px;
                min-height: 36px;
                padding: 0 14px 0 10px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface);
                font: 500 13px/18px var(--font-body);
                transition: background 0.15s ease;
            }
            .hero-status .favorite-btn:hover { background: var(--surface-container-highest); }
            .hero-status .favorite-btn:disabled { opacity: 0.6; cursor: default; }
            .hero-status .favorite-btn .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); }
            .hero-status .favorite-btn .favorite-on {
                color: var(--favorite);
                font-variation-settings: 'FILL' 1, 'wght' 400;
            }
            /* Token id: a quiet label chip. */
            .hero-status .chip {
                display: inline-flex;
                align-items: center;
                gap: 4px;
                padding: 3px 8px 3px 6px;
                border-radius: var(--radius-sm);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
                font: var(--type-label);
            }
            .hero-status .chip .material-symbols-outlined { font-size: 14px; }
            /* License plate chip, per the plate-chip spec. */
            .hero-status .plate-chip {
                padding: 3px 8px 3px 6px;
                border-radius: 5px;
                background: var(--surface-container-highest);
                color: var(--on-surface);
                font: 600 11px/16px var(--font-body);
                letter-spacing: 0.06em;
                cursor: default;
            }
            .hero-status .plate-chip .material-symbols-outlined { font-size: 13px; color: var(--on-surface-variant); }
            .hero-status .plate-chip .plate { color: var(--on-surface); }
            .hero-status .meta {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                gap: 10px;
            }
            .hero-status .meta .dot {
                width: 3px;
                height: 3px;
                border-radius: var(--radius-full);
                background: var(--outline);
            }

            /* Group chips: 12px/500 text on a 14% tint of the group color
               (--gc, set inline) with a 6px dot. */
            .hero-groups {
                display: flex;
                flex-wrap: wrap;
                align-items: center;
                gap: 6px;
                margin: -12px 0 24px;
            }
            .hero-groups .group-chip {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                padding: 4px 10px 4px 8px;
                border-radius: var(--radius-full);
                background: color-mix(in srgb, var(--gc) 14%, transparent);
                color: var(--on-surface);
                font: var(--type-label);
                white-space: nowrap;
                text-decoration: none;
                cursor: pointer;
                transition: background 0.15s ease;
            }
            .hero-groups .group-chip:hover { background: color-mix(in srgb, var(--gc) 24%, transparent); }
            .hero-groups .group-chip .dot {
                width: 6px;
                height: 6px;
                border-radius: var(--radius-full);
                background: var(--gc);
                flex-shrink: 0;
            }

            .grid {
                display: grid;
                grid-template-columns: repeat(12, 1fr);
                gap: 16px;
                margin-bottom: 48px;
            }
            @media (max-width: 768px) {
                .grid { grid-template-columns: 1fr; }
            }

            /* ---- cards: tonal surfaces, no outline ---- */
            .data-card {
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
                padding: 20px;
                min-width: 0;
                transition: background 0.15s ease;
            }
            .data-card:hover { background: var(--surface-container); }
            .data-card h4 {
                font: 600 15px/22px var(--font-headline);
                color: var(--primary);
            }
            .data-card-head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-bottom: 20px;
            }
            .data-card-head .material-symbols-outlined { font-size: 20px; color: var(--on-surface-variant); }

            /* Tab jumps land below the sticky header, not under it. */
            #trips, #behavior, #status { scroll-margin-top: calc(var(--top-bar-height) + 8px); }
            /* The phone header wraps to two rows (~96-104px). */
            @media (max-width: 768px) {
                #trips, #behavior, #status { scroll-margin-top: 112px; }
            }

            .col-12 { grid-column: span 12; }
            .col-6  { grid-column: span 6; }
            .col-4  { grid-column: span 4; }
            .col-3  { grid-column: span 3; }
            @media (max-width: 768px) {
                .col-12, .col-6, .col-4, .col-3 { grid-column: span 1; }
            }

            /* Section titles: sentence case headline, not tiny labels. */
            .section-label,
            .section-headline {
                /* Full row in any column count: span 12 in the phones' 1fr grid
                   adds 11 implicit tracks and scrolls the page sideways. */
                grid-column: 1 / -1;
                margin-top: 24px;
                font: var(--type-headline-md);
                letter-spacing: -0.01em;
                color: var(--primary);
            }

            .stat-row {
                display: flex;
                gap: 32px;
                flex: 1;
                height: 100%;
                min-width: 0;
            }
            .stat-col {
                display: flex;
                flex-direction: column;
                justify-content: space-between;
                flex-shrink: 0;
            }
            .stat-label {
                font: var(--type-label);
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
            .stat-value-lg .unit,
            .stat-value-md .unit {
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
                letter-spacing: -0.02em;
                color: var(--primary);
            }

            /* ---- bar charts ---- */
            .chart {
                flex: 1;
                min-width: 0;
                display: flex;
                align-items: flex-end;
                justify-content: space-between;
                gap: 4px;
                height: 100%;
                padding-bottom: 4px;
                position: relative;
                overflow: hidden;
            }
            .chart.narrow { gap: 3px; }
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
                border-radius: 4px 4px 1px 1px;
                min-height: 2px;
                transition: height 0.45s cubic-bezier(0.16, 1, 0.3, 1), opacity 0.2s ease;
            }
            /* Data series are calm sky; mint stays reserved for live/actionable. */
            .bar.orange,
            .bar.blue {
                background: linear-gradient(to top, color-mix(in srgb, var(--data-1) 20%, transparent) 0%, var(--data-1) 100%);
            }
            .bar.green { background: linear-gradient(to top, var(--accent-soft) 0%, var(--accent) 100%); }
            .bar-col:hover .bar:not(.ghost) { filter: brightness(1.12); }
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
                font: 500 10px/14px var(--font-body);
                color: var(--on-surface-variant);
                text-align: center;
                margin-top: 6px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: clip;
            }
            .chart.empty .bar-label { color: transparent; }

            .chart-empty-overlay {
                position: absolute;
                inset: 0;
                display: flex;
                align-items: center;
                justify-content: center;
                font: var(--type-label);
                color: var(--on-surface-variant);
                pointer-events: none;
            }
            .chart.narrow .chart-empty-overlay { font-size: 11px; }

            /* Animate the value swap on units toggle (subtle) */
            .num { transition: opacity 0.18s ease; }

            .card-tall  { height: 280px; display: flex; flex-direction: column; }
            .card-mid   { height: 200px; display: flex; flex-direction: column; justify-content: space-between; }
            .card-short { height: 168px; display: flex; flex-direction: column; justify-content: space-between; }

            .fuel-bar {
                width: 100%;
                height: 8px;
                border-radius: var(--radius-full);
                background: var(--surface-container-highest);
                position: relative;
                overflow: hidden;
            }
            .fuel-bar-fill {
                position: absolute;
                inset: 0 auto 0 0;
                border-radius: var(--radius-full);
                background: var(--data-1);
                transition: width 0.5s ease;
            }

            /* Missing-permissions notice: warning tint, no outline. */
            .grant-setup { font: var(--type-body-sm); color: var(--on-surface-variant); max-width: 320px; }
            .grant-setup code { font: 500 12px/16px var(--font-body); color: var(--on-surface); }
            .perms-banner {
                grid-column: 1 / -1;
                display: flex;
                flex-wrap: wrap;
                align-items: center;
                gap: 16px;
                padding: 16px 20px;
                background: color-mix(in srgb, var(--warning) 10%, transparent);
                border-radius: var(--radius-lg);
            }
            .perms-banner strong { color: var(--primary); font: 600 15px/22px var(--font-body); }
            .perms-banner p { font: var(--type-body-sm); color: var(--on-surface-variant); margin-top: 4px; }
            .perms-banner code { font: 600 12px/16px var(--font-body); color: var(--warning); }
            .perms-banner a.grant {
                flex-shrink: 0;
                display: inline-flex;
                align-items: center;
                gap: 6px;
                min-height: 40px;
                padding: 0 18px;
                border-radius: var(--radius-full);
                background: var(--btn-primary-bg);
                color: var(--btn-primary-fg);
                font: 600 14px/20px var(--font-body);
                text-decoration: none;
                transition: background 0.15s ease;
            }
            .perms-banner a.grant:hover { background: var(--btn-primary-hover); }

            .data-card.placeholder { opacity: 0.55; }
            .placeholder-body p { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 4px; }
            .placeholder-body p.small { font: var(--type-label); }

            .pill-normal {
                padding: 2px 8px;
                border-radius: var(--radius-sm);
                background: color-mix(in srgb, var(--positive) 14%, transparent);
                color: var(--positive);
                font: var(--type-label);
            }

            .distance-row {
                display: flex;
                align-items: flex-end;
                justify-content: space-between;
                height: 100%;
                margin-top: 16px;
                min-width: 0;
            }
            .distance-row .chart { flex: 1; margin-left: 16px; max-width: 60%; }

            .err-engineering {
                position: absolute;
                bottom: 16px;
                right: 16px;
                font-size: 48px;
                color: var(--outline-variant);
                opacity: 0.5;
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
                    <h4>${msg('Speed')}</h4>
                    <span class="material-symbols-outlined">chevron_right</span>
                </div>
                <div class="stat-row">
                    <div class="stat-col">
                        <div>
                            <p class="stat-label">${msg('Top')}</p>
                            <div class="stat-value-lg"><span class="num">${topFmt.value}</span><span class="unit">${topFmt.unit}</span></div>
                        </div>
                        <div>
                            <p class="stat-label">${msg('Average')}</p>
                            <div class="stat-value-md"><span class="num">${avgFmt.value}</span><span class="unit">${avgFmt.unit}</span></div>
                        </div>
                    </div>
                    ${this.renderBarChart(bars, 'orange')}
                </div>
            </div>
        `;
    }

    /**
     * Driving time over the last 7 days, derived from telemetry-api trip
     * segments (sum of segment durations; ongoing trips clipped at now).
     */
    private renderUtilizationCard() {
        const totalMs = this.weekSegments.reduce((sum, t) => sum + tripDurationMs(t), 0);
        const totalH = totalMs / 3_600_000;
        const total = formatHours(totalH, 1);
        const perDay = formatHours(totalH / 7, 1);
        return html`
            <div class="data-card col-6 card-tall">
                <div class="data-card-head">
                    <h4>${msg('Driving time')}</h4>
                    <span class="material-symbols-outlined">schedule</span>
                </div>
                <div class="stat-row">
                    <div class="stat-col">
                        <div>
                            <p class="stat-label">${msg('Total')}</p>
                            <div class="stat-value-lg">
                                <span class="num">${this.weekSegmentsLoaded ? total.value : '—'}</span>
                                <span class="unit">${total.unit}</span>
                            </div>
                        </div>
                        <div>
                            <p class="stat-label">${msg('Avg per day')}</p>
                            <div class="stat-value-md">
                                <span class="num">${this.weekSegmentsLoaded ? perDay.value : '—'}</span>
                                <span class="unit">${perDay.unit}</span>
                            </div>
                        </div>
                    </div>
                    <div class="stat-col">
                        <div>
                            <p class="stat-label">${msg('Trips')}</p>
                            <div class="stat-value-md">
                                <span class="num">${this.weekSegmentsLoaded ? this.weekSegments.length : '—'}</span>
                            </div>
                        </div>
                    </div>
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
                    <h4>${msg('Fuel level')}</h4>
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
                <div class="data-card-head">
                    <h4>${msg('Coolant temperature')}</h4>
                </div>
                <div style="display:flex; justify-content:space-between; align-items:flex-end;">
                    <div class="stat-value-lg"><span class="num">${fmt.value}</span><span class="unit">${fmt.unit}</span></div>
                    ${typeof c === 'number'
                        ? html`<span class="pill-normal">${normal ? msg('Normal') : msg('Check')}</span>`
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
                    <h4>${msg('Distance · 7d')}</h4>
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
                    <h4>${msg('Battery voltage')}</h4>
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
                    <h4>${msg('Error codes')}</h4>
                    <span class="material-symbols-outlined">hourglass_empty</span>
                </div>
                <div>
                    <div class="stat-value-md" style="margin-bottom: 8px;">
                        <span class="num">—</span><span class="unit">${msg('DTCs')}</span>
                    </div>
                    <p class="stat-label">${msg('Not yet wired')}</p>
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
                <div class="data-card-head"><h4>${msg('Odometer')}</h4></div>
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
                        <a href="#" class=${this.activeTab === 'overview' ? 'active' : ''}
                           @click=${(e: Event) => this.goToSection(e, 'overview')}>${msg('Overview')}</a>
                        <a href="#" class=${this.activeTab === 'trips' ? 'active' : ''}
                           @click=${(e: Event) => this.goToSection(e, 'trips')}>${msg('Trips')}</a>
                        <a href="#" class=${this.activeTab === 'status' ? 'active' : ''}
                           @click=${(e: Event) => this.goToSection(e, 'status')}>${msg('Diagnostics')}</a>
                    </nav>
                </div>
                <div class="right">
                    <tenant-switcher .currentTenantId=${this.tenantId}></tenant-switcher>
                </div>
            </header>

            <div class="canvas">
                <div class="hero-status">
                    <div class="chip">
                        <span class="material-symbols-outlined">tag</span>
                        <span>${msg(str`Token #${this.tokenId}`)}</span>
                    </div>
                    ${this.vehicle?.licensePlate
                        ? html`<div class="chip plate-chip" title=${msg('License plate')}>
                            <span class="material-symbols-outlined">directions_car</span>
                            <span class="plate">${this.vehicle.licensePlate}</span>
                        </div>`
                        : ''}
                    ${this.vehicle?.aftermarketDevice
                        ? html`<div class="meta">
                            <span class="dot"></span>
                            <span>${msg(str`Aftermarket device #${this.vehicle.aftermarketDevice.tokenId}`)}</span>
                        </div>`
                        : this.vehicle?.syntheticDevice && this.vehicle.syntheticDevice.tokenId > 0
                            ? html`<div class="meta">
                                <span class="dot"></span>
                                <span>${msg(str`Synthetic device #${this.vehicle.syntheticDevice.tokenId}`)}</span>
                            </div>`
                            : html`<div class="meta">
                                <span class="dot"></span>
                                <span>${msg('No DIMO integration yet')}</span>
                            </div>`
                    }
                    <button class="favorite-btn"
                            ?disabled=${this.favoriteBusy} @click=${() => this.toggleFavorite()}>
                        <span class="material-symbols-outlined ${this.vehicle?.isFavorite ? 'favorite-on' : ''}">
                            ${this.vehicle?.isFavorite ? 'star' : 'star_border'}
                        </span>
                        ${this.vehicle?.isFavorite ? msg('Remove from Favorites') : msg('Make Favorite')}
                    </button>
                </div>

                ${this.vehicle?.groups && this.vehicle.groups.length > 0
                    ? html`<div class="hero-groups">
                        ${this.vehicle.groups.map((g) => html`
                            <a class="group-chip" style="--gc:${g.color}"
                               href="#/${this.tenantId}/stats?group=${encodeURIComponent(g.id)}"
                               title=${msg(str`View vehicles in ${g.name}`)}>
                                <span class="dot"></span>${g.name}
                            </a>
                        `)}
                    </div>`
                    : nothing}

                <div class="grid">
                    <!-- Trips: live mini-map + period picker + detected trips -->
                    <div class="col-12" id="trips">
                        <vehicle-trips-panel .tokenId=${this.tokenId}></vehicle-trips-panel>
                    </div>

                    <!-- Driver behaviour: 30-day event totals, daily stacked bars, all-time footer -->
                    <div class="col-12" id="behavior">
                        <vehicle-behavior-panel .tokenId=${this.tokenId}></vehicle-behavior-panel>
                    </div>

                    <div class="section-label">${msg('Last 7 days')}</div>

                    ${this.telemetryPermissionsRequired ? html`
                        <div class="perms-banner">
                            <div>
                                <strong>${msg('Grant DIMO permissions to see live telemetry on this vehicle.')}</strong>
                                <p>
                                    ${msg(html`
                                    The fleet-lite dev license <code>${this.telemetryDevLicense}</code>
                                    needs SACD permissions on this vehicle before we can read signals from
                                    telemetry-api. Charts below are placeholders until permissions are granted.
                                `)}
                                </p>
                            </div>
                            ${this.grant.render([this.tokenId], 14)}
                        </div>
                    ` : nothing}

                    ${this.renderSpeedCard()}
                    ${this.renderUtilizationCard()}
                    ${this.renderFuelCard()}
                    ${this.renderCoolantCard()}
                    ${this.renderDistanceCard()}

                    <div class="section-headline" id="status">${msg('Vehicle status')}</div>

                    ${this.renderBatteryCard()}
                    ${this.renderErrorCodesPlaceholder()}
                    ${this.renderOdometerCard()}
                    ${this.renderAdBlueCard()}
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
