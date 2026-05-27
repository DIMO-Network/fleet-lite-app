export type UnitSystem = 'imperial' | 'metric';

const STORAGE_KEY = 'fleet-lite:units';
const EVENT_NAME = 'fleet-lite-prefs-changed';

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
     * Subscribe to preference changes. Returns an unsubscribe fn. Caller is
     * responsible for cleanup in disconnectedCallback to avoid leaks.
     */
    public subscribe(cb: () => void): () => void {
        const h = () => cb();
        window.addEventListener(EVENT_NAME, h);
        return () => window.removeEventListener(EVENT_NAME, h);
    }
}
