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
    /** Groups this vehicle belongs to (always present, [] when none). */
    groups: VehicleGroupRef[];
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
    seenAt: string;
    online: boolean;
    notification?: number;
    errorMessage?: string;
    noPermissions?: boolean;
    isFavorite?: boolean;
    /** Groups this vehicle belongs to, for the map/list group filter. */
    groups?: VehicleGroupRef[];
}
