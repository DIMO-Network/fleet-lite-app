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

export interface Vehicle {
    id: string;
    tokenId: number;
    mintedAt: string | null;
    owner: string;
    definition: Definition;
    syntheticDevice: SyntheticDevice;
    aftermarketDevice?: AftermarketDevice | null;
}

export interface VehiclesResponse {
    vehicles: Vehicle[];
}

/** A vehicle's latest GPS fix for the fleet-overview map (GET /telemetry/locations). */
export interface VehicleLocation {
    tokenId: number;
    title: string;
    latitude: number;
    longitude: number;
    timestamp: string;
}

export interface LocationsResponse {
    locations: VehicleLocation[];
}
