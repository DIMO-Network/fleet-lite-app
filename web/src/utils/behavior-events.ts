import { msg } from '@lit/localize';
import { EventCount } from '../types/telemetry.ts';

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
