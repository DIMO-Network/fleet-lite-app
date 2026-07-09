import { get as idbGet, set as idbSet, del as idbDel } from 'idb-keyval';
import { VehicleCard } from '../types/vehicle.ts';

export interface FleetOverviewData {
    vehicles: VehicleCard[];
    locations: Record<string, { lat: number; lon: number }>;
}

interface PersistedFleetData extends FleetOverviewData {
    savedAt: number;
}

// Persisted snapshots older than this are ignored — painting a days-old fleet
// is more misleading than a brief loading state.
const PERSIST_MAX_AGE_MS = 24 * 60 * 60 * 1000;

const idbKey = (tenantId: string) => `fleet:${tenantId}`;

/**
 * Holds the last-loaded fleet overview (vehicle list + map locations) so
 * navigating away to vehicle details and back doesn't re-trigger the loading
 * state and network round trips. Cleared whenever the data could go stale —
 * e.g. a favorite toggle changes the vehicle ordering shown on the map.
 *
 * Keyed by tenant id so switching tenants doesn't serve the previous tenant's
 * cached vehicles/locations.
 *
 * The in-memory slot is the trusted intra-session path; each set() also
 * writes through to IndexedDB (best-effort) so a cold load can paint
 * instantly from the last snapshot via loadPersisted() while it revalidates.
 */
let cached: { tenantId: string; data: FleetOverviewData } | null = null;

export const FleetCache = {
    get(tenantId: string): FleetOverviewData | null {
        return cached && cached.tenantId === tenantId ? cached.data : null;
    },
    set(tenantId: string, data: FleetOverviewData): void {
        cached = { tenantId, data };
        const persisted: PersistedFleetData = { ...data, savedAt: Date.now() };
        idbSet(idbKey(tenantId), persisted).catch(() => {
            // Persistence failing (private mode, quota) must never break the view.
        });
    },
    /**
     * Last persisted snapshot for the tenant, or null if absent/expired.
     * Paint-only: callers must still fetch /vehicles and revalidate — this
     * never substitutes for the network.
     */
    async loadPersisted(tenantId: string): Promise<FleetOverviewData | null> {
        try {
            const stored = await idbGet<PersistedFleetData>(idbKey(tenantId));
            if (!stored || Date.now() - stored.savedAt > PERSIST_MAX_AGE_MS) return null;
            return { vehicles: stored.vehicles, locations: stored.locations };
        } catch {
            return null;
        }
    },
    invalidate(): void {
        if (cached) {
            idbDel(idbKey(cached.tenantId)).catch(() => {});
        }
        cached = null;
    },
};
