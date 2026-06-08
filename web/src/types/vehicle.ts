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
    isFavorite?: boolean;
}

export interface VehiclesResponse {
    vehicles: Vehicle[];
}

export interface VehicleCard {
    tokenId: string;
    title: string;
    location: string;
    seenAt: string;
    online: boolean;
    notification?: number;
    errorMessage?: string;
    noPermissions?: boolean;
    isFavorite?: boolean;
}
