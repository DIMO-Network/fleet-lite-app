import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { TCOService } from '../services/tco-service.ts';
import { FleetTCOSummary, VehicleTCOSummary } from '../types/tco.ts';

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

    private openVehicle(v: VehicleTCOSummary) {
        // TODO(Task 10): open the per-vehicle TCO drilldown.
        void v;
    }

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

    render() {
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
