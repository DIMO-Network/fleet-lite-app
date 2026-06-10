import { ApiService } from './api-service.ts';
import { FleetLocationsResponse, LatestSignalsResponse, TimeSeriesResponse } from '../types/telemetry.ts';

export class TelemetryService {
    private static instance: TelemetryService;
    public static getInstance(): TelemetryService {
        if (!TelemetryService.instance) {
            TelemetryService.instance = new TelemetryService();
        }
        return TelemetryService.instance;
    }

    /** GET /telemetry/:tokenId/latest — curated signal set chosen server-side. */
    latest(tokenId: number): Promise<LatestSignalsResponse> {
        return ApiService.getInstance().get<LatestSignalsResponse>(`/telemetry/${tokenId}/latest`);
    }

    /**
     * GET /telemetry/locations — last-known coordinates for the whole fleet.
     * `force` bypasses the backend's per-tenant cache (manual refresh).
     */
    fleetLocations(force = false): Promise<FleetLocationsResponse> {
        return ApiService.getInstance().get<FleetLocationsResponse>(
            `/telemetry/locations${force ? '?force=true' : ''}`,
        );
    }

    /**
     * GET /telemetry/:tokenId/timeseries — aggregation buckets for one signal.
     * Caller picks interval (e.g. `1d` for 7 daily buckets).
     */
    timeSeries(tokenId: number, signal: string, from: string, to: string, interval = '1d'): Promise<TimeSeriesResponse> {
        const q = new URLSearchParams({ signal, from, to, interval });
        return ApiService.getInstance().get<TimeSeriesResponse>(`/telemetry/${tokenId}/timeseries?${q.toString()}`);
    }
}
