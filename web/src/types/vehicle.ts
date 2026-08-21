export interface Definition {
    id: string;
    make: string;
    model: string;
    year: number;
}

export interface SyntheticDevice {
    id: string;
    tokenId: number;
    mintedAt: string;
}

export interface AftermarketDevice {
    tokenId: number;
    serial: string;
    imei: string;
}

/** A fleet group a vehicle belongs to, as embedded in the /vehicles response. */
export interface VehicleGroupRef {
    id: string;
    name: string;
    color: string;
}

export interface Vehicle {
    id: string;
    tokenId: number;
    mintedAt: string | null;
    owner: string;
    definition: Definition;
    syntheticDevice: SyntheticDevice;
    aftermarketDevice?: AftermarketDevice | null;
    isFavorite?: boolean;
    /** License plate, cached from the vehicle's registration attestation. Absent when unknown. */
    licensePlate?: string;
    /** VIN, cached from the same registration attestation as the plate. Absent when unknown. */
    vin?: string;
    /** Cached latest GPS fix (display cache, written through by the telemetry
     * fan-out). Absent until a fix has been fetched. Lets the map paint markers
     * from the DB before live locations stream in. */
    lastLat?: number;
    lastLon?: number;
    /** ISO timestamp of that latest GPS fix — shown in the list as "last seen". */
    lastSeen?: string;
    /** ISO timestamp of when we last fetched this vehicle's location from
     * telemetry-api (distinct from lastSeen, the fix time). Drives the refresh
     * window: a vehicle pulled within the window is rendered from the cache
     * instead of re-pulled. Absent until first pulled. */
    locationPulledAt?: string;
    /** Groups this vehicle belongs to (always present, [] when none). */
    groups: VehicleGroupRef[];
    /**
     * Whether this vehicle can be shared with another wallet without its
     * owner's passkey — resolved by fleet-tenancy-api against accounts-api.
     * Absent (falsy) when it cannot be, or when the lookup was unavailable.
     */
    canShare?: boolean;
    /**
     * Why the vehicle cannot be shared, when it cannot: 'owner' (the owner's
     * account has not authorized fleet sharing — typically personally owned),
     * 'no_owner', or 'unknown' (the check failed). Absent when shareable, and
     * absent when sharing does not exist in this deployment at all — the icon
     * renders only when canShare or shareBlocker is present.
     */
    shareBlocker?: 'owner' | 'no_owner' | 'unknown';
    /**
     * The vehicle is in this tenant's resolved set — entitled, membered and in
     * scope — but no metadata has been cached for it yet, so `definition`,
     * `syntheticDevice` and the rest are zeroed rather than absent.
     *
     * Expect it briefly between an operator granting an entitlement and the
     * next nightly sync. Render a "details syncing" placeholder: the vehicle is
     * genuinely theirs, and it must not be mistaken for one with no device
     * paired, which is what a zeroed syntheticDevice otherwise looks like.
     */
    metadataPending?: boolean;
}

export interface VehiclesResponse {
    vehicles: Vehicle[];
}

export interface VehicleCard {
    /** Owner wallet, for the share tooltip's "owned by you" comparison. */
    owner?: string;
    tokenId: string;
    /** Vehicle make (e.g. "Tesla"), used to resolve the OEM brand logo. */
    make: string;
    title: string;
    location: string;
    /** Fallback line shown when no GPS fix is known (device-integration label). */
    seenAt: string;
    /** ISO timestamp of the latest GPS fix; rendered as a relative "last seen" time. */
    lastSeen?: string;
    online: boolean;
    notification?: number;
    errorMessage?: string;
    noPermissions?: boolean;
    isFavorite?: boolean;
    /** License plate, cached from the vehicle's registration attestation. Absent when unknown. */
    licensePlate?: string;
    /** VIN, cached from the same registration attestation as the plate. Absent when unknown. */
    vin?: string;
    /** Groups this vehicle belongs to, for the map/list group filter. */
    groups?: VehicleGroupRef[];
    /**
     * Whether this vehicle can be shared with another wallet without its
     * owner's passkey. A display gate only — the API re-checks it, and the
     * tenancy service checks it again before anything goes on chain.
     */
    canShare?: boolean;
    /**
     * Why the vehicle cannot be shared, when it cannot: 'owner' (the owner's
     * account has not authorized fleet sharing — typically personally owned),
     * 'no_owner', or 'unknown' (the check failed). Absent when shareable, and
     * absent when sharing does not exist in this deployment at all — the icon
     * renders only when canShare or shareBlocker is present.
     */
    shareBlocker?: 'owner' | 'no_owner' | 'unknown';
    /** Metadata has not been cached for this vehicle yet; see Vehicle.metadataPending. */
    metadataPending?: boolean;
}
