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

    static styles = [
        sharedStyles,
        css`
            :host {
                position: fixed; inset: 0; z-index: 100;
                display: flex; align-items: center; justify-content: center;
                background: rgba(0, 0, 0, 0.6); backdrop-filter: blur(4px);
            }
            .card {
                width: 100%; max-width: 560px; max-height: 82vh;
                background: var(--surface-container); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg); padding: 24px; color: var(--on-surface);
                position: relative; display: flex; flex-direction: column;
            }
            h2 { font: var(--type-headline-md); margin-bottom: 4px; display: flex; align-items: center; gap: 10px; }
            h2 .dot { width: 14px; height: 14px; border-radius: var(--radius-full); }
            .close {
                position: absolute; top: 16px; right: 16px;
                background: none; border: none; color: var(--on-surface-variant); padding: 4px; cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            .controls { display: flex; align-items: center; gap: 10px; margin: 12px 0; }
            .controls select {
                background: var(--surface-container-high); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md); color: var(--on-surface); font: var(--type-body-sm); padding: 8px 10px;
            }
            .controls select:focus { outline: 1px solid var(--primary); }

            .progress { display: flex; align-items: center; gap: 8px; font: var(--type-body-sm); color: var(--on-surface-variant); }
            .bar { flex: 1; height: 4px; border-radius: var(--radius-full); background: var(--surface-container-high); overflow: hidden; }
            .bar > i { display: block; height: 100%; background: var(--primary); transition: width 0.25s ease; }
            .capped { font: var(--type-body-sm); color: #f5c84b; margin: 4px 0 0; display: flex; gap: 6px; align-items: flex-start; }
            .capped .material-symbols-outlined { font-size: 16px; margin-top: 1px; }

            .list { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 10px; margin-top: 12px; }
            .list::-webkit-scrollbar { width: 6px; }
            .list::-webkit-scrollbar-thumb { background-color: var(--outline-variant); border-radius: 10px; }

            .veh {
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                background: var(--surface-container-low); padding: 12px;
            }
            .veh .title { font: var(--type-body-md); color: var(--primary); margin-bottom: 6px; }
            .pass {
                display: flex; align-items: center; justify-content: space-between; gap: 10px;
                font: var(--type-body-sm); color: var(--on-surface-variant); padding: 3px 0;
            }
            .pass .times { display: inline-flex; align-items: center; gap: 5px; white-space: nowrap; }
            .pass .times .material-symbols-outlined { font-size: 12px; }
            .pass .meta { display: inline-flex; align-items: center; gap: 10px; white-space: nowrap; }
            .speed { display: inline-flex; align-items: center; gap: 3px; }
            .speed.over { color: var(--error); font-weight: 600; }
            .speed .material-symbols-outlined { font-size: 13px; }

            .empty-state { color: var(--on-surface-variant); font: var(--type-body-sm); padding: 24px; text-align: center; }
            .error-text {
                padding: 12px; background: rgba(255, 180, 171, 0.04);
                border: 1px solid rgba(255, 180, 171, 0.2); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm); margin-top: 12px;
            }
            .footer { display: flex; justify-content: flex-end; margin-top: 16px; }
            .footer button {
                padding: 10px 18px; border-radius: var(--radius-md);
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700;
                border: 1px solid transparent; background: var(--primary); color: var(--on-primary); cursor: pointer;
            }
        `,
    ];

    private renderPass(p: VehiclePasses['passes'][number]) {
        const max = p.maxSpeedKph != null ? formatSpeed(p.maxSpeedKph) : null;
        return html`
            <div class="pass">
                <span class="times">
                    ${tripTimeShort(p.enteredAt)}
                    <span class="material-symbols-outlined">arrow_forward</span>
                    ${tripTimeShort(p.exitedAt)}
                </span>
                <span class="meta">
                    <span>${formatDwell(p.dwellS)}</span>
                    ${max ? html`<span class="speed ${p.speedExceeded ? 'over' : ''}">
                        ${p.speedExceeded ? html`<span class="material-symbols-outlined">speed</span>` : nothing}${max.value} ${max.unit}
                    </span>` : nothing}
                </span>
            </div>
        `;
    }

    render() {
        const pct = this.total > 0 ? Math.round((this.scanned / this.total) * 100) : 0;
        const passCount = this.results.reduce((n, r) => n + r.passes.length, 0);
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2><span class="dot" style="background:${this.geofence.color}"></span>${this.geofence.name}</h2>

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

                <div class="list">
                    ${this.results.length === 0 && !this.scanning
                        ? html`<p class="empty-state">${msg('No vehicles passed through in this window.')}</p>`
                        : this.results.map((r) => html`
                            <div class="veh">
                                <div class="title">${this.vehicleTitle(r.tokenId)}</div>
                                ${r.passes.map((p) => this.renderPass(p))}
                            </div>
                        `)}
                </div>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <div class="footer">
                    <button @click=${this.dispatchClose}>${msg('Done')}</button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'geofence-activity-modal': GeofenceActivityModal;
    }
}
