import { ApiService } from './api-service.ts';
import { getLocale } from '../localization.ts';

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
    /** Email shown in Members (DIMO's JWT has no name; this comes from the OAuth redirect). */
    email?: string;
    /** ISO timestamp of the member's last recorded login, if any. */
    lastLoginAt?: string;
}

interface MembersResponse {
    members: Member[];
}

export interface Invitation {
    id: string;
    email: string;
    role: string;
    /** pending | accepted | revoked */
    status: string;
    /** Wallet of the owner who issued the invite. */
    invitedBy?: string;
    createdAt: string;
    expiresAt: string;
    acceptedAt?: string;
}

interface InvitationsResponse {
    invitations: Invitation[];
}

/** Create/resend responses carry whether the email actually dispatched. */
export interface InvitationResult extends Invitation {
    emailSent?: boolean;
}

interface ResendResult {
    ok: boolean;
    emailSent?: boolean;
}

interface AcceptInviteResponse {
    ok: boolean;
    tenantId: string;
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

    /**
     * POST /tenants/:id/login — record this session's login: bumps the member's
     * last_login_at (drives the group-sync cron tiering) and stores the email
     * from the OAuth redirect (DIMO's JWT carries no name/email). Best-effort.
     */
    public async recordLogin(tenantId: string): Promise<void> {
        const email = localStorage.getItem('email') ?? '';
        await ApiService.getInstance().post(
            `/tenants/${encodeURIComponent(tenantId)}/login`,
            { email },
        );
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

    /** GET /tenants/:id/invitations — every invitation for the tenant. */
    public async fetchInvitations(tenantId: string): Promise<Invitation[]> {
        const res = await ApiService.getInstance().get<InvitationsResponse>(
            `/tenants/${encodeURIComponent(tenantId)}/invitations`,
        );
        return res.invitations ?? [];
    }

    /**
     * POST /tenants/:id/invitations — owner-only; email an invite + accept link.
     * Sends the active UI locale so the email goes out in the inviter's language.
     */
    public async createInvitation(
        tenantId: string,
        email: string,
        role: string = ROLE_MEMBER,
        locale: string = getLocale(),
    ): Promise<InvitationResult> {
        return ApiService.getInstance().post<InvitationResult>(
            `/tenants/${encodeURIComponent(tenantId)}/invitations`,
            { email, role, locale },
        );
    }

    /** DELETE /tenants/:id/invitations/:invId — owner-only; revoke a pending invite. */
    public async revokeInvitation(tenantId: string, invitationId: string): Promise<void> {
        await ApiService.getInstance().delete(
            `/tenants/${encodeURIComponent(tenantId)}/invitations/${encodeURIComponent(invitationId)}`,
        );
    }

    /**
     * POST /tenants/:id/invitations/:invId/resend — owner-only; re-send a pending
     * invite. Re-sends in the inviter's current UI locale.
     */
    public async resendInvitation(
        tenantId: string,
        invitationId: string,
        locale: string = getLocale(),
    ): Promise<ResendResult> {
        return ApiService.getInstance().post<ResendResult>(
            `/tenants/${encodeURIComponent(tenantId)}/invitations/${encodeURIComponent(invitationId)}/resend`,
            { locale },
        );
    }

    /**
     * POST /invitations/accept — bind the logged-in wallet to the tenant the
     * token belongs to. JWT-authenticated (the invitee must be signed in); the
     * token is the authorization and resolves the tenant. Returns the tenant id.
     */
    public async acceptInvitation(token: string): Promise<string> {
        const res = await ApiService.getInstance().post<AcceptInviteResponse>(
            '/invitations/accept',
            { token },
        );
        return res.tenantId;
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
