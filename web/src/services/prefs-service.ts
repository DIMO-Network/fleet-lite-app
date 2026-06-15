export type UnitSystem = 'imperial' | 'metric';
export type Locale = 'en' | 'es';

/**
 * Trip-detection method for the details-screen trips panel. 'auto' lets the
 * server pick (its device-aware heuristic + fallback); the rest force a
 * specific telemetry-api segmentation strategy.
 */
export type TripMechanism =
    | 'auto'
    | 'ignitionDetection'
    | 'frequencyAnalysis'
    | 'changePointDetection'
    | 'idling'
    | 'refuel'
    | 'recharge';

const STORAGE_KEY = 'fleet-lite:units';
const LOCALE_KEY = 'fleet-lite:locale';
const TRIP_MECHANISM_KEY = 'fleet-lite:trip-mechanism';
const EVENT_NAME = 'fleet-lite-prefs-changed';

const VALID_TRIP_MECHANISMS: readonly TripMechanism[] = [
    'auto', 'ignitionDetection', 'frequencyAnalysis', 'changePointDetection', 'idling', 'refuel', 'recharge',
];

/** Friendly endonym for the language itself (shown in its own language). */
export function localeLabel(l: Locale): string {
    return l === 'es' ? 'Español' : 'English';
}

/**
 * Singleton holding user preferences that don't yet need a DB (units,
 * eventually language, etc.). Reads/writes localStorage and emits a
 * window-level CustomEvent so any component can subscribe and re-render
 * without a context provider.
 */
export class PrefsService {
    private static _instance: PrefsService;

    public static getInstance(): PrefsService {
        if (!PrefsService._instance) {
            PrefsService._instance = new PrefsService();
        }
        return PrefsService._instance;
    }

    /** Returns the current preference (defaults to imperial for US-centric UX). */
    public getUnits(): UnitSystem {
        const v = localStorage.getItem(STORAGE_KEY);
        return v === 'metric' ? 'metric' : 'imperial';
    }

    public setUnits(u: UnitSystem): void {
        localStorage.setItem(STORAGE_KEY, u);
        window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: { units: u } }));
    }

    public toggleUnits(): UnitSystem {
        const next: UnitSystem = this.getUnits() === 'imperial' ? 'metric' : 'imperial';
        this.setUnits(next);
        return next;
    }

    /**
     * Returns the current locale. Defaults to a saved choice, else the
     * browser language (es-* → 'es'), else English.
     */
    public getLocale(): Locale {
        const saved = localStorage.getItem(LOCALE_KEY);
        if (saved === 'es' || saved === 'en') return saved;
        return navigator.language.startsWith('es') ? 'es' : 'en';
    }

    public setLocale(l: Locale): void {
        localStorage.setItem(LOCALE_KEY, l);
        window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: { locale: l } }));
    }

    public toggleLocale(): Locale {
        const next: Locale = this.getLocale() === 'en' ? 'es' : 'en';
        this.setLocale(next);
        return next;
    }

    /** Persisted trip-detection method (defaults to 'auto' — server decides). */
    public getTripMechanism(): TripMechanism {
        const v = localStorage.getItem(TRIP_MECHANISM_KEY);
        return (VALID_TRIP_MECHANISMS as readonly string[]).includes(v ?? '') ? (v as TripMechanism) : 'auto';
    }

    public setTripMechanism(m: TripMechanism): void {
        localStorage.setItem(TRIP_MECHANISM_KEY, m);
        window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: { tripMechanism: m } }));
    }

    /**
     * Subscribe to preference changes. Returns an unsubscribe fn. Caller is
     * responsible for cleanup in disconnectedCallback to avoid leaks.
     */
    public subscribe(cb: () => void): () => void {
        const h = () => cb();
        window.addEventListener(EVENT_NAME, h);
        return () => window.removeEventListener(EVENT_NAME, h);
    }
}
