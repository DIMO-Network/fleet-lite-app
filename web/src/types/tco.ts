export interface TCOSettings {
    tokenId: number;
    purchasePrice?: number;
    purchaseDate?: string; // YYYY-MM-DD
    usefulLifeYears?: number;
    currency: string;
}

export interface LineItem {
    /** The document's parsed CE id — pass to TCOService.backfillAmount to attach an amount. */
    id: string;
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
    /** Cost-eligible documents with no amount yet — neither typed in at
     * upload nor backfilled. Offer TCOService.backfillAmount for these. */
    missingAmounts?: LineItem[];
    /** True when the dev license lacks SACD permissions on this vehicle — its
     * document/operating-cost figures could not be read (acquisition/
     * depreciation still work, since those are DB-only). */
    permissionsRequired?: boolean;
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
