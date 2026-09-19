export interface ChargingSettings {
    electricityRate?: number;
    gasPrice?: number;
    gasMpgEquivalent?: number;
    vehicleKwhPerMile?: number;
    currency: string;
}

export interface ChargingSessionView {
    tokenId: number;
    vehicleLabel: string;
    vin?: string;
    startedAt: string;
    endedAt: string;
    addedEnergyKwh?: number;
    avgPowerKw?: number;
    socStartPct?: number;
    socEndPct?: number;
    lat?: number;
    lng?: number;
    currency: string;
    /** Undefined (not 0) when the tenant hasn't configured an electricity rate. */
    cost?: number;
    /** Undefined when gas-comparison settings (price/mpg/efficiency) are incomplete. */
    gasCostAvoided?: number;
    savings?: number;
}

export interface ChargingFleetTotals {
    addedEnergyKwh: number;
    cost?: number;
    savings?: number;
}

export interface ChargingFleetSummary {
    sessions: ChargingSessionView[];
    fleet: ChargingFleetTotals;
}
