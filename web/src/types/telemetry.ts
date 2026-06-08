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

export interface Trip {
    startTime: string;
    startLocation: { lat: number; lon: number } | null;
    endTime: string | null;
    endLocation: { lat: number; lon: number } | null;
    duration: number; // seconds
    isOngoing: boolean;
    distanceKm: number | null;
    avgSpeedKph: number | null;
    maxSpeedKph: number | null;
}

export interface TripsResponse {
    trips: Trip[];
    from: string;
    to: string;
    permissionsRequired?: boolean;
    devLicense?: string;
}

export interface TripWaypoint {
    timestamp: string;
    lat: number;
    lng: number;
}

export interface TripEvent {
    timestamp: string;
    name: string;
    durationNs: number;
}

export interface TripRouteResponse {
    waypoints: TripWaypoint[];
    events: TripEvent[];
    from: string;
    to: string;
    permissionsRequired?: boolean;
}

export interface TimeSeriesResponse {
    signal: string;
    interval: string;
    buckets: TimeSeriesBucket[];
    permissionsRequired?: boolean;
    devLicense?: string;
}
