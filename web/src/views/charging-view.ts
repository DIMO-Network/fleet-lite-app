// web/src/views/charging-view.ts
import { LitElement, html, css, unsafeCSS, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import 'leaflet.markercluster';
import markerClusterCss from 'leaflet.markercluster/dist/MarkerCluster.css?inline';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { ChargingCache } from '../services/charging-cache.ts';
import { ChargingService } from '../services/charging-service.ts';
import { ChargingFleetSummary, ChargingSettings, ChargingSessionView } from '../types/charging.ts';
import { createFleetMap, applyTileTheme } from '../utils/fleet-map.ts';

function formatMoney(n?: number, currency = 'USD'): string {
    if (n == null) return '—';
    return n.toLocaleString(undefined, { style: 'currency', currency });
}

function formatKwh(n?: number): string {
    if (n == null) return '—';
    return `${n.toLocaleString(undefined, { maximumFractionDigits: 1 })} kWh`;
}

const WINDOW_DAYS = 30;

@customElement('charging-view')
export class ChargingView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private loading = true;
    @state() private error = '';
    @state() private summary: ChargingFleetSummary | null = null;
    @state() private settings: ChargingSettings = { currency: 'USD' };
    @state() private savingSettings = false;
    @state() private settingsError = '';
    @state() private exporting = false;
    @state() private selectedTokenId: number | null = null;

    private leafletMap: L.Map | null = null;
    private tileLayer: L.TileLayer | null = null;
    private markers: L.MarkerClusterGroup | null = null;
    private renderedSummary: ChargingFleetSummary | null = null;

    private boundOnThemeChange = (e: Event) => {
        const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
        this.updateTileLayer(theme);
    };

    private updateTileLayer(theme: 'dark' | 'light'): void {
        if (!this.leafletMap) return;
        const mapEl = this.renderRoot.querySelector<HTMLElement>('#charging-map');
        this.tileLayer = applyTileTheme(this.leafletMap, this.tileLayer, mapEl, theme);
    }

    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        unsafeCSS(markerClusterCss),
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
                z-index: 40;
                display: flex;
                align-items: center;
                justify-content: space-between;
                height: var(--top-bar-height, 80px);
                padding: 0 var(--gutter);
                background: var(--background);
                border-bottom: 1px solid var(--border-color);
            }
            .totals {
                display: flex;
                gap: 24px;
                padding: 16px var(--gutter);
            }
            .totals .stat { display: flex; flex-direction: column; }
            .totals .stat .value { font-size: 1.4rem; font-weight: 600; }
            .totals .stat .label { font-size: 0.8rem; color: var(--text-secondary); }
            #charging-map { height: 360px; margin: 0 var(--gutter); border-radius: 8px; }
            table { width: 100%; border-collapse: collapse; margin-top: 16px; }
            th, td { text-align: left; padding: 8px 16px; border-bottom: 1px solid var(--border-color); }

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

            .settings-form {
                display: flex;
                flex-wrap: wrap;
                gap: 16px;
                align-items: flex-end;
                margin: 16px var(--gutter);
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

            .vehicle-select {
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface);
                font: var(--type-body-sm);
                padding: 8px 12px;
                margin: 0 var(--gutter) 16px;
                outline: none;
                cursor: pointer;
            }

            .error { color: var(--error, #d33); padding: 0 var(--gutter); }
        `,
    ];

    override connectedCallback(): void {
        super.connectedCallback();
        window.addEventListener('theme-change', this.boundOnThemeChange);
        this.load();
    }

    override disconnectedCallback(): void {
        super.disconnectedCallback();
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        this.leafletMap?.remove();
        this.leafletMap = null;
    }

    private windowRange(): { from: Date; to: Date } {
        const to = new Date();
        const from = new Date(to.getTime() - WINDOW_DAYS * 24 * 60 * 60 * 1000);
        return { from, to };
    }

    private async load(): Promise<void> {
        const cached = ChargingCache.get(this.tenantId);
        if (cached) {
            this.summary = cached;
            this.loading = false;
        }
        this.settings = await ChargingService.getInstance().getSettings().catch(() => this.settings);
        try {
            const { from, to } = this.windowRange();
            const summary = await ChargingService.getInstance().getSummary(from, to);
            this.summary = summary;
            ChargingCache.set(this.tenantId, summary);
        } catch (e) {
            this.error = e instanceof Error ? e.message : String(e);
        } finally {
            this.loading = false;
        }
    }

    protected override updated(): void {
        const el = this.renderRoot.querySelector('#charging-map') as HTMLElement | null;
        if (el && !this.leafletMap) {
            this.leafletMap = createFleetMap(el, { zoomControl: true });
            this.tileLayer = applyTileTheme(this.leafletMap, null, el, themeService.current);
            this.markers = L.markerClusterGroup();
            this.leafletMap.addLayer(this.markers);
        }
        if (this.summary !== this.renderedSummary) {
            this.renderMarkers();
            this.renderedSummary = this.summary;
        }
    }

    /** Distinct vehicles that reported a charging session, for the filter dropdown. */
    private vehicleOptions(): { tokenId: number; label: string }[] {
        const seen = new Map<number, string>();
        for (const s of this.summary?.sessions ?? []) {
            if (!seen.has(s.tokenId)) seen.set(s.tokenId, s.vehicleLabel);
        }
        return [...seen.entries()]
            .map(([tokenId, label]) => ({ tokenId, label }))
            .sort((a, b) => a.label.localeCompare(b.label));
    }

    private buildPopupContent(s: ChargingSessionView): HTMLElement {
        const container = document.createElement('div');
        const label = document.createElement('div');
        label.textContent = s.vehicleLabel;
        const energy = document.createElement('div');
        energy.textContent = formatKwh(s.addedEnergyKwh);
        const costSaved = document.createElement('div');
        costSaved.textContent = msg(
            str`${formatMoney(s.cost, s.currency)} spent / ${formatMoney(s.savings, s.currency)} saved`,
        );
        container.append(label, energy, costSaved);
        return container;
    }

    private renderMarkers(): void {
        if (!this.markers) return;
        this.markers.clearLayers();
        for (const s of this.summary?.sessions ?? []) {
            if (s.lat == null || s.lng == null) continue;
            const marker = L.circleMarker([s.lat, s.lng], {
                radius: 6, fillColor: '#69dbad', color: '#fff', weight: 1.5, fillOpacity: 0.85,
            });
            marker.bindPopup(this.buildPopupContent(s));
            marker.on('click', () => { this.selectedTokenId = s.tokenId; });
            this.markers.addLayer(marker);
        }
    }

    private async saveSettings(e: Event): Promise<void> {
        e.preventDefault();
        this.savingSettings = true;
        this.settingsError = '';
        try {
            this.settings = await ChargingService.getInstance().putSettings(this.settings);
            ChargingCache.invalidate();
            await this.load();
        } catch (err) {
            this.settingsError = err instanceof Error ? err.message : String(err);
        } finally {
            this.savingSettings = false;
        }
    }

    private updateSetting(key: keyof ChargingSettings, value: string): void {
        const n = value === '' ? undefined : Number(value);
        this.settings = { ...this.settings, [key]: n };
    }

    private async exportCsv(): Promise<void> {
        this.exporting = true;
        try {
            const { from, to } = this.windowRange();
            await ChargingService.getInstance().exportCsv(from, to, this.selectedTokenId ?? undefined);
        } catch (e) {
            this.error = e instanceof Error ? e.message : String(e);
        } finally {
            this.exporting = false;
        }
    }

    override render() {
        const sessions = (this.summary?.sessions ?? [])
            .filter((s) => this.selectedTokenId == null || s.tokenId === this.selectedTokenId)
            .filter((s) => s.addedEnergyKwh != null)
            .sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime());
        const totals = this.selectedTokenId == null
            ? this.summary?.fleet
            : sessions.reduce((acc, s) => ({
                addedEnergyKwh: acc.addedEnergyKwh + (s.addedEnergyKwh ?? 0),
                cost: s.cost != null ? (acc.cost ?? 0) + s.cost : acc.cost,
                savings: s.savings != null ? (acc.savings ?? 0) + s.savings : acc.savings,
            }), { addedEnergyKwh: 0, cost: undefined as number | undefined, savings: undefined as number | undefined });
        const vehicleOpts = this.vehicleOptions();
        return html`
            <header class="top-bar">
                <h1>${msg('Charging')}</h1>
                <button class="export-btn" ?disabled=${this.exporting} @click=${() => this.exportCsv()}>
                    ${msg('Export CSV')}
                </button>
            </header>
            ${this.error ? html`<p class="error">${this.error}</p>` : nothing}
            <div class="totals">
                <div class="stat">
                    <span class="value">${formatKwh(totals?.addedEnergyKwh)}</span>
                    <span class="label">${msg('Energy added')}</span>
                </div>
                <div class="stat">
                    <span class="value">${formatMoney(totals?.cost, this.settings.currency)}</span>
                    <span class="label">${msg('Spent on electricity')}</span>
                </div>
                <div class="stat">
                    <span class="value">${formatMoney(totals?.savings, this.settings.currency)}</span>
                    <span class="label">${msg('Saved vs. gasoline')}</span>
                </div>
            </div>
            <div id="charging-map"></div>
            ${vehicleOpts.length > 1 ? html`
                <select class="vehicle-select"
                    @change=${(e: Event) => {
                        const v = (e.target as HTMLSelectElement).value;
                        this.selectedTokenId = v === '' ? null : Number(v);
                    }}>
                    <option value="" ?selected=${this.selectedTokenId == null}>${msg('All vehicles')}</option>
                    ${vehicleOpts.map((v) => html`
                        <option value=${v.tokenId} ?selected=${v.tokenId === this.selectedTokenId}>${v.label}</option>
                    `)}
                </select>
            ` : nothing}
            <form class="settings-form" @submit=${(e: Event) => this.saveSettings(e)}>
                <div class="field">
                    <label>${msg('Electricity rate (per kWh)')}</label>
                    <input type="number" step="0.01" .value=${this.settings.electricityRate?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('electricityRate', (e.target as HTMLInputElement).value)} />
                </div>
                <div class="field">
                    <label>${msg('Gas price (per gallon)')}</label>
                    <input type="number" step="0.01" .value=${this.settings.gasPrice?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('gasPrice', (e.target as HTMLInputElement).value)} />
                </div>
                <div class="field">
                    <label>${msg('Gas MPG equivalent')}</label>
                    <input type="number" step="1" .value=${this.settings.gasMpgEquivalent?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('gasMpgEquivalent', (e.target as HTMLInputElement).value)} />
                </div>
                <div class="field">
                    <label>${msg('Vehicle efficiency (kWh/mile)')}</label>
                    <input type="number" step="0.01" .value=${this.settings.vehicleKwhPerMile?.toString() ?? ''}
                        @input=${(e: InputEvent) => this.updateSetting('vehicleKwhPerMile', (e.target as HTMLInputElement).value)} />
                </div>
                <button type="submit" class="save-btn" ?disabled=${this.savingSettings}>${msg('Save settings')}</button>
                ${this.settingsError ? html`<span class="form-error">${this.settingsError}</span>` : nothing}
            </form>
            ${this.loading
                ? html`<p>${msg('Loading…')}</p>`
                : html`
                    <table>
                        <thead>
                            <tr>
                                <th>${msg('Vehicle')}</th>
                                <th>${msg('Started')}</th>
                                <th>${msg('Energy')}</th>
                                <th>${msg('Cost')}</th>
                                <th>${msg('Saved')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            ${sessions.map(
                                (s: ChargingSessionView) => html`
                                    <tr>
                                        <td>${s.vehicleLabel}</td>
                                        <td>${new Date(s.startedAt).toLocaleString()}</td>
                                        <td>${formatKwh(s.addedEnergyKwh)}</td>
                                        <td>${formatMoney(s.cost, s.currency)}</td>
                                        <td>${formatMoney(s.savings, s.currency)}</td>
                                    </tr>
                                `,
                            )}
                        </tbody>
                    </table>
                `}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'charging-view': ChargingView;
    }
}
