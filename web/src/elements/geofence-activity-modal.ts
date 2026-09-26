import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { GeofenceService } from '../services/geofence-service.ts';
import { Geofence, VehiclePasses } from '../types/geofence.ts';
import { Vehicle } from '../types/vehicle.ts';
import { formatSpeed } from '../utils/units.ts';
import { formatDwell, tripTimeShort } from '../utils/trips.ts';

/** Selectable scan windows (server caps at 3 days). */
interface ScanWindow { label: () => string; days: number; }
const WINDOWS: ScanWindow[] = [
    { label: () => msg('Last 24 hours'), days: 1 },
    { label: () => msg('Last 3 days'), days: 3 },
];

/** How many vehicles to scan per request, so results stream in progressively. */
const BATCH = 10;

/**
 * geofence-activity-modal — entry point 2: for a chosen window (≤3 days),
 * computes which of the geofence's effective vehicles passed through it, with
 * per-pass enter/exit/dwell/speed. The effective set is fetched from
 * scan-targets, then paged through /passes in batches so results fill in
 * progressively with a scan-progress indicator. Past data is cached server-side,
 * so re-opening the same window returns instantly.
 *
 * Props:
 *   - geofence: the geofence to analyze.
 *   - vehicles: all tenant vehicles (for display names).
 */
@customElement('geofence-activity-modal')
export class GeofenceActivityModal extends LitElement {
    @property({ attribute: false }) geofence!: Geofence;
    @property({ attribute: false }) vehicles: Vehicle[] = [];

    @state() private windowIndex = 0;
    @state() private scanning = false;
    @state() private scanned = 0;
    @state() private total = 0;
    @state() private capped = false;
    @state() private results: VehiclePasses[] = [];
    @state() private errorMessage = '';
    // Bumped on window change / disconnect to abandon an in-flight scan.
    private scanGeneration = 0;

    connectedCallback() {
        super.connectedCallback();
        void this.runScan();
    }

    disconnectedCallback() {
        this.scanGeneration++; // abandon any in-flight scan
        super.disconnectedCallback();
    }

    private async runScan() {
        const gen = ++this.scanGeneration;
        const svc = GeofenceService.getInstance();
        this.scanning = true;
        this.errorMessage = '';
        this.results = [];
        this.scanned = 0;
        this.total = 0;
        this.capped = false;

        const w = WINDOWS[this.windowIndex];
        const now = new Date();
        const from = new Date(now.getTime() - w.days * 24 * 3600 * 1000);
        const fromIso = from.toISOString();
        const toIso = now.toISOString();

        try {
            const targets = await svc.scanTargets(this.geofence.id);
            if (gen !== this.scanGeneration) return;
            this.total = targets.tokenIds.length;
            this.capped = targets.capped;

            for (let i = 0; i < targets.tokenIds.length; i += BATCH) {
                const batch = targets.tokenIds.slice(i, i + BATCH);
                const found = await svc.passes(this.geofence.id, fromIso, toIso, batch);
                if (gen !== this.scanGeneration) return; // window changed / closed
                if (found.length > 0) {
                    // Keep results sorted by token id as they stream in.
                    this.results = [...this.results, ...found].sort((a, b) => a.tokenId - b.tokenId);
                }
                this.scanned = Math.min(i + batch.length, targets.tokenIds.length);
            }
        } catch (err) {
            if (gen !== this.scanGeneration) return;
            console.error('geofence activity scan failed', err);
            this.errorMessage = err instanceof Error ? err.message : msg('Failed to scan geofence activity');
        } finally {
            if (gen === this.scanGeneration) this.scanning = false;
        }
    }

    private onWindowChange(e: Event) {
        this.windowIndex = Number((e.target as HTMLSelectElement).value);
        void this.runScan();
    }

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private vehicleTitle(tokenId: number): string {
        const v = this.vehicles.find((x) => x.tokenId === tokenId);
        if (!v) return msg(str`Vehicle #${tokenId}`);
        const d = v.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : msg(str`Vehicle #${tokenId}`);
    }

    /** Per-vehicle grouping-row header: bold name + plate + VIN + pass count. */
    private renderVehicleHead(tokenId: number, passCount: number) {
        const v = this.vehicles.find((x) => x.tokenId === tokenId);
        return html`
            <div class="veh-head">
                <span class="name">${this.vehicleTitle(tokenId)}</span>
                ${v?.licensePlate
                    ? html`<span class="plate" title=${msg('License plate')}>
                        <span class="material-symbols-outlined">directions_car</span>${v.licensePlate}
                    </span>`
                    : nothing}
                ${v?.vin
                    ? html`<span class="vin" title=${msg('VIN')}>${v.vin}</span>`
                    : nothing}
                <span class="count">${passCount}×</span>
            </div>
        `;
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                --modal-bg: var(--surface-overlay);
                position: fixed; inset: 0; z-index: 100;
                display: flex; align-items: center; justify-content: center;
                padding: 16px;
                background: var(--scrim);
                backdrop-filter: blur(8px);
                -webkit-backdrop-filter: blur(8px);
            }
            .card {
                width: 100%; max-width: 680px; max-height: calc(100vh - 40px);
                background: var(--modal-bg); border: none;
                border-radius: var(--radius-xl); box-shadow: var(--shadow-float);
                padding: 24px; color: var(--on-surface);
                position: relative; display: flex; flex-direction: column;
            }
            h2 {
                font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary);
                margin-bottom: 4px; padding-right: 40px;
                display: flex; align-items: center; gap: 10px;
            }
            h2 .dot {
                width: 12px; height: 12px; border-radius: var(--radius-full); flex-shrink: 0;
                background: var(--c);
                box-shadow: 0 0 8px color-mix(in srgb, var(--c) 55%, transparent);
            }
            .close {
                position: absolute; top: 16px; right: 16px;
                width: 36px; height: 36px;
                display: flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .close .material-symbols-outlined { font-size: 20px; }

            .controls { display: flex; align-items: center; gap: 10px; margin: 12px 0 14px; }
            .controls select {
                height: 36px; padding: 0 12px;
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface);
                border-radius: var(--radius-full);
            }

            .progress { display: flex; align-items: center; gap: 12px; font: var(--type-body-sm); color: var(--on-surface-variant); }
            .bar { flex: 1; height: 4px; border-radius: var(--radius-full); background: var(--surface-container-high); overflow: hidden; }
            .bar > i { display: block; height: 100%; border-radius: inherit; background: var(--progress-fill); transition: width 0.25s ease; }
            .capped { font: var(--type-body-sm); color: var(--warning); margin: 8px 0 0; display: flex; gap: 6px; align-items: flex-start; }
            .capped .material-symbols-outlined { font-size: 16px; margin-top: 2px; }

            /* Legend clarifying the two duration-like columns (users confused
               dwell-inside vs engine-on time). */
            .legend { display: flex; flex-wrap: wrap; gap: 4px 16px; margin: 10px 0 0; font: var(--type-label); color: var(--on-surface-variant); }
            .legend b { color: var(--on-surface); font-weight: 500; }

            .list { flex: 1; overflow-y: auto; margin: 12px -8px 0; padding: 0 8px; }
            .list::-webkit-scrollbar { width: 6px; }
            .list::-webkit-scrollbar-thumb { background-color: var(--outline-variant); border-radius: 10px; }

            table.passes { width: 100%; border-collapse: collapse; }
            .passes thead th {
                position: sticky; top: 0; z-index: 1;
                /* Opaque so rows scroll under it; same tone as the modal. */
                background: var(--modal-bg);
                text-align: left; padding: 10px 12px; white-space: nowrap;
                color: var(--on-surface-variant); font: var(--type-label);
                box-shadow: inset 0 -1px 0 var(--outline-variant);
            }
            .passes th.num, .passes td.num { text-align: right; }
            .passes tbody td {
                height: 44px; padding: 0 12px; white-space: nowrap;
                font: var(--type-body-sm); color: var(--on-surface);
                border-bottom: 1px solid var(--outline-variant);
            }
            .pass-row:hover td { background: var(--surface-container); }
            /* Per-vehicle grouping row spanning the table width. */
            .veh-row td { height: auto; padding: 20px 12px 8px; }
            .passes tbody:first-of-type .veh-row td { padding-top: 12px; }
            .veh-head { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 10px; }
            .veh-head .name { font: 600 15px/22px var(--font-headline); color: var(--primary); }
            /* Plate chip per the design spec. */
            .veh-head .plate {
                display: inline-flex; align-items: center; gap: 4px; padding: 1px 6px;
                border-radius: 5px; background: var(--surface-container-highest);
                font: 600 11px/16px var(--font-body); letter-spacing: 0.06em; color: var(--on-surface);
            }
            .veh-head .plate .material-symbols-outlined { font-size: 13px; color: var(--on-surface-variant); }
            .veh-head .vin { font: 400 12px/16px var(--font-body); letter-spacing: 0.02em; color: var(--on-surface-variant); }
            .veh-head .count {
                margin-left: auto; padding: 2px 8px;
                border-radius: var(--radius-sm); background: var(--surface-container-high);
                color: var(--on-surface-variant); font: var(--type-label);
            }
            .pass-row td:first-child { color: var(--on-surface); }
            .pass-row td { color: var(--on-surface-variant); }
            .speed.over { color: var(--error); font-weight: 600; }
            .dash { color: var(--on-surface-variant); }

            .empty-state { color: var(--on-surface-variant); font: var(--type-body-sm); padding: 32px 24px; text-align: center; }
            .error-text {
                padding: 10px 12px; margin-top: 12px;
                background: var(--error-container); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm);
            }
        `,
    ];

    private renderPassRow(p: VehiclePasses['passes'][number]) {
        const max = p.maxSpeedKph != null ? formatSpeed(p.maxSpeedKph) : null;
        const dash = html`<span class="dash">—</span>`;
        return html`
            <tr class="pass-row">
                <td>${tripTimeShort(p.enteredAt)}</td>
                <td>${tripTimeShort(p.exitedAt)}</td>
                <td class="num">${formatDwell(p.dwellS)}</td>
                <td class="num">
                    ${max
                        ? html`<span class="speed ${p.speedExceeded ? 'over' : ''}">${max.value} ${max.unit}</span>`
                        : dash}
                </td>
                <td class="num">${p.obdRuntimeS != null ? formatDwell(p.obdRuntimeS) : dash}</td>
            </tr>
        `;
    }

    render() {
        const pct = this.total > 0 ? Math.round((this.scanned / this.total) * 100) : 0;
        const passCount = this.results.reduce((n, r) => n + r.passes.length, 0);
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2 style="--c:${this.geofence.color}"><span class="dot"></span>${this.geofence.name}</h2>

                <div class="controls">
                    <select @change=${this.onWindowChange} .value=${String(this.windowIndex)}>
                        ${WINDOWS.map((w, i) => html`<option value=${i} ?selected=${i === this.windowIndex}>${w.label()}</option>`)}
                    </select>
                </div>

                ${this.scanning
                    ? html`<div class="progress">
                            <span>${msg(str`Scanning ${this.scanned} of ${this.total} vehicles…`)}</span>
                            <span class="bar"><i style="width:${pct}%"></i></span>
                        </div>`
                    : html`<div class="progress">
                            <span>${msg(str`${this.results.length} of ${this.total} vehicles passed through · ${passCount} passes`)}</span>
                        </div>`}
                ${this.capped
                    ? html`<p class="capped"><span class="material-symbols-outlined">info</span>
                        ${msg(str`This geofence applies to many vehicles — scanning the first ${this.total}.`)}</p>`
                    : nothing}

                ${this.results.length > 0
                    ? html`<p class="legend">
                        <span><b>${msg('Duration')}</b> — ${msg('time inside the geofence')}</span>
                        <span><b>${msg('Engine runtime')}</b> — ${msg('engine-on time during the pass')}</span>
                    </p>`
                    : nothing}

                <div class="list">
                    ${this.results.length === 0 && !this.scanning
                        ? html`<p class="empty-state">${msg('No vehicles passed through in this window.')}</p>`
                        : this.results.length === 0
                            ? nothing
                            : html`<table class="passes">
                                <thead>
                                    <tr>
                                        <th>${msg('Time in')}</th>
                                        <th>${msg('Time out')}</th>
                                        <th class="num">${msg('Duration')}</th>
                                        <th class="num">${msg('Max speed')}</th>
                                        <th class="num">${msg('Engine runtime')}</th>
                                    </tr>
                                </thead>
                                ${this.results.map((r) => html`
                                    <tbody>
                                        <tr class="veh-row">
                                            <td colspan="5">${this.renderVehicleHead(r.tokenId, r.passes.length)}</td>
                                        </tr>
                                        ${r.passes.map((p) => this.renderPassRow(p))}
                                    </tbody>
                                `)}
                            </table>`}
                </div>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'geofence-activity-modal': GeofenceActivityModal;
    }
}
