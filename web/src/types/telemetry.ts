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
    /** Per-vehicle latest GPS fix; `timestamp` is the fix time (used to refresh
     * "last seen" on real-time polls). */
    locations: Record<string, { lat: number; lon: number; timestamp?: string }>;
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

/** Per-event count, as returned by telemetry-api `eventCounts` on segments/days. */
export interface EventCount {
    name: string;
    count: number;
}

/** A detected trip from telemetry-api's `segments` query. */
export interface Trip {
    start: TripPoint;
    end: TripPoint;
    isOngoing: boolean;
    signals: TripSignal[];
    /** Driver-behaviour event counts within the trip. Absent for connections that never emit events. */
    eventCounts?: EventCount[];
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

/** One timestamped GPS fix along a trip, for animated replay. */
export interface TripWaypoint {
    timestamp: string;
    lat: number;
    lng: number;
}

/** A discrete driving-behavior event (harsh braking, cornering, etc.). */
export interface TripEvent {
    timestamp: string;
    name: string;
    durationNs: number;
}

export interface TripReplayResponse {
    waypoints: TripWaypoint[];
    events: TripEvent[];
    permissionsRequired?: boolean;
}

/** All-time total for one behaviour event (telemetry-api `dataSummary.eventDataSummary`). */
export interface BehaviorEventTotal {
    name: string;
    count: number;
    firstSeen: string;
    lastSeen: string;
}

/** One calendar day of driver-behaviour counts (telemetry-api `dailyActivity` + `eventRequests`). */
export interface BehaviorDay {
    /** YYYY-MM-DD in the requested timezone. */
    date: string;
    counts: Record<string, number>;
    /** Travelled distance that day; null when the vehicle reports no odometer. */
    distanceKm: number | null;
    driveSeconds: number;
    tripCount: number;
}

/** GET /telemetry/:tokenId/behavior */
export interface BehaviorResponse {
    /** false when the vehicle has never reported a behaviour event (its connection can't). */
    supported: boolean;
    allTime: BehaviorEventTotal[];
    days: BehaviorDay[];
    permissionsRequired?: boolean;
    devLicense?: string;
}
