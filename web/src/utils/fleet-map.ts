import L from 'leaflet';
import 'leaflet.markercluster';
import { Vehicle } from '../types/vehicle.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { SettingsService } from '../services/settings-service.ts';

/**
 * Shared Leaflet helpers for the fleet maps (vehicle map + geofence map): the
 * CARTO base map + theme tiles, the vehicle GPS-dot styling and clustered
 * layer, and the DB-seed → background-telemetry location loader. Both views use
 * these so the GPS points look and load identically.
 */

/** A vehicle's last GPS fix. heading is degrees clockwise from true north,
 *  present only for vehicles that report currentLocationHeading. */
export type LatLon = { lat: number; lon: number; heading?: number };

// ---- Base map + tiles ----------------------------------------------------

/** Latitude clamped to Web Mercator's valid range; longitude left open so
 *  panning across the antimeridian wraps into the next copy of the world. */
export const WORLD_BOUNDS = L.latLngBounds([-85.051129, -Infinity], [85.051129, Infinity]);
export const MAP_HOME_CENTER: [number, number] = [39.5, -98.35];
export const MAP_HOME_ZOOM = 4;

/** Create the shared fleet base map. zoomControl differs per view (the vehicle
 *  map has custom controls; the geofence map uses Leaflet's). */
export function createFleetMap(el: HTMLElement, opts: { zoomControl: boolean }): L.Map {
    return L.map(el, {
        zoomControl: opts.zoomControl,
        attributionControl: true,
        maxBounds: WORLD_BOUNDS,
        maxBoundsViscosity: 1.0,
        worldCopyJump: true,
    }).setView(MAP_HOME_CENTER, MAP_HOME_ZOOM);
}

const TILE_ATTRIBUTION = '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>';

/** CARTO basemap key, from GET /public/settings. CARTO now stamps "API KEY
 *  REQUIRED" across keyless tiles (they still return 200), so this is required
 *  for a usable map, not an optional upgrade.
 *
 *  Tile layers are built synchronously from theme changes and map init, but the
 *  key arrives over the network. Rather than make every call site async, layers
 *  built before the key lands are retargeted with setUrl() when it does. */
let basemapKey = '';
let keyLoad: Promise<void> | null = null;
const awaitingKey = new Set<L.TileLayer>();

function tileURL(theme: 'dark' | 'light'): string {
    const style = theme === 'light' ? 'light_all' : 'dark_all';
    const suffix = basemapKey ? `?key=${encodeURIComponent(basemapKey)}` : '';
    return `https://{s}.basemaps.cartocdn.com/${style}/{z}/{x}/{y}{r}.png${suffix}`;
}

function loadBasemapKey(): Promise<void> {
    keyLoad ??= SettingsService.getInstance()
        .fetchPublicSettings()
        .then((settings) => {
            basemapKey = settings.cartoBasemapKey ?? '';
            if (!basemapKey) return;
            // Re-point every layer that was created keyless. Leaflet re-requests
            // its tiles from the new template.
            for (const layer of awaitingKey) {
                const theme = (layer.options as { fleetTheme?: 'dark' | 'light' }).fleetTheme ?? 'dark';
                layer.setUrl(tileURL(theme));
            }
            awaitingKey.clear();
        })
        .catch(() => {
            // /public/settings is already surfaced elsewhere when it fails; a
            // watermarked map is a better outcome here than no map at all.
            keyLoad = null;
        });
    return keyLoad;
}

export function buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
    const layer = L.tileLayer(tileURL(theme), {
        attribution: TILE_ATTRIBUTION,
        subdomains: 'abcd',
        maxZoom: 19,
        fleetTheme: theme,
    } as L.TileLayerOptions);
    if (!basemapKey) {
        awaitingKey.add(layer);
        layer.on('remove', () => awaitingKey.delete(layer));
        void loadBasemapKey();
    }
    return layer;
}

/** Swap the map's tile layer for the given theme and toggle the `.dark-tiles`
 *  brightness class on the map element. Returns the new layer. Pass prev=null
 *  for the initial add. */
export function applyTileTheme(
    map: L.Map,
    prev: L.TileLayer | null,
    mapEl: HTMLElement | null,
    theme: 'dark' | 'light',
): L.TileLayer {
    prev?.remove();
    const layer = buildTileLayer(theme);
    layer.addTo(map);
    mapEl?.classList.toggle('dark-tiles', theme === 'dark');
    return layer;
}

// ---- Vehicle GPS markers -------------------------------------------------

// Small dots by default — fleets can run to thousands of vehicles, so the map
// stays readable at density. Hover grows the dot for an easier click target;
// selection grows it further and recolors.
// Leaflet paints markers on canvas/SVG, so these can't read CSS custom
// properties. They mirror the DIMO brand tokens in global-styles.ts.
export const MAP_COLORS = {
    mint: '#46F1E4',
    sky: '#8CD0FF',
    ink: '#0E0F11',
    muted: '#808080',
} as const;

export const VEHICLE_MARKER_STYLE: L.CircleMarkerOptions = { radius: 5, fillColor: MAP_COLORS.mint, color: MAP_COLORS.ink, weight: 2, opacity: 0.9, fillOpacity: 1 };
export const VEHICLE_MARKER_STYLE_HOVER: L.CircleMarkerOptions = { radius: 8, fillColor: MAP_COLORS.mint, color: MAP_COLORS.ink, weight: 2.5, opacity: 1, fillOpacity: 1 };
// Ink ring like the unselected dots: a white ring vanishes on the light tiles.
export const VEHICLE_MARKER_STYLE_SELECTED: L.CircleMarkerOptions = { radius: 9, fillColor: MAP_COLORS.sky, color: MAP_COLORS.ink, weight: 3, opacity: 1, fillOpacity: 1 };
export const VEHICLE_MARKER_STYLE_HIDDEN: L.CircleMarkerOptions = { radius: 4, fillColor: MAP_COLORS.muted, color: MAP_COLORS.ink, weight: 1, opacity: 0.35, fillOpacity: 0.35 };

// Internals of L.CircleMarker / L.SVG that HeadingMarker draws with. Leaflet's
// typings don't expose them.
type CircleMarkerInternals = {
    _point: L.Point;
    _radius: number;
    _renderer: L.Renderer & { _setPath(layer: L.Path, d: string): void };
    _pxBounds: L.Bounds;
    _clickTolerance(): number;
    _empty(): boolean;
};

/** How far the heading point reaches from the dot's centre. Scales with the
 *  hover/selected radius so the shape holds; the constant keeps the point
 *  legible on the smallest (default 5px) dot. */
const headingTipReach = (r: number) => r * 1.6 + 2;

/**
 * A vehicle GPS dot that, when the vehicle reports a heading, draws as a
 * teardrop pointing in that direction: the same fill and ring, one silhouette,
 * so a fleet mixing vehicles with and without heading still reads as one set of
 * dots. Everything else is CircleMarker — setStyle/radius (hover, selected,
 * hidden), tooltips, clustering. SVG renderer only; under Canvas it stays a dot.
 */
export class HeadingMarker extends L.CircleMarker {
    private heading: number | null = null;

    setHeading(deg: number | null | undefined): this {
        this.heading = typeof deg === 'number' && Number.isFinite(deg) ? ((deg % 360) + 360) % 360 : null;
        if ((this as unknown as { _map?: L.Map })._map) this.redraw();
        return this;
    }

    getHeading(): number | null {
        return this.heading;
    }

    private get drawsHeading(): boolean {
        const self = this as unknown as CircleMarkerInternals;
        return this.heading !== null && self._renderer instanceof L.SVG;
    }

    _updateBounds(): void {
        const self = this as unknown as CircleMarkerInternals;
        if (!this.drawsHeading) {
            // @ts-expect-error — Leaflet internal, untyped
            return super._updateBounds();
        }
        // Grow the culling box to cover the point, whichever way it faces.
        const reach = headingTipReach(self._radius) + self._clickTolerance();
        const p = L.point(reach, reach);
        self._pxBounds = L.bounds(self._point.subtract(p), self._point.add(p));
    }

    _updatePath(): void {
        const self = this as unknown as CircleMarkerInternals;
        if (!this.drawsHeading) {
            // @ts-expect-error — Leaflet internal, untyped
            return super._updatePath();
        }
        if (self._empty()) {
            self._renderer._setPath(this, 'M0 0');
            return;
        }
        const { x, y } = self._point;
        const r = Math.max(Math.round(self._radius), 1);
        const d = headingTipReach(r);
        // Screen space: y grows downward, so north (0°) is -y.
        const rad = (this.heading! * Math.PI) / 180;
        const ux = Math.sin(rad);
        const uy = -Math.cos(rad);
        // The point meets the circle along the two tangents from the tip.
        const a = Math.acos(r / d);
        const at = (ang: number) => [
            x + r * (ux * Math.cos(ang) - uy * Math.sin(ang)),
            y + r * (ux * Math.sin(ang) + uy * Math.cos(ang)),
        ];
        const [p1x, p1y] = at(a);
        const [p2x, p2y] = at(-a);
        const f = (n: number) => n.toFixed(2);
        self._renderer._setPath(this,
            `M${f(p1x)},${f(p1y)}L${f(x + ux * d)},${f(y + uy * d)}L${f(p2x)},${f(p2y)}` +
            `A${r},${r} 0 1,0 ${f(p1x)},${f(p1y)}Z`);
    }
}

const COMPASS_POINTS = ['north', 'northeast', 'east', 'southeast', 'south', 'southwest', 'west', 'northwest'];

/** "northeast" for 45°, etc. — eight points is as precise as a glance needs. */
export function compassPoint(deg: number): string {
    return COMPASS_POINTS[Math.round((((deg % 360) + 360) % 360) / 45) % 8];
}

function escapeHtml(s: string): string {
    return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
}

/** Tooltip body for a vehicle dot: its title, plus which way it faces when
 *  known. "Facing" rather than "heading" — a parked vehicle keeps the heading
 *  of its last fix, and "heading" would read as moving. */
export function vehicleTooltipHtml(title: string, heading?: number | null): string {
    const facing = typeof heading === 'number' && Number.isFinite(heading)
        ? `<span class="vehicle-tip-heading">Facing ${compassPoint(heading)}</span>`
        : '';
    return `${escapeHtml(title)}${facing}`;
}

/** Update a vehicle dot's heading and its tooltip together. */
export function setVehicleHeading(marker: HeadingMarker, title: string, heading?: number | null): void {
    marker.setHeading(heading);
    marker.setTooltipContent(vehicleTooltipHtml(title, heading));
}

/** A green GPS dot (teardrop when heading is known) with a hover tooltip,
 *  matching the vehicle map's style. */
export function createVehicleMarker(
    coords: LatLon,
    title: string,
    style: L.CircleMarkerOptions = VEHICLE_MARKER_STYLE,
): HeadingMarker {
    return new HeadingMarker([coords.lat, coords.lon], style)
        .setHeading(coords.heading)
        // Clears the point at hover size even when it faces straight up.
        .bindTooltip(vehicleTooltipHtml(title, coords.heading), { permanent: false, direction: 'top', offset: [0, -14] });
}

/** Tooltip lines for vehicle dots. Each map's shadow root needs it. */
export const VEHICLE_TOOLTIP_CSS = `
    .leaflet-tooltip .vehicle-tip-heading { display: block; font-weight: 400; opacity: 0.72; }
`;

function clusterIcon(count: number): L.DivIcon {
    const size = count < 10 ? 30 : count < 50 ? 38 : 46;
    const total = size + 16;
    // DIMO gradient disc with a soft mint halo: clusters read as "live fleet".
    return L.divIcon({
        html: `<div style="width:${size}px;height:${size}px;margin:8px;background:linear-gradient(135deg,${MAP_COLORS.sky},${MAP_COLORS.mint});border:2px solid ${MAP_COLORS.ink};border-radius:50%;box-shadow:0 0 0 6px rgba(70,241,228,0.18),0 0 24px 4px rgba(70,241,228,0.25);display:flex;align-items:center;justify-content:center;font-family:'Euclid Circular A',system-ui,sans-serif;font-size:${size < 38 ? 12 : 14}px;font-weight:600;color:#06201E;font-feature-settings:'tnum' 1;">${count}</div>`,
        className: '',
        iconSize: [total, total],
        iconAnchor: [total / 2, total / 2],
    });
}

/** A clustered layer for vehicle GPS dots — matches the vehicle map at density. */
export function createVehicleClusterGroup(): L.MarkerClusterGroup {
    return L.markerClusterGroup({
        iconCreateFunction: (cluster) => clusterIcon(cluster.getChildCount()),
        showCoverageOnHover: false,
        zoomToBoundsOnClick: true,
        spiderfyOnMaxZoom: true,
        maxClusterRadius: 60,
        animate: true,
    });
}

// ---- Trip routes ---------------------------------------------------------

export interface TripMapStyles {
    route: string;
    start: L.CircleMarkerOptions;
    end: L.CircleMarkerOptions;
}

/** Route + endpoint styling for a drawn trip. The route keeps the brand sky on
 *  dark tiles but deepens to a blue that holds >=3:1 on CARTO's light tiles
 *  (land and water; sky is ~1.6:1 there). Start and end differ in shape as
 *  well as hue so they read without color: start is a solid green dot, end a
 *  red ring around a white core. */
export function tripMapStyles(theme: 'dark' | 'light'): TripMapStyles {
    const light = theme === 'light';
    return {
        route: light ? '#2272C4' : MAP_COLORS.sky,
        start: { radius: 6, fillColor: light ? '#1B8842' : '#36DF71', color: MAP_COLORS.ink, weight: 2, opacity: 1, fillOpacity: 1 },
        end: { radius: 6, fillColor: '#FFFFFF', color: light ? '#C70000' : '#FF6060', weight: 4, opacity: 1, fillOpacity: 1 },
    };
}

// ---- Location loading ----------------------------------------------------

/** DB-cached last-GPS-fix coordinates, so markers paint instantly on first
 *  load before the live telemetry fan-out reconciles them. */
export function seedLocationsFromDb(vehicles: Vehicle[]): Record<string, LatLon> {
    const seed: Record<string, LatLon> = {};
    for (const v of vehicles) {
        if (typeof v.lastLat === 'number' && typeof v.lastLon === 'number') {
            seed[String(v.tokenId)] = { lat: v.lastLat, lon: v.lastLon, heading: v.lastHeading };
        }
    }
    return seed;
}

// Progressive loading tuning: one tokenId per request, three requests in
// flight, so every marker paints the moment its location resolves. Don't
// re-pull a vehicle fetched within the freshness window — render it from the
// DB seed instead. See docs/LOCATION_REFRESH_PLAN.md.
export const LOCATION_FRESH_WINDOW_MS = 5 * 60 * 1000;
export const LOCATIONS_CHUNK_SIZE = 1;
export const LOCATIONS_PARALLEL = 3;

export interface FetchFleetLocationsOpts {
    vehicles: Vehicle[];
    /** Manual refresh: re-pull everything, bypassing the freshness window. */
    force?: boolean;
    freshWindowMs?: number;
    chunkSize?: number;
    parallel?: number;
    /** Guard checked before/after each request; return false to abandon stale
     *  in-flight work (superseded load, tenant switch, toggled off). */
    isCurrent: () => boolean;
    /** Called per resolved chunk with just that chunk's coordinates, so markers
     *  stream onto the map instead of waiting for the whole fleet. */
    onBatch: (locations: Record<string, LatLon>) => void;
    onNoPermissions?: (ids: string[]) => void;
}

/**
 * Fan out to telemetry-api for the vehicles whose cached location is stale
 * (older than the freshness window, or never pulled), streaming results via
 * onBatch as each chunk resolves. `force` re-pulls everything. Returns the full
 * set of freshly-fetched coordinates. A fully-fresh fleet makes zero telemetry
 * calls. Shared by the vehicle map and the geofence map so both get the same
 * DB-seed → background-refresh behavior.
 */
export async function fetchFleetLocations(opts: FetchFleetLocationsOpts): Promise<Record<string, LatLon>> {
    const { vehicles, force = false, isCurrent, onBatch, onNoPermissions } = opts;
    const freshWindowMs = opts.freshWindowMs ?? LOCATION_FRESH_WINDOW_MS;
    const chunkSize = opts.chunkSize ?? LOCATIONS_CHUNK_SIZE;
    const parallel = opts.parallel ?? LOCATIONS_PARALLEL;

    const now = Date.now();
    const isStale = (v: Vehicle) =>
        force || !v.locationPulledAt ||
        now - new Date(v.locationPulledAt).getTime() >= freshWindowMs;
    const ids = vehicles.filter(isStale).map((v) => String(v.tokenId));
    const chunks: string[][] = [];
    for (let i = 0; i < ids.length; i += chunkSize) chunks.push(ids.slice(i, i + chunkSize));

    const fetched: Record<string, LatLon> = {};
    let nextChunk = 0;
    const worker = async () => {
        while (nextChunk < chunks.length) {
            if (!isCurrent()) return; // superseded by a newer load
            const batch = chunks[nextChunk++];
            try {
                const res = await TelemetryService.getInstance().fleetLocations(force, batch);
                if (!isCurrent()) return;
                if (res.noPermissions?.length) onNoPermissions?.(res.noPermissions);
                const locs: Record<string, LatLon> = {};
                for (const [id, loc] of Object.entries(res.locations ?? {})) {
                    locs[id] = { lat: loc.lat, lon: loc.lon, heading: loc.heading };
                    fetched[id] = locs[id];
                }
                if (Object.keys(locs).length) onBatch(locs);
            } catch {
                // Batch failed (network) — keep going; the map shows what it has.
            }
        }
    };
    await Promise.all(Array.from({ length: Math.min(parallel, chunks.length) }, () => worker()));
    return fetched;
}
