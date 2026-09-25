import { LitElement, html, css, unsafeCSS, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import markerClusterCss from 'leaflet.markercluster/dist/MarkerCluster.css?inline';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { ApiService } from '../services/api-service.ts';
import { GeofenceService } from '../services/geofence-service.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { TenantService } from '../services/tenant-service.ts';
import { Geofence, GeoJSONPolygon } from '../types/geofence.ts';
import { FleetGroup } from '../types/group.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';
import { formatArea } from '../utils/geo.ts';
import {
    createFleetMap, applyTileTheme, createVehicleClusterGroup, createVehicleMarker,
    seedLocationsFromDb, fetchFleetLocations, LatLon,
    VEHICLE_MARKER_STYLE, VEHICLE_MARKER_STYLE_HOVER, MAP_COLORS,
} from '../utils/fleet-map.ts';
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
    @state() private searchQuery = '';
    /** True for limited members: management controls hide, data arrives pre-filtered. */
    @state() private readOnly = false;
    // Optional overlay of live vehicle GPS dots (off by default). Geofences stay
    // the zoom focus — turning this on never refits the map.
    @state() private showVehicleLocations = false;

    private leafletMap: L.Map | null = null;
    private tileLayer: L.TileLayer | null = null;
    private geofenceLayers = new Map<string, L.Polygon>();
    private vehicleLayer: L.MarkerClusterGroup | null = null;
    private vehicleMarkers = new Map<string, L.CircleMarker>();
    // Bumped per vehicle-location load; abandons stale in-flight fan-out when the
    // overlay is toggled off or reloaded.
    private vehLoadGeneration = 0;
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
        this.leafletMap = createFleetMap(el, { zoomControl: true });
        this.tileLayer = applyTileTheme(this.leafletMap, null, el, themeService.current);
        window.addEventListener('theme-change', this.boundOnThemeChange);
        // Data may have arrived before the map existed — draw it now.
        this.renderGeofences();
        if (this.showVehicleLocations) void this.loadVehicleLocations();
    }

    override disconnectedCallback() {
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        this.leafletMap?.off('click', this.boundMapClick);
        this.geofenceLayers.clear();
        this.vehLoadGeneration++;
        this.vehicleMarkers.clear();
        this.vehicleLayer = null;
        this.leafletMap?.remove();
        this.leafletMap = null;
        this.tileLayer = null;
        super.disconnectedCallback();
    }

    private updateTileLayer(theme: 'dark' | 'light'): void {
        if (!this.leafletMap) return;
        const el = this.renderRoot.querySelector<HTMLElement>('.map');
        this.tileLayer = applyTileTheme(this.leafletMap, this.tileLayer, el, theme);
    }

    // ---- vehicle GPS overlay --------------------------------------------------

    /** Toggle the live vehicle GPS dots. On: seed from the DB and background-
     *  refresh via telemetry (same flow as the vehicle map). Off: drop the layer
     *  and abandon any in-flight fan-out. Never refits — geofences own the zoom. */
    private toggleVehicleLocations() {
        this.showVehicleLocations = !this.showVehicleLocations;
        if (this.showVehicleLocations) {
            void this.loadVehicleLocations();
        } else {
            this.vehLoadGeneration++;
            this.vehicleLayer?.remove();
            this.vehicleLayer = null;
            this.vehicleMarkers.clear();
        }
    }

    private vehicleTitle(v: Vehicle): string {
        const d = v.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : `Vehicle #${v.tokenId}`;
    }

    private async loadVehicleLocations() {
        if (!this.leafletMap) return;
        if (!this.vehicleLayer) {
            this.vehicleLayer = createVehicleClusterGroup();
            this.vehicleLayer.addTo(this.leafletMap);
        }
        const gen = ++this.vehLoadGeneration;
        // First paint from the DB's cached last-GPS-fix (already loaded with the
        // geofences), then background-refresh the stale ones via telemetry.
        this.addVehicleMarkers(seedLocationsFromDb(this.vehicles));
        await fetchFleetLocations({
            vehicles: this.vehicles,
            isCurrent: () => gen === this.vehLoadGeneration && this.showVehicleLocations,
            onBatch: (locations) => this.addVehicleMarkers(locations),
        });
        if (gen === this.vehLoadGeneration) this.vehicleLayer?.refreshClusters();
    }

    /** Add new vehicle dots or move existing ones in place. Never fits bounds —
     *  the geofences remain the map's focus. */
    private addVehicleMarkers(locations: Record<string, LatLon>) {
        if (!this.vehicleLayer) return;
        const titleMap = new Map(this.vehicles.map((v) => [String(v.tokenId), this.vehicleTitle(v)]));
        for (const [tokenId, coords] of Object.entries(locations)) {
            const existing = this.vehicleMarkers.get(tokenId);
            if (existing) {
                existing.setLatLng([coords.lat, coords.lon]);
                continue;
            }
            const marker = createVehicleMarker(coords.lat, coords.lon, titleMap.get(tokenId) ?? `Vehicle ${tokenId}`);
            marker.on('mouseover', () => marker.setStyle(VEHICLE_MARKER_STYLE_HOVER));
            marker.on('mouseout', () => marker.setStyle(VEHICLE_MARKER_STYLE));
            this.vehicleLayer.addLayer(marker);
            this.vehicleMarkers.set(tokenId, marker);
        }
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
            // Limited members get a read-only view (the API rejects mutations too).
            try {
                const access = await TenantService.getInstance().fetchMyAccess();
                this.readOnly = access.allowedGroupIds !== null;
            } catch {
                this.readOnly = false;
            }
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
                color: MAP_COLORS.mint, weight: 2, dashArray: '6 6',
            }).addTo(this.leafletMap);
        } else {
            this.drawLine = null;
        }

        this.drawVertices = L.layerGroup().addTo(this.leafletMap);
        this.drawPoints.forEach((pt, i) => {
            const first = i === 0;
            const m = L.circleMarker(pt, {
                radius: first ? 7 : 5,
                color: MAP_COLORS.ink,
                weight: 2,
                // The first vertex is the "finish" target, so it carries the
                // actionable mint; the rest are sky.
                fillColor: first ? MAP_COLORS.mint : MAP_COLORS.sky,
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
        unsafeCSS(markerClusterCss),
        css`
            :host {
                display: flex; flex-direction: column; position: relative;
                width: 100%; height: 100%; overflow: hidden; background: var(--background);
            }
            /* Same header as the map view: floats over the map and fades into it. */
            header.top-bar {
                position: absolute; top: 0; left: 0; right: 0; z-index: 40;
                display: flex; align-items: center; justify-content: space-between;
                height: var(--top-bar-height); padding: 0 var(--gutter);
                background: linear-gradient(to bottom, var(--background) 0%, color-mix(in srgb, var(--background) 72%, transparent) 55%, transparent 100%);
                pointer-events: none;
            }
            header.top-bar > * { pointer-events: auto; }
            header.top-bar h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            .top-actions { display: flex; align-items: center; gap: 10px; }
            .new-btn { padding-left: 14px; }
            .new-btn .material-symbols-outlined { font-size: 20px; }
            /* Toggle as a glass pill (matches the map controls). */
            .toggle-btn {
                display: inline-flex; align-items: center; gap: 8px;
                min-height: 40px; padding: 0 16px 0 12px;
                border-radius: var(--radius-full);
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
                box-shadow: var(--shadow-float);
                color: var(--on-surface);
                font: 500 14px/20px var(--font-body);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .toggle-btn:hover:not(:disabled):not(.active) { background: var(--surface-container-high); }
            .toggle-btn.active { background: var(--accent-soft-strong); color: var(--accent-ink); }
            .toggle-btn:disabled { opacity: 0.5; cursor: not-allowed; }
            .toggle-btn .material-symbols-outlined { font-size: 20px; }

            .body { position: relative; flex: 1; min-height: 0; }
            .map { position: absolute; inset: 0; z-index: 0; }
            .map.dark-tiles .leaflet-tile { filter: brightness(1.8); }
            .map.drawing { cursor: crosshair; }
            .map.drawing .leaflet-grab, .map.drawing .leaflet-interactive { cursor: crosshair; }
            .map .leaflet-control-attribution { font-size: 9px; opacity: 0.5; }

            /* Leaflet zoom control restyled as a floating glass pill. */
            .map .leaflet-top.leaflet-left { top: calc(var(--top-bar-height) + 16px); }
            .map .leaflet-top .leaflet-control { margin: 0 0 0 24px; }
            .map .leaflet-control-zoom.leaflet-bar {
                display: flex; flex-direction: column; gap: 2px;
                border: none;
                border-radius: var(--radius-full);
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
                box-shadow: var(--shadow-float);
                padding: 4px;
            }
            .map .leaflet-control-zoom.leaflet-bar a {
                width: 36px; height: 36px;
                display: flex; align-items: center; justify-content: center;
                border: none; border-radius: var(--radius-full);
                background: transparent; color: var(--on-surface);
                transition: background 0.15s ease;
            }
            /* Swap Leaflet's text glyphs for the icon font the other map controls use. */
            .map .leaflet-control-zoom.leaflet-bar a span { display: none; }
            .map .leaflet-control-zoom.leaflet-bar a::before {
                font-family: 'Material Symbols Outlined';
                font-size: 20px; line-height: 1;
                font-variation-settings: 'wght' 350;
                font-feature-settings: 'liga';
                -webkit-font-smoothing: antialiased;
            }
            .map .leaflet-control-zoom-in::before { content: 'add'; }
            .map .leaflet-control-zoom-out::before { content: 'remove'; }
            .map .leaflet-control-zoom.leaflet-bar a:hover { background: var(--surface-container-high); }
            .map .leaflet-control-zoom.leaflet-bar a.leaflet-disabled { color: var(--outline); background: transparent; }

            .map .leaflet-tooltip {
                background: var(--surface-container-high); color: var(--on-surface);
                border: none; border-radius: var(--radius-sm);
                box-shadow: var(--shadow-float);
                font: var(--type-label); padding: 4px 8px;
            }
            .map .leaflet-tooltip-top::before { border-top-color: var(--surface-container-high); }
            .map .leaflet-tooltip-bottom::before { border-bottom-color: var(--surface-container-high); }
            .map .leaflet-tooltip.gf-tooltip { font: 600 12px/16px var(--font-body); color: var(--primary); }

            /* Floating glass panel over the map, same as the vehicles panel. */
            .panel {
                position: absolute; top: calc(var(--top-bar-height) + 16px); right: 24px; z-index: 30;
                width: 340px; max-width: calc(100% - 48px); max-height: calc(100% - var(--top-bar-height) - 40px);
                display: flex; flex-direction: column;
                background: var(--glass-bg);
                backdrop-filter: blur(24px) saturate(1.4);
                -webkit-backdrop-filter: blur(24px) saturate(1.4);
                border-radius: var(--radius-xl);
                box-shadow: var(--shadow-float);
                overflow: hidden;
            }
            .panel-head {
                padding: 16px 20px 10px;
                font: 600 17px/24px var(--font-headline); letter-spacing: -0.01em; color: var(--primary);
            }
            .panel-head .n { font: 500 15px/24px var(--font-body); color: var(--on-surface-variant); margin-left: 2px; }
            .panel-list {
                overflow-y: auto; padding: 8px; display: flex; flex-direction: column; gap: 2px;
                border-top: 1px solid var(--outline-variant);
            }
            .panel-list::-webkit-scrollbar { width: 6px; }
            .panel-list::-webkit-scrollbar-thumb { background-color: var(--outline-variant); border-radius: 10px; }

            .gf-card {
                border-radius: 14px; padding: 12px; display: flex; flex-direction: column; gap: 8px; cursor: pointer;
                transition: background 0.15s ease;
            }
            .gf-card:hover { background: var(--surface-container-high); }
            .gf-card.selected { background: var(--accent-soft); }
            .gf-head { display: flex; align-items: center; gap: 10px; }
            .gf-head .dot {
                width: 10px; height: 10px; border-radius: var(--radius-full); flex-shrink: 0;
                background: var(--c);
                box-shadow: 0 0 8px color-mix(in srgb, var(--c) 55%, transparent);
            }
            .gf-head .name {
                font: 600 15px/22px var(--font-headline); color: var(--primary);
                flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }
            .gf-meta { display: flex; flex-wrap: wrap; gap: 4px 12px; padding-left: 20px; font: 400 13px/18px var(--font-body); color: var(--on-surface-variant); }
            .gf-meta .chip { display: inline-flex; align-items: center; gap: 4px; }
            .gf-meta .material-symbols-outlined { font-size: 14px; }

            /* Quiet actions: "Activity" reads as text; the rest are icons named
               by aria-label + tooltip. */
            .card-actions { display: flex; align-items: center; gap: 2px; padding-left: 12px; margin-right: -4px; }
            .card-actions button {
                display: inline-flex; align-items: center; justify-content: center; gap: 6px;
                height: 32px; min-width: 32px; padding: 0 8px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                font: 500 13px/18px var(--font-body);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .card-actions button.icon { padding: 0; }
            .card-actions button:first-child { margin-right: auto; }
            .card-actions button:hover,
            .card-actions button:focus-visible { background: var(--surface-container-highest); color: var(--on-surface); }
            .card-actions button.danger:hover,
            .card-actions button.danger:focus-visible { background: var(--error-container); color: var(--error); }
            .card-actions .material-symbols-outlined { font-size: 18px; }

            .confirm {
                display: flex; align-items: center; flex-wrap: wrap; gap: 8px; padding-left: 20px;
                font: var(--type-body-sm); color: var(--on-surface);
            }
            .confirm span { flex: 1 1 100%; }
            .confirm button {
                min-height: 32px; padding: 0 14px; border-radius: var(--radius-full);
                font: 500 13px/18px var(--font-body);
                transition: filter 0.15s ease, background 0.15s ease;
            }
            .confirm .yes { background: var(--error-container); color: var(--error); font-weight: 600; }
            .confirm .yes:hover { filter: brightness(1.15); }
            .confirm .no { background: var(--surface-container-high); color: var(--on-surface); }
            .confirm .no:hover { background: var(--surface-container-highest); }

            .panel-empty { padding: 24px 16px; text-align: center; color: var(--on-surface-variant); font: var(--type-body-sm); }

            /* Same search field as the vehicles panel. */
            .search-wrap {
                display: flex;
                align-items: center;
                gap: 8px;
                height: 40px;
                padding: 0 12px;
                margin: 4px 16px 12px;
                border-radius: var(--radius-md);
                background: var(--surface-container-high);
                transition: box-shadow 0.15s ease;
            }
            .search-wrap:focus-within { box-shadow: 0 0 0 2px var(--accent-soft-strong); }
            .search-wrap > .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); flex-shrink: 0; }
            .search-wrap input {
                background: none;
                border: none;
                outline: none;
                color: var(--on-surface);
                font: var(--type-body-sm);
                flex: 1;
                min-width: 0;
            }
            .search-wrap input::placeholder { color: var(--on-surface-variant); }
            .search-wrap input::-webkit-search-cancel-button { display: none; }
            .clear-btn {
                padding: 2px;
                color: var(--on-surface-variant);
                border-radius: var(--radius-full);
                display: inline-flex;
            }
            .clear-btn:hover { color: var(--primary); background: var(--surface-container-highest); }
            .clear-btn .material-symbols-outlined { font-size: 16px; }

            .draw-bar {
                position: absolute; bottom: 24px; left: 50%; transform: translateX(-50%); z-index: 35;
                display: flex; align-items: center; gap: 8px;
                max-width: calc(100% - 48px);
                padding: 6px 6px 6px 20px;
                border-radius: var(--radius-full);
                background: var(--glass-bg);
                backdrop-filter: blur(24px) saturate(1.4);
                -webkit-backdrop-filter: blur(24px) saturate(1.4);
                box-shadow: var(--shadow-float);
            }
            .draw-bar .hint { font: var(--type-body-sm); color: var(--on-surface); margin-right: 8px; }
            .draw-bar button { min-height: 36px; flex-shrink: 0; }

            .toast-error {
                position: absolute; top: calc(var(--top-bar-height) + 16px); left: 50%; transform: translateX(-50%);
                z-index: 35; max-width: 360px;
                padding: 10px 14px;
                background: var(--error-container); color: var(--error);
                border-radius: var(--radius-md); box-shadow: var(--shadow-float);
                font: var(--type-body-sm);
            }
        `,
    ];

    /** Panel list filtered by the search box; map overlays stay unfiltered. */
    private get visibleGeofences(): Geofence[] {
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) return this.geofences;
        return this.geofences.filter((g) => g.name.toLowerCase().includes(q));
    }

    private renderCard(g: Geofence) {
        const confirming = this.confirmingDeleteId === g.id;
        const count = g.vehicleCount ?? 0;
        return html`
            <div class=${this.selectedId === g.id ? 'gf-card selected' : 'gf-card'} style="--c:${g.color}" @click=${() => this.selectGeofence(g.id)}>
                <div class="gf-head">
                    <span class="dot"></span>
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
                        ${!this.readOnly && g.scope === 'manual'
                            ? html`<button class="icon" aria-label=${msg('Vehicles')} title=${msg('Vehicles')} @click=${() => { this.managing = g; }}>
                                <span class="material-symbols-outlined">directions_car</span>
                            </button>`
                            : nothing}
                        ${!this.readOnly
                            ? html`
                                <button class="icon" aria-label=${msg('Edit')} title=${msg('Edit')} @click=${() => { this.editing = g; }}>
                                    <span class="material-symbols-outlined">edit</span>
                                </button>
                                <button class="icon danger" aria-label=${msg('Delete')} title=${msg('Delete')} @click=${() => { this.confirmingDeleteId = g.id; }}>
                                    <span class="material-symbols-outlined">delete</span>
                                </button>
                              `
                            : nothing}
                    </div>`}
            </div>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <h2>${msg('Geofences')}</h2>
                <div class="top-actions">
                    <button
                        class=${this.showVehicleLocations ? 'toggle-btn active' : 'toggle-btn'}
                        ?disabled=${this.loading}
                        aria-pressed=${this.showVehicleLocations}
                        @click=${() => this.toggleVehicleLocations()}
                    >
                        <span class="material-symbols-outlined">${this.showVehicleLocations ? 'location_on' : 'location_off'}</span>
                        ${msg('Vehicles')}
                    </button>
                    ${!this.readOnly
                        ? html`<button class="btn-primary new-btn" ?disabled=${this.drawing} @click=${() => this.startDraw()}>
                            <span class="material-symbols-outlined">add_location_alt</span> ${msg('New geofence')}
                        </button>`
                        : nothing}
                </div>
            </header>

            <div class="body">
                <div class="map"></div>

                ${this.errorMessage ? html`<div class="toast-error">${this.errorMessage}</div>` : nothing}

                <div class="panel">
                    <div class="panel-head">${msg('Geofences')} ${this.geofences.length ? html`<span class="n">(${this.geofences.length})</span>` : ''}</div>
                    ${this.geofences.length > 0
                        ? html`
                            <div class="search-wrap">
                                <span class="material-symbols-outlined">search</span>
                                <input
                                    type="search"
                                    placeholder="${msg('Search geofences…')}"
                                    .value=${this.searchQuery}
                                    @input=${(e: Event) => { this.searchQuery = (e.target as HTMLInputElement).value; }}
                                />
                                ${this.searchQuery ? html`
                                    <button class="clear-btn" @click=${() => { this.searchQuery = ''; }}>
                                        <span class="material-symbols-outlined">close</span>
                                    </button>
                                ` : nothing}
                            </div>
                          `
                        : nothing}
                    <div class="panel-list">
                        ${this.loading
                            ? html`<p class="panel-empty">${msg('Loading geofences…')}</p>`
                            : this.geofences.length === 0
                                ? html`<p class="panel-empty">${msg('No geofences yet. Click “New geofence” and draw an area on the map.')}</p>`
                                : this.visibleGeofences.length === 0
                                    ? html`<p class="panel-empty">${msg('No geofences match your search.')}</p>`
                                    : this.visibleGeofences.map((g) => this.renderCard(g))}
                    </div>
                </div>

                ${this.drawing
                    ? html`<div class="draw-bar">
                        <span class="hint">${msg(str`${this.drawCount} point(s) — click the map to add, the first point to finish.`)}</span>
                        <button class="btn-primary finish" ?disabled=${this.drawCount < 3} @click=${() => this.finishDraw()}>${msg('Finish')}</button>
                        <button class="btn-secondary cancel" @click=${() => this.cancelDraw()}>${msg('Cancel')}</button>
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
