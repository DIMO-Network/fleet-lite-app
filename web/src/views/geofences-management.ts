import { LitElement, html, css, unsafeCSS, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { ApiService } from '../services/api-service.ts';
import { GeofenceService } from '../services/geofence-service.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { Geofence, GeoJSONPolygon } from '../types/geofence.ts';
import { FleetGroup } from '../types/group.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';
import { formatArea } from '../utils/geo.ts';
import '../elements/create-geofence-modal.ts';
import '../elements/manage-geofence-vehicles-modal.ts';
import '../elements/geofence-activity-modal.ts';

/**
 * geofences-management-view — define, draw, list, and assign polygon geofences.
 * Reached at #/:tenantId/geofences. Existing geofences render as colored
 * polygons; "New geofence" enters a click-to-draw mode that opens the create
 * modal on finish.
 */
@customElement('geofences-management-view')
export class GeofencesManagementView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private geofences: Geofence[] = [];
    @state() private vehicles: Vehicle[] = [];
    @state() private groups: FleetGroup[] = [];
    @state() private loading = true;
    @state() private errorMessage: string | null = null;

    @state() private drawing = false;
    @state() private drawCount = 0;
    @state() private pendingGeometry: GeoJSONPolygon | null = null;
    @state() private creating = false;
    @state() private editing: Geofence | null = null;
    @state() private managing: Geofence | null = null;
    @state() private activity: Geofence | null = null;
    @state() private confirmingDeleteId: string | null = null;
    @state() private selectedId: string | null = null;

    private leafletMap: L.Map | null = null;
    private tileLayer: L.TileLayer | null = null;
    private geofenceLayers = new Map<string, L.Polygon>();
    private drawPoints: L.LatLng[] = [];
    private drawLine: L.Polyline | null = null;
    private drawVertices: L.LayerGroup | null = null;
    private fitted = false;

    private boundOnThemeChange = (e: Event) => {
        const theme = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail.theme;
        this.updateTileLayer(theme);
    };
    private boundMapClick = (e: L.LeafletMouseEvent) => this.onMapClick(e);

    connectedCallback() {
        super.connectedCallback();
        void this.load();
    }

    override firstUpdated() {
        const el = this.renderRoot.querySelector<HTMLElement>('.map');
        if (!el) return;
        const worldBounds = L.latLngBounds([-85.051129, -Infinity], [85.051129, Infinity]);
        this.leafletMap = L.map(el, {
            zoomControl: true,
            attributionControl: true,
            maxBounds: worldBounds,
            maxBoundsViscosity: 1.0,
            worldCopyJump: true,
        }).setView([39.5, -98.35], 4);
        this.tileLayer = this.buildTileLayer(themeService.current);
        this.tileLayer.addTo(this.leafletMap);
        if (themeService.current === 'dark') el.classList.add('dark-tiles');
        window.addEventListener('theme-change', this.boundOnThemeChange);
        // Data may have arrived before the map existed — draw it now.
        this.renderGeofences();
    }

    override disconnectedCallback() {
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        this.leafletMap?.off('click', this.boundMapClick);
        this.geofenceLayers.clear();
        this.leafletMap?.remove();
        this.leafletMap = null;
        this.tileLayer = null;
        super.disconnectedCallback();
    }

    private buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
        const url = theme === 'light'
            ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
            : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
        return L.tileLayer(url, {
            attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
            subdomains: 'abcd',
            maxZoom: 19,
        });
    }

    private updateTileLayer(theme: 'dark' | 'light'): void {
        if (!this.leafletMap) return;
        this.tileLayer?.remove();
        this.tileLayer = this.buildTileLayer(theme);
        this.tileLayer.addTo(this.leafletMap);
        const el = this.renderRoot.querySelector<HTMLElement>('.map');
        el?.classList.toggle('dark-tiles', theme === 'dark');
    }

    private async load() {
        this.loading = true;
        try {
            const [geofences, vehiclesRes, groups] = await Promise.all([
                GeofenceService.getInstance().list(),
                ApiService.getInstance().get<VehiclesResponse>('/vehicles'),
                FleetGroupService.getInstance().list(),
            ]);
            this.geofences = geofences;
            this.vehicles = vehiclesRes.vehicles || [];
            this.groups = groups;
            this.errorMessage = null;
            this.renderGeofences();
        } catch (e) {
            console.error('Failed to load geofences', e);
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to load geofences');
        } finally {
            this.loading = false;
        }
    }

    // ---- existing geofence polygons ------------------------------------------

    private renderGeofences() {
        if (!this.leafletMap) return;
        for (const layer of this.geofenceLayers.values()) layer.remove();
        this.geofenceLayers.clear();

        for (const g of this.geofences) {
            const ring = g.geometry?.coordinates?.[0];
            if (!ring || ring.length < 3) continue;
            const latlngs = ring.map(([lng, lat]) => [lat, lng] as [number, number]);
            const selected = this.selectedId === g.id;
            const poly = L.polygon(latlngs, {
                color: g.color,
                weight: selected ? 3 : 2,
                fillColor: g.color,
                fillOpacity: selected ? 0.35 : 0.15,
            });
            poly.bindTooltip(g.name, { direction: 'center', className: 'gf-tooltip' });
            poly.on('click', () => this.selectGeofence(g.id));
            poly.addTo(this.leafletMap);
            this.geofenceLayers.set(g.id, poly);
        }

        if (!this.fitted && this.geofenceLayers.size > 0) {
            const group = L.featureGroup([...this.geofenceLayers.values()]);
            this.leafletMap.fitBounds(group.getBounds().pad(0.3), { maxZoom: 14 });
            this.fitted = true;
        }
    }

    private selectGeofence(id: string) {
        this.selectedId = id;
        for (const [gid, layer] of this.geofenceLayers) {
            const sel = gid === id;
            layer.setStyle({ weight: sel ? 3 : 2, fillOpacity: sel ? 0.35 : 0.15 });
        }
        const layer = this.geofenceLayers.get(id);
        if (layer && this.leafletMap) {
            this.leafletMap.flyToBounds(layer.getBounds().pad(0.3), { maxZoom: 15 });
        }
    }

    // ---- draw mode (hand-rolled click-to-add-vertex) -------------------------

    private startDraw() {
        if (!this.leafletMap || this.drawing) return;
        this.drawing = true;
        this.clearDrawLayers();
        this.leafletMap.on('click', this.boundMapClick);
        this.renderRoot.querySelector<HTMLElement>('.map')?.classList.add('drawing');
    }

    private onMapClick(e: L.LeafletMouseEvent) {
        if (!this.drawing) return;
        this.drawPoints.push(e.latlng);
        this.drawCount = this.drawPoints.length;
        this.redrawDraw();
    }

    private redrawDraw() {
        if (!this.leafletMap) return;
        this.drawLine?.remove();
        this.drawVertices?.remove();

        if (this.drawPoints.length >= 2) {
            this.drawLine = L.polyline([...this.drawPoints, this.drawPoints[0]], {
                color: '#86f8c8', weight: 2, dashArray: '6 6',
            }).addTo(this.leafletMap);
        } else {
            this.drawLine = null;
        }

        this.drawVertices = L.layerGroup().addTo(this.leafletMap);
        this.drawPoints.forEach((pt, i) => {
            const first = i === 0;
            const m = L.circleMarker(pt, {
                radius: first ? 7 : 5,
                color: '#ffffff',
                weight: 2,
                fillColor: first ? '#86f8c8' : '#69dbad',
                fillOpacity: 1,
            });
            if (first && this.drawPoints.length >= 3) {
                m.bindTooltip(msg('Click to finish'), { direction: 'top' });
                m.on('click', (ev) => { L.DomEvent.stop(ev); this.finishDraw(); });
            }
            m.addTo(this.drawVertices!);
        });
    }

    private finishDraw() {
        if (this.drawPoints.length < 3) {
            this.errorMessage = msg('Add at least 3 points to make an area.');
            return;
        }
        const ring = this.drawPoints.map((p) => [p.lng, p.lat] as [number, number]);
        ring.push(ring[0]); // close the ring
        this.pendingGeometry = { type: 'Polygon', coordinates: [ring] };
        this.endDraw();
        this.creating = true;
    }

    private cancelDraw() {
        this.endDraw();
    }

    private endDraw() {
        this.drawing = false;
        this.clearDrawLayers();
        this.leafletMap?.off('click', this.boundMapClick);
        this.renderRoot.querySelector<HTMLElement>('.map')?.classList.remove('drawing');
    }

    private clearDrawLayers() {
        this.drawLine?.remove();
        this.drawVertices?.remove();
        this.drawLine = null;
        this.drawVertices = null;
        this.drawPoints = [];
        this.drawCount = 0;
    }

    // ---- CRUD plumbing -------------------------------------------------------

    private async onDelete(g: Geofence) {
        try {
            await GeofenceService.getInstance().delete(g.id);
            this.confirmingDeleteId = null;
            if (this.selectedId === g.id) this.selectedId = null;
            await this.load();
        } catch (e) {
            console.error('Failed to delete geofence', e);
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to delete geofence');
        }
    }

    private afterSave() {
        this.creating = false;
        this.editing = null;
        this.pendingGeometry = null;
        void this.load();
    }

    private scopeLabel(g: Geofence): string {
        if (g.scope === 'group') return msg(str`${g.groupIds.length} group(s)`);
        if (g.scope === 'manual') return msg('Specific vehicles');
        return msg('All vehicles');
    }

    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        css`
            :host { display: flex; flex-direction: column; width: 100%; height: 100%; background: var(--background); }
            header.top-bar {
                position: relative; z-index: 40; flex-shrink: 0;
                display: flex; align-items: center; justify-content: space-between;
                height: var(--top-bar-height); padding: 0 var(--gutter);
                background: var(--background); border-bottom: 1px solid var(--outline-variant);
            }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            .new-btn {
                display: flex; align-items: center; gap: 8px;
                background: var(--primary); color: var(--on-primary);
                border: none; padding: 10px 16px; border-radius: var(--radius-md);
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700; cursor: pointer;
            }
            .new-btn:disabled { opacity: 0.5; cursor: not-allowed; }

            .body { position: relative; flex: 1; min-height: 0; }
            .map { position: absolute; inset: 0; z-index: 0; }
            .map.dark-tiles .leaflet-tile { filter: brightness(1.8); }
            .map.drawing { cursor: crosshair; }
            .map.drawing .leaflet-grab, .map.drawing .leaflet-interactive { cursor: crosshair; }
            .gf-tooltip { background: var(--surface-container-high); color: var(--on-surface); border: none; font: var(--type-label-caps); letter-spacing: 0.04em; text-transform: uppercase; }

            .panel {
                position: absolute; top: 16px; right: 16px; z-index: 30;
                width: 320px; max-width: calc(100% - 32px); max-height: calc(100% - 32px);
                display: flex; flex-direction: column;
                background: color-mix(in srgb, var(--surface-container-low) 92%, transparent);
                backdrop-filter: blur(8px);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-lg);
                overflow: hidden;
            }
            .panel-head { padding: 14px 16px; border-bottom: 1px solid var(--outline-variant); font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant); }
            .panel-list { overflow-y: auto; padding: 12px; display: flex; flex-direction: column; gap: 10px; }
            .panel-list::-webkit-scrollbar { width: 6px; }
            .panel-list::-webkit-scrollbar-thumb { background-color: var(--outline-variant); border-radius: 10px; }

            .gf-card {
                background: var(--surface-container); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md); padding: 12px; display: flex; flex-direction: column; gap: 8px; cursor: pointer;
            }
            .gf-card.selected { border-color: var(--primary); }
            .gf-head { display: flex; align-items: center; gap: 10px; }
            .gf-head .dot { width: 14px; height: 14px; border-radius: var(--radius-full); flex-shrink: 0; }
            .gf-head .name { font: var(--type-body-md); font-weight: 600; color: var(--primary); flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
            .gf-meta { display: flex; flex-wrap: wrap; gap: 4px 12px; font: var(--type-body-sm); color: var(--on-surface-variant); }
            .gf-meta .chip { display: inline-flex; align-items: center; gap: 4px; }
            .gf-meta .material-symbols-outlined { font-size: 14px; }

            .card-actions { display: flex; gap: 6px; flex-wrap: wrap; }
            .card-actions button {
                display: flex; align-items: center; gap: 4px;
                background: transparent; color: var(--on-surface-variant);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-sm);
                padding: 6px 8px; font: var(--type-label-caps); letter-spacing: 0.03em; text-transform: uppercase; cursor: pointer;
            }
            .card-actions button:hover { color: var(--primary); border-color: var(--primary); }
            .card-actions button.danger:hover { color: var(--error); border-color: var(--error); }
            .card-actions .material-symbols-outlined { font-size: 14px; }

            .confirm { display: flex; align-items: center; gap: 8px; font: var(--type-body-sm); color: var(--error); }
            .confirm button { border: none; border-radius: var(--radius-sm); padding: 5px 9px; font: var(--type-label-caps); letter-spacing: 0.03em; text-transform: uppercase; cursor: pointer; }
            .confirm .yes { background: var(--error); color: var(--on-primary); }
            .confirm .no { background: var(--surface-container-high); color: var(--on-surface); }

            .panel-empty { padding: 24px 16px; text-align: center; color: var(--on-surface-variant); font: var(--type-body-sm); }

            .draw-bar {
                position: absolute; bottom: 24px; left: 50%; transform: translateX(-50%); z-index: 35;
                display: flex; align-items: center; gap: 12px;
                background: var(--surface-container-high); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full); padding: 10px 16px; box-shadow: var(--shadow-md, 0 4px 16px rgba(0,0,0,0.4));
            }
            .draw-bar .hint { font: var(--type-body-sm); color: var(--on-surface); }
            .draw-bar button {
                padding: 8px 14px; border-radius: var(--radius-full);
                font: var(--type-label-caps); letter-spacing: 0.04em; text-transform: uppercase; font-weight: 700; cursor: pointer; border: 1px solid transparent;
            }
            .draw-bar .finish { background: var(--primary); color: var(--on-primary); }
            .draw-bar .finish:disabled { opacity: 0.5; cursor: not-allowed; }
            .draw-bar .cancel { background: transparent; color: var(--on-surface-variant); border-color: var(--outline-variant); }

            .toast-error {
                position: absolute; top: 16px; left: 16px; z-index: 35; max-width: 340px;
                padding: 12px 14px; background: var(--surface-container-high);
                border: 1px solid rgba(255,180,171,0.3); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm);
            }
        `,
    ];

    private renderCard(g: Geofence) {
        const confirming = this.confirmingDeleteId === g.id;
        const count = g.vehicleCount ?? 0;
        return html`
            <div class=${this.selectedId === g.id ? 'gf-card selected' : 'gf-card'} @click=${() => this.selectGeofence(g.id)}>
                <div class="gf-head">
                    <span class="dot" style="background:${g.color}"></span>
                    <span class="name">${g.name}</span>
                </div>
                <div class="gf-meta">
                    <span class="chip"><span class="material-symbols-outlined">crop_free</span>${formatArea(g.areaM2)}</span>
                    <span class="chip"><span class="material-symbols-outlined">directions_car</span>${count} ${count === 1 ? msg('vehicle') : msg('vehicles')}</span>
                    <span class="chip"><span class="material-symbols-outlined">filter_alt</span>${this.scopeLabel(g)}</span>
                    ${g.speedLimitKph != null ? html`<span class="chip"><span class="material-symbols-outlined">speed</span>${g.speedLimitKph} ${msg('km/h')}</span>` : nothing}
                </div>
                ${confirming
                    ? html`<div class="confirm" @click=${(e: Event) => e.stopPropagation()}>
                        <span>${msg(str`Delete “${g.name}”?`)}</span>
                        <button class="yes" @click=${() => this.onDelete(g)}>${msg('Delete')}</button>
                        <button class="no" @click=${() => { this.confirmingDeleteId = null; }}>${msg('Cancel')}</button>
                    </div>`
                    : html`<div class="card-actions" @click=${(e: Event) => e.stopPropagation()}>
                        <button @click=${() => { this.activity = g; }}>
                            <span class="material-symbols-outlined">history</span> ${msg('Activity')}
                        </button>
                        ${g.scope === 'manual'
                            ? html`<button @click=${() => { this.managing = g; }}>
                                <span class="material-symbols-outlined">directions_car</span> ${msg('Vehicles')}
                            </button>`
                            : nothing}
                        <button @click=${() => { this.editing = g; }}>
                            <span class="material-symbols-outlined">edit</span> ${msg('Edit')}
                        </button>
                        <button class="danger" @click=${() => { this.confirmingDeleteId = g.id; }}>
                            <span class="material-symbols-outlined">delete</span> ${msg('Delete')}
                        </button>
                    </div>`}
            </div>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <h2>${msg('Geofences')}</h2>
                <button class="new-btn" ?disabled=${this.drawing} @click=${() => this.startDraw()}>
                    <span class="material-symbols-outlined">add_location_alt</span> ${msg('New geofence')}
                </button>
            </header>

            <div class="body">
                <div class="map"></div>

                ${this.errorMessage ? html`<div class="toast-error">${this.errorMessage}</div>` : nothing}

                <div class="panel">
                    <div class="panel-head">${msg('Geofences')} ${this.geofences.length ? `(${this.geofences.length})` : ''}</div>
                    <div class="panel-list">
                        ${this.loading
                            ? html`<p class="panel-empty">${msg('Loading geofences…')}</p>`
                            : this.geofences.length === 0
                                ? html`<p class="panel-empty">${msg('No geofences yet. Click “New geofence” and draw an area on the map.')}</p>`
                                : this.geofences.map((g) => this.renderCard(g))}
                    </div>
                </div>

                ${this.drawing
                    ? html`<div class="draw-bar">
                        <span class="hint">${msg(str`${this.drawCount} point(s) — click the map to add, the first point to finish.`)}</span>
                        <button class="finish" ?disabled=${this.drawCount < 3} @click=${() => this.finishDraw()}>${msg('Finish')}</button>
                        <button class="cancel" @click=${() => this.cancelDraw()}>${msg('Cancel')}</button>
                    </div>`
                    : nothing}
            </div>

            ${this.creating && this.pendingGeometry
                ? html`<create-geofence-modal
                    .pendingGeometry=${this.pendingGeometry}
                    .groups=${this.groups}
                    @close=${() => { this.creating = false; this.pendingGeometry = null; }}
                    @saved=${() => this.afterSave()}
                  ></create-geofence-modal>`
                : nothing}

            ${this.editing
                ? html`<create-geofence-modal
                    .geofence=${this.editing}
                    .groups=${this.groups}
                    @close=${() => { this.editing = null; }}
                    @saved=${() => this.afterSave()}
                  ></create-geofence-modal>`
                : nothing}

            ${this.managing
                ? html`<manage-geofence-vehicles-modal
                    .geofence=${this.managing}
                    .vehicles=${this.vehicles}
                    @close=${() => { this.managing = null; }}
                    @changed=${() => { void this.load(); }}
                  ></manage-geofence-vehicles-modal>`
                : nothing}

            ${this.activity
                ? html`<geofence-activity-modal
                    .geofence=${this.activity}
                    .vehicles=${this.vehicles}
                    @close=${() => { this.activity = null; }}
                  ></geofence-activity-modal>`
                : nothing}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'geofences-management-view': GeofencesManagementView;
    }
}
