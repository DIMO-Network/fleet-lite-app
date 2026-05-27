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

export interface Vehicle {
    id: string;
    tokenId: number;
    mintedAt: string | null;
    owner: string;
    definition: Definition;
    syntheticDevice: SyntheticDevice;
}

export interface VehiclesResponse {
    vehicles: Vehicle[];
}
