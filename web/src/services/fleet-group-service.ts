import { ApiService } from './api-service.ts';
import { FleetGroup, FleetGroupsResponse } from '../types/group.ts';
import { VehicleGroupRef } from '../types/vehicle.ts';

/** Response of POST /fleet/vehicles/:tokenId/groups/sync (lazy per-vehicle sync). */
export interface VehicleGroupsSyncResponse {
    /** The vehicle's current groups after the additive merge. */
    groups: VehicleGroupRef[];
    /** false when the cooldown short-circuited the pull (cached groups returned). */
    synced: boolean;
    /** Memberships added by this sync. */
    added: number;
}

/**
 * Singleton client over the tenant-scoped /fleet/groups API. ApiService injects
 * the Bearer token and the Tenant-Id header (from the hash route) automatically.
 */
export class FleetGroupService {
    private static instance: FleetGroupService;

    public static getInstance(): FleetGroupService {
        if (!FleetGroupService.instance) {
            FleetGroupService.instance = new FleetGroupService();
        }
        return FleetGroupService.instance;
    }

    /** GET /fleet/groups — the tenant's groups with member counts. */
    public async list(): Promise<FleetGroup[]> {
        const res = await ApiService.getInstance().get<FleetGroupsResponse>('/fleet/groups');
        return res.groups || [];
    }

    /** POST /fleet/groups — create a group. */
    public create(name: string, color: string): Promise<FleetGroup> {
        return ApiService.getInstance().post<FleetGroup>('/fleet/groups', { name, color });
    }

    /**
     * PATCH /fleet/groups/:id — update a group. Name is immutable in the UI
     * (rename is disabled to bound re-attest fan-out), so callers pass only the
     * color, but the field is optional here to match the API.
     */
    public update(id: string, patch: { name?: string; color?: string }): Promise<FleetGroup> {
        return ApiService.getInstance().patch<FleetGroup>(`/fleet/groups/${encodeURIComponent(id)}`, patch);
    }

    /** DELETE /fleet/groups/:id. */
    public delete(id: string): Promise<void> {
        return ApiService.getInstance().delete<void>(`/fleet/groups/${encodeURIComponent(id)}`);
    }

    /** POST /fleet/vehicles/:tokenId/group/:groupId — add a vehicle to a group. */
    public addVehicle(tokenId: number, groupId: string): Promise<void> {
        return ApiService.getInstance().post<void>(
            `/fleet/vehicles/${tokenId}/group/${encodeURIComponent(groupId)}`,
            {},
        );
    }

    /** DELETE /fleet/vehicles/:tokenId/group/:groupId — remove a vehicle from a group. */
    public removeVehicle(tokenId: number, groupId: string): Promise<void> {
        return ApiService.getInstance().delete<void>(
            `/fleet/vehicles/${tokenId}/group/${encodeURIComponent(groupId)}`,
        );
    }

    /**
     * POST /fleet/vehicles/:tokenId/groups/sync — lazy per-vehicle sync. Pulls
     * this vehicle's group attestations, additively merges them, and returns its
     * current groups. Cooldown-gated server-side, so it's cheap to call on every
     * vehicle view.
     */
    public syncGroups(tokenId: number): Promise<VehicleGroupsSyncResponse> {
        return ApiService.getInstance().post<VehicleGroupsSyncResponse>(
            `/fleet/vehicles/${tokenId}/groups/sync`,
            {},
        );
    }
}
