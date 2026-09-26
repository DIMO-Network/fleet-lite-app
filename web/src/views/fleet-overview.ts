import { LitElement, html, css, unsafeCSS, nothing } from 'lit';
import { customElement, state, property } from 'lit/decorators.js';
import { msg } from '@lit/localize';
import { getLocale } from '../localization.ts';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import 'leaflet.markercluster';
import markerClusterCss from 'leaflet.markercluster/dist/MarkerCluster.css?inline';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { hiddenVehiclesService } from '../services/hidden-vehicles-service.ts';
import { ApiService } from '../services/api-service.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import { Vehicle, VehicleCard, VehiclesResponse, VehicleGroupRef } from '../types/vehicle.ts';
import {
    createFleetMap, applyTileTheme, createVehicleClusterGroup, createVehicleMarker, setVehicleHeading,
    seedLocationsFromDb, fetchFleetLocations, HeadingMarker, LatLon, VEHICLE_TOOLTIP_CSS,
    VEHICLE_MARKER_STYLE, VEHICLE_MARKER_STYLE_HOVER, VEHICLE_MARKER_STYLE_SELECTED, VEHICLE_MARKER_STYLE_HIDDEN,
    tripMapStyles,
} from '../utils/fleet-map.ts';
import { brandLogoUrl } from '../utils/brand-logo.ts';
import { contrastingBadgeBackground } from '../utils/logo-color.ts';
import '../elements/vehicle-quick-view.ts';

@customElement('fleet-overview-view')
export class FleetOverviewView extends LitElement {
    @property({ type: String }) tenantId = '';
    @state() private vehicles: VehicleCard[] = [];
    // Selected group id for the map/list filter ('' = all vehicles). Group
    // membership rides on each VehicleCard, so the dropdown options and the
    // filtered card/marker sets all derive from `this.vehicles`.
    @state() private selectedGroupId = '';
    // Free-text filter applied on top of the group filter; matches title,
    // token id, and integration label. Filters both the list and the markers.
    @state() private searchQuery = '';
    @state() private loading = true;
    @state() private errorMessage: string | null = null;

    private leafletMap: L.Map | null = null;
    private tileLayer: L.TileLayer | null = null;
    private clusterGroup: L.MarkerClusterGroup | null = null;
    private markers = new Map<string, HeadingMarker>();
    private lastLocations: Record<string, LatLon> = {};
    private resizeObserver: ResizeObserver | null = null;
    /** Tokens whose brand logo failed to load — fall back to the generic car icon. */
    @state() private brokenLogos = new Set<string>();
    /** Per-vehicle badge background colors, picked to contrast with the loaded logo. */
    @state() private logoBadgeColors = new Map<string, string>();
    // Incremented per load; lets stale in-flight chunk results from a
    // superseded load (tenant switch, manual refresh) be discarded. The
    // progressive-loading tuning + freshness window now live in fleet-map.ts.
    private loadGeneration = 0;
    // Vehicle shown in the quick-view overlay (null = closed). Opened by
    // clicking a marker or a list card; full details remain a click away
    // inside the overlay.
    @state() private quickViewVehicle: VehicleCard | null = null;
    // Selected trip's route drawn on the map (polyline + start/end dots).
    private tripRouteLayer: L.Polyline | null = null;
    private tripEndpointLayers: L.CircleMarker[] = [];
    @state() private panelCollapsed = false;
    @state() private panelExpanded = true;
    @state() private denseCards = false;
    @state() private searchOpen = false;
    @state() private refreshing = false;
    @state() private hiddenVehicles = new Set<string>();
    @state() private showHidden = false;
    private unsubscribeHidden: (() => void) | null = null;
    // Key of the most recently copied value (`<tokenId>:plate|vin`), used to
    // flash a "copied" checkmark on that one button. Cleared after a moment.
    @state() private copiedKey = '';

    private boundOnThemeChange = (e: Event) => {
        const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
        this.updateTileLayer(theme);
    };

    private updateTileLayer(theme: 'dark' | 'light'): void {
        if (!this.leafletMap) return;
        const mapEl = this.renderRoot.querySelector<HTMLElement>('.map');
        this.tileLayer = applyTileTheme(this.leafletMap, this.tileLayer, mapEl, theme);
        const trip = tripMapStyles(theme);
        this.tripRouteLayer?.setStyle({ color: trip.route });
        const [start, end] = this.tripEndpointLayers;
        start?.setStyle(trip.start);
        end?.setStyle(trip.end);
    }

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
        if (!marker || !this.leafletMap || !this.clusterGroup) return;
        if (window.innerWidth < 768) {
            this.panelCollapsed = true;
        }
        this.clusterGroup.zoomToShowLayer(marker, () => {
            this.leafletMap!.flyTo(marker.getLatLng(), Math.max(this.leafletMap!.getZoom(), 14));
        });
    }

    /**
     * Copy a card value (license plate or VIN) to the clipboard. Stops the click
     * from bubbling to the card (which would open the quick-view) and briefly
     * flashes a checkmark on the originating button via copiedKey.
     */
    private copyValue = async (e: Event, value: string, key: string) => {
        e.preventDefault();
        e.stopPropagation();
        if (!value || !navigator.clipboard) return;
        try {
            await navigator.clipboard.writeText(value);
            this.copiedKey = key;
            window.setTimeout(() => {
                if (this.copiedKey === key) this.copiedKey = '';
            }, 1200);
        } catch { /* clipboard blocked — noop */ }
    };

    /** Default and selected circle-marker styles for the quick-view highlight. */
    // Small dots by default — fleets can run to thousands of vehicles, so the
    // map must stay readable at density. Hover grows the dot to make it an
    // easier click target; selection grows it further and recolors.
    private static readonly MARKER_STYLE = VEHICLE_MARKER_STYLE;
    private static readonly MARKER_STYLE_HOVER = VEHICLE_MARKER_STYLE_HOVER;
    private static readonly MARKER_STYLE_SELECTED = VEHICLE_MARKER_STYLE_SELECTED;
    private static readonly MARKER_STYLE_HIDDEN = VEHICLE_MARKER_STYLE_HIDDEN;

    private openQuickView(v: VehicleCard) {
        // Restore the previously selected marker, highlight the new one. Any
        // trip route belongs to the previous vehicle — clear it.
        if (this.quickViewVehicle) {
            this.markers.get(this.quickViewVehicle.tokenId)?.setStyle(FleetOverviewView.MARKER_STYLE);
        }
        this.clearTripRoute();
        this.quickViewVehicle = v;
        const marker = this.markers.get(v.tokenId);
        if (marker && this.leafletMap) {
            marker.setStyle(FleetOverviewView.MARKER_STYLE_SELECTED);
            this.leafletMap.flyTo(marker.getLatLng(), Math.max(this.leafletMap.getZoom(), 12));
        }
        // On mobile the bottom sheet covers the list anyway; collapse it.
        if (window.innerWidth < 768) this.panelCollapsed = true;
    }

    private closeQuickView() {
        if (this.quickViewVehicle) {
            this.markers.get(this.quickViewVehicle.tokenId)?.setStyle(FleetOverviewView.MARKER_STYLE);
        }
        this.clearTripRoute();
        this.quickViewVehicle = null;
    }

    /**
     * Real-time tick from the quick-view for the selected vehicle: re-pull its
     * location (force, bypassing the freshness window + backend cache) and move
     * its marker in place. The quick-view refreshes its own signals separately.
     */
    private async onRealtimeTick(e: CustomEvent<{ tokenId: string }>) {
        const tokenId = e.detail?.tokenId ?? this.quickViewVehicle?.tokenId;
        if (!tokenId) return;
        try {
            const res = await TelemetryService.getInstance().fleetLocations(true, [tokenId]);
            // Ignore if the user closed/switched the panel while in flight.
            if (this.quickViewVehicle?.tokenId !== tokenId) return;
            const loc = res.locations?.[tokenId];
            if (!loc) return;
            const fix: LatLon = { lat: loc.lat, lon: loc.lon, heading: loc.heading };
            this.lastLocations = { ...this.lastLocations, [tokenId]: fix };
            const marker = this.markers.get(tokenId);
            if (marker) {
                marker.setLatLng([loc.lat, loc.lon]);
                const title = this.vehicles.find((c) => c.tokenId === tokenId)?.title ?? `Vehicle ${tokenId}`;
                setVehicleHeading(marker, title, loc.heading);
            } else {
                this.addMarkers({ [tokenId]: fix });
            }
            // Refresh the card's "last seen" so the list reflects the live fix.
            if (loc.timestamp) {
                this.vehicles = this.vehicles.map((c) =>
                    c.tokenId === tokenId ? { ...c, lastSeen: loc.timestamp } : c);
            }
        } catch {
            // Transient — the next tick retries.
        }
    }

    /** Draw (or clear, when points is null) the selected trip's route. */
    private onTripRoute(e: CustomEvent<{ points: Array<[number, number]> | null }>) {
        this.clearTripRoute();
        const points = e.detail.points;
        if (!points || points.length === 0 || !this.leafletMap) return;
        const trip = tripMapStyles(themeService.current);
        this.tripRouteLayer = L.polyline(points, {
            color: trip.route,
            weight: 4,
            opacity: 0.9,
        }).addTo(this.leafletMap);
        // Start/end dots so direction is readable at a glance.
        this.tripEndpointLayers = [
            L.circleMarker(points[0], trip.start).addTo(this.leafletMap),
            L.circleMarker(points[points.length - 1], trip.end).addTo(this.leafletMap),
        ];
        this.leafletMap.fitBounds(this.tripRouteLayer.getBounds(), { padding: [40, 40], maxZoom: 15 });
    }

    private clearTripRoute() {
        this.tripRouteLayer?.remove();
        this.tripRouteLayer = null;
        this.tripEndpointLayers.forEach((m) => m.remove());
        this.tripEndpointLayers = [];
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
            make: v.definition.make,
            title: this.formatTitle(v),
            // The VIN (when known) is shown on its own line; this is the
            // device-integration fallback for when we don't have a VIN.
            location: integration,
            vin: v.vin || undefined,
            seenAt: `Token #${v.tokenId}`,
            lastSeen: v.lastSeen,
            online: integrated,
            // See fleet-list-view.toCard: an unsynced vehicle arrives zeroed and
            // must not be reported as having no device paired.
            errorMessage: v.metadataPending
                ? msg('Details still syncing — this vehicle was added recently')
                : integrated
                    ? undefined
                    : msg('No DIMO integration — pair a device to stream telemetry'),
            isFavorite: v.isFavorite ?? false,
            licensePlate: v.licensePlate,
            groups: v.groups ?? [],
            metadataPending: v.metadataPending ?? false,
        };
    }

    /** Stable sort that pins favorites to the top. The server already returns
     * vehicles most-recently-seen first, so within each group (favorite /
     * non-favorite) the last-seen ordering is preserved. */
    private sortByFavorite(cards: VehicleCard[]): VehicleCard[] {
        return [...cards].sort((a, b) => Number(!!b.isFavorite) - Number(!!a.isFavorite));
    }

    /** Render an ISO timestamp as a localized "last seen 5 min ago" string,
     * picking the largest sensible unit. Empty for an unparseable/future time. */
    private formatLastSeen(iso?: string): string {
        if (!iso) return '';
        const then = new Date(iso).getTime();
        if (Number.isNaN(then)) return '';
        const diffMs = then - Date.now();
        const sec = Math.round(diffMs / 1000);
        const rtf = new Intl.RelativeTimeFormat(getLocale(), { numeric: 'auto', style: 'long' });
        const abs = Math.abs(sec);
        let value: number;
        let unit: Intl.RelativeTimeFormatUnit;
        if (abs < 60) { value = sec; unit = 'second'; }
        else if (abs < 3600) { value = Math.round(sec / 60); unit = 'minute'; }
        else if (abs < 86400) { value = Math.round(sec / 3600); unit = 'hour'; }
        else if (abs < 2592000) { value = Math.round(sec / 86400); unit = 'day'; }
        else if (abs < 31536000) { value = Math.round(sec / 2592000); unit = 'month'; }
        else { value = Math.round(sec / 31536000); unit = 'year'; }
        return msg('Last seen', { id: 'last-seen-prefix' }) + ' ' + rtf.format(value, unit);
    }

    /**
     * Add markers for `locations` that aren't on the map yet, respecting the
     * current group/search/hidden filter. Additive on purpose: the progressive loader
     * calls this per batch so markers stream in without clearing the map.
     */
    private addMarkers(locations: Record<string, LatLon>) {
        if (!this.leafletMap) return;
        const titleMap = new Map(this.vehicles.map((v) => [v.tokenId, v.title]));
        const allowed = (this.selectedGroupId || this.searchQuery.trim()) ? this.visibleTokenIds() : null;
        for (const [tokenId, coords] of Object.entries(locations)) {
            if (this.markers.has(tokenId)) continue;
            const isHidden = this.hiddenVehicles.has(tokenId);
            if (isHidden && !this.showHidden) continue;
            if (allowed && !allowed.has(tokenId)) continue;
            const selected = this.quickViewVehicle?.tokenId === tokenId;
            const baseStyle = isHidden ? FleetOverviewView.MARKER_STYLE_HIDDEN : FleetOverviewView.MARKER_STYLE;
            const marker = createVehicleMarker(
                coords,
                titleMap.get(tokenId) ?? `Vehicle ${tokenId}`,
                selected ? FleetOverviewView.MARKER_STYLE_SELECTED : baseStyle,
            );
            marker.on('click', () => {
                const v = this.vehicles.find((c) => c.tokenId === tokenId);
                if (v) this.openQuickView(v);
            });
            // Grow on hover for an easier click target; never shrink the
            // selected marker back down. Hidden markers don't grow.
            marker.on('mouseover', () => {
                if (this.quickViewVehicle?.tokenId !== tokenId && !isHidden) {
                    marker.setStyle(FleetOverviewView.MARKER_STYLE_HOVER);
                }
            });
            marker.on('mouseout', () => {
                if (this.quickViewVehicle?.tokenId !== tokenId) {
                    marker.setStyle(isHidden ? FleetOverviewView.MARKER_STYLE_HIDDEN : FleetOverviewView.MARKER_STYLE);
                }
            });
            this.clusterGroup!.addLayer(marker);
            this.markers.set(tokenId, marker);
        }
    }

    /** (Re)place map markers from `this.vehicles` + `this.lastLocations`. No-op until the map exists. */
    private placeMarkers() {
        if (!this.leafletMap) return;
        this.clusterGroup!.clearLayers();
        this.markers.clear();
        this.addMarkers(this.lastLocations);

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
        let paintedFromPersist = false;
        if (!force) {
            const cached = FleetCache.get(this.tenantId);
            if (cached) {
                this.vehicles = cached.vehicles;
                this.lastLocations = cached.locations;
                this.loading = false;
                this.placeMarkers();
                return;
            }
            // Cold load (hard reload / new tab): paint instantly from the last
            // persisted snapshot while the /vehicles fetch below revalidates.
            // Paint-only — execution always continues to the network, and the
            // fresh response replaces everything painted here.
            const tid = this.tenantId;
            const persisted = await FleetCache.loadPersisted(tid);
            if (persisted && tid === this.tenantId) {
                this.vehicles = persisted.vehicles;
                this.lastLocations = persisted.locations;
                this.loading = false;
                paintedFromPersist = true;
                this.placeMarkers();
                this.centerMap();
            }
        }

        let rawVehicles: Vehicle[] = [];
        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            rawVehicles = res.vehicles || [];
            this.vehicles = this.sortByFavorite(rawVehicles.map((v) => this.toCard(v)));
            this.loading = false;
        } catch (e) {
            // ApiService already redirected to /login.html on 401/400.
            this.loading = false;
            console.error('Failed to load vehicles', e);
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to load vehicles');
            return;
        }

        // Paint markers immediately from the DB's cached last-GPS-fix so the map
        // isn't blank while the live per-vehicle fan-out (below) streams in; the
        // fan-out then overwrites these with fresh coordinates.
        this.lastLocations = seedLocationsFromDb(rawVehicles);
        if (Object.keys(this.lastLocations).length > 0) {
            this.placeMarkers();
            this.centerMap();
        }
        // Sync map markers to the updated vehicle list immediately: remove any
        // marker for a vehicle that's no longer in the fleet (e.g. a shared
        // vehicle that was revoked). Needed after a force refresh and after a
        // persisted-snapshot paint, both of which can have drawn markers for
        // vehicles the fresh /vehicles response no longer contains. New
        // vehicles get markers as locations stream in below; existing vehicles
        // keep their current markers until the final placeMarkers() call
        // rebuilds everything at full fidelity.
        if (force || paintedFromPersist) {
            const currentIds = new Set(this.vehicles.map((v) => v.tokenId));
            for (const [tokenId, marker] of this.markers) {
                if (!currentIds.has(tokenId)) {
                    this.clusterGroup?.removeLayer(marker);
                    this.markers.delete(tokenId);
                }
            }
        }
        // Locations load progressively: the fleet is paged in chunks with a
        // few requests in flight, and markers drop onto the map as each chunk
        // resolves — a 100+ vehicle fleet paints in seconds instead of waiting
        // for one monolithic call. Per-vehicle JWT checks on the backend
        // determine which vehicles the dev license has SACD access to.
        // Partial refresh: only fan out for vehicles whose cached location is
        // stale (older than the window, or never pulled). Fresh vehicles are
        // already painted from the DB seed above, so a fully-fresh fleet makes
        // zero telemetry calls. A manual refresh (force) re-pulls everything.
        // The loader streams results per chunk; we frame the map on the first.
        const gen = ++this.loadGeneration;
        const noPermSet = new Set<string>();
        let fittedOnce = false;
        await fetchFleetLocations({
            vehicles: rawVehicles,
            force,
            isCurrent: () => gen === this.loadGeneration,
            onNoPermissions: (ids) => ids.forEach((id) => noPermSet.add(id)),
            onBatch: (locations) => {
                Object.assign(this.lastLocations, locations);
                this.addMarkers(locations);
                if (!fittedOnce && this.markers.size > 0) {
                    fittedOnce = true;
                    this.centerMap();
                }
            },
        });
        if (gen !== this.loadGeneration) return;

        // Mark vehicles where JWT exchange failed (no SACD permissions).
        if (noPermSet.size > 0) {
            this.vehicles = this.vehicles.map((v) =>
                noPermSet.has(v.tokenId) ? { ...v, noPermissions: true } : v
            );
        }

        this.placeMarkers();
        FleetCache.set(this.tenantId, { vehicles: this.vehicles, locations: this.lastLocations });
    }

    willUpdate(changed: Map<string, unknown>) {
        if (changed.has('tenantId') && this.tenantId && !this.loading) {
            this.loading = true;
            this.errorMessage = null;
            this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
            void this.loadVehicleData();
        }
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
        this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        this.unsubscribeHidden = hiddenVehiclesService.subscribe(() => {
            this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
            this.placeMarkers();
        });
        await this.loadVehicleData();
    }

    private updateMinZoom() {
        if (!this.leafletMap) return;
        this.leafletMap.invalidateSize();
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
        this.leafletMap = createFleetMap(el, { zoomControl: false });
        this.tileLayer = applyTileTheme(this.leafletMap, null, el, themeService.current);
        window.addEventListener('theme-change', this.boundOnThemeChange);

        this.clusterGroup = createVehicleClusterGroup();
        this.clusterGroup.addTo(this.leafletMap);

        this.resizeObserver = new ResizeObserver(() => this.updateMinZoom());
        this.resizeObserver.observe(el);
        this.updateMinZoom();

        // Cached data may have already been loaded into `this.vehicles` /
        // `this.lastLocations` before the map existed — place markers now and
        // frame them (a persisted-snapshot paint can beat map creation).
        this.placeMarkers();
        if (this.markers.size > 0) this.centerMap();
    }

    override disconnectedCallback() {
        this.unsubscribeHidden?.();
        this.unsubscribeHidden = null;
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        super.disconnectedCallback();
        this.resizeObserver?.disconnect();
        this.resizeObserver = null;
        this.markers.clear();
        this.leafletMap?.remove();
        this.leafletMap = null;
        this.clusterGroup = null;
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

    /** Cards passing the group filter, text search, and hidden filter. */
    private visibleCards(): VehicleCard[] {
        let cards = this.vehicles;
        if (this.selectedGroupId) {
            cards = cards.filter((c) => (c.groups || []).some((g) => g.id === this.selectedGroupId));
        }
        const q = this.searchQuery.trim().toLowerCase();
        if (q) {
            cards = cards.filter((c) =>
                c.title.toLowerCase().includes(q)
                || c.tokenId.includes(q)
                || c.location.toLowerCase().includes(q)
                || (c.vin?.toLowerCase().includes(q) ?? false)
                || (c.licensePlate?.toLowerCase().includes(q) ?? false));
        }
        if (this.showHidden) {
            const visible = cards.filter((c) => !this.hiddenVehicles.has(c.tokenId));
            const hidden = cards.filter((c) => this.hiddenVehicles.has(c.tokenId));
            return [...visible, ...hidden];
        }
        return cards.filter((c) => !this.hiddenVehicles.has(c.tokenId));
    }

    /** Token ids visible under the current filter — used to filter map markers. */
    private visibleTokenIds(): Set<string> {
        return new Set(this.visibleCards().map((c) => c.tokenId));
    }

    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        unsafeCSS(markerClusterCss),
        unsafeCSS(VEHICLE_TOOLTIP_CSS),
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
                height: var(--top-bar-height);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
                z-index: 40;
                /* Fades into the map instead of slicing it with a bar. */
                background: linear-gradient(to bottom, var(--background) 0%, color-mix(in srgb, var(--background) 72%, transparent) 55%, transparent 100%);
                pointer-events: none;
            }
            header.top-bar > * { pointer-events: auto; }
            @media (max-width: 768px) {
                header.top-bar { display: none; }
            }
            header.top-bar .left { display: flex; align-items: center; gap: 20px; }
            header.top-bar h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            /* Segmented control: the view switch is one choice, not two links. */
            header.top-bar nav {
                display: flex;
                gap: 2px;
                padding: 3px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
            }
            header.top-bar nav a {
                text-decoration: none;
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface-variant);
                padding: 6px 14px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            header.top-bar nav a:hover { color: var(--on-surface); }
            header.top-bar nav a.active {
                color: var(--primary);
                background: var(--surface-bright);
                box-shadow: var(--shadow-sm);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }
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
            /* Brightness boost only for dark CARTO tiles */
            .map.dark-tiles .leaflet-tile {
                filter: brightness(1.8);
            }
            /* Push attribution below the vehicles panel on mobile */
            .map .leaflet-control-attribution {
                font-size: 9px;
                opacity: 0.5;
            }

            .map-controls {
                position: absolute;
                top: calc(var(--top-bar-height) + 16px);
                left: 24px;
                display: flex;
                flex-direction: column;
                gap: 12px;
                z-index: 10;
            }
            .map-controls button {
                width: 40px;
                height: 40px;
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
                border-radius: var(--radius-full);
                box-shadow: var(--shadow-float);
                color: var(--on-surface);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .map-controls button .material-symbols-outlined { font-size: 20px; }
            .map-controls button:hover { background: var(--surface-container-high); }
            .map-controls button.active {
                background: var(--selected-bg);
                color: var(--selected-fg);
            }
            .map-controls button.active:hover { background: var(--selected-bg); }

            .map-legend {
                position: absolute;
                bottom: 24px;
                left: 24px;
                display: flex;
                flex-direction: row;
                gap: 2px;
                padding: 4px;
                border-radius: var(--radius-full);
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
                box-shadow: var(--shadow-float);
                z-index: 10;
            }
            .map-legend button {
                width: 36px;
                height: 36px;
                border-radius: var(--radius-full);
                color: var(--on-surface);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease;
            }
            .map-legend button .material-symbols-outlined { font-size: 20px; }
            /* Phones: the collapsed vehicles sheet keeps a 56px strip along the
               bottom above the map, so the pill sits above it, not under it. */
            @media (max-width: 767px) {
                .map-legend { bottom: calc(56px + 12px); }
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
                background: var(--glass-bg);
                backdrop-filter: blur(24px) saturate(1.4);
                -webkit-backdrop-filter: blur(24px) saturate(1.4);
                border-radius: var(--radius-xl) var(--radius-xl) 0 0;
                display: flex;
                flex-direction: column;
                overflow: hidden;
                box-shadow: var(--shadow-float);
                transition: transform 0.3s ease, width 0.3s ease;
            }
            .vehicles-panel.collapsed {
                transform: translateY(calc(100% - 56px));
            }
            @media (min-width: 768px) {
                .vehicles-panel {
                    top: calc(var(--top-bar-height) + 16px);
                    bottom: 24px;
                    right: 24px;
                    left: auto;
                    width: 372px;
                    border-radius: var(--radius-xl);
                }
                .vehicles-panel.narrow { width: 96px; }
                .vehicles-panel.narrow .panel-header { display: none; }
                .vehicles-panel.narrow .group-filter { display: none; }
                .vehicles-panel.narrow .vehicle-list { padding: 8px; gap: 8px; }
            }

            .chevron-tab {
                display: none;
                position: absolute;
                top: 120px;
                right: calc(24px + 372px - 12px);
                width: 24px;
                height: 24px;
                background: var(--surface-container-high);
                box-shadow: var(--shadow-float);
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
                padding: 16px 12px 8px 20px;
            }
            .panel-header h3 { font: 600 17px/24px var(--font-headline); letter-spacing: -0.01em; color: var(--primary); }
            .panel-header-actions { display: flex; align-items: center; gap: 2px; }
            .panel-header button .material-symbols-outlined { font-size: 20px; }
            .panel-header button {
                color: var(--on-surface-variant);
                background: none;
                border: none;
                padding: 8px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease;
            }
            .panel-header button:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .panel-header button.search-active { color: var(--selected-fg); background: var(--selected-bg); }

            .vehicle-card-dense {
                display: flex;
                align-items: center;
                gap: 10px;
                padding: 8px 10px;
                border-radius: var(--radius-md);
                text-decoration: none;
                color: inherit;
                position: relative;
                cursor: pointer;
                transition: background 0.15s ease;
            }
            .vehicle-card-dense:hover { background: var(--surface-container-high); }
            .vehicle-card-dense .zoom-btn {
                position: relative;
                top: auto; right: auto;
                margin-left: auto;
                width: 24px; height: 24px;
                flex-shrink: 0;
            }
            .vehicle-card-dense .zoom-btn .material-symbols-outlined { font-size: 14px; }
            .dense-token {
                position: relative;
                display: flex;
                align-items: center;
                gap: 5px;
                flex-shrink: 0;
                font: 500 11px/14px var(--font-body);
                color: var(--on-surface-variant);
                background: var(--surface-container-high);
                border-radius: var(--radius-sm);
                padding: 3px 7px;
            }
            .dense-token .status-dot {
                position: static;
                width: 7px;
                height: 7px;
                border: none;
                flex-shrink: 0;
            }
            .vehicle-card-dense.offline .dense-token { opacity: 0.5; }
            .dense-title {
                font: var(--type-body-sm);
                font-weight: 500;
                color: var(--primary);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                flex: 1;
                min-width: 0;
                display: flex;
                align-items: center;
                gap: 4px;
            }
            .dense-title .favorite-star { font-size: 13px; flex-shrink: 0; }

            .search-filter {
                display: flex;
                align-items: center;
                gap: 8px;
                margin: 4px 16px 8px;
                padding: 0 12px;
                height: 40px;
                border-radius: var(--radius-md);
                background: var(--surface-container-high);
            }
            .search-filter:focus-within { box-shadow: 0 0 0 2px var(--focus-ring); }
            .search-filter > .material-symbols-outlined {
                font-size: 18px;
                color: var(--on-surface-variant);
                flex-shrink: 0;
            }
            .search-filter input {
                flex: 1;
                min-width: 0;
                background: none;
                border: none;
                color: var(--on-surface);
                font: var(--type-body-sm);
            }
            .search-filter input:focus { outline: none; }
            /* The pill draws the ring; drop the global input focus halo. */
            .search-filter input:focus-visible { box-shadow: none; }
            .search-filter input::placeholder { color: var(--on-surface-variant); }
            /* Hide the native WebKit clear button — we render our own. */
            .search-filter input::-webkit-search-cancel-button { display: none; }
            .search-filter .clear {
                background: none;
                border: none;
                color: var(--on-surface-variant);
                padding: 2px;
                border-radius: var(--radius-full);
                cursor: pointer;
                display: inline-flex;
            }
            .search-filter .clear:hover { color: var(--primary); background: var(--surface-container-high); }
            .search-filter .clear .material-symbols-outlined { font-size: 16px; }

            .group-filter {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 4px 16px 8px 20px;
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
                height: 36px;
                color: var(--on-surface);
                padding: 0 12px;
                font: 500 13px/18px var(--font-body);
            }

            .vehicle-list {
                flex: 1;
                overflow-y: auto;
                padding: 4px 8px 12px;
                display: flex;
                flex-direction: column;
                gap: 2px;
                border-top: 1px solid var(--outline-variant);
                padding-top: 8px;
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
                background: transparent;
                border-radius: 14px;
                padding: 12px;
                cursor: pointer;
                transition: background 0.15s ease;
                flex-shrink: 0;
                text-decoration: none;
                color: inherit;
                display: block;
                position: relative;
            }

            .zoom-btn {
                position: absolute;
                top: 12px;
                right: 12px;
                width: 30px;
                height: 30px;
                background: transparent;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease, color 0.15s ease;
                z-index: 1;
            }
            .zoom-btn:hover {
                background: var(--accent-soft);
                color: var(--accent-ink);
            }
            .zoom-btn .material-symbols-outlined { font-size: 16px; }
            .vehicle-card:hover { background: var(--surface-container-high); }
            .vehicle-card.hidden-card { opacity: 0.5; }
            .vehicle-card.hidden-card:hover { opacity: 0.75; }
            .vehicle-card-dense.hidden-card { opacity: 0.5; }
            .vehicle-card-dense.hidden-card:hover { opacity: 0.75; }

            .hide-btn {
                position: absolute;
                top: 12px;
                right: 52px;
                width: 30px;
                height: 30px;
                background: var(--surface-container-highest);
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                opacity: 0;
                transition: opacity 0.15s, background 0.15s, color 0.15s;
                z-index: 1;
            }
            .vehicle-card:hover .hide-btn { opacity: 1; }
            .hide-btn:hover { background: var(--error-container); color: var(--error); }
            .hide-btn .material-symbols-outlined { font-size: 16px; }

            .unhide-btn {
                position: absolute;
                top: 12px;
                right: 12px;
                width: 30px;
                height: 30px;
                background: var(--surface-container-highest);
                border-radius: var(--radius-full);
                color: var(--primary);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s, color 0.15s;
                z-index: 1;
            }
            .unhide-btn:hover { background: var(--accent-soft); color: var(--accent-ink); }
            .unhide-btn .material-symbols-outlined { font-size: 16px; }

            .dense-hide-btn {
                position: relative;
                top: auto; right: auto;
                width: 24px; height: 24px;
                flex-shrink: 0;
                opacity: 0;
            }
            .vehicle-card-dense:hover .dense-hide-btn { opacity: 1; }
            .dense-hide-btn .material-symbols-outlined { font-size: 14px; }

            .hidden-count-badge {
                font-size: 10px;
                font-weight: 700;
                line-height: 1;
                background: var(--secondary);
                color: var(--on-secondary);
                border-radius: var(--radius-full);
                padding: 2px 5px;
                margin-left: 2px;
            }
            .panel-header button.show-hidden-active .hidden-count-badge {
                background: var(--on-secondary-container);
                color: var(--secondary-container);
            }

            .status-dot {
                position: absolute;
                width: 12px;
                height: 12px;
                border-radius: var(--radius-full);
                border: 2px solid var(--surface);
            }
            .status-dot.status-green { background: var(--accent); box-shadow: 0 0 8px var(--accent-soft-strong); }
            .status-dot.status-red { background: var(--error); }
            .status-dot.status-amber { background: var(--warning); }

            .vehicle-row { display: flex; align-items: flex-start; gap: 14px; }
            .vehicle-icon {
                position: relative;
                width: 48px;
                height: 48px;
                border-radius: var(--radius-full);
                background: var(--surface-container-highest);
                display: flex;
                align-items: center;
                justify-content: center;
                flex-shrink: 0;
            }
            .vehicle-icon .status-dot { bottom: -2px; right: -2px; }
            .vehicle-icon .material-symbols-outlined { color: var(--primary); font-size: 24px; }
            .vehicle-icon .brand-logo {
                width: 100%;
                height: 100%;
                border-radius: var(--radius-full);
                object-fit: cover;
            }
            .vehicle-card.offline .vehicle-icon { opacity: 0.5; }
            .vehicle-card.offline .vehicle-icon .material-symbols-outlined { color: var(--on-surface-variant); }
            .vehicle-card.offline .vehicle-icon .brand-logo { filter: grayscale(1); }

            .vehicle-meta { flex: 1; min-width: 0; }
            .vehicle-meta h4 {
                font: 600 15px/22px var(--font-headline);
                padding-right: 32px;
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
                color: var(--favorite);
                font-variation-settings: 'FILL' 1;
                flex-shrink: 0;
            }
            .favorite-star-compact {
                position: absolute;
                top: 4px;
                right: 4px;
                font-size: 12px;
                color: var(--favorite);
                font-variation-settings: 'FILL' 1;
            }
            .vehicle-meta .location {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 4px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            /* License-plate pill: monospace plate value with a primary-tinted
               accent, sized to sit under the title in the list card. */
            .vehicle-meta .plate-pill {
                display: inline-flex;
                align-items: center;
                gap: 4px;
                margin-top: 4px;
                padding: 1px 6px;
                border-radius: 5px;
                background: var(--surface-container-highest);
                font: 600 11px/16px var(--font-body);
                letter-spacing: 0.06em;
                color: var(--on-surface);
                cursor: default;
            }
            .vehicle-meta .plate-pill .material-symbols-outlined { font-size: 13px; color: var(--on-surface-variant); }
            /* VIN line: caps label + monospace value with a copy affordance,
               sitting under the title like the plate pill. */
            .vehicle-meta .vin-line {
                display: flex;
                align-items: center;
                gap: 6px;
                margin-top: 4px;
                font: 400 12px/16px var(--font-body);
                color: var(--on-surface-variant);
            }
            .vehicle-meta .vin-line .vin-label {
                font: 500 11px/16px var(--font-body);
                opacity: 0.7;
            }
            .vehicle-meta .vin-line .vin-value {
                font: 400 12px/16px var(--font-body);
                letter-spacing: 0.02em;
                color: var(--on-surface-variant);
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            /* Inline copy button shared by the plate pill and VIN line. */
            .vehicle-meta .copy-btn {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                flex: none;
                padding: 0;
                background: none;
                border: none;
                color: var(--on-surface-variant);
                cursor: pointer;
            }
            .vehicle-meta .copy-btn:hover { color: var(--primary); }
            /* Reveal-on-hover only where hovering exists; on touch they stay visible. */
            @media (hover: hover) {
                .vehicle-meta .copy-btn { opacity: 0; transition: opacity 0.15s ease; }
                .vehicle-card:hover .copy-btn, .vehicle-meta .copy-btn:focus-visible { opacity: 1; }
            }
            .vehicle-meta .copy-btn .material-symbols-outlined { font-size: 13px; }
            .vehicle-meta .seen {
                font: 400 12px/16px var(--font-body);
                color: var(--on-surface-variant);
                margin-top: 6px;
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
                background: color-mix(in srgb, var(--warning) 14%, transparent);
                color: var(--warning);
                border-radius: var(--radius-sm);
                padding: 2px 8px;
                font: var(--type-label-caps);
            }
            .no-permissions-badge .material-symbols-outlined { font-size: 14px; }
        `,
    ];

    /** Sample the loaded logo and pin a contrasting badge background for this vehicle. */
    private onLogoLoad(tokenId: string, e: Event) {
        const bg = contrastingBadgeBackground(e.target as HTMLImageElement);
        if (bg && this.logoBadgeColors.get(tokenId) !== bg) {
            this.logoBadgeColors.set(tokenId, bg);
            this.requestUpdate();
        }
    }

    /** Badge background: contrasts with the logo once sampled, default surface otherwise. */
    private iconBadgeStyle(v: VehicleCard): string {
        const bg = this.logoBadgeColors.get(v.tokenId);
        return bg ? `background: ${bg};` : '';
    }

    /** Brand logo when one exists for the make, falling back to a generic car icon on load failure. */
    private renderVehicleIcon(v: VehicleCard) {
        const logoUrl = v.make ? brandLogoUrl(v.make) : null;
        if (logoUrl && !this.brokenLogos.has(v.tokenId)) {
            return html`<img
                class="brand-logo"
                src=${logoUrl}
                alt=${v.make}
                @load=${(e: Event) => this.onLogoLoad(v.tokenId, e)}
                @error=${() => { this.brokenLogos.add(v.tokenId); this.requestUpdate(); }}
            />`;
        }
        return html`<span class="material-symbols-outlined">directions_car</span>`;
    }

    private hideVehicle(e: Event, tokenId: string) {
        e.preventDefault();
        e.stopPropagation();
        if (this.quickViewVehicle?.tokenId === tokenId) this.closeQuickView();
        hiddenVehiclesService.hide(this.tenantId, tokenId);
    }

    private unhideVehicle(e: Event, tokenId: string) {
        e.preventDefault();
        e.stopPropagation();
        hiddenVehiclesService.unhide(this.tenantId, tokenId);
    }

    private renderCard(v: VehicleCard) {
        const isHidden = this.hiddenVehicles.has(v.tokenId);
        const cls = [v.online ? 'vehicle-card' : 'vehicle-card offline', isHidden ? 'hidden-card' : ''].join(' ').trim();
        // Plain click opens the quick-view overlay (map context preserved);
        // the href stays so middle-click/cmd-click still opens full details.
        return html`
            <a class=${cls} href="#/${this.tenantId}/vehicles/${v.tokenId}"
               @click=${(e: MouseEvent) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); if (!isHidden) this.openQuickView(v); } }}>
                ${isHidden ? html`
                    <button class="unhide-btn" title="${msg('Unhide vehicle')}" @click=${(e: Event) => this.unhideVehicle(e, v.tokenId)}>
                        <span class="material-symbols-outlined">visibility</span>
                    </button>
                ` : html`
                    ${this.markers.has(v.tokenId) ? html`
                        <button class="zoom-btn" title="${msg('Zoom to vehicle')}" @click=${(e: Event) => this.zoomToVehicle(e, v.tokenId)}>
                            <span class="material-symbols-outlined">my_location</span>
                        </button>
                    ` : ''}
                    <button class="hide-btn" title="${msg('Hide vehicle')}" @click=${(e: Event) => this.hideVehicle(e, v.tokenId)}>
                        <span class="material-symbols-outlined">visibility_off</span>
                    </button>
                `}
                <div class="vehicle-row">
                    <div class="vehicle-icon" style=${this.iconBadgeStyle(v)}>
                        ${this.renderVehicleIcon(v)}
                        <span class="status-dot ${this.statusClass(v)}"></span>
                    </div>
                    <div class="vehicle-meta">
                        <h4>
                            ${v.isFavorite ? html`<span class="material-symbols-outlined favorite-star" title="${msg('Favorite')}">star</span>` : ''}
                            ${v.title}
                        </h4>
                        ${v.licensePlate ? html`
                            <span class="plate-pill" title=${msg('License plate')}>
                                <span class="material-symbols-outlined">directions_car</span>${v.licensePlate}
                                <button class="copy-btn" title=${msg('Copy license plate')}
                                    @click=${(e: Event) => this.copyValue(e, v.licensePlate!, `${v.tokenId}:plate`)}>
                                    <span class="material-symbols-outlined">${this.copiedKey === `${v.tokenId}:plate` ? 'check' : 'content_copy'}</span>
                                </button>
                            </span>
                        ` : ''}
                        ${v.vin ? html`
                            <p class="vin-line" title=${msg('VIN')}>
                                <span class="vin-label">VIN</span>
                                <span class="vin-value">${v.vin}</span>
                                <button class="copy-btn" title=${msg('Copy VIN')}
                                    @click=${(e: Event) => this.copyValue(e, v.vin!, `${v.tokenId}:vin`)}>
                                    <span class="material-symbols-outlined">${this.copiedKey === `${v.tokenId}:vin` ? 'check' : 'content_copy'}</span>
                                </button>
                            </p>
                        ` : ''}
                        ${v.online ? html`
                            ${v.notification
                                ? html`<div class="row-flex">
                                        <p class="seen">${v.lastSeen ? this.formatLastSeen(v.lastSeen) : v.seenAt}</p>
                                        <span class="notif-badge">${v.notification}</span>
                                    </div>`
                                : html`<p class="seen">${v.lastSeen ? this.formatLastSeen(v.lastSeen) : v.seenAt}</p>`
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
            <a class="vehicle-card-compact" href="#/${this.tenantId}/vehicles/${v.tokenId}"
               title=${v.licensePlate ? `${v.title} — ${msg('License plate')}: ${v.licensePlate}` : v.title}
               @click=${(e: MouseEvent) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); this.openQuickView(v); } }}>
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

    private renderDenseCard(v: VehicleCard) {
        const isHidden = this.hiddenVehicles.has(v.tokenId);
        const cls = [v.online ? 'vehicle-card-dense' : 'vehicle-card-dense offline', isHidden ? 'hidden-card' : ''].join(' ').trim();
        return html`
            <a class=${cls} href="#/${this.tenantId}/vehicles/${v.tokenId}"
               title=${v.licensePlate ? `${v.title} — ${msg('License plate')}: ${v.licensePlate}` : v.title}
               @click=${(e: MouseEvent) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); if (!isHidden) this.openQuickView(v); } }}>
                <div class="dense-token">
                    <span class="status-dot ${this.statusClass(v)}"></span>
                    <span>#${v.tokenId}</span>
                </div>
                <span class="dense-title">
                    ${v.isFavorite ? html`<span class="material-symbols-outlined favorite-star">star</span>` : ''}
                    ${v.title}
                </span>
                ${isHidden ? html`
                    <button class="zoom-btn" title="${msg('Unhide vehicle')}" @click=${(e: Event) => this.unhideVehicle(e, v.tokenId)}>
                        <span class="material-symbols-outlined">visibility</span>
                    </button>
                ` : html`
                    ${this.markers.has(v.tokenId) ? html`
                        <button class="zoom-btn" title="${msg('Zoom to vehicle')}" @click=${(e: Event) => this.zoomToVehicle(e, v.tokenId)}>
                            <span class="material-symbols-outlined">my_location</span>
                        </button>
                    ` : ''}
                    <button class="hide-btn dense-hide-btn" title="${msg('Hide vehicle')}" @click=${(e: Event) => this.hideVehicle(e, v.tokenId)}>
                        <span class="material-symbols-outlined">visibility_off</span>
                    </button>
                `}
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
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <button class="icon-btn"><span class="material-symbols-outlined">account_circle</span></button>
                </div>
            </header>

            <div class="map"></div>

            <vehicle-quick-view
                .tenantId=${this.tenantId}
                .vehicle=${this.quickViewVehicle}
                @close=${this.closeQuickView}
                @trip-route=${this.onTripRoute}
                @rt-tick=${this.onRealtimeTick}
            ></vehicle-quick-view>

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
                    <div class="panel-header-actions">
                        ${this.hiddenVehicles.size > 0 ? html`
                            <button
                                class=${this.showHidden ? 'search-active show-hidden-active' : ''}
                                title=${this.showHidden ? msg('Hide hidden vehicles') : msg('Show hidden vehicles')}
                                @click=${() => { this.showHidden = !this.showHidden; this.placeMarkers(); }}
                            >
                                <span class="material-symbols-outlined">visibility_off</span>
                                <span class="hidden-count-badge">${this.hiddenVehicles.size}</span>
                            </button>
                        ` : ''}
                        <button
                            class=${this.searchOpen || this.searchQuery ? 'search-active' : ''}
                            title=${this.searchOpen ? msg('Close search') : msg('Search vehicles')}
                            @click=${() => this.searchOpen ? this.closeSearch() : this.openSearch()}
                        >
                            <span class="material-symbols-outlined">search</span>
                        </button>
                        <button
                            title=${this.denseCards ? msg('Comfortable view') : msg('Compact view')}
                            @click=${() => { this.denseCards = !this.denseCards; }}
                        >
                            <span class="material-symbols-outlined">
                                ${this.denseCards ? 'density_large' : 'density_small'}
                            </span>
                        </button>
                        <a href="#/${this.tenantId}/groups" title="${msg('Manage groups')}">
                            <button><span class="material-symbols-outlined">workspaces</span></button>
                        </a>
                    </div>
                </div>
                ${this.renderSearch()}
                ${this.renderGroupFilter()}
                <div class="vehicle-list custom-scrollbar">
                    ${this.renderList()}
                </div>
            </div>
        `;
    }

    private async openSearch() {
        this.searchOpen = true;
        await this.updateComplete;
        this.renderRoot.querySelector<HTMLInputElement>('.search-filter input')?.focus();
    }

    private closeSearch() {
        this.searchOpen = false;
        this.searchQuery = '';
        this.placeMarkers();
    }

    private renderSearch() {
        if (!this.searchOpen) return nothing;
        return html`
            <div class="search-filter">
                <span class="material-symbols-outlined">search</span>
                <input
                    type="search"
                    placeholder="${msg('Search vehicles…')}"
                    .value=${this.searchQuery}
                    @input=${(e: Event) => {
                        this.searchQuery = (e.target as HTMLInputElement).value;
                        this.placeMarkers();
                    }}
                    @keydown=${(e: KeyboardEvent) => { if (e.key === 'Escape') this.closeSearch(); }}
                />
                <button class="clear" title="${msg('Close search')}"
                    @click=${() => this.closeSearch()}>
                    <span class="material-symbols-outlined">close</span>
                </button>
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
            if (this.searchQuery.trim()) {
                return html`<p class="empty-state">${msg('No vehicles match your search.')}</p>`;
            }
            return html`<p class="empty-state">${this.selectedGroupId ? msg('No vehicles in this group.') : msg('No vehicles found on this account.')}</p>`;
        }
        if (!this.panelExpanded) return cards.map((c) => this.renderCompactCard(c));
        if (this.denseCards) return cards.map((c) => this.renderDenseCard(c));
        return cards.map((c) => this.renderCard(c));
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'fleet-overview-view': FleetOverviewView;
    }
}
