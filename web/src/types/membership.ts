// A membership is the commercial record for one vehicle: a term the customer
// has paid for, which their operator can move to a different vehicle if this
// one is discontinued.
//
// It is NOT the same thing as having the vehicle. Whether a vehicle is in this
// fleet at all is the operator's assignment; the membership is whether it is
// paid for, and until when. When the operator has enforcement turned on, only
// vehicles with an active membership are returned to this app at all — so a
// vehicle missing from the fleet and a membership missing from this list are
// two views of the same fact.
//
// Owned by fleet-tenancy-api. This app only ever reads it: memberships are
// bought and administered through the operator, never here.

export type MembershipStatus = 'active' | 'expiring_soon' | 'expired' | 'canceled';

export interface Membership {
    id: string;
    vehicleTokenId: number;
    termMonths: number;
    startsAt: string;
    expiresAt: string;
    canceledAt: string | null;
    status: MembershipStatus;
    // Filled in from the fleet list where the vehicle is still visible. A
    // membership whose vehicle this app cannot see leaves these null rather
    // than hiding the row — the customer paid for it either way, and silently
    // dropping it is how a support call starts.
    vin: string | null;
    make: string | null;
    model: string | null;
    year: number | null;
}

export interface MembershipsResponse {
    // Whether the operator is hiding this fleet's unmembered vehicles right now.
    // Read from the same call as the list so the page never has to reason about
    // two answers that could disagree.
    enforced: boolean;
    memberships: Membership[];
}

// What the page renders from. `available` is false when the backend does not
// serve memberships yet, which is a different situation from "you have none"
// and has to read differently on screen.
export interface MembershipsView extends MembershipsResponse {
    available: boolean;
}
