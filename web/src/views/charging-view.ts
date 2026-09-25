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
import { createFleetMap, applyTileTheme, createVehicleClusterGroup, MAP_COLORS } from '../utils/fleet-map.ts';

function formatMoney(n?: number, currency = 'USD'): string {
    if (n == null) return '—';
    return n.toLocaleString(undefined, { style: 'currency', currency });
}

function formatKwh(n?: number): string {
    if (n == null) return '—';
    return `${n.toLocaleString(undefined, { maximumFractionDigits: 1 })} kWh`;
}

/** Energy cell fallback for connections that don't report added-energy/power
 *  (some aftermarket devices only ever report state of charge) — shows the
 *  SOC gained during the session instead of leaving the row blank. */
function formatEnergyCell(s: ChargingSessionView): string {
    if (s.addedEnergyKwh != null) return formatKwh(s.addedEnergyKwh);
    if (s.socStartPct != null && s.socEndPct != null) {
        return `${Math.round(s.socStartPct)}% → ${Math.round(s.socEndPct)}% SOC`;
    }
    return '—';
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
                padding-bottom: var(--stack-lg);
            }
            /* The host scrolls; its sections must not shrink to fit it. */
            :host > * { flex-shrink: 0; }
            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                flex-shrink: 0;
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 12px;
                height: var(--top-bar-height);
                padding: 0 var(--gutter);
                background: var(--background);
            }
            header.top-bar h1 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }

            /* Big numbers, label above (column-reverse keeps the markup order). */
            .totals {
                display: flex;
                flex-wrap: wrap;
                gap: 24px;
                padding: 8px var(--gutter) 24px;
            }
            .totals .stat {
                display: flex;
                flex-direction: column-reverse;
                justify-content: flex-end;
                gap: 6px;
                min-width: 180px;
            }
            .totals .stat + .stat { padding-left: 24px; border-left: 1px solid var(--outline-variant); }
            .totals .stat .value {
                font: var(--type-data-display);
                letter-spacing: -0.03em;
                color: var(--primary);
                white-space: nowrap;
            }
            .totals .stat .label { font: var(--type-label); color: var(--on-surface-variant); }

            #charging-map {
                height: 360px;
                flex-shrink: 0;
                margin: 0 var(--gutter);
                border-radius: var(--radius-lg);
                overflow: hidden;
                background: var(--surface-container-low);
                isolation: isolate;
            }
            #charging-map.dark-tiles .leaflet-tile { filter: brightness(1.8); }
            #charging-map .leaflet-control-attribution { font-size: 9px; opacity: 0.5; }
            /* Leaflet's white zoom buttons → floating glass pills. */
            #charging-map .leaflet-bar {
                border: none;
                border-radius: var(--radius-full);
                overflow: hidden;
                box-shadow: var(--shadow-float);
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
            }
            #charging-map .leaflet-bar a {
                width: 36px;
                height: 36px;
                line-height: 36px;
                background: transparent;
                color: var(--on-surface);
                border-bottom: 1px solid var(--outline-variant);
                font: 400 18px/36px var(--font-body);
                transition: background 0.15s ease;
            }
            #charging-map .leaflet-bar a:last-child { border-bottom: none; }
            #charging-map .leaflet-bar a:hover { background: var(--surface-container-high); }
            #charging-map .leaflet-bar a.leaflet-disabled { color: var(--on-surface-variant); opacity: 0.5; }
            #charging-map .leaflet-popup-content-wrapper,
            #charging-map .leaflet-popup-tip {
                background: var(--surface-container-high);
                color: var(--on-surface);
                box-shadow: var(--shadow-float);
            }
            #charging-map .leaflet-popup-content-wrapper { border-radius: var(--radius-md); }
            #charging-map .leaflet-popup-content { font: var(--type-body-sm); margin: 12px 14px; }
            #charging-map .leaflet-popup-content > div > div:first-child { font-weight: 600; color: var(--primary); }
            #charging-map a.leaflet-popup-close-button { color: var(--on-surface-variant); }

            /* ── Sessions table (DESIGN.md) ─────────────────────────── */
            /* The nowrap date/number cells scroll inside the wrapper on
               phones instead of pushing the whole view sideways. flex-shrink:0
               because :host is a fixed-height flex column and an overflow box
               would otherwise shrink into its own vertical scroller. */
            .table-wrap {
                flex-shrink: 0;
                overflow-x: auto;
                margin: 16px var(--gutter) 0;
            }
            table {
                width: 100%;
                border-collapse: collapse;
                font: var(--type-body-sm);
                color: var(--on-surface);
            }
            th {
                height: 40px;
                padding: 0 12px;
                text-align: left;
                font: var(--type-label);
                color: var(--on-surface-variant);
                border-bottom: 1px solid var(--outline-variant);
                white-space: nowrap;
            }
            td {
                height: 52px;
                padding: 8px 12px;
                border-bottom: 1px solid var(--outline-variant);
                vertical-align: middle;
            }
            tbody tr { transition: background 0.12s ease; }
            tbody tr:hover { background: var(--surface-container-low); }
            tbody tr:last-child td { border-bottom: none; }
            tbody td:first-child { color: var(--primary); font-weight: 500; }
            td:nth-child(2), td:nth-child(3) { color: var(--on-surface-variant); white-space: nowrap; }
            th.num, td.num { text-align: right; white-space: nowrap; }

            /* Primary action: DIMO gradient pill. */
            .export-btn {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                gap: 8px;
                min-height: 40px;
                padding: 0 18px;
                border-radius: var(--radius-full);
                background: var(--brand-gradient);
                color: var(--on-accent);
                font: 600 14px/20px var(--font-body);
                white-space: nowrap;
                transition: filter 0.15s ease, box-shadow 0.15s ease;
            }
            .export-btn:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }
            .export-btn:disabled { filter: grayscale(1) opacity(0.5); box-shadow: none; cursor: not-allowed; }

            .settings-form {
                display: flex;
                flex-wrap: wrap;
                gap: 16px;
                align-items: flex-end;
                margin: 16px var(--gutter);
                padding: 20px;
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
            }
            .settings-form .field { display: flex; flex-direction: column; gap: 6px; flex: 1 1 160px; }
            .settings-form label {
                font: var(--type-label);
                color: var(--on-surface-variant);
            }
            .settings-form input {
                height: 40px;
                background: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 0 12px;
                font: var(--type-body-sm);
            }
            .settings-form .save-btn {
                min-height: 40px;
                padding: 0 18px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                font: 500 14px/20px var(--font-body);
                white-space: nowrap;
                transition: background 0.15s ease, border-color 0.15s ease;
            }
            .settings-form .save-btn:hover { background: var(--surface-container-highest); border-color: var(--outline); }
            .settings-form .save-btn:disabled { opacity: 0.5; cursor: not-allowed; }
            .settings-form .form-error { font: var(--type-body-sm); color: var(--error); flex-basis: 100%; }

            .vehicle-select {
                align-self: flex-start;
                height: 40px;
                min-width: 220px;
                margin: 16px var(--gutter) 0;
                padding: 0 12px;
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface);
            }

            :host > p:not(.error) {
                padding: 48px var(--gutter);
                text-align: center;
                color: var(--on-surface-variant);
                font: var(--type-body-md);
            }
            .error { color: var(--error); font: var(--type-body-sm); padding: 0 var(--gutter) 8px; }
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
            this.markers = createVehicleClusterGroup();
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
        energy.textContent = formatEnergyCell(s);
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
                radius: 6, fillColor: MAP_COLORS.mint, color: MAP_COLORS.ink, weight: 2, fillOpacity: 1,
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
                    <div class="table-wrap">
                        <table>
                            <thead>
                                <tr>
                                    <th>${msg('Vehicle')}</th>
                                    <th>${msg('Started')}</th>
                                    <th>${msg('Ended')}</th>
                                    <th class="num">${msg('Energy')}</th>
                                    <th class="num">${msg('Cost')}</th>
                                    <th class="num">${msg('Saved')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                ${sessions.map(
                                    (s: ChargingSessionView) => html`
                                        <tr>
                                            <td>${s.vehicleLabel}</td>
                                            <td>${new Date(s.startedAt).toLocaleString()}</td>
                                            <td>${new Date(s.endedAt).toLocaleString()}</td>
                                            <td class="num">${formatEnergyCell(s)}</td>
                                            <td class="num">${formatMoney(s.cost, s.currency)}</td>
                                            <td class="num">${formatMoney(s.savings, s.currency)}</td>
                                        </tr>
                                    `,
                                )}
                            </tbody>
                        </table>
                    </div>
                `}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'charging-view': ChargingView;
    }
}
