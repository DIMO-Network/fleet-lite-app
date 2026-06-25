import { GeoJSONPolygon } from '../types/geofence.ts';

const EARTH_RADIUS_M = 6378137.0;

function rad(deg: number): number {
    return (deg * Math.PI) / 180.0;
}

/**
 * Geodesic area of a single ring (positions as [lng, lat] degrees), in m².
 * Spherical-excess formula; sign depends on winding, callers take abs.
 */
function ringAreaM2(ring: number[][]): number {
    const n = ring.length;
    if (n < 3) return 0;
    let total = 0;
    for (let i = 0; i < n; i++) {
        const p1 = ring[i];
        const p2 = ring[(i + 1) % n];
        total += rad(p2[0] - p1[0]) * (2 + Math.sin(rad(p1[1])) + Math.sin(rad(p2[1])));
    }
    return (total * EARTH_RADIUS_M * EARTH_RADIUS_M) / 2.0;
}

/**
 * Geodesic area of a GeoJSON Polygon (outer ring minus holes), in m². Mirrors
 * the server's calculation so the draw preview matches the stored value.
 */
export function polygonAreaM2(poly: GeoJSONPolygon): number {
    if (!poly || poly.type !== 'Polygon' || !poly.coordinates?.length) return 0;
    let area = Math.abs(ringAreaM2(poly.coordinates[0]));
    for (let i = 1; i < poly.coordinates.length; i++) {
        area -= Math.abs(ringAreaM2(poly.coordinates[i]));
    }
    return Math.abs(area);
}

/** Human-readable area: m² under 0.1 km², else km² with one decimal. */
export function formatArea(m2: number): string {
    if (m2 < 100_000) return `${Math.round(m2).toLocaleString()} m²`;
    return `${(m2 / 1_000_000).toFixed(1)} km²`;
}
