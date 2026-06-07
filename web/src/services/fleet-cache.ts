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
 */
let cached: FleetOverviewData | null = null;

export const FleetCache = {
    get(): FleetOverviewData | null {
        return cached;
    },
    set(data: FleetOverviewData): void {
        cached = data;
    },
    invalidate(): void {
        cached = null;
    },
};
