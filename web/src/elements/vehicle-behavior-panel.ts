import { LitElement, html, css, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { msg, str } from '@lit/localize';
import { sharedStyles } from '../global-styles.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { BehaviorDay, BehaviorResponse } from '../types/telemetry.ts';
import { BEHAVIOR_SERIES, BEHAVIOR_DEMO, behaviorTotal, demoBehavior, seriesCount } from '../utils/behavior-events.ts';
import { formatDistance, formatHours } from '../utils/units.ts';

const DAYS = 30;

/**
 * Driving-behaviour card for the vehicle details screen: 30-day totals per
 * series with a per-distance rate, a stacked daily bar chart, and an all-time
 * footer. Renders a single muted line for vehicles whose connection never
 * emits behaviour events (AutoPi / Tesla / HashDog). Self-contained —
 * fetches its own data from /telemetry/:tokenId/behavior.
 */
@customElement('vehicle-behavior-panel')
export class VehicleBehaviorPanel extends LitElement {
    @property({ type: String }) tokenId = '';

    @state() private data: BehaviorResponse | null = null;
    @state() private loading = false;
    @state() private error = false;
    @state() private hoverIdx: number | null = null;

    private loadGeneration = 0;

    updated(changed: Map<string, unknown>) {
        if (changed.has('tokenId') && this.tokenId) void this.load();
    }

    private async load() {
        const gen = ++this.loadGeneration;
        this.loading = true;
        this.error = false;
        this.data = null;
        try {
            let res: BehaviorResponse;
            if (BEHAVIOR_DEMO) {
                await new Promise((r) => setTimeout(r, 400));
                // Token 180895 is the HashDog Camry from the probe — never emits events.
                res = this.tokenId === '180895' ? { supported: false, allTime: [], days: [] } : demoBehavior(DAYS);
            } else {
                res = await TelemetryService.getInstance().behavior(Number(this.tokenId), DAYS);
            }
            if (gen !== this.loadGeneration) return;
            this.data = res;
        } catch (e) {
            console.error('behavior load failed', e);
            if (gen === this.loadGeneration) this.error = true;
        } finally {
            if (gen === this.loadGeneration) this.loading = false;
        }
    }

    // ---- derived -----------------------------------------------------------

    /** Per-event-name totals over the period (series are summed from these). */
    private periodTotals(): Record<string, number> {
        const t: Record<string, number> = {};
        for (const day of this.data?.days ?? []) {
            for (const [k, v] of Object.entries(day.counts)) t[k] = (t[k] ?? 0) + v;
        }
        return t;
    }

    private periodDistanceKm(): number {
        return (this.data?.days ?? []).reduce((s, d) => s + (d.distanceKm ?? 0), 0);
    }

    private periodTrips(): number {
        return (this.data?.days ?? []).reduce((s, d) => s + d.tripCount, 0);
    }

    private periodDriveHours(): number {
        return (this.data?.days ?? []).reduce((s, d) => s + d.driveSeconds, 0) / 3600;
    }

    /** Distance unit in the user's preference; 100 km or 100 mi as the rate base. */
    private rateBase(): { unit: string; perKm: number } {
        const unit = formatDistance(100, 0).unit;
        return { unit, perKm: unit === 'mi' ? 0.621371 : 1 };
    }

    private dayLabel(iso: string, long = false): string {
        const d = new Date(iso + 'T00:00:00');
        return d.toLocaleDateString(undefined, long
            ? { weekday: 'short', month: 'short', day: 'numeric' }
            : { month: 'short', day: 'numeric' });
    }

    private fmtDate(iso: string): string {
        const d = new Date(iso);
        return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
    }

    // ---- render ------------------------------------------------------------

    static styles = [
        sharedStyles,
        css`
            :host { display: block; }
            .card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: var(--gutter);
                display: flex;
                flex-direction: column;
                gap: 20px;
            }
            .head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 12px;
            }
            .head h4 {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                gap: 8px;
            }
            .head h4 .material-symbols-outlined { font-size: 16px; }
            .range {
                font: var(--type-label-caps);
                font-size: 10px;
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                padding: 4px 8px;
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-sm);
            }

            .body {
                display: grid;
                grid-template-columns: 300px 1fr;
                gap: 32px;
                min-height: 220px;
            }
            @media (max-width: 900px) {
                .body { grid-template-columns: 1fr; }
            }

            /* ---- tiles: one row per series ---- */
            .tiles {
                display: flex;
                flex-direction: column;
                gap: 10px;
            }
            .tile {
                position: relative;
                flex: 1;
                padding: 10px 16px 10px 18px;
                border-radius: var(--radius-md);
                background: var(--surface-container);
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 12px;
                overflow: hidden;
            }
            .tile::before {
                content: '';
                position: absolute;
                left: 0; top: 10px; bottom: 10px;
                width: 3px;
                border-radius: 0 2px 2px 0;
                background: var(--c);
            }
            .tile .label {
                font: var(--type-label-caps);
                font-size: 10px;
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                white-space: nowrap;
                margin-bottom: 2px;
            }
            .tile .num {
                font: var(--type-headline-lg);
                font-size: 28px;
                letter-spacing: -0.01em;
                color: var(--primary);
                line-height: 1.1;
                font-variant-numeric: tabular-nums;
            }
            .tile .rate {
                font: var(--type-body-sm);
                font-size: 12px;
                color: var(--on-surface-variant);
                display: flex;
                align-items: baseline;
                gap: 2px;
                white-space: nowrap;
            }
            .tile .rate b { color: var(--on-surface); font-weight: 600; font-size: 14px; font-variant-numeric: tabular-nums; }

            /* ---- chart ---- */
            .chart-wrap { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
            .chart-meta {
                display: flex;
                justify-content: space-between;
                align-items: baseline;
                font: var(--type-label-caps);
                font-size: 10px;
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .chart {
                position: relative;
                flex: 1;
                display: flex;
                align-items: flex-end;
                gap: 3px;
                height: 150px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .gridline {
                position: absolute;
                left: 0; right: 0; top: 0;
                border-top: 1px dashed var(--outline-variant);
                opacity: 0.6;
                pointer-events: none;
            }
            .bar-col {
                flex: 1;
                min-width: 0;
                height: 100%;
                display: flex;
                flex-direction: column;
                justify-content: flex-end;
                position: relative;
                cursor: default;
            }
            .bar-col .hit { position: absolute; inset: 0 -2px; }
            .bar-col:hover .stack { filter: brightness(1.15); }
            .stack {
                display: flex;
                flex-direction: column-reverse;
                height: 100%;
                justify-content: flex-start;
                transition: filter 0.15s ease;
            }
            .seg {
                width: 100%;
                min-height: 0;
                margin-top: 2px;   /* the surface gap between stacked fills */
                transition: height 0.45s cubic-bezier(0.16, 1, 0.3, 1);
            }
            .seg.top { border-radius: 3px 3px 0 0; }
            .seg.first { margin-top: 0; }
            .idle {
                height: 2px;
                background: var(--outline-variant);
                opacity: 0.6;
                border-radius: 1px;
            }
            .labels { display: flex; gap: 3px; }
            .labels span {
                flex: 1;
                min-width: 0;
                font-family: var(--font-mono);
                font-size: 9px;
                letter-spacing: 0.06em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                opacity: 0.7;
                white-space: nowrap;
                text-align: center;
                overflow: visible;
            }
            .legend {
                display: flex;
                flex-wrap: wrap;
                gap: 6px 16px;
                margin-top: 2px;
            }
            .legend span {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                font: var(--type-body-sm);
                font-size: 12px;
                color: var(--on-surface-variant);
            }
            .swatch {
                width: 10px; height: 10px;
                border-radius: 2px;
                background: var(--c);
                flex-shrink: 0;
            }

            /* ---- tooltip ---- */
            .tip {
                position: absolute;
                bottom: calc(100% + 8px);
                z-index: 2;
                min-width: 210px;
                padding: 10px 12px;
                border-radius: var(--radius-md);
                background: var(--surface-container-highest);
                border: 1px solid var(--outline-variant);
                box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
                pointer-events: none;
                font: var(--type-body-sm);
                font-size: 12px;
                color: var(--on-surface);
            }
            .tip .t-date { font-weight: 600; margin-bottom: 6px; color: var(--primary); }
            .tip .t-row {
                display: flex;
                align-items: center;
                gap: 8px;
                justify-content: space-between;
                line-height: 18px;
            }
            .tip .t-row .n { display: inline-flex; align-items: center; gap: 6px; color: var(--on-surface-variant); white-space: nowrap; }
            .tip .t-row b { font-weight: 600; font-variant-numeric: tabular-nums; }
            .tip .t-foot {
                margin-top: 6px;
                padding-top: 6px;
                border-top: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
                display: flex;
                justify-content: space-between;
                gap: 10px;
            }

            /* ---- footer / states ---- */
            .foot {
                display: flex;
                flex-wrap: wrap;
                gap: 6px 18px;
                font: var(--type-body-sm);
                font-size: 12px;
                color: var(--on-surface-variant);
                border-top: 1px solid var(--outline-variant);
                padding-top: 14px;
            }
            .foot b { color: var(--on-surface); font-weight: 600; }
            .foot .sep { opacity: 0.5; }
            .state {
                display: flex;
                align-items: center;
                gap: 8px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                min-height: 48px;
            }
            .state .material-symbols-outlined { font-size: 18px; }

            .ghost {
                border-radius: var(--radius-md);
                background: linear-gradient(90deg, var(--surface-container-high) 0%, var(--surface-container-highest) 50%, var(--surface-container-high) 100%);
                background-size: 200% 100%;
                animation: shimmer 1.4s ease-in-out infinite;
            }
            @keyframes shimmer {
                0%   { background-position: 200% 0; }
                100% { background-position: -200% 0; }
            }
        `,
    ];

    render() {
        return html`
            <div class="card">
                <div class="head">
                    <h4><span class="material-symbols-outlined">speed</span>${msg('Driving behaviour')}</h4>
                    <span class="range">${msg(str`Last ${DAYS} days`)}</span>
                </div>
                ${this.renderBody()}
            </div>
        `;
    }

    private renderBody() {
        if (this.loading) return this.renderLoading();
        if (this.error) {
            return html`<div class="state">
                <span class="material-symbols-outlined">error</span>${msg('Failed to load driving behaviour — check console for details.')}
            </div>`;
        }
        if (!this.data) return nothing;
        if (this.data.permissionsRequired) {
            return html`<div class="state">
                <span class="material-symbols-outlined">lock</span>${msg('Grant DIMO permissions to see driving behaviour on this vehicle.')}
            </div>`;
        }
        if (!this.data.supported) {
            return html`<div class="state">
                <span class="material-symbols-outlined">sensors_off</span>${msg("This vehicle's connection doesn't report driving events.")}
            </div>`;
        }
        return html`
            <div class="body">
                ${this.renderTiles()}
                ${this.renderChart()}
            </div>
            ${this.renderFoot()}
        `;
    }

    private renderLoading() {
        return html`
            <div class="body">
                <div class="tiles">
                    ${BEHAVIOR_SERIES.map(() => html`<div class="tile ghost" style="height:64px"></div>`)}
                </div>
                <div class="chart-wrap"><div class="ghost" style="height:150px"></div></div>
            </div>
        `;
    }

    private renderTiles() {
        const totals = this.periodTotals();
        const km = this.periodDistanceKm();
        const { unit, perKm } = this.rateBase();
        const dist = km * perKm;
        return html`
            <div class="tiles">
                ${BEHAVIOR_SERIES.map((s) => {
                    const n = seriesCount(s, totals);
                    const rate = dist > 0 ? (n / dist) * 100 : null;
                    return html`
                        <div class="tile" style="--c:var(${s.cssVar})">
                            <div>
                                <div class="label">${s.label()}</div>
                                <div class="num">${n.toLocaleString()}</div>
                            </div>
                            <div class="rate">
                                ${rate == null
                                    ? html`<span>${msg('No distance data')}</span>`
                                    : html`<b>${rate.toFixed(1)}</b><span>/100 ${unit}</span>`}
                            </div>
                        </div>
                    `;
                })}
            </div>
        `;
    }

    private renderChart() {
        const days = this.data?.days ?? [];
        const max = Math.max(1, ...days.map((d) => behaviorTotal(d.counts)));
        const n = days.length;
        // Label every 5th day from the right so the newest day is always named.
        const labelIdx = new Set<number>();
        for (let i = n - 1; i >= 0; i -= 5) labelIdx.add(i);
        return html`
            <div class="chart-wrap">
                <div class="chart-meta">
                    <span>${msg('Events per day')}</span>
                    <span>${msg(str`Peak ${max}`)}</span>
                </div>
                <div class="chart" @mouseleave=${() => (this.hoverIdx = null)}>
                    <div class="gridline"></div>
                    ${days.map((d, i) => this.renderColumn(d, i, max, n))}
                </div>
                <div class="labels">
                    ${days.map((d, i) => html`<span>${labelIdx.has(i) ? this.dayLabel(d.date) : ''}</span>`)}
                </div>
                <div class="legend">
                    ${BEHAVIOR_SERIES.map((s) => html`
                        <span><i class="swatch" style="--c:var(${s.cssVar})"></i>${s.label()}</span>
                    `)}
                </div>
            </div>
        `;
    }

    private renderColumn(d: BehaviorDay, i: number, max: number, n: number) {
        const total = behaviorTotal(d.counts);
        // Stack in fixed series order; the last non-zero segment gets the rounded top.
        const segs = BEHAVIOR_SERIES
            .map((s) => ({ s, count: seriesCount(s, d.counts) }))
            .filter((x) => x.count > 0);
        const hovered = this.hoverIdx === i;
        return html`
            <div class="bar-col" @mouseenter=${() => (this.hoverIdx = i)}>
                <div class="hit"></div>
                ${total === 0
                    ? html`<div class="idle"></div>`
                    : html`<div class="stack">
                        ${segs.map((x, j) => html`
                            <div class="seg ${j === 0 ? 'first' : ''} ${j === segs.length - 1 ? 'top' : ''}"
                                 style="height:${(x.count / max) * 100}%;background:var(${x.s.cssVar})"></div>
                        `)}
                    </div>`}
                ${hovered ? this.renderTip(d, i, n) : nothing}
            </div>
        `;
    }

    private renderTip(d: BehaviorDay, i: number, n: number) {
        // Keep the tooltip inside the chart near the edges.
        const pos = i < 4 ? 'left:0' : i > n - 5 ? 'right:0' : 'left:50%;transform:translateX(-50%)';
        const distFv = d.distanceKm != null ? formatDistance(d.distanceKm, 0) : null;
        const hrs = formatHours(d.driveSeconds / 3600, 1);
        return html`
            <div class="tip" style=${pos}>
                <div class="t-date">${this.dayLabel(d.date, true)}</div>
                ${BEHAVIOR_SERIES.map((s) => html`
                    <div class="t-row">
                        <span class="n"><i class="swatch" style="--c:var(${s.cssVar})"></i>${s.label()}</span>
                        <b>${seriesCount(s, d.counts)}</b>
                    </div>
                `)}
                <div class="t-foot">
                    <span>${distFv ? `${distFv.value} ${distFv.unit}` : msg('No distance')}</span>
                    <span>${d.tripCount > 0 ? msg(str`${d.tripCount} trips · ${hrs.value} ${hrs.unit}`) : msg('No trips')}</span>
                </div>
            </div>
        `;
    }

    private renderFoot() {
        const all = this.data?.allTime ?? [];
        const allTotal = all.reduce((s, e) => s + e.count, 0);
        const first = all.map((e) => e.firstSeen).filter(Boolean).sort()[0];
        const distFv = formatDistance(this.periodDistanceKm(), 0);
        const hrs = formatHours(this.periodDriveHours(), 1);
        const trips = this.periodTrips();
        return html`
            <div class="foot">
                <span>${msg(html`<b>${distFv.value} ${distFv.unit}</b> driven`)}</span>
                <span class="sep">·</span>
                <span>${msg(html`<b>${trips}</b> trips`)}</span>
                <span class="sep">·</span>
                <span>${msg(html`<b>${hrs.value} ${hrs.unit}</b> driving time`)}</span>
                ${first ? html`
                    <span class="sep">·</span>
                    <span>${msg(html`<b>${allTotal.toLocaleString()}</b> events since ${this.fmtDate(first)}`)}</span>
                ` : nothing}
            </div>
        `;
    }
}
