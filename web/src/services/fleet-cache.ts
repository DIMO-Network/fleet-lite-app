import { VehicleCard } from '../types/vehicle.ts';

export interface FleetOverviewData {
    vehicles: VehicleCard[];
    locations: Record<string, { lat: number; lon: number }>;
}

/**
 * Holds the last-loaded fleet overview (vehicle list + map locations) so
 * navigating away to vehicle details and back doesn't re-trigger the loading
 * state and network round trips. Cleared whenever the data could go stale —
 * e.g. a favorite toggle changes the vehicle ordering shown on the map.
 *
 * Keyed by tenant id so switching tenants doesn't serve the previous tenant's
 * cached vehicles/locations.
 */
let cached: { tenantId: string; data: FleetOverviewData } | null = null;

export const FleetCache = {
    get(tenantId: string): FleetOverviewData | null {
        return cached && cached.tenantId === tenantId ? cached.data : null;
    },
    set(tenantId: string, data: FleetOverviewData): void {
        cached = { tenantId, data };
    },
    invalidate(): void {
        cached = null;
    },
};
