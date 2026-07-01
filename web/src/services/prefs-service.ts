import { ApiService } from './api-service.ts';

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
const PREFS_ENDPOINT = '/me/preferences';

/** The explicitly-set preferences this browser mirrors to the backend. */
type StoredPrefs = Partial<Record<'units' | 'locale' | 'tripMechanism', string>>;

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

    /** Returns the current preference (defaults to metric; explicit imperial is respected). */
    public getUnits(): UnitSystem {
        const v = localStorage.getItem(STORAGE_KEY);
        return v === 'imperial' ? 'imperial' : 'metric';
    }

    public setUnits(u: UnitSystem): void {
        localStorage.setItem(STORAGE_KEY, u);
        window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: { units: u } }));
        void this.pushToServer();
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
        void this.pushToServer();
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
        void this.pushToServer();
    }

    /** The explicitly-set preferences in localStorage (the set the server mirrors). */
    private storedPrefs(): StoredPrefs {
        const p: StoredPrefs = {};
        const u = localStorage.getItem(STORAGE_KEY);
        if (u) p.units = u;
        const l = localStorage.getItem(LOCALE_KEY);
        if (l) p.locale = l;
        const t = localStorage.getItem(TRIP_MECHANISM_KEY);
        if (t) p.tripMechanism = t;
        return p;
    }

    /**
     * Best-effort write-through of the current preferences to the backend. No-op
     * when unauthenticated (no token) — anonymous users stay localStorage-only.
     */
    private async pushToServer(): Promise<void> {
        if (!localStorage.getItem('token')) return;
        try {
            await ApiService.getInstance().put(PREFS_ENDPOINT, this.storedPrefs());
        } catch {
            // best-effort; localStorage remains the local source of truth
        }
    }

    /**
     * Load the wallet's server-stored preferences and reconcile with this
     * browser: server wins, and any local-only keys the server lacked are
     * backfilled up (migrating this browser's existing choices). Applied to
     * localStorage and broadcast so components re-render. Call once per session
     * after auth (from app-root). Best-effort — offline keeps working locally.
     */
    public async hydrateFromServer(): Promise<void> {
        if (!localStorage.getItem('token')) return;
        let server: StoredPrefs;
        try {
            server = await ApiService.getInstance().get<StoredPrefs>(PREFS_ENDPOINT);
        } catch {
            return; // unreachable / unauthorized — stay on localStorage
        }
        const merged: StoredPrefs = { ...this.storedPrefs(), ...server }; // server precedence

        let changed = false;
        const apply = (key: string, next: string | undefined) => {
            if (next && next !== localStorage.getItem(key)) {
                localStorage.setItem(key, next);
                changed = true;
            }
        };
        apply(STORAGE_KEY, merged.units);
        apply(LOCALE_KEY, merged.locale);
        apply(TRIP_MECHANISM_KEY, merged.tripMechanism);

        // Backfill: this browser had keys the server didn't → push the merge up.
        const keys: (keyof StoredPrefs)[] = ['units', 'locale', 'tripMechanism'];
        if (keys.some(k => merged[k] !== undefined && merged[k] !== server[k])) {
            void this.pushToServer();
        }
        if (changed) {
            window.dispatchEvent(new CustomEvent(EVENT_NAME, { detail: { hydrated: true } }));
        }
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
