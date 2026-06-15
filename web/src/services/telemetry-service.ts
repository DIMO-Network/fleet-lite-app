import { ApiService } from './api-service.ts';
import { FleetLocationsResponse, LatestSignalsResponse, SegmentsResponse, TimeSeriesResponse, TripRouteResponse, TripReplayResponse } from '../types/telemetry.ts';

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
     * GET /telemetry/locations — last-known coordinates for the fleet.
     * `tokenIds` restricts the call to a subset (the map pages the fleet in
     * chunks so markers stream in); omitted = whole fleet. `force` bypasses
     * the backend's per-vehicle cache (manual refresh).
     */
    fleetLocations(force = false, tokenIds?: string[]): Promise<FleetLocationsResponse> {
        const params = new URLSearchParams();
        if (force) params.set('force', 'true');
        if (tokenIds?.length) params.set('tokenIds', tokenIds.join(','));
        const q = params.toString();
        return ApiService.getInstance().get<FleetLocationsResponse>(
            `/telemetry/locations${q ? `?${q}` : ''}`,
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

    /** GET /telemetry/:tokenId/segments — detected trips in the window, newest first. */
    segments(tokenId: number, from: string, to: string): Promise<SegmentsResponse> {
        const q = new URLSearchParams({ from, to });
        return ApiService.getInstance().get<SegmentsResponse>(`/telemetry/${tokenId}/segments?${q.toString()}`);
    }

    /** GET /telemetry/:tokenId/route — sampled location points across one trip. */
    tripRoute(tokenId: number, from: string, to: string): Promise<TripRouteResponse> {
        const q = new URLSearchParams({ from, to });
        return ApiService.getInstance().get<TripRouteResponse>(`/telemetry/${tokenId}/route?${q.toString()}`);
    }

    /** GET /telemetry/:tokenId/replay — timestamped waypoints + behavior events for replay. */
    tripReplay(tokenId: number, from: string, to: string): Promise<TripReplayResponse> {
        const q = new URLSearchParams({ from, to });
        return ApiService.getInstance().get<TripReplayResponse>(`/telemetry/${tokenId}/replay?${q.toString()}`);
    }
}
