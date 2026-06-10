import { LitElement, html, css, unsafeCSS, nothing } from 'lit';
import { customElement, state, property } from 'lit/decorators.js';
import { msg } from '@lit/localize';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import { Vehicle, VehicleCard, VehiclesResponse, VehicleGroupRef } from '../types/vehicle.ts';

@customElement('fleet-overview-view')
export class FleetOverviewView extends LitElement {
    @property({ type: String }) tenantId = '';
    @state() private vehicles: VehicleCard[] = [];
    // Selected group id for the map/list filter ('' = all vehicles). Group
    // membership rides on each VehicleCard, so the dropdown options and the
    // filtered card/marker sets all derive from `this.vehicles`.
    @state() private selectedGroupId = '';
    @state() private loading = true;
    @state() private errorMessage: string | null = null;

    private leafletMap: L.Map | null = null;
    private markers = new Map<string, L.CircleMarker>();
    private lastLocations: Record<string, { lat: number; lon: number }> = {};
    private resizeObserver: ResizeObserver | null = null;
    @state() private panelCollapsed = false;
    @state() private panelExpanded = true;
    @state() private refreshing = false;

    private centerMap() {
        if (!this.leafletMap) return;
        if (this.markers.size > 0) {
            const group = L.featureGroup([...this.markers.values()]);
            this.leafletMap.fitBounds(group.getBounds().pad(0.4), { maxZoom: 12 });
        } else {
            this.leafletMap.setView([39.5, -98.35], 4);
        }
    }

    private zoomToVehicle(e: Event, tokenId: string) {
        e.preventDefault();
        e.stopPropagation();
        const marker = this.markers.get(tokenId);
        if (!marker || !this.leafletMap) return;
        this.leafletMap.flyTo(marker.getLatLng(), 14);
        if (window.innerWidth < 768) {
            this.panelCollapsed = true;
        }
    }

    private statusClass(v: VehicleCard): string {
        if (!v.online) return 'status-red';
        if (v.noPermissions) return 'status-amber';
        return 'status-green';
    }

    private formatTitle(v: Vehicle): string {
        const d = v.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : `Vehicle #${v.tokenId}`;
    }

    private toCard(v: Vehicle): VehicleCard {
        const hasSynthetic = !!(v.syntheticDevice && v.syntheticDevice.tokenId > 0);
        const hasAftermarket = !!(v.aftermarketDevice && v.aftermarketDevice.tokenId > 0);
        const integrated = hasSynthetic || hasAftermarket;
        const integration = hasAftermarket
            ? `Aftermarket #${v.aftermarketDevice!.tokenId}`
            : hasSynthetic
                ? `Synthetic #${v.syntheticDevice.tokenId}`
                : '';
        return {
            tokenId: String(v.tokenId),
            title: this.formatTitle(v),
            location: integration,
            seenAt: `Token #${v.tokenId}`,
            online: integrated,
            errorMessage: integrated ? undefined : msg('No DIMO integration — pair a device to stream telemetry'),
            isFavorite: v.isFavorite ?? false,
            groups: v.groups ?? [],
        };
    }

    /** Stable sort that pins favorites to the top, preserving relative order otherwise. */
    private sortByFavorite(cards: VehicleCard[]): VehicleCard[] {
        return [...cards].sort((a, b) => Number(!!b.isFavorite) - Number(!!a.isFavorite));
    }

    /** (Re)place map markers from `this.vehicles` + `this.lastLocations`. No-op until the map exists. */
    private placeMarkers() {
        if (!this.leafletMap) return;
        this.markers.forEach((m) => m.remove());
        this.markers.clear();

        const titleMap = new Map(this.vehicles.map((v) => [v.tokenId, v.title]));
        const allowed = this.selectedGroupId ? this.visibleTokenIds() : null;
        for (const [tokenId, coords] of Object.entries(this.lastLocations)) {
            if (allowed && !allowed.has(tokenId)) continue;
            const marker = L.circleMarker([coords.lat, coords.lon], {
                radius: 8,
                fillColor: '#69dbad',
                color: '#ffffff',
                weight: 2,
                opacity: 0.9,
                fillOpacity: 0.85,
            }).bindTooltip(titleMap.get(tokenId) ?? `Vehicle ${tokenId}`, { permanent: false, direction: 'top', offset: [0, -10] });
            marker.addTo(this.leafletMap);
            this.markers.set(tokenId, marker);
        }

        if (this.markers.size > 0) {
            const group = L.featureGroup([...this.markers.values()]);
            this.leafletMap.fitBounds(group.getBounds().pad(0.4), { maxZoom: 12 });
        }
    }

    /**
     * Loads the vehicle list + map locations. Reuses the cached result from a
     * prior visit unless `force` is set (manual refresh), so navigating to
     * vehicle details and back doesn't re-trigger the loading state or refetch.
     */
    private async loadVehicleData(force = false) {
        if (!force) {
            const cached = FleetCache.get();
            if (cached) {
                this.vehicles = cached.vehicles;
                this.lastLocations = cached.locations;
                this.loading = false;
                this.placeMarkers();
                return;
            }
        }

        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            this.vehicles = this.sortByFavorite((res.vehicles || []).map((v) => this.toCard(v)));
            this.loading = false;
        } catch (e) {
            // ApiService already redirected to /login.html on 401/400.
            this.loading = false;
            console.error('Failed to load vehicles', e);
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to load vehicles');
            return;
        }

        this.lastLocations = {};
        // Single request for all vehicle locations. Per-vehicle JWT check on the
        // backend determines which vehicles the dev license has SACD access to.
        try {
            const res = await TelemetryService.getInstance().fleetLocations();

            // Mark vehicles where JWT exchange failed (no SACD permissions).
            const noPermSet = new Set(res.noPermissions ?? []);
            if (noPermSet.size > 0) {
                this.vehicles = this.vehicles.map((v) =>
                    noPermSet.has(v.tokenId) ? { ...v, noPermissions: true } : v
                );
            }

            this.lastLocations = res.locations;
        } catch {
            // Network failure — map stays at default view.
        }

        this.placeMarkers();
        FleetCache.set({ vehicles: this.vehicles, locations: this.lastLocations });
    }

    private async refreshVehicles() {
        if (this.refreshing) return;
        this.refreshing = true;
        this.errorMessage = null;
        try {
            await this.loadVehicleData(true);
        } finally {
            this.refreshing = false;
        }
    }

    async connectedCallback() {
        super.connectedCallback();
        await this.loadVehicleData();
    }

    private updateMinZoom() {
        if (!this.leafletMap) return;
        // Tiles are 256px at zoom 0; minZoom must ensure world width >= container width.
        const w = this.leafletMap.getContainer().clientWidth;
        const minZoom = Math.ceil(Math.log2(w / 256));
        this.leafletMap.setMinZoom(minZoom);
        if (this.leafletMap.getZoom() < minZoom) {
            this.leafletMap.setZoom(minZoom);
        }
    }

    override firstUpdated() {
        const el = this.renderRoot.querySelector<HTMLElement>('.map');
        if (!el) return;
        const worldBounds = L.latLngBounds([[-85.051129, -180], [85.051129, 180]]);
        this.leafletMap = L.map(el, {
            zoomControl: false,
            attributionControl: true,
            maxBounds: worldBounds,
            maxBoundsViscosity: 1.0,
        }).setView([39.5, -98.35], 4);
        L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
            attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
            subdomains: 'abcd',
            maxZoom: 19,
            noWrap: true,
        }).addTo(this.leafletMap);

        this.resizeObserver = new ResizeObserver(() => this.updateMinZoom());
        this.resizeObserver.observe(el);
        this.updateMinZoom();

        // Cached data may have already been loaded into `this.vehicles` /
        // `this.lastLocations` before the map existed — place markers now.
        this.placeMarkers();
    }

    override disconnectedCallback() {
        super.disconnectedCallback();
        this.resizeObserver?.disconnect();
        this.resizeObserver = null;
        this.markers.clear();
        this.leafletMap?.remove();
        this.leafletMap = null;
    }

    /** Distinct groups across all vehicles, for the filter dropdown (by name). */
    private groupOptions(): VehicleGroupRef[] {
        const byId = new Map<string, VehicleGroupRef>();
        for (const c of this.vehicles) {
            for (const g of c.groups || []) {
                if (!byId.has(g.id)) byId.set(g.id, g);
            }
        }
        return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name));
    }

    /** Cards in the selected group (or all when no group is selected). */
    private visibleCards(): VehicleCard[] {
        if (!this.selectedGroupId) return this.vehicles;
        return this.vehicles.filter((c) => (c.groups || []).some((g) => g.id === this.selectedGroupId));
    }

    /** Token ids visible under the current filter — used to filter map markers. */
    private visibleTokenIds(): Set<string> {
        return new Set(this.visibleCards().map((c) => c.tokenId));
    }

    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        css`
            :host {
                display: flex;
                flex-direction: column;
                position: relative;
                width: 100%;
                height: 100%;
                overflow: hidden;
            }

            header.top-bar {
                position: absolute;
                top: 0;
                left: 0;
                right: 0;
                height: 80px;
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
                border-bottom: 1px solid var(--outline-variant);
                z-index: 40;
                background: rgba(28, 27, 27, 0.85);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
            }
            @media (max-width: 768px) {
                header.top-bar { display: none; }
            }
            header.top-bar .left { display: flex; align-items: center; gap: 32px; }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            header.top-bar nav { display: flex; gap: 24px; }
            header.top-bar nav a {
                text-decoration: none;
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                padding-bottom: 4px;
            }
            header.top-bar nav a.active {
                color: var(--primary);
                border-bottom: 2px solid var(--primary);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }
            header.top-bar .live-tracking {
                background: var(--primary);
                color: var(--on-primary);
                padding: 8px 16px;
                border-radius: var(--radius-md);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
            }
            header.top-bar .icon-btn {
                color: var(--on-surface-variant);
                background: none;
                border: none;
                padding: 4px;
            }

            .map {
                position: absolute;
                inset: 0;
                z-index: 0;
            }
            /* Lift the very-dark CARTO tiles to a readable contrast level */
            .map .leaflet-tile {
                filter: brightness(1.8);
            }
            /* Push attribution below the vehicles panel on mobile */
            .map .leaflet-control-attribution {
                font-size: 9px;
                opacity: 0.5;
            }

            .map-controls {
                position: absolute;
                top: 96px;
                left: 24px;
                display: flex;
                flex-direction: column;
                gap: 12px;
                z-index: 10;
            }
            .map-controls button {
                width: 48px;
                height: 48px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full);
                color: var(--on-surface);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease;
            }
            .map-controls button:hover { background: var(--surface-container-high); }

            .map-legend {
                position: absolute;
                bottom: 40px;
                left: 24px;
                display: flex;
                flex-direction: row;
                gap: 8px;
                z-index: 10;
            }
            .map-legend button {
                width: 48px;
                height: 48px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full);
                color: var(--on-surface);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease;
            }
            .map-legend button:hover { background: var(--surface-container-high); }
            .map-legend button:disabled { cursor: default; opacity: 0.6; }
            .map-legend .spinning .material-symbols-outlined {
                animation: spin 0.8s linear infinite;
            }
            @keyframes spin {
                from { transform: rotate(0deg); }
                to { transform: rotate(360deg); }
            }

            .vehicles-panel {
                position: absolute;
                bottom: 0;
                left: 0;
                right: 0;
                z-index: 20;
                background: rgba(28, 27, 27, 0.85);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                border: 1px solid var(--outline-variant);
                border-radius: 24px 24px 0 0;
                display: flex;
                flex-direction: column;
                overflow: hidden;
                box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
                transition: transform 0.3s ease, width 0.3s ease;
            }
            .vehicles-panel.collapsed {
                transform: translateY(calc(100% - 56px));
            }
            @media (min-width: 768px) {
                .vehicles-panel {
                    top: 96px;
                    bottom: 24px;
                    right: 24px;
                    left: auto;
                    width: 384px;
                    border-radius: 24px;
                }
                .vehicles-panel.narrow { width: 96px; }
                .vehicles-panel.narrow .panel-header { display: none; }
                .vehicles-panel.narrow .vehicle-list { padding: 8px; gap: 8px; }
            }

            .chevron-tab {
                display: none;
                position: absolute;
                top: 120px;
                right: calc(24px + 384px - 12px);
                width: 24px;
                height: 24px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                align-items: center;
                justify-content: center;
                z-index: 25;
                cursor: pointer;
                transition: right 0.3s ease, background 0.15s ease;
            }
            .chevron-tab .material-symbols-outlined { font-size: 16px; }
            .chevron-tab:hover { background: var(--surface-container-high); }
            @media (min-width: 768px) {
                .chevron-tab { display: flex; }
                .chevron-tab.narrow { right: calc(24px + 96px - 12px); }
            }

            .vehicle-card-compact {
                display: flex;
                align-items: center;
                justify-content: center;
                height: 64px;
                border-radius: var(--radius-md);
                text-decoration: none;
                color: inherit;
                position: relative;
                flex-shrink: 0;
                transition: background 0.15s ease;
            }
            .vehicle-card-compact:hover { background: var(--surface-container-high); }
            .compact-token-id {
                font: var(--type-label-caps);
                font-size: 11px;
                letter-spacing: 0.04em;
                color: var(--on-surface-variant);
                text-align: center;
                line-height: 1.2;
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
                max-width: 100%;
                padding: 0 4px;
            }
            .vehicle-card-compact .zoom-btn {
                top: auto;
                bottom: 4px;
                right: 4px;
                width: 20px;
                height: 20px;
            }
            .vehicle-card-compact .status-dot {
                top: 6px;
                left: 6px;
                width: 9px;
                height: 9px;
                border: none;
            }
            .vehicle-card-compact .zoom-btn .material-symbols-outlined { font-size: 12px; }

            .drag-handle {
                width: 100%;
                display: flex;
                justify-content: center;
                padding: 12px 0;
            }
            .drag-handle div {
                width: 48px;
                height: 4px;
                background: var(--outline-variant);
                border-radius: var(--radius-full);
            }
            @media (min-width: 768px) {
                .drag-handle { display: none; }
            }

            .panel-header {
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px 24px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .panel-header h3 { font: var(--type-headline-md); color: var(--primary); }
            .panel-header button {
                color: var(--primary);
                background: none;
                border: none;
                padding: 8px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease;
            }
            .panel-header button:hover { background: var(--surface-container-high); }

            .group-filter {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 12px 24px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .group-filter .swatch {
                width: 12px;
                height: 12px;
                border-radius: var(--radius-full);
                border: 1px solid var(--outline-variant);
                flex-shrink: 0;
            }
            .group-filter select {
                flex: 1;
                background: var(--surface-container-low);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 8px 10px;
                font-family: inherit;
                font-size: 13px;
            }
            .group-filter select:focus { outline: 1px solid var(--primary); }

            .vehicle-list {
                flex: 1;
                overflow-y: auto;
                padding: 16px;
                display: flex;
                flex-direction: column;
                gap: 16px;
            }
            .empty-state {
                color: var(--on-surface-variant);
                font: var(--type-body-sm);
                padding: 24px;
                text-align: center;
            }
            .empty-state.error { color: var(--error); }

            .vehicle-list::-webkit-scrollbar { width: 6px; }
            .vehicle-list::-webkit-scrollbar-thumb {
                background-color: var(--outline-variant);
                border-radius: 10px;
            }

            .vehicle-card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 16px;
                cursor: pointer;
                transition: border-color 0.15s ease;
                text-decoration: none;
                color: inherit;
                display: block;
                position: relative;
            }

            .zoom-btn {
                position: absolute;
                top: 12px;
                right: 12px;
                width: 32px;
                height: 32px;
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease, color 0.15s ease;
                z-index: 1;
            }
            .zoom-btn:hover {
                background: var(--primary);
                color: var(--on-primary);
                border-color: var(--primary);
            }
            .zoom-btn .material-symbols-outlined { font-size: 16px; }
            .vehicle-card:hover { border-color: rgba(255, 255, 255, 0.5); }
            .vehicle-card.offline { border-color: rgba(255, 180, 171, 0.2); }
            .vehicle-card.offline:hover { border-color: rgba(255, 180, 171, 0.5); }

            .status-dot {
                position: absolute;
                width: 12px;
                height: 12px;
                border-radius: var(--radius-full);
                border: 2px solid var(--surface-container-low);
            }
            .status-dot.status-green { background: #69dbad; }
            .status-dot.status-red { background: var(--error); }
            .status-dot.status-amber { background: #ffb432; }

            .vehicle-row { display: flex; align-items: flex-start; gap: 16px; }
            .vehicle-icon {
                position: relative;
                width: 64px;
                height: 64px;
                border-radius: var(--radius-full);
                background: var(--surface-container-highest);
                border: 1px solid var(--outline-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                flex-shrink: 0;
            }
            .vehicle-icon .status-dot { bottom: -2px; right: -2px; }
            .vehicle-icon .material-symbols-outlined { color: var(--primary); font-size: 32px; }
            .vehicle-card.offline .vehicle-icon { opacity: 0.5; }
            .vehicle-card.offline .vehicle-icon .material-symbols-outlined { color: var(--on-surface-variant); }

            .vehicle-meta { flex: 1; min-width: 0; }
            .vehicle-meta h4 {
                font: var(--type-body-lg);
                font-weight: 600;
                color: var(--primary);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                display: flex;
                align-items: center;
                gap: 6px;
            }
            .favorite-star {
                font-size: 16px;
                color: #ffb432;
                flex-shrink: 0;
            }
            .favorite-star-compact {
                position: absolute;
                top: 4px;
                right: 4px;
                font-size: 12px;
                color: #ffb432;
            }
            .vehicle-meta .location {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 4px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            .vehicle-meta .seen {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-top: 8px;
            }
            .vehicle-meta .row-flex {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-top: 8px;
            }
            .notif-badge {
                background: var(--surface-container-highest);
                color: var(--on-surface);
                padding: 2px 8px;
                border-radius: var(--radius-sm);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
            }
            .vehicle-meta .error-msg {
                margin-top: 4px;
                display: flex;
                align-items: center;
                gap: 4px;
                color: var(--error);
                font: var(--type-body-sm);
            }
            .vehicle-meta .error-msg .material-symbols-outlined { font-size: 16px; }
            .no-permissions-badge {
                margin-top: 6px;
                display: inline-flex;
                align-items: center;
                gap: 4px;
                background: rgba(255, 180, 50, 0.12);
                border: 1px solid rgba(255, 180, 50, 0.35);
                color: #ffb432;
                border-radius: var(--radius-sm);
                padding: 2px 8px;
                font: var(--type-label-caps);
                letter-spacing: 0.04em;
            }
            .no-permissions-badge .material-symbols-outlined { font-size: 14px; }
        `,
    ];

    private renderCard(v: VehicleCard) {
        const cls = v.online ? 'vehicle-card' : 'vehicle-card offline';
        return html`
            <a class=${cls} href="#/${this.tenantId}/vehicles/${v.tokenId}">
                ${this.markers.has(v.tokenId) ? html`
                    <button class="zoom-btn" title="${msg('Zoom to vehicle')}" @click=${(e: Event) => this.zoomToVehicle(e, v.tokenId)}>
                        <span class="material-symbols-outlined">my_location</span>
                    </button>
                ` : ''}
                <div class="vehicle-row">
                    <div class="vehicle-icon">
                        <span class="material-symbols-outlined">directions_car</span>
                        <span class="status-dot ${this.statusClass(v)}"></span>
                    </div>
                    <div class="vehicle-meta">
                        <h4>
                            ${v.isFavorite ? html`<span class="material-symbols-outlined favorite-star" title="${msg('Favorite')}">star</span>` : ''}
                            ${v.title}
                        </h4>
                        ${v.online ? html`
                            <p class="location">${v.location}</p>
                            ${v.notification
                                ? html`<div class="row-flex">
                                        <p class="seen">${v.seenAt}</p>
                                        <span class="notif-badge">${v.notification}</span>
                                    </div>`
                                : html`<p class="seen">${v.seenAt}</p>`
                            }
                            ${v.noPermissions ? html`
                                <div class="no-permissions-badge">
                                    <span class="material-symbols-outlined">lock</span>
                                    <span>${msg('No location access')}</span>
                                </div>
                            ` : ''}
                        ` : html`
                            <div class="error-msg">
                                <span class="material-symbols-outlined">warning</span>
                                <span>${v.errorMessage}</span>
                            </div>
                        `}
                    </div>
                </div>
            </a>
        `;
    }

    private renderCompactCard(v: VehicleCard) {
        return html`
            <a class="vehicle-card-compact" href="#/${this.tenantId}/vehicles/${v.tokenId}" title=${v.title}>
                <span class="status-dot ${this.statusClass(v)}"></span>
                ${v.isFavorite ? html`<span class="material-symbols-outlined favorite-star-compact" title="${msg('Favorite')}">star</span>` : ''}
                <span class="compact-token-id">${v.tokenId}</span>
                ${this.markers.has(v.tokenId) ? html`
                    <button class="zoom-btn" title="${msg('Zoom to vehicle')}" @click=${(e: Event) => this.zoomToVehicle(e, v.tokenId)}>
                        <span class="material-symbols-outlined">my_location</span>
                    </button>
                ` : ''}
            </a>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <div class="left">
                    <h2>${msg('Fleet Overview')}</h2>
                    <nav>
                        <a href="#/${this.tenantId}/" class="active">${msg('Map View')}</a>
                        <a href="#/${this.tenantId}/stats">${msg('List View')}</a>
                    </nav>
                </div>
                <div class="right">
                    <tenant-switcher .currentTenantId=${this.tenantId}></tenant-switcher>
                    <button class="live-tracking">${msg('Live Tracking')}</button>
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <button class="icon-btn"><span class="material-symbols-outlined">account_circle</span></button>
                </div>
            </header>

            <div class="map"></div>

            <div class="map-controls">
                <button @click=${this.centerMap} title="${msg('Fit all vehicles')}">
                    <span class="material-symbols-outlined">my_location</span>
                </button>
            </div>

            <div class="map-legend">
                <button title="${msg('Center map')}" @click=${() => this.centerMap()}>
                    <span class="material-symbols-outlined">center_focus_strong</span>
                </button>
                <button title="${msg('Zoom in')}" @click=${() => this.leafletMap?.zoomIn()}>
                    <span class="material-symbols-outlined">add</span>
                </button>
                <button title="${msg('Zoom out')}" @click=${() => this.leafletMap?.zoomOut()}>
                    <span class="material-symbols-outlined">remove</span>
                </button>
                <button
                    class=${this.refreshing ? 'spinning' : ''}
                    title="${msg('Refresh vehicle state')}"
                    ?disabled=${this.refreshing}
                    @click=${() => this.refreshVehicles()}
                >
                    <span class="material-symbols-outlined">refresh</span>
                </button>
            </div>

            <button
                class="chevron-tab ${!this.panelExpanded ? 'narrow' : ''}"
                title="${this.panelExpanded ? msg('Collapse panel') : msg('Expand panel')}"
                @click=${() => { this.panelExpanded = !this.panelExpanded; }}
            >
                <span class="material-symbols-outlined">${this.panelExpanded ? 'chevron_right' : 'chevron_left'}</span>
            </button>

            <div class="vehicles-panel ${this.panelCollapsed ? 'collapsed' : ''} ${!this.panelExpanded ? 'narrow' : ''}">
                <div class="drag-handle" @click=${() => { this.panelCollapsed = false; }}><div></div></div>
                <div class="panel-header">
                    <h3>${msg('Your cars')}</h3>
                    <a href="#/${this.tenantId}/groups" title="${msg('Manage groups')}">
                        <button><span class="material-symbols-outlined">workspaces</span></button>
                    </a>
                </div>
                ${this.renderGroupFilter()}
                <div class="vehicle-list custom-scrollbar">
                    ${this.renderList()}
                </div>
            </div>
        `;
    }

    private renderGroupFilter() {
        const options = this.groupOptions();
        if (options.length === 0) return nothing;
        const selected = options.find((g) => g.id === this.selectedGroupId);
        return html`
            <div class="group-filter">
                <span class="swatch" style="background:${selected ? selected.color : 'transparent'}"></span>
                <select @change=${(e: Event) => { this.selectedGroupId = (e.target as HTMLSelectElement).value; this.placeMarkers(); }}>
                    <option value="" ?selected=${this.selectedGroupId === ''}>${msg('All groups')}</option>
                    ${options.map((g) => html`
                        <option value=${g.id} ?selected=${g.id === this.selectedGroupId}>${g.name}</option>
                    `)}
                </select>
            </div>
        `;
    }

    private renderList() {
        if (this.loading) return html`<p class="empty-state">${msg('Loading vehicles…')}</p>`;
        if (this.errorMessage) return html`<p class="empty-state error">${this.errorMessage}</p>`;
        const cards = this.visibleCards();
        if (cards.length === 0) {
            return html`<p class="empty-state">${this.selectedGroupId ? msg('No vehicles in this group.') : msg('No vehicles found on this account.')}</p>`;
        }
        return cards.map((c) => this.panelExpanded ? this.renderCard(c) : this.renderCompactCard(c));
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'fleet-overview-view': FleetOverviewView;
    }
}
