import { ChargingFleetSummary } from '../types/charging.ts';

/**
 * Holds the last-loaded charging fleet summary so navigating away to a
 * vehicle drilldown and back — or leaving the tab and returning — doesn't
 * re-trigger the full loading state and the fleet's worth of telemetry
 * round trips. Mirrors TCOCache's pattern.
 *
 * Keyed by tenant id so switching tenants doesn't serve the previous
 * tenant's cached data. No TTL — served until explicitly invalidated (a
 * settings save, or the view's own manual refresh).
 */
let cached: { tenantId: string; data: ChargingFleetSummary } | null = null;

export const ChargingCache = {
    get(tenantId: string): ChargingFleetSummary | null {
        return cached && cached.tenantId === tenantId ? cached.data : null;
    },
    set(tenantId: string, data: ChargingFleetSummary): void {
        cached = { tenantId, data };
    },
    invalidate(): void {
        cached = null;
    },
};
