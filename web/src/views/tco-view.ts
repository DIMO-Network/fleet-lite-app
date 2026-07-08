import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { TCOService } from '../services/tco-service.ts';
import { FleetTCOSummary, VehicleTCOSummary } from '../types/tco.ts';
import { categoryLabel } from '../utils/document-categories.ts';

function formatMoney(n: number): string {
    return n.toLocaleString(undefined, { style: 'currency', currency: 'USD' });
}

@customElement('tco-view')
export class TCOView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private loading = true;
    @state() private error = '';
    @state() private summary: FleetTCOSummary | null = null;
    @state() private exporting = false;
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
                display: block;
                width: 100%;
                height: 100%;
                overflow-y: auto;
                padding: var(--stack-lg) var(--gutter);
            }
            .header {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-bottom: var(--stack-lg);
            }
            .header h1 { font: var(--type-headline-lg); color: var(--primary); }
            button.export {
                padding: 10px 18px;
                border-radius: var(--radius-md);
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                cursor: pointer;
            }
            button.export:disabled { opacity: 0.5; cursor: not-allowed; }
            table { width: 100%; border-collapse: collapse; }
            th, td {
                text-align: left;
                padding: 12px 16px;
                border-bottom: 1px solid var(--outline-variant);
                font: var(--type-body-sm);
            }
            th {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            tbody tr { cursor: pointer; }
            tbody tr:hover { background: var(--surface-container-low); }
            td.num { text-align: right; font-variant-numeric: tabular-nums; }
            .empty, .loading, .error {
                padding: 48px 0;
                text-align: center;
                color: var(--on-surface-variant);
            }
            .error { color: var(--error); }
            tfoot td { font-weight: 700; border-top: 2px solid var(--outline-variant); border-bottom: none; }
            .back { background: none; border: none; color: var(--on-surface-variant); cursor: pointer; margin-bottom: var(--stack-md); font: var(--type-body-sm); display: flex; align-items: center; gap: 4px; }
            .back:hover { color: var(--primary); }
            .breakdown { display: flex; flex-wrap: wrap; gap: 16px; margin-bottom: var(--stack-lg); }
            .stat { background: var(--surface-container-low); border: 1px solid var(--outline-variant); border-radius: var(--radius-md); padding: 16px; min-width: 160px; }
            .stat .label { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant); margin-bottom: 4px; }
            .stat .value { font: var(--type-headline-md); color: var(--primary); }
            .settings-form { display: flex; flex-wrap: wrap; gap: 16px; align-items: flex-end; margin-bottom: var(--stack-lg); padding: 16px; background: var(--surface-container-low); border: 1px solid var(--outline-variant); border-radius: var(--radius-md); }
            .settings-form .field { display: flex; flex-direction: column; gap: 4px; }
            .settings-form label { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant); }
            .settings-form input { background: var(--surface-container); color: var(--on-surface); border: 1px solid var(--outline-variant); border-radius: var(--radius-md); padding: 8px 10px; font-family: inherit; }
            .settings-form button { padding: 10px 16px; border-radius: var(--radius-md); background: var(--primary); color: var(--on-primary); border: none; font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700; cursor: pointer; }
            .settings-form button:disabled { opacity: 0.5; cursor: not-allowed; }
        `,
    ];

    async connectedCallback() {
        super.connectedCallback();
        await this.loadSummary();
    }

    private async loadSummary() {
        this.loading = true;
        this.error = '';
        try {
            this.summary = await TCOService.getInstance().getSummary();
        } catch (e) {
            console.error('Failed to load TCO summary', e);
            this.error = e instanceof Error ? e.message : msg('Failed to load TCO summary');
        } finally {
            this.loading = false;
        }
    }

    private async openVehicle(v: VehicleTCOSummary) {
        this.detailTokenId = v.tokenId;
        this.loadingDetail = true;
        this.detailError = '';
        try {
            this.detail = await TCOService.getInstance().getVehicleDetail(v.tokenId);
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

    private closeDetail = async () => {
        this.detailTokenId = null;
        this.detail = null;
        await this.loadSummary();
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

    private renderFleetTable() {
        const summary = this.summary!;
        if (summary.vehicles.length === 0) {
            return html`<div class="empty">${msg('No vehicles on this account.')}</div>`;
        }
        return html`
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
                    ${summary.vehicles.map((v) => html`
                        <tr @click=${() => this.openVehicle(v)}>
                            <td>${v.vehicleLabel}</td>
                            <td class="num">${formatMoney(v.operatingCost)}</td>
                            <td class="num">${formatMoney(v.acquisitionCost)}</td>
                            <td class="num">${formatMoney(v.depreciationToDate)}</td>
                            <td class="num">${formatMoney(v.totalTco)}</td>
                        </tr>
                    `)}
                </tbody>
                <tfoot>
                    <tr>
                        <td>${msg('Fleet total')}</td>
                        <td class="num">${formatMoney(summary.fleet.operatingCost)}</td>
                        <td class="num">${formatMoney(summary.fleet.acquisitionCost)}</td>
                        <td class="num">${formatMoney(summary.fleet.depreciationToDate)}</td>
                        <td class="num">${formatMoney(summary.fleet.totalTco)}</td>
                    </tr>
                </tfoot>
            </table>
        `;
    }

    private renderDetail() {
        if (this.loadingDetail) return html`<div class="loading">${msg('Loading…')}</div>`;
        if (this.detailError) return html`<div class="error">${this.detailError}</div>`;
        const d = this.detail;
        if (!d) return nothing;
        const categories = Object.entries(d.costByCategory).sort((a, b) => b[1] - a[1]);
        return html`
            <button class="back" @click=${this.closeDetail}>
                <span class="material-symbols-outlined">arrow_back</span>
                ${msg('Back to fleet')}
            </button>
            <div class="header">
                <h1>${d.vehicleLabel}</h1>
                <button class="export" ?disabled=${this.exportingVehicle} @click=${() => this.exportVehicleCsv()}>
                    ${this.exportingVehicle ? msg('Exporting…') : msg('Export CSV')}
                </button>
            </div>

            <div class="breakdown">
                <div class="stat"><div class="label">${msg('Operating cost')}</div><div class="value">${formatMoney(d.operatingCost)}</div></div>
                <div class="stat"><div class="label">${msg('Acquisition')}</div><div class="value">${formatMoney(d.acquisitionCost)}</div></div>
                <div class="stat"><div class="label">${msg('Depreciation to date')}</div><div class="value">${formatMoney(d.depreciationToDate)}</div></div>
                <div class="stat"><div class="label">${msg('Total TCO')}</div><div class="value">${formatMoney(d.totalTco)}</div></div>
                ${categories.map(([cat, amount]) => html`
                    <div class="stat"><div class="label">${categoryLabel(cat)}</div><div class="value">${formatMoney(amount)}</div></div>
                `)}
            </div>

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
                <button ?disabled=${this.savingSettings} @click=${() => this.saveSettings()}>
                    ${this.savingSettings ? msg('Saving…') : msg('Save')}
                </button>
                ${this.settingsError ? html`<span class="error">${this.settingsError}</span>` : nothing}
            </div>

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
        `;
    }

    render() {
        if (this.detailTokenId !== null) {
            return this.renderDetail();
        }
        return html`
            <div class="header">
                <h1>${msg('Total Cost of Ownership')}</h1>
                <button class="export" ?disabled=${this.exporting || this.loading} @click=${() => this.exportFleetCsv()}>
                    ${this.exporting ? msg('Exporting…') : msg('Export CSV')}
                </button>
            </div>
            ${this.loading
                ? html`<div class="loading">${msg('Loading…')}</div>`
                : this.error
                    ? html`<div class="error">${this.error}</div>`
                    : this.summary
                        ? this.renderFleetTable()
                        : nothing
            }
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'tco-view': TCOView;
    }
}
