import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { hiddenVehiclesService } from '../services/hidden-vehicles-service.ts';
import { TCOService } from '../services/tco-service.ts';
import { VehicleTCOSummary } from '../types/tco.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';
import { categoryLabel } from '../utils/document-categories.ts';

function formatMoney(n: number): string {
    return n.toLocaleString(undefined, { style: 'currency', currency: 'USD' });
}

function vehicleTitle(v: Vehicle): string {
    const d = v.definition;
    const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
    return parts.length ? parts.join(' ') : `Vehicle #${v.tokenId}`;
}

@customElement('tco-view')
export class TCOView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private loading = true;
    @state() private error = '';
    @state() private vehicles: Vehicle[] = [];
    // TCO figures fill in per-vehicle as they arrive (see prefetchTco), so the
    // table paints immediately from the fast /vehicles list rather than
    // blocking on the whole fleet's fetch-api round trips.
    @state() private tcoByToken = new Map<number, VehicleTCOSummary>();
    @state() private exporting = false;
    @state() private hiddenVehicles = new Set<string>();
    @state() private showHidden = false;
    private unsubscribeHidden: (() => void) | null = null;
    private connected = false;
    private static readonly PREFETCH_PARALLEL = 3;
    @state() private detailTokenId: number | null = null;
    @state() private detail: VehicleTCOSummary | null = null;
    @state() private loadingDetail = false;
    @state() private detailError = '';
    @state() private exportingVehicle = false;
    @state() private savingSettings = false;
    @state() private settingsError = '';
    @state() private formPurchasePrice = '';
    @state() private formPurchaseDate = '';
    @state() private formUsefulLifeYears = '';

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

            /* ── Top bar (matches fleet-list-view / groups-management) ─── */
            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                flex-shrink: 0;
                display: flex;
                align-items: center;
                justify-content: space-between;
                height: var(--top-bar-height, 80px);
                padding: 0 var(--gutter);
                background: var(--background);
                border-bottom: 1px solid var(--outline-variant);
            }
            header.top-bar .left { display: flex; align-items: center; gap: 16px; }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }

            .back-link {
                background: none;
                border: none;
                color: var(--on-surface-variant);
                cursor: pointer;
                font: var(--type-body-sm);
                display: flex;
                align-items: center;
                gap: 4px;
                padding: 0;
                transition: color 0.15s ease;
            }
            .back-link:hover { color: var(--primary); }
            .back-link .material-symbols-outlined { font-size: 18px; }

            .export-btn {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 10px 16px;
                border-radius: var(--radius-md);
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                cursor: pointer;
                transition: opacity 0.15s ease;
            }
            .export-btn:hover { opacity: 0.9; }
            .export-btn:disabled { opacity: 0.5; cursor: not-allowed; }

            /* ── Canvas ───────────────────────────────────────────────── */
            .canvas {
                flex: 1;
                width: 100%;
                max-width: var(--container-max-width);
                margin: 0 auto;
                padding: var(--stack-lg) var(--gutter);
                box-sizing: border-box;
            }
            .canvas h1 { font: var(--type-headline-md); color: var(--primary); margin-bottom: var(--stack-md); }

            /* ── Table (matches fleet-list-view) ─────────────────────── */
            .table-wrap {
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                overflow: hidden;
            }
            table { width: 100%; border-collapse: collapse; font: var(--type-body-sm); color: var(--on-surface); }
            thead { background: var(--surface-container-low); }
            th {
                text-align: left;
                padding: 12px 16px;
                font: var(--type-label-caps);
                letter-spacing: 0.06em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                border-bottom: 1px solid var(--outline-variant);
                white-space: nowrap;
            }
            td {
                padding: 14px 16px;
                vertical-align: middle;
                border-bottom: 1px solid var(--outline-variant);
            }
            tbody tr { cursor: pointer; transition: background 0.1s ease; }
            tbody tr:hover { background: var(--surface-container); }
            tbody tr:last-child td { border-bottom: none; }
            td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
            tfoot td {
                font-weight: 700;
                background: var(--surface-container-low);
                border-top: 1px solid var(--outline-variant);
                border-bottom: none;
            }

            /* ── States ───────────────────────────────────────────────── */
            .empty, .loading, .error {
                padding: 64px var(--gutter);
                text-align: center;
                color: var(--on-surface-variant);
                font: var(--type-body-md);
            }
            .error { color: var(--error); }
            .cell-loading { color: var(--on-surface-variant); opacity: 0.5; letter-spacing: 1px; }
            .no-perm-badge {
                display: inline-flex;
                align-items: center;
                gap: 3px;
                font: var(--type-label-caps);
                letter-spacing: 0.04em;
                color: #ffb432;
                font-size: 10px;
            }
            .no-perm-badge .material-symbols-outlined { font-size: 12px; }
            .perms-notice {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 12px 16px;
                background: rgba(255, 180, 52, 0.08);
                border: 1px solid rgba(255, 180, 52, 0.25);
                border-radius: var(--radius-md);
                color: #ffb432;
                font: var(--type-body-sm);
                margin-bottom: var(--stack-lg);
            }
            .perms-notice .material-symbols-outlined { font-size: 18px; flex-shrink: 0; }

            /* ── Hidden vehicles ──────────────────────────────────────── */
            .hidden-toggle-row { display: flex; justify-content: flex-end; margin-bottom: var(--stack-sm); }
            .show-hidden-btn {
                display: flex;
                align-items: center;
                gap: 6px;
                background: none;
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                font: var(--type-body-sm);
                padding: 8px 12px;
                cursor: pointer;
                transition: background 0.15s, color 0.15s, border-color 0.15s;
                white-space: nowrap;
            }
            .show-hidden-btn:hover { color: var(--primary); border-color: var(--primary); }
            .show-hidden-btn.active {
                background: var(--primary-container);
                color: var(--on-primary-container);
                border-color: var(--primary);
            }
            .show-hidden-btn .material-symbols-outlined { font-size: 18px; }
            .show-hidden-btn .hidden-count { font-weight: 700; font-size: 12px; }
            tbody tr.hidden-row { opacity: 0.45; }
            tbody tr.hidden-row:hover { opacity: 0.7; }

            /* ── Vehicle drilldown ────────────────────────────────────── */
            .drilldown-head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-bottom: var(--stack-md);
            }
            .breakdown { display: flex; flex-wrap: wrap; gap: 12px; margin-bottom: var(--stack-lg); }
            .stat {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 16px;
                min-width: 160px;
            }
            .stat .label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .stat .value { font: var(--type-headline-md); color: var(--primary); }

            .settings-form {
                display: flex;
                flex-wrap: wrap;
                gap: 16px;
                align-items: flex-end;
                margin-bottom: var(--stack-lg);
                padding: 16px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
            }
            .settings-form .field { display: flex; flex-direction: column; gap: 6px; }
            .settings-form label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .settings-form input {
                background: var(--surface-container);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 10px 12px;
                font-family: inherit;
                font-size: 14px;
            }
            .settings-form input:focus { outline: 1px solid var(--primary); }
            .settings-form .save-btn {
                padding: 10px 16px;
                border-radius: var(--radius-md);
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                cursor: pointer;
                transition: opacity 0.15s ease;
            }
            .settings-form .save-btn:hover { opacity: 0.9; }
            .settings-form .save-btn:disabled { opacity: 0.5; cursor: not-allowed; }
            .settings-form .form-error { font: var(--type-body-sm); color: var(--error); }

            .section-label {
                font: var(--type-label-caps);
                letter-spacing: 0.06em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: var(--stack-sm);
            }
        `,
    ];

    async connectedCallback() {
        super.connectedCallback();
        this.connected = true;
        this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        this.unsubscribeHidden = hiddenVehiclesService.subscribe(() => {
            this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        });
        this.loading = true;
        this.error = '';
        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            this.vehicles = res.vehicles || [];
        } catch (e) {
            console.error('Failed to load vehicles', e);
            this.error = e instanceof Error ? e.message : msg('Failed to load vehicles');
        } finally {
            this.loading = false;
        }
        // Fire-and-forget: fill in each vehicle's cost figures in the
        // background so the table shows real numbers as they arrive instead
        // of blocking the whole view on the slowest vehicle's fetch-api call.
        void this.prefetchTco();
    }

    disconnectedCallback() {
        this.connected = false;
        this.unsubscribeHidden?.();
        this.unsubscribeHidden = null;
        super.disconnectedCallback();
    }

    /** Vehicles hidden elsewhere (Fleet List) are skipped here too, unless
     * the user has toggled "Show hidden" for this session. */
    private visibleVehicles(): Vehicle[] {
        if (this.showHidden) return this.vehicles;
        return this.vehicles.filter((v) => !this.hiddenVehicles.has(String(v.tokenId)));
    }

    private async prefetchTco() {
        // Skip fetching cost figures for hidden vehicles too — no point
        // spending fetch-api round trips on rows the user won't see.
        const targets = this.visibleVehicles().filter((v) => !this.tcoByToken.has(v.tokenId));
        let next = 0;
        const worker = async () => {
            while (next < targets.length) {
                if (!this.connected) return;
                const v = targets[next++];
                try {
                    const detail = await TCOService.getInstance().getVehicleDetail(v.tokenId);
                    if (!this.connected) return;
                    this.tcoByToken = new Map(this.tcoByToken).set(v.tokenId, detail);
                } catch (e) {
                    console.error('Failed to load TCO for vehicle', v.tokenId, e);
                    // Leave uncached — the row falls back to a loading dash.
                }
            }
        };
        await Promise.all(
            Array.from({ length: Math.min(TCOView.PREFETCH_PARALLEL, targets.length) }, () => worker()),
        );
    }

    private async openVehicle(tokenId: number) {
        this.detailTokenId = tokenId;
        this.loadingDetail = true;
        this.detailError = '';
        this.settingsError = '';
        try {
            // Reuse the prefetched figures if already in hand so the drilldown
            // paints instantly; otherwise fetch fresh (e.g. clicked before its
            // prefetch turn came up).
            const cached = this.tcoByToken.get(tokenId);
            this.detail = cached ?? await TCOService.getInstance().getVehicleDetail(tokenId);
            if (!cached) {
                this.tcoByToken = new Map(this.tcoByToken).set(tokenId, this.detail);
            }
            this.formPurchasePrice = this.detail.settings.purchasePrice?.toString() ?? '';
            this.formPurchaseDate = this.detail.settings.purchaseDate ?? '';
            this.formUsefulLifeYears = this.detail.settings.usefulLifeYears?.toString() ?? '';
        } catch (e) {
            console.error('Failed to load vehicle TCO detail', e);
            this.detailError = e instanceof Error ? e.message : msg('Failed to load vehicle detail');
        } finally {
            this.loadingDetail = false;
        }
    }

    private closeDetail = () => {
        this.detailTokenId = null;
        this.detail = null;
    };

    private async exportFleetCsv() {
        this.exporting = true;
        try {
            await TCOService.getInstance().exportCsv();
        } catch (e) {
            console.error('Failed to export TCO CSV', e);
        } finally {
            this.exporting = false;
        }
    }

    private async saveSettings() {
        if (this.detailTokenId === null) return;
        this.savingSettings = true;
        this.settingsError = '';
        try {
            const price = this.formPurchasePrice.trim() === '' ? undefined : Number(this.formPurchasePrice);
            const life = this.formUsefulLifeYears.trim() === '' ? undefined : Number(this.formUsefulLifeYears);
            await TCOService.getInstance().putSettings({
                tokenId: this.detailTokenId,
                purchasePrice: price !== undefined && !Number.isNaN(price) ? price : undefined,
                purchaseDate: this.formPurchaseDate.trim() === '' ? undefined : this.formPurchaseDate,
                usefulLifeYears: life !== undefined && !Number.isNaN(life) ? life : undefined,
                currency: 'USD',
            });
            this.detail = await TCOService.getInstance().getVehicleDetail(this.detailTokenId);
            this.tcoByToken = new Map(this.tcoByToken).set(this.detailTokenId, this.detail);
        } catch (e) {
            console.error('Failed to save TCO settings', e);
            this.settingsError = e instanceof Error ? e.message : msg('Failed to save');
        } finally {
            this.savingSettings = false;
        }
    }

    private async exportVehicleCsv() {
        if (this.detailTokenId === null) return;
        this.exportingVehicle = true;
        try {
            await TCOService.getInstance().exportCsv(this.detailTokenId);
        } catch (e) {
            console.error('Failed to export vehicle TCO CSV', e);
        } finally {
            this.exportingVehicle = false;
        }
    }

    private renderNum(v: Vehicle, pick: (t: VehicleTCOSummary) => number) {
        const t = this.tcoByToken.get(v.tokenId);
        return t ? formatMoney(pick(t)) : html`<span class="cell-loading">···</span>`;
    }

    /** Operating cost specifically needs fetch-api document access — show why
     * it's $0 when the dev license lacks SACD permissions on this vehicle,
     * rather than a plain (misleadingly complete-looking) dollar figure. */
    private renderOperatingCost(v: Vehicle) {
        const t = this.tcoByToken.get(v.tokenId);
        if (!t) return html`<span class="cell-loading">···</span>`;
        if (t.permissionsRequired) {
            return html`
                <span class="no-perm-badge" title=${msg('DIMO permissions required to read this vehicle’s documents')}>
                    <span class="material-symbols-outlined">lock</span>
                    ${msg('No access')}
                </span>
            `;
        }
        return formatMoney(t.operatingCost);
    }

    private toggleShowHidden() {
        this.showHidden = !this.showHidden;
        if (this.showHidden) {
            // Rows just revealed may not have been prefetched yet (prefetch
            // skips hidden vehicles) — top up in the background.
            void this.prefetchTco();
        }
    }

    private renderFleetTable() {
        const visible = this.visibleVehicles();
        if (this.vehicles.length === 0) {
            return html`
                <div class="empty">
                    <span class="material-symbols-outlined" style="font-size:40px;display:block;margin-bottom:12px;opacity:0.6;">payments</span>
                    ${msg('No vehicles on this account.')}
                </div>
            `;
        }
        if (visible.length === 0) {
            return html`
                <div class="hidden-toggle-row">
                    <button class="show-hidden-btn" @click=${() => this.toggleShowHidden()}>
                        <span class="material-symbols-outlined">visibility_off</span>
                        ${msg('Show hidden vehicles')}
                        <span class="hidden-count">${this.hiddenVehicles.size}</span>
                    </button>
                </div>
                <div class="empty">
                    <span class="material-symbols-outlined" style="font-size:40px;display:block;margin-bottom:12px;opacity:0.6;">visibility_off</span>
                    ${msg('All vehicles on this account are hidden.')}
                </div>
            `;
        }
        const allLoaded = visible.every((v) => this.tcoByToken.has(v.tokenId));
        let fleetOperating = 0, fleetAcquisition = 0, fleetDepreciation = 0, fleetTotal = 0;
        for (const v of visible) {
            const t = this.tcoByToken.get(v.tokenId);
            if (!t) continue;
            fleetOperating += t.operatingCost;
            fleetAcquisition += t.acquisitionCost;
            fleetDepreciation += t.depreciationToDate;
            fleetTotal += t.totalTco;
        }
        return html`
            ${this.hiddenVehicles.size > 0 ? html`
                <div class="hidden-toggle-row">
                    <button class="show-hidden-btn ${this.showHidden ? 'active' : ''}" @click=${() => this.toggleShowHidden()}>
                        <span class="material-symbols-outlined">${this.showHidden ? 'visibility' : 'visibility_off'}</span>
                        ${this.showHidden ? msg('Hide hidden vehicles') : msg('Show hidden vehicles')}
                        <span class="hidden-count">${this.hiddenVehicles.size}</span>
                    </button>
                </div>
            ` : nothing}
            <div class="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>${msg('Vehicle')}</th>
                            <th class="num">${msg('Operating cost')}</th>
                            <th class="num">${msg('Acquisition')}</th>
                            <th class="num">${msg('Depreciation to date')}</th>
                            <th class="num">${msg('Total TCO')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${visible.map((v) => html`
                            <tr class=${this.hiddenVehicles.has(String(v.tokenId)) ? 'hidden-row' : ''}
                                @click=${() => this.openVehicle(v.tokenId)}>
                                <td>${vehicleTitle(v)}</td>
                                <td class="num">${this.renderOperatingCost(v)}</td>
                                <td class="num">${this.renderNum(v, (t) => t.acquisitionCost)}</td>
                                <td class="num">${this.renderNum(v, (t) => t.depreciationToDate)}</td>
                                <td class="num">${this.renderNum(v, (t) => t.totalTco)}</td>
                            </tr>
                        `)}
                    </tbody>
                    <tfoot>
                        <tr>
                            <td>${msg('Fleet total')}</td>
                            <td class="num">${allLoaded ? formatMoney(fleetOperating) : html`<span class="cell-loading">···</span>`}</td>
                            <td class="num">${allLoaded ? formatMoney(fleetAcquisition) : html`<span class="cell-loading">···</span>`}</td>
                            <td class="num">${allLoaded ? formatMoney(fleetDepreciation) : html`<span class="cell-loading">···</span>`}</td>
                            <td class="num">${allLoaded ? formatMoney(fleetTotal) : html`<span class="cell-loading">···</span>`}</td>
                        </tr>
                    </tfoot>
                </table>
            </div>
        `;
    }

    private renderDetail() {
        if (this.loadingDetail) return html`<div class="loading">${msg('Loading…')}</div>`;
        if (this.detailError) return html`<div class="error">${this.detailError}</div>`;
        const d = this.detail;
        if (!d) return nothing;
        const categories = Object.entries(d.costByCategory).sort((a, b) => b[1] - a[1]);
        return html`
            <button class="back-link" @click=${this.closeDetail}>
                <span class="material-symbols-outlined">arrow_back</span>
                ${msg('Back to fleet')}
            </button>

            <div class="drilldown-head">
                <h1>${d.vehicleLabel}</h1>
                <button class="export-btn" ?disabled=${this.exportingVehicle} @click=${() => this.exportVehicleCsv()}>
                    <span class="material-symbols-outlined" style="font-size:18px;">download</span>
                    ${this.exportingVehicle ? msg('Exporting…') : msg('Export CSV')}
                </button>
            </div>

            ${d.permissionsRequired ? html`
                <div class="perms-notice">
                    <span class="material-symbols-outlined">lock</span>
                    ${msg('Grant DIMO permissions to this vehicle to include its documents in operating cost. Acquisition and depreciation below are unaffected.')}
                </div>
            ` : nothing}

            <div class="breakdown">
                <div class="stat"><div class="label">${msg('Operating cost')}</div><div class="value">${formatMoney(d.operatingCost)}</div></div>
                <div class="stat"><div class="label">${msg('Acquisition')}</div><div class="value">${formatMoney(d.acquisitionCost)}</div></div>
                <div class="stat"><div class="label">${msg('Depreciation to date')}</div><div class="value">${formatMoney(d.depreciationToDate)}</div></div>
                <div class="stat"><div class="label">${msg('Total TCO')}</div><div class="value">${formatMoney(d.totalTco)}</div></div>
                ${categories.map(([cat, amount]) => html`
                    <div class="stat"><div class="label">${categoryLabel(cat)}</div><div class="value">${formatMoney(amount)}</div></div>
                `)}
            </div>

            <div class="section-label">${msg('Acquisition & depreciation')}</div>
            <div class="settings-form">
                <div class="field">
                    <label for="price">${msg('Purchase price')}</label>
                    <input id="price" type="text" inputmode="decimal" placeholder="0.00"
                        .value=${this.formPurchasePrice}
                        @input=${(e: Event) => { this.formPurchasePrice = (e.target as HTMLInputElement).value; }} />
                </div>
                <div class="field">
                    <label for="date">${msg('Purchase date')}</label>
                    <input id="date" type="date"
                        .value=${this.formPurchaseDate}
                        @input=${(e: Event) => { this.formPurchaseDate = (e.target as HTMLInputElement).value; }} />
                </div>
                <div class="field">
                    <label for="life">${msg('Useful life (years)')}</label>
                    <input id="life" type="text" inputmode="numeric" placeholder="10"
                        .value=${this.formUsefulLifeYears}
                        @input=${(e: Event) => { this.formUsefulLifeYears = (e.target as HTMLInputElement).value; }} />
                </div>
                <button class="save-btn" ?disabled=${this.savingSettings} @click=${() => this.saveSettings()}>
                    ${this.savingSettings ? msg('Saving…') : msg('Save')}
                </button>
                ${this.settingsError ? html`<span class="form-error">${this.settingsError}</span>` : nothing}
            </div>

            <div class="section-label">${msg('Line items')}</div>
            ${d.lineItems.length === 0
                ? html`<div class="empty">${msg('No cost documents on file for this vehicle yet.')}</div>`
                : html`
                    <div class="table-wrap">
                        <table>
                            <thead>
                                <tr>
                                    <th>${msg('Date')}</th>
                                    <th>${msg('Category')}</th>
                                    <th>${msg('Description')}</th>
                                    <th class="num">${msg('Amount')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                ${d.lineItems.map((li) => html`
                                    <tr>
                                        <td>${new Date(li.date).toLocaleDateString()}</td>
                                        <td>${categoryLabel(li.category)}</td>
                                        <td>${li.description}</td>
                                        <td class="num">${formatMoney(li.amount)}</td>
                                    </tr>
                                `)}
                            </tbody>
                        </table>
                    </div>
                `
            }
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <div class="left">
                    <h2>${msg('Total Cost of Ownership')}</h2>
                </div>
                <div class="right">
                    ${this.detailTokenId === null ? html`
                        <button class="export-btn" ?disabled=${this.exporting || this.loading} @click=${() => this.exportFleetCsv()}>
                            <span class="material-symbols-outlined" style="font-size:18px;">download</span>
                            ${this.exporting ? msg('Exporting…') : msg('Export CSV')}
                        </button>
                    ` : nothing}
                    <tenant-switcher .currentTenantId=${this.tenantId}></tenant-switcher>
                </div>
            </header>

            <div class="canvas">
                ${this.detailTokenId !== null
                    ? this.renderDetail()
                    : this.loading
                        ? html`<div class="loading">${msg('Loading…')}</div>`
                        : this.error
                            ? html`<div class="error">${this.error}</div>`
                            : this.renderFleetTable()
                }
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'tco-view': TCOView;
    }
}
