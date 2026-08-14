import { ApiError, ApiService } from './api-service.ts';
import { FleetCache } from './fleet-cache.ts';
import { currentTenantIdFromHash } from './tenant-service.ts';
import { Membership, MembershipsResponse, MembershipsView } from '../types/membership.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';

// Read-only client for this tenant's vehicle memberships.
//
// THE BACKEND DOES NOT SERVE THIS YET. The page is built first, deliberately —
// see fleet-tenancy-api docs/plans/02-vehicle-memberships.md, which sequences
// the UI ahead of the schema so the shapes are settled by using them. Until
// GET /memberships exists, this reports `available: false` and the page says so
// instead of showing an error the customer might report.
//
// MOCK MODE. localStorage.membershipsMock = 'true' serves fixtures instead, so
// the screen can be developed and demoed before the endpoint lands. Named to
// match b2b-fleet-mgr-app's `tenancyStub` flag, which does the same job there.
// Delete this together with the flag once the read is live.

const MOCK_FLAG_KEY = 'membershipsMock';

export class MembershipService {
    private static instance: MembershipService;

    public static getInstance(): MembershipService {
        if (!MembershipService.instance) {
            MembershipService.instance = new MembershipService();
        }
        return MembershipService.instance;
    }

    public isMocked(): boolean {
        return localStorage.getItem(MOCK_FLAG_KEY) === 'true';
    }

    /**
     * GET /memberships, joined to the fleet list for vehicle descriptions.
     *
     * A 404 means the route does not exist on this deployment yet, which is
     * reported as unavailable rather than thrown: it is the expected state
     * until the backend steps of the plan ship. Every other failure still
     * throws, because those are real and the page should say so.
     */
    public async list(): Promise<MembershipsView> {
        if (this.isMocked()) return this.mock();

        let res: MembershipsResponse;
        try {
            res = await ApiService.getInstance().get<MembershipsResponse>('/memberships');
        } catch (e) {
            if (e instanceof ApiError && e.status === 404) {
                return { available: false, enforced: false, memberships: [] };
            }
            throw e;
        }

        // Same funnel as /me/access: this response also carries the enforcement
        // state, and hearing about a flip from either source must invalidate
        // cached vehicle lists.
        FleetCache.noteMembershipsEnforced(currentTenantIdFromHash(), res.enforced ?? false);

        return {
            available: true,
            enforced: res.enforced ?? false,
            memberships: await this.hydrate(res.memberships ?? []),
        };
    }

    /**
     * Fills in VIN/make/model from the fleet list.
     *
     * Best-effort by design: a membership whose vehicle this app cannot see —
     * because enforcement is on and it has lapsed, or because the operator
     * un-assigned the vehicle — still renders, with the description blank. The
     * customer paid for it either way, and dropping the row would turn a
     * visible fact into an invisible one.
     */
    private async hydrate(memberships: Membership[]): Promise<Membership[]> {
        if (memberships.length === 0) return [];

        let byToken = new Map<number, Vehicle>();
        try {
            const fleet = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            byToken = new Map((fleet.vehicles ?? []).map((v) => [v.tokenId, v]));
        } catch {
            // No descriptions, rather than no page.
        }

        return memberships.map((m) => {
            const v = byToken.get(m.vehicleTokenId);
            return {
                ...m,
                vin: v?.vin ?? null,
                make: v?.definition?.make ?? null,
                model: v?.definition?.model ?? null,
                year: v?.definition?.year ?? null,
            };
        });
    }

    // FIXTURES — mock mode only.
    //
    // A spread of statuses, so the page's states are all reachable without
    // arranging data: one comfortably active, one inside the 30-day warning,
    // and one lapsed (which, with enforcement on, is a vehicle the customer can
    // no longer see — the case the page exists to explain).
    private async mock(): Promise<MembershipsView> {
        const at = (days: number) => new Date(Date.now() + days * 86400000).toISOString();
        const rows: Membership[] = [
            {
                id: 'mock-1', vehicleTokenId: 3681, termMonths: 12,
                startsAt: at(-155), expiresAt: at(210), canceledAt: null, status: 'active',
                vin: null, make: null, model: null, year: null,
            },
            {
                id: 'mock-2', vehicleTokenId: 3682, termMonths: 12,
                startsAt: at(-347), expiresAt: at(18), canceledAt: null, status: 'expiring_soon',
                vin: null, make: null, model: null, year: null,
            },
            {
                id: 'mock-3', vehicleTokenId: 3683, termMonths: 1,
                startsAt: at(-35), expiresAt: at(-5), canceledAt: null, status: 'expired',
                vin: null, make: null, model: null, year: null,
            },
        ];
        return { available: true, enforced: true, memberships: await this.hydrate(rows) };
    }
}
