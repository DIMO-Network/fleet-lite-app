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

export interface TimeSeriesResponse {
    signal: string;
    interval: string;
    buckets: TimeSeriesBucket[];
    permissionsRequired?: boolean;
    devLicense?: string;
}
