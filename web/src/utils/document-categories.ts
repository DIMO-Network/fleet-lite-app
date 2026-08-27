/**
 * Map of canonical CE types → friendly UI labels.
 * Source of truth for both upload (category dropdown) and list (group headers).
 */
export const CE_TYPE_TO_LABEL: Record<string, string> = {
    // extract-api's taxonomy is hierarchical: it classifies at the coarse
    // parent when it cannot pin the leaf, so the parent needs a label of its
    // own. Missing these is what put real documents under "Other".
    'dimo.document.vehicle.service':         'Service & parts',
    'dimo.document.vehicle.service.invoice': 'Service & parts',
    'dimo.document.vehicle.maintenance':     'Service & parts',
    'dimo.document.vehicle.insurance':       'Insurance',
    'dimo.document.vehicle.registration':    'Registration',
    'dimo.document.vehicle.inspection':      'Inspection',
    'dimo.document.vehicle.title':           'Title',
    'dimo.document.vehicle.finance':         'Finance',
    'dimo.document.vehicle.ownership':       'Ownership',
    'dimo.document.vehicle.regulatory':      'Regulatory',
    'dimo.document.vehicle.regulatory.other':'Regulatory',
    'dimo.document.vehicle.note':            'Note',
    'dimo.document.vehicle.condition':       'Condition',
    // Expenses are their own thing, not leftovers. Fuel is an expense with a
    // category of its own; both carry the same payload.
    'dimo.document.vehicle.expense':         'Expenses',
    'dimo.document.vehicle.fuel':            'Fuel',
    'dimo.document.unknown':                 'Uncategorized',
};

/**
 * Choices shown in the upload modal's category dropdown.
 * Order is the same order they'll appear to the user.
 */
export const UPLOAD_CATEGORIES: Array<{ ceType: string; label: string }> = [
    { ceType: 'dimo.document.vehicle.insurance',       label: 'Insurance' },
    { ceType: 'dimo.document.vehicle.registration',    label: 'Registration' },
    { ceType: 'dimo.document.vehicle.inspection',      label: 'Inspection' },
    { ceType: 'dimo.document.vehicle.service.invoice', label: 'Service & parts' },
    { ceType: 'dimo.document.vehicle.fuel',            label: 'Fuel' },
    { ceType: 'dimo.document.vehicle.title',           label: 'Title' },
    { ceType: 'dimo.document.vehicle.finance',         label: 'Finance' },
    { ceType: 'dimo.document.vehicle.regulatory.other',label: 'Other regulatory' },
    { ceType: 'dimo.document.unknown',                 label: 'Other' },
];

/**
 * CE types we expect a vehicle owner to keep on file. Used by the glovebox
 * "Missing" rail — anything in this set that the vehicle does not have an
 * attestation for shows as a prompt to add it.
 */
export const EXPECTED_CE_TYPES: string[] = [
    'dimo.document.vehicle.insurance',
    'dimo.document.vehicle.registration',
    'dimo.document.vehicle.inspection',
];

/**
 * CE types eligible to carry a cost amount, and to count toward TCO operating
 * costs. Mirrors the backend's CostEligibleCETypes (api/internal/service/tco_service.go).
 * Everything except Note/Condition/Title.
 */
export const COST_ELIGIBLE_CATEGORIES = new Set<string>([
    'dimo.document.vehicle.service.invoice',
    'dimo.document.vehicle.insurance',
    'dimo.document.vehicle.registration',
    'dimo.document.vehicle.inspection',
    'dimo.document.vehicle.finance',
    'dimo.document.vehicle.regulatory.other',
    'dimo.document.vehicle.maintenance',
    'dimo.document.vehicle.expense',
    'dimo.document.vehicle.fuel',
]);

export function categoryLabel(ceType: string): string {
    return CE_TYPE_TO_LABEL[ceType] ?? 'Other';
}
