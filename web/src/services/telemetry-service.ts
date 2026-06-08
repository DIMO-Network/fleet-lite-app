import { ApiService } from './api-service.ts';
import { FleetLocationsResponse, LatestSignalsResponse, TimeSeriesResponse, TripsResponse, TripRouteResponse } from '../types/telemetry.ts';

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
     * GET /telemetry/:tokenId/timeseries — aggregation buckets for one signal.
     * Caller picks interval as a Go duration string, e.g. `24h` for daily
     * buckets (the telemetry-api has no `d` unit — only h/m/s).
     */
    fleetLocations(): Promise<FleetLocationsResponse> {
        return ApiService.getInstance().get<FleetLocationsResponse>('/telemetry/locations');
    }

    timeSeries(tokenId: number, signal: string, from: string, to: string, interval = '24h'): Promise<TimeSeriesResponse> {
        const q = new URLSearchParams({ signal, from, to, interval });
        return ApiService.getInstance().get<TimeSeriesResponse>(`/telemetry/${tokenId}/timeseries?${q.toString()}`);
    }

    /**
     * GET /telemetry/:tokenId/trips — detected driving segments ("trips").
     * Omit `from`/`to` to get the server's default last-3-days window.
     */
    trips(tokenId: number, from?: string, to?: string): Promise<TripsResponse> {
        const q = new URLSearchParams();
        if (from) q.set('from', from);
        if (to) q.set('to', to);
        const suffix = q.toString() ? `?${q.toString()}` : '';
        return ApiService.getInstance().get<TripsResponse>(`/telemetry/${tokenId}/trips${suffix}`);
    }

    /**
     * GET /telemetry/:tokenId/trip-route — GPS waypoints and behavior events
     * for a trip's time window, used to animate route playback. `from`/`to`
     * are required (the trip's start/end timestamps).
     */
    tripRoute(tokenId: number, from: string, to: string): Promise<TripRouteResponse> {
        const q = new URLSearchParams({ from, to });
        return ApiService.getInstance().get<TripRouteResponse>(`/telemetry/${tokenId}/trip-route?${q.toString()}`);
    }
}
