/** A GeoJSON Polygon: an array of linear rings, each a list of [lng, lat] positions. */
export interface GeoJSONPolygon {
    type: 'Polygon';
    coordinates: number[][][];
}

/** Which vehicles a geofence applies to (per-geofence). See docs/GEOFENCES_PLAN.md. */
export type GeofenceScope = 'all' | 'group' | 'manual';

/** A tenant's geofence as returned by the /fleet/geofences endpoints. */
export interface Geofence {
    id: string;
    name: string;
    color: string;
    geometry: GeoJSONPolygon;
    /** Geodesic area in square meters (computed server-side). */
    areaM2: number;
    /** Optional speed limit in km/h; null/undefined when unset. */
    speedLimitKph?: number | null;
    scope: GeofenceScope;
    /** Target fleet-group ids when scope = 'group'; [] otherwise. */
    groupIds: string[];
    /** Number of vehicles the geofence currently resolves to (across its scope). */
    vehicleCount?: number;
    createdBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface GeofencesResponse {
    geofences: Geofence[];
}

/** Response of GET /fleet/geofences/:id/vehicles. */
export interface GeofenceVehiclesResponse {
    tokenIds: number[];
}

/** One contiguous interval a vehicle was inside a geofence (Phase 2 detection). */
export interface GeofencePass {
    enteredAt: string;
    exitedAt: string;
    /** Seconds spent inside the polygon. */
    dwellS: number;
    /** Max speed (km/h) observed during the pass; absent when no speed reported. */
    maxSpeedKph?: number;
    /** True when maxSpeedKph exceeds the geofence's current speed limit. */
    speedExceeded: boolean;
    entryLat: number;
    entryLng: number;
    exitLat: number;
    exitLng: number;
    maxSpeedLat?: number;
    maxSpeedLng?: number;
    numSamples: number;
}

/** A geofence a trip/window touched, with its passes. */
export interface GeofenceCrossing {
    geofenceId: string;
    name: string;
    color: string;
    speedLimitKph?: number | null;
    passes: GeofencePass[];
}

/**
 * Response of GET /telemetry/:tokenId/trip-geofences. permissionsRequired is set
 * (with an empty list) when the dev license lacks SACD permissions on the vehicle.
 */
export interface TripGeofencesResponse {
    geofences: GeofenceCrossing[];
    permissionsRequired?: boolean;
    devLicense?: string;
}
