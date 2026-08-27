/**
 * Turns a document's extracted fields into the two lines a list row shows.
 *
 * This mirrors `documentFormatters.ts` in dimo-driver — same fields, same
 * precedence, same fallbacks — because both apps render the same CloudEvents
 * and a document should not read differently depending on which one you opened.
 * When the two disagree, dimo-driver is right.
 *
 * Everything here is defensive: a document whose metadata is missing, partly
 * filled, or shaped unexpectedly still renders. Losing the vendor name off a
 * row is a blemish; throwing takes the whole glovebox down.
 */
import { categoryLabel } from './document-categories.ts';

export interface DocumentSummary {
    /** The row's first line. Never empty. */
    title: string;
    /** Supporting detail, or '' when the document carries none. */
    subtitle: string;
}

type Fields = Record<string, unknown>;

/**
 * Digs the extracted fields out of a CE's `data`.
 *
 * Three shapes are in circulation. dimo-app-backend attests the extraction's
 * fields as the payload directly, so `data` IS the field bag — that is the
 * common case and the one the old code missed, which is why every
 * mobile-uploaded document fell back to its category name. The nested forms
 * come from extract-api responses stored whole.
 */
function extractFields(data: unknown): Fields {
    if (!data || typeof data !== 'object') return {};
    const d = data as Record<string, unknown>;
    const nested = d.data as Record<string, unknown> | undefined;
    if (nested && typeof nested === 'object' && nested.fields && typeof nested.fields === 'object') {
        return nested.fields as Fields;
    }
    if (d.fields && typeof d.fields === 'object') return d.fields as Fields;
    return d as Fields;
}

function str(v: unknown): string | undefined {
    if (typeof v !== 'string') return undefined;
    const t = v.trim();
    return t.length > 0 ? t : undefined;
}

function num(v: unknown): number | undefined {
    if (typeof v === 'number' && Number.isFinite(v)) return v;
    if (typeof v === 'string') {
        const n = Number(v.replace(/[^0-9.-]/g, ''));
        if (Number.isFinite(n)) return n;
    }
    return undefined;
}

function money(amount: unknown, currency: unknown): string | undefined {
    const n = num(amount);
    if (n === undefined) return undefined;
    const code = str(currency) ?? 'USD';
    try {
        return new Intl.NumberFormat(undefined, { style: 'currency', currency: code }).format(n);
    } catch {
        // An unrecognised currency code must not cost us the amount.
        return `${n.toLocaleString()} ${code}`;
    }
}

function odometer(v: unknown): string | undefined {
    const n = num(v);
    return n === undefined ? undefined : `${n.toLocaleString()} mi`;
}

/** Renders an ISO-ish date as a short local date, or undefined if unparseable. */
function date(v: unknown, prefix: string): string | undefined {
    const s = str(v);
    if (!s) return undefined;
    const d = new Date(s);
    if (Number.isNaN(d.getTime())) return undefined;
    return `${prefix} ${d.toLocaleDateString()}`;
}

/** snake_case / kebab-case → Title Case, for machine-written enum values. */
function humanize(v: unknown): string | undefined {
    const s = str(v);
    if (!s) return undefined;
    return s
        .replace(/[_-]+/g, ' ')
        .replace(/\s+/g, ' ')
        .trim()
        .replace(/\b\w/g, (c) => c.toUpperCase());
}

function join(parts: Array<string | undefined>): string {
    return parts.filter((p): p is string => !!p).join(' · ');
}

/**
 * `summary` is the agent's own one-line description and the best title we have
 * whenever it is present — it is what dimo-driver leads with for every
 * agent-written type, and extract-api fills it too.
 */
function summaryOf(f: Fields): string | undefined {
    return str(f.summary) ?? str(f.description) ?? str(f.name);
}

export function documentSummary(ceType: string, data: unknown): DocumentSummary {
    const fallback = categoryLabel(ceType);
    try {
        const f = extractFields(data);
        const { title, subtitle } = byType(ceType, f);
        return {
            title: title ?? summaryOf(f) ?? fallback,
            subtitle: subtitle ?? '',
        };
    } catch {
        // Matches dimo-driver: an unexpected metadata shape degrades to the
        // plain category rather than breaking the list.
        return { title: fallback, subtitle: '' };
    }
}

function byType(ceType: string, f: Fields): { title?: string; subtitle?: string } {
    switch (ceType) {
        // extract-api's taxonomy is hierarchical — it classifies at the coarse
        // parent when it cannot pin the leaf. Both go to the same renderer.
        case 'dimo.document.vehicle.service':
        case 'dimo.document.vehicle.service.invoice':
            return {
                title: summaryOf(f),
                subtitle: join([
                    str(f.providerName),
                    money(f.totalCost, f.currency),
                    odometer(f.odometerReading),
                ]),
            };
        case 'dimo.document.vehicle.insurance':
            return {
                title: str(f.insurerName),
                subtitle: join([
                    str(f.coverageType),
                    str(f.policyNumber),
                    date(f.expirationDate, 'expires'),
                ]),
            };
        case 'dimo.document.vehicle.registration':
            return {
                title: str(f.plateNumber),
                subtitle: join([str(f.issuingAuthority), date(f.expirationDate, 'expires')]),
            };
        case 'dimo.document.vehicle.title':
            return {
                title: str(f.vin),
                subtitle: join([str(f.issuingAuthority), odometer(f.odometer)]),
            };
        case 'dimo.document.vehicle.inspection':
            return {
                title: humanize(f.result),
                subtitle: join([str(f.inspectionStation), date(f.nextInspectionDue, 'next')]),
            };
        case 'dimo.document.vehicle.regulatory':
        case 'dimo.document.vehicle.regulatory.other':
            return {
                title: humanize(f.documentKind),
                subtitle: join([
                    str(f.vendor),
                    money(f.amountDue, f.currency),
                    date(f.expirationDate, 'expires'),
                ]),
            };
        case 'dimo.document.vehicle.finance':
            return financeSummary(f);
        // `ownership` is the parent of title + finance. A lender or a balance
        // means financing paperwork; otherwise it is a title.
        case 'dimo.document.vehicle.ownership':
            return str(f.lender) || num(f.balance) !== undefined
                ? financeSummary(f)
                : {
                      title: str(f.vin),
                      subtitle: join([str(f.issuingAuthority), odometer(f.odometer)]),
                  };
        // Fuel is an expense with its own category; same payload shape.
        case 'dimo.document.vehicle.expense':
        case 'dimo.document.vehicle.fuel':
            return {
                title: summaryOf(f),
                subtitle: join([
                    str(f.vendor),
                    money(f.amount, f.currency),
                    str(f.location),
                    odometer(f.mileage),
                ]),
            };
        case 'dimo.document.vehicle.maintenance': {
            const ops = Array.isArray(f.ops)
                ? f.ops.map(humanize).filter((s): s is string => !!s).join(', ')
                : undefined;
            return {
                title: summaryOf(f),
                subtitle: join([ops || undefined, money(f.totalCost, f.currency), odometer(f.mileage)]),
            };
        }
        case 'dimo.document.vehicle.condition':
        case 'dimo.document.vehicle.note':
            return { title: summaryOf(f), subtitle: join([humanize(f.severity), str(f.location)]) };
        default:
            return { title: summaryOf(f), subtitle: '' };
    }
}

function financeSummary(f: Fields): { title?: string; subtitle?: string } {
    const kind = humanize(f.documentKind);
    const lender = str(f.lender);
    return {
        title: join([kind, lender]) || undefined,
        subtitle: join([money(f.balance, f.currency), date(f.dueDate, 'due')]),
    };
}
