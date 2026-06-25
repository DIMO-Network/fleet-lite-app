import { Trip } from '../types/telemetry.ts';

/**
 * Helpers over telemetry-api `segments` results, shared by the quick-view
 * trips list and the details screen's trips panel.
 */

/** Lookup one aggregation value on a trip (e.g. speed AVG). */
export function tripSignal(trip: Trip, name: string, agg: string): number | undefined {
    return trip.signals?.find((s) => s.name === name && s.agg === agg)?.value;
}

/** Trip distance in km: odometer LAST − FIRST (how the b2b app derives it). */
export function tripDistanceKm(trip: Trip): number | undefined {
    const first = tripSignal(trip, 'powertrainTransmissionTravelledDistance', 'FIRST');
    const last = tripSignal(trip, 'powertrainTransmissionTravelledDistance', 'LAST');
    if (first == null || last == null) return undefined;
    const d = last - first;
    return d >= 0 ? d : undefined;
}

/** "Wed 2:15 PM" in the user's locale — compact enough for a list row. */
export function tripTimeShort(iso: string): string {
    const d = new Date(iso);
    return d.toLocaleString(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' });
}

/**
 * Trip duration in milliseconds; ongoing trips are clipped at `now`.
 * Returns 0 for malformed timestamps rather than NaN-poisoning sums.
 */
export function tripDurationMs(trip: Trip, now = Date.now()): number {
    const start = Date.parse(trip.start.timestamp);
    const end = trip.isOngoing ? now : Date.parse(trip.end.timestamp);
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return 0;
    return end - start;
}

/** A geofence dwell duration (seconds) as a compact "1h 5m" / "2m 30s" / "45s". */
export function formatDwell(seconds: number): string {
    const s = Math.max(0, Math.round(seconds));
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) {
        const rem = s % 60;
        return rem ? `${m}m ${rem}s` : `${m}m`;
    }
    const h = Math.floor(m / 60);
    const remM = m % 60;
    return remM ? `${h}h ${remM}m` : `${h}h`;
}
