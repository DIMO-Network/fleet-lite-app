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
}

export interface VehiclesResponse {
    vehicles: Vehicle[];
}

export interface VehicleCard {
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
}
