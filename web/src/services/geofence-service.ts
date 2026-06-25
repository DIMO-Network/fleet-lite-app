import { ApiService } from './api-service.ts';
import {
    Geofence,
    GeofencesResponse,
    GeofenceVehiclesResponse,
    GeoJSONPolygon,
    GeofenceScope,
    GeofenceScanTargetsResponse,
    GeofencePassesResponse,
    VehiclePasses,
} from '../types/geofence.ts';

/** Fields accepted when creating a geofence. */
export interface CreateGeofenceInput {
    name: string;
    color: string;
    geometry: GeoJSONPolygon;
    speedLimitKph?: number | null;
    scope: GeofenceScope;
    groupIds?: string[];
}

/** Partial update; omitted fields are left unchanged server-side. */
export interface UpdateGeofenceInput {
    name?: string;
    color?: string;
    geometry?: GeoJSONPolygon;
    speedLimitKph?: number | null;
    scope?: GeofenceScope;
    groupIds?: string[];
}

/**
 * Singleton client over the tenant-scoped /fleet/geofences API. ApiService
 * injects the Bearer token and the Tenant-Id header (from the hash route)
 * automatically.
 */
export class GeofenceService {
    private static instance: GeofenceService;

    public static getInstance(): GeofenceService {
        if (!GeofenceService.instance) {
            GeofenceService.instance = new GeofenceService();
        }
        return GeofenceService.instance;
    }

    /** GET /fleet/geofences — the tenant's geofences with effective vehicle counts. */
    public async list(): Promise<Geofence[]> {
        const res = await ApiService.getInstance().get<GeofencesResponse>('/fleet/geofences');
        return res.geofences || [];
    }

    /** POST /fleet/geofences — create a geofence. */
    public create(input: CreateGeofenceInput): Promise<Geofence> {
        return ApiService.getInstance().post<Geofence>('/fleet/geofences', input);
    }

    /** PATCH /fleet/geofences/:id — update a geofence. */
    public update(id: string, patch: UpdateGeofenceInput): Promise<Geofence> {
        return ApiService.getInstance().patch<Geofence>(`/fleet/geofences/${encodeURIComponent(id)}`, patch);
    }

    /** DELETE /fleet/geofences/:id. */
    public delete(id: string): Promise<void> {
        return ApiService.getInstance().delete<void>(`/fleet/geofences/${encodeURIComponent(id)}`);
    }

    /** GET /fleet/geofences/:id/vehicles — token ids the geofence resolves to. */
    public async vehicles(id: string): Promise<number[]> {
        const res = await ApiService.getInstance().get<GeofenceVehiclesResponse>(
            `/fleet/geofences/${encodeURIComponent(id)}/vehicles`,
        );
        return res.tokenIds || [];
    }

    /** POST /fleet/vehicles/:tokenId/geofence/:geofenceId — assign (manual scope). */
    public addVehicle(tokenId: number, geofenceId: string): Promise<void> {
        return ApiService.getInstance().post<void>(
            `/fleet/vehicles/${tokenId}/geofence/${encodeURIComponent(geofenceId)}`,
            {},
        );
    }

    /** DELETE /fleet/vehicles/:tokenId/geofence/:geofenceId — unassign (manual scope). */
    public removeVehicle(tokenId: number, geofenceId: string): Promise<void> {
        return ApiService.getInstance().delete<void>(
            `/fleet/vehicles/${tokenId}/geofence/${encodeURIComponent(geofenceId)}`,
        );
    }

    /**
     * GET /fleet/geofences/:id/scan-targets — the effective vehicles to scan for
     * an activity (window) query, capped. The caller pages these through passes().
     */
    public scanTargets(id: string): Promise<GeofenceScanTargetsResponse> {
        return ApiService.getInstance().get<GeofenceScanTargetsResponse>(
            `/fleet/geofences/${encodeURIComponent(id)}/scan-targets`,
        );
    }

    /**
     * GET /fleet/geofences/:id/passes — passes through the geofence in [from,to]
     * for a batch of vehicles. Window must be ≤ 3 days. Call once per batch of
     * tokenIds so results accumulate progressively.
     */
    public async passes(id: string, from: string, to: string, tokenIds: number[]): Promise<VehiclePasses[]> {
        const q = new URLSearchParams({ from, to, tokenIds: tokenIds.join(',') });
        const res = await ApiService.getInstance().get<GeofencePassesResponse>(
            `/fleet/geofences/${encodeURIComponent(id)}/passes?${q.toString()}`,
        );
        return res.results || [];
    }
}
