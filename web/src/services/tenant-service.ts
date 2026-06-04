import { ApiService } from './api-service.ts';

export interface Tenant {
    id: string;
    name: string;
    role?: string;
}

interface TenantsResponse {
    tenants: Tenant[];
}

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
}

/** Parse the current tenant id from `location.hash` (`#/:tenantId/...`). */
export function currentTenantIdFromHash(): string {
    const path = location.hash.slice(1); // drop leading '#'
    const seg = path.split('/').filter(Boolean);
    if (seg.length === 0) return '';
    if (seg[0] === 'onboard') return '';
    return seg[0];
}
