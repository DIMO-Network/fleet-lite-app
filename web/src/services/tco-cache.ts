import { Vehicle } from '../types/vehicle.ts';
import { VehicleTCOSummary } from '../types/tco.ts';

export interface TCOCacheData {
    vehicles: Vehicle[];
    tcoByToken: Map<number, VehicleTCOSummary>;
}

/**
 * Holds the last-loaded TCO fleet table (vehicle list + per-vehicle cost
 * figures) so navigating away to a drilldown and back — or leaving the tab
 * and returning — doesn't re-trigger the full loading state and the fleet's
 * worth of fetch-api round trips. Mirrors FleetCache's pattern.
 *
 * Keyed by tenant id so switching tenants doesn't serve the previous
 * tenant's cached data. No TTL — served until explicitly invalidated by an
 * event that could make it stale (a document upload, a settings save, a
 * backfilled amount).
 */
let cached: { tenantId: string; data: TCOCacheData } | null = null;

export const TCOCache = {
    get(tenantId: string): TCOCacheData | null {
        return cached && cached.tenantId === tenantId ? cached.data : null;
    },
    set(tenantId: string, data: TCOCacheData): void {
        cached = { tenantId, data };
    },
    invalidate(): void {
        cached = null;
    },
};
