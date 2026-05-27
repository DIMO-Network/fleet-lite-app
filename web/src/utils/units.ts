import { PrefsService, UnitSystem } from '../services/prefs-service.ts';

/**
 * Telemetry-api returns SI units (km/h, km, °C, V, %). The UI wants
 * units-appropriate display values. Each formatter takes the *raw* SI value
 * (or undefined if missing) and the current preference; returns
 * `{value, unit}` for the UI to slot in.
 */

export interface FormattedValue {
    value: string;
    unit: string;
}

function currentUnits(): UnitSystem {
    return PrefsService.getInstance().getUnits();
}

function fmtNum(n: number, decimals = 0): string {
    if (Number.isNaN(n) || !Number.isFinite(n)) return '—';
    if (Number.isInteger(n) || decimals === 0) {
        return Math.round(n).toLocaleString();
    }
    return n.toLocaleString(undefined, { minimumFractionDigits: decimals, maximumFractionDigits: decimals });
}

/** Distance: telemetry gives km. */
export function formatDistance(km: number | undefined, decimals = 0): FormattedValue {
    if (km == null) return { value: '—', unit: '' };
    if (currentUnits() === 'metric') return { value: fmtNum(km, decimals), unit: 'km' };
    return { value: fmtNum(km * 0.621371, decimals), unit: 'mi' };
}

/** Speed: telemetry gives km/h. */
export function formatSpeed(kmh: number | undefined, decimals = 0): FormattedValue {
    if (kmh == null) return { value: '—', unit: '' };
    if (currentUnits() === 'metric') return { value: fmtNum(kmh, decimals), unit: 'km/h' };
    return { value: fmtNum(kmh * 0.621371, decimals), unit: 'mph' };
}

/** Temperature: telemetry gives °C. */
export function formatTemperature(c: number | undefined, decimals = 0): FormattedValue {
    if (c == null) return { value: '—', unit: '' };
    if (currentUnits() === 'metric') return { value: fmtNum(c, decimals), unit: '°C' };
    return { value: fmtNum(c * 9 / 5 + 32, decimals), unit: '°F' };
}

/** Voltage — unit-system-agnostic, just kept here for symmetry. */
export function formatVoltage(v: number | undefined, decimals = 3): FormattedValue {
    if (v == null) return { value: '—', unit: '' };
    return { value: fmtNum(v, decimals), unit: 'V' };
}

/** Percent — unit-system-agnostic. */
export function formatPercent(p: number | undefined, decimals = 0): FormattedValue {
    if (p == null) return { value: '—', unit: '' };
    return { value: fmtNum(p, decimals), unit: '%' };
}

/** Hours — for utilization-like cards. */
export function formatHours(h: number | undefined, decimals = 1): FormattedValue {
    if (h == null) return { value: '—', unit: '' };
    return { value: fmtNum(h, decimals), unit: 'h' };
}

/** Friendly label for the preference itself. */
export function unitsLabel(u: UnitSystem): string {
    return u === 'metric' ? 'Metric' : 'Imperial';
}
