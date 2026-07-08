export interface TCOSettings {
    tokenId: number;
    purchasePrice?: number;
    purchaseDate?: string; // YYYY-MM-DD
    usefulLifeYears?: number;
    currency: string;
}

export interface LineItem {
    vehicleTokenId: number;
    vehicleLabel: string;
    vin: string;
    date: string;
    category: string;
    description: string;
    amount: number;
    currency: string;
}

export interface VehicleTCOSummary {
    tokenId: number;
    vehicleLabel: string;
    vin?: string;
    operatingCost: number;
    costByCategory: Record<string, number>;
    acquisitionCost: number;
    depreciationToDate: number;
    totalTco: number;
    settings: TCOSettings;
    lineItems: LineItem[];
}

export interface FleetTotals {
    operatingCost: number;
    acquisitionCost: number;
    depreciationToDate: number;
    totalTco: number;
}

export interface FleetTCOSummary {
    vehicles: VehicleTCOSummary[];
    fleet: FleetTotals;
}
