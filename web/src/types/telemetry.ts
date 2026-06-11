export interface SignalLatest {
    value: number | string | boolean | null;
    timestamp: string;
}

export interface LatestSignalsResponse {
    signals: Record<string, SignalLatest>;
    permissionsRequired?: boolean;
    devLicense?: string;
}

export interface TimeSeriesBucket {
    timestamp: string;
    min: number;
    max: number;
    avg: number;
    last: number;
}

export interface FleetLocationsResponse {
    locations: Record<string, { lat: number; lon: number }>;
    noPermissions: string[];
}

export interface TimeSeriesResponse {
    signal: string;
    interval: string;
    buckets: TimeSeriesBucket[];
    permissionsRequired?: boolean;
    devLicense?: string;
}

/** One end of a detected trip: GPS fix + timestamp. */
export interface TripPoint {
    value: { latitude: number; longitude: number };
    timestamp: string;
}

/** One aggregation over the trip window (odometer FIRST/LAST, speed AVG/MAX). */
export interface TripSignal {
    name: string;
    agg: string;
    value: number;
}

/** A detected trip from telemetry-api's `segments` query. */
export interface Trip {
    start: TripPoint;
    end: TripPoint;
    isOngoing: boolean;
    signals: TripSignal[];
}

export interface SegmentsResponse {
    segments: Trip[];
    mechanism?: string;
    permissionsRequired?: boolean;
    devLicense?: string;
}

export interface TripRouteResponse {
    points: Array<{ lat: number; lon: number }>;
    permissionsRequired?: boolean;
}
