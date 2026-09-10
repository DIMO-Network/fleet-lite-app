import { msg } from '@lit/localize';
import { BehaviorDay, BehaviorResponse, EventCount, Trip } from '../types/telemetry.ts';
import { tripDistanceKm } from './trips.ts';

/**
 * The four DIMO driver-behaviour event names (model-garage
 * `default-event-names.yaml`). Only Ruptela hardware (and the Kaufmann
 * oracle's Ruptela family) emits these; AutoPi / Tesla / HashDog never will —
 * see docs/DRIVER_BEHAVIOUR_PLAN.md.
 */
export const BEHAVIOR_EVENT_NAMES: readonly string[] = [
    'behavior.harshBraking',
    'behavior.extremeBraking',
    'behavior.harshCornering',
    'behavior.harshAcceleration',
];

/**
 * What the UI shows: three series, in the fixed display/stack order used
 * everywhere (tiles, stacked bars, legend, trip badges, replay ticks). Harsh
 * and extreme braking are collapsed into one "braking" series — a fleet
 * manager cares that the vehicle brakes hard, not which Ruptela threshold
 * tripped.
 *
 * Colours are theme tokens (`global-styles.ts`), validated per theme against
 * the chart surface with the dataviz palette validator; every use pairs the
 * colour with a label, and stacked segments keep a 2px surface gap.
 */
export interface BehaviorSeries {
    key: 'braking' | 'cornering' | 'acceleration';
    /** Render-time thunk — never call msg() at module scope (docs/LOCALIZATION.md). */
    label: () => string;
    /** Event names folded into this series. */
    names: readonly string[];
    cssVar: string;
}

export const BEHAVIOR_SERIES: readonly BehaviorSeries[] = [
    { key: 'braking',      label: () => msg('Harsh braking'),      names: ['behavior.harshBraking', 'behavior.extremeBraking'], cssVar: '--bhv-braking' },
    { key: 'cornering',    label: () => msg('Harsh cornering'),    names: ['behavior.harshCornering'],                          cssVar: '--bhv-cornering' },
    { key: 'acceleration', label: () => msg('Harsh acceleration'), names: ['behavior.harshAcceleration'],                       cssVar: '--bhv-acceleration' },
];

export function isBehaviorEvent(name: string): boolean {
    return BEHAVIOR_EVENT_NAMES.includes(name);
}

export function seriesFor(name: string): BehaviorSeries | undefined {
    return BEHAVIOR_SERIES.find((s) => s.names.includes(name));
}

/** CSS colour expression for an event name (falls back to a neutral). */
export function behaviorColor(name: string): string {
    const s = seriesFor(name);
    return s ? `var(${s.cssVar})` : 'var(--outline)';
}

/** Count for one series out of per-name counts (array or record form). */
export function seriesCount(series: BehaviorSeries, counts: EventCount[] | Record<string, number> | undefined): number {
    if (!counts) return 0;
    if (Array.isArray(counts)) {
        return counts.reduce((sum, c) => sum + (series.names.includes(c.name) ? c.count : 0), 0);
    }
    return series.names.reduce((sum, n) => sum + (counts[n] ?? 0), 0);
}

/** Sum of counts across all behaviour events (unknown names ignored). */
export function behaviorTotal(counts: EventCount[] | Record<string, number> | undefined): number {
    return BEHAVIOR_SERIES.reduce((sum, s) => sum + seriesCount(s, counts), 0);
}

// ---------------------------------------------------------------------------
// DESIGN-REVIEW MOCK — remove once GET /telemetry/:tokenId/behavior and the
// segments `eventCounts` field ship (docs/DRIVER_BEHAVIOUR_PLAN.md steps 1–2).
// ---------------------------------------------------------------------------

export const BEHAVIOR_DEMO = true;

function lcg(seed: number): () => number {
    let s = seed >>> 0;
    return () => {
        s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
        return s / 2 ** 32;
    };
}

/** Deterministic 30-day mock shaped like the real Ruptela F-150 probe data. */
export function demoBehavior(days = 30): BehaviorResponse {
    const rnd = lcg(20260910);
    const out: BehaviorDay[] = [];
    const totals: Record<string, number> = {};
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    for (let i = days - 1; i >= 0; i--) {
        const d = new Date(today);
        d.setDate(today.getDate() - i);
        const weekend = d.getDay() === 0 || d.getDay() === 6;
        const idle = rnd() < (weekend ? 0.7 : 0.12);
        const distanceKm = idle ? 0 : Math.round(40 + rnd() * 180);
        const scale = distanceKm / 100;
        const counts: Record<string, number> = {
            'behavior.harshBraking':      idle ? 0 : Math.round(scale * 5 * (0.4 + rnd())),
            'behavior.harshCornering':    idle ? 0 : Math.round(scale * 1.5 * (0.4 + rnd())),
            'behavior.extremeBraking':    idle ? 0 : Math.round(scale * 7 * (0.4 + rnd())),
            'behavior.harshAcceleration': idle ? 0 : Math.round(scale * 8 * (0.4 + rnd())),
        };
        for (const [k, v] of Object.entries(counts)) totals[k] = (totals[k] ?? 0) + v;
        out.push({
            date: d.toISOString().slice(0, 10),
            counts,
            distanceKm: idle ? null : distanceKm,
            driveSeconds: idle ? 0 : Math.round((distanceKm / 42) * 3600),
            tripCount: idle ? 0 : 1 + Math.floor(rnd() * 4),
        });
    }
    return {
        supported: true,
        allTime: BEHAVIOR_EVENT_NAMES.map((name) => ({
            name,
            count: (totals[name] ?? 0) * 6,
            firstSeen: '2026-03-10T16:04:01Z',
            lastSeen: new Date().toISOString(),
        })),
        days: out,
    };
}

/**
 * Mock trips for a window, for reviewing the trips panel when the local
 * tenant's vehicle has no telemetry access. Newest first, like the API.
 */
export function demoTrips(from: Date, to: Date): Trip[] {
    const rnd = lcg(Math.floor(from.getTime() / 86_400_000));
    const trips: Trip[] = [];
    let odo = 13_900;
    const dayMs = 86_400_000;
    for (let t = from.getTime(); t < to.getTime(); t += dayMs) {
        const perDay = rnd() < 0.3 ? 0 : 1 + Math.floor(rnd() * 3);
        let cursor = t + (7 + rnd() * 3) * 3_600_000;
        for (let k = 0; k < perDay; k++) {
            const km = Math.round(5 + rnd() * 90);
            const avg = 30 + rnd() * 35;
            const durMs = (km / avg) * 3_600_000;
            if (cursor + durMs > Date.now()) break;
            const start = new Date(cursor).toISOString();
            const end = new Date(cursor + durMs).toISOString();
            const trip: Trip = {
                start: { value: { latitude: 25.76 + rnd() * 0.2, longitude: -80.19 - rnd() * 0.2 }, timestamp: start },
                end:   { value: { latitude: 25.76 + rnd() * 0.2, longitude: -80.19 - rnd() * 0.2 }, timestamp: end },
                isOngoing: false,
                signals: [
                    { name: 'powertrainTransmissionTravelledDistance', agg: 'FIRST', value: odo },
                    { name: 'powertrainTransmissionTravelledDistance', agg: 'LAST', value: odo + km },
                    { name: 'speed', agg: 'AVG', value: Math.round(avg) },
                    { name: 'speed', agg: 'MAX', value: Math.round(avg * (1.6 + rnd() * 0.6)) },
                ],
            };
            trip.eventCounts = demoTripEventCounts(trip);
            trips.push(trip);
            odo += km;
            cursor += durMs + (1 + rnd() * 4) * 3_600_000;
        }
    }
    return trips.reverse();
}

/** Mock per-trip counts, seeded from the trip so re-renders are stable. */
export function demoTripEventCounts(trip: Trip): EventCount[] {
    const rnd = lcg(Date.parse(trip.start.timestamp) / 1000);
    const km = tripDistanceKm(trip) ?? 10 + rnd() * 60;
    const scale = km / 100;
    if (rnd() < 0.3) return BEHAVIOR_EVENT_NAMES.map((name) => ({ name, count: 0 }));
    return [
        { name: 'behavior.harshBraking',      count: Math.round(scale * 5 * (0.3 + rnd())) },
        { name: 'behavior.harshCornering',    count: Math.round(scale * 1.5 * (0.3 + rnd())) },
        { name: 'behavior.extremeBraking',    count: Math.round(scale * 7 * (0.3 + rnd())) },
        { name: 'behavior.harshAcceleration', count: Math.round(scale * 8 * (0.3 + rnd())) },
    ];
}
