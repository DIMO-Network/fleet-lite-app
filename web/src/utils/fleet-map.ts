import L from 'leaflet';
import 'leaflet.markercluster';
import { Vehicle } from '../types/vehicle.ts';
import { TelemetryService } from '../services/telemetry-service.ts';

/**
 * Shared Leaflet helpers for the fleet maps (vehicle map + geofence map): the
 * CARTO base map + theme tiles, the vehicle GPS-dot styling and clustered
 * layer, and the DB-seed → background-telemetry location loader. Both views use
 * these so the GPS points look and load identically.
 */

export type LatLon = { lat: number; lon: number };

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

export function buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
    const url = theme === 'light'
        ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
        : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
    return L.tileLayer(url, {
        attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
        subdomains: 'abcd',
        maxZoom: 19,
    });
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
export const VEHICLE_MARKER_STYLE: L.CircleMarkerOptions = { radius: 4, fillColor: '#69dbad', color: '#ffffff', weight: 1.5, opacity: 0.9, fillOpacity: 0.85 };
export const VEHICLE_MARKER_STYLE_HOVER: L.CircleMarkerOptions = { radius: 8, fillColor: '#69dbad', color: '#ffffff', weight: 2, opacity: 1, fillOpacity: 0.95 };
export const VEHICLE_MARKER_STYLE_SELECTED: L.CircleMarkerOptions = { radius: 9, fillColor: '#f5c84b', color: '#ffffff', weight: 2.5, opacity: 1, fillOpacity: 0.95 };
export const VEHICLE_MARKER_STYLE_HIDDEN: L.CircleMarkerOptions = { radius: 4, fillColor: '#808080', color: '#ffffff', weight: 1, opacity: 0.35, fillOpacity: 0.35 };

/** A green GPS dot with a hover tooltip, matching the vehicle map's style. */
export function createVehicleMarker(
    lat: number,
    lon: number,
    title: string,
    style: L.CircleMarkerOptions = VEHICLE_MARKER_STYLE,
): L.CircleMarker {
    return L.circleMarker([lat, lon], style)
        .bindTooltip(title, { permanent: false, direction: 'top', offset: [0, -10] });
}

function clusterIcon(count: number): L.DivIcon {
    const size = count < 10 ? 32 : count < 50 ? 40 : 48;
    const total = size + 12;
    return L.divIcon({
        html: `<div style="width:${size}px;height:${size}px;background:#69dbad;border:2px solid #1a2332;border-radius:50%;box-shadow:0 0 0 6px rgba(105,219,173,0.25);display:flex;align-items:center;justify-content:center;font-size:${size < 40 ? 11 : 13}px;font-weight:600;color:#1a2332;">${count}</div>`,
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

// ---- Location loading ----------------------------------------------------

/** DB-cached last-GPS-fix coordinates, so markers paint instantly on first
 *  load before the live telemetry fan-out reconciles them. */
export function seedLocationsFromDb(vehicles: Vehicle[]): Record<string, LatLon> {
    const seed: Record<string, LatLon> = {};
    for (const v of vehicles) {
        if (typeof v.lastLat === 'number' && typeof v.lastLon === 'number') {
            seed[String(v.tokenId)] = { lat: v.lastLat, lon: v.lastLon };
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
                    locs[id] = { lat: loc.lat, lon: loc.lon };
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
