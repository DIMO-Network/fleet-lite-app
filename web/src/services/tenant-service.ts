import { ApiService } from './api-service.ts';

export interface Tenant {
    id: string;
    name: string;
    role?: string;
}

interface TenantsResponse {
    tenants: Tenant[];
}

export interface Member {
    wallet: string;
    role: string;
}

interface MembersResponse {
    members: Member[];
}

export const ROLE_OWNER = 'owner';
export const ROLE_MEMBER = 'member';

/**
 * Tenant state is route-driven: the current tenant id is the first segment of
 * the hash route (`#/:tenantId/...`), so the route is the single source of
 * truth. This service derives the current tenant from the hash and fetches the
 * user's tenant list.
 */
export class TenantService {
    private static instance: TenantService;

    public static getInstance(): TenantService {
        if (!TenantService.instance) {
            TenantService.instance = new TenantService();
        }
        return TenantService.instance;
    }

    /** Current tenant id parsed from the hash route, or '' on /onboard or `/`. */
    public currentTenantId(): string {
        return currentTenantIdFromHash();
    }

    /** `Tenant-Id` header for tenant-scoped API calls (empty when no tenant). */
    public tenantIdHeader(): Record<string, string> {
        const id = this.currentTenantId();
        return id ? { 'Tenant-Id': id } : {};
    }

    /** Hash prefix for building links under the current tenant, e.g. `#/<id>`. */
    public tenantPrefix(): string {
        const id = this.currentTenantId();
        return id ? `#/${id}` : '#';
    }

    /** GET /tenants — the tenants the logged-in wallet belongs to. */
    public async fetchTenants(): Promise<Tenant[]> {
        const res = await ApiService.getInstance().get<TenantsResponse>('/tenants');
        return res.tenants ?? [];
    }

    /** GET /tenants/:id/members — every wallet that belongs to the tenant. */
    public async fetchMembers(tenantId: string): Promise<Member[]> {
        const res = await ApiService.getInstance().get<MembersResponse>(
            `/tenants/${encodeURIComponent(tenantId)}/members`,
        );
        return res.members ?? [];
    }

    /** POST /tenants/:id/members — owner-only; add a wallet to the tenant. */
    public async addMember(tenantId: string, wallet: string, role: string = ROLE_MEMBER): Promise<void> {
        await ApiService.getInstance().post(
            `/tenants/${encodeURIComponent(tenantId)}/members`,
            { wallet, role },
        );
    }

    /** DELETE /tenants/:id/members/:wallet — owner-only; remove a wallet. */
    public async removeMember(tenantId: string, wallet: string): Promise<void> {
        await ApiService.getInstance().delete(
            `/tenants/${encodeURIComponent(tenantId)}/members/${encodeURIComponent(wallet)}`,
        );
    }
}

/** Parse the current tenant id from `location.hash` (`#/:tenantId/...`). */
export function currentTenantIdFromHash(): string {
    const path = location.hash.slice(1); // drop leading '#'
    const seg = path.split('/').filter(Boolean);
    if (seg.length === 0) return '';
    if (seg[0] === 'onboard') return '';
    return seg[0];
}
