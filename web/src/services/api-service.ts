import { isLocalhost } from '../utils/utils.ts';
import { logout } from '../utils/token.ts';
import { currentTenantIdFromHash } from './tenant-service.ts';

/**
 * Singleton HTTP client. In dev the frontend lives on :3009 and the API on
 * :8084 (cross-origin), so we hard-code the dev base URL. In production the
 * Go binary serves the SPA from its own dist/, so same-origin requests work.
 */
export class ApiService {
    private static instance: ApiService;
    private readonly baseUrl: string;
    private static readonly DEFAULT_LOCAL_DEV_URL = 'https://local-fleet-lite.dimo.org:8084';

    private constructor() {
        this.baseUrl = isLocalhost() ? ApiService.DEFAULT_LOCAL_DEV_URL : '';
    }

    public static getInstance(): ApiService {
        if (!ApiService.instance) {
            ApiService.instance = new ApiService();
        }
        return ApiService.instance;
    }

    public getApiBaseUrl(): string {
        return this.baseUrl;
    }

    private buildUrl(endpoint: string): string {
        return endpoint.startsWith('/') ? `${this.baseUrl}${endpoint}` : endpoint;
    }

    private authHeader(auth: boolean): Record<string, string> {
        if (!auth) return {};
        const token = localStorage.getItem('token');
        const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
        // Attach the current tenant (from the route) so tenant-scoped endpoints
        // resolve the right developer license. Harmless on non-tenant endpoints.
        const tenantId = currentTenantIdFromHash();
        if (tenantId) headers['Tenant-Id'] = tenantId;
        return headers;
    }

    private handle401IfAuthed(status: number, auth: boolean, body: string): void {
        // Only bounce on a genuine auth failure: a 401, or the JWT middleware's
        // 400 "missing or malformed JWT". A plain 400 (e.g. "Tenant-Id header is
        // required") must NOT log the user out — that's a routing/state issue,
        // not a dead token.
        if (!auth) return;
        if (status === 401 || (status === 400 && /jwt/i.test(body))) {
            logout();
        }
    }

    public async get<T>(endpoint: string, auth: boolean = true): Promise<T> {
        const res = await fetch(this.buildUrl(endpoint), {
            method: 'GET',
            headers: { 'Content-Type': 'application/json', ...this.authHeader(auth) },
        });
        if (!res.ok) {
            const text = await safeText(res);
            this.handle401IfAuthed(res.status, auth, text);
            throw new ApiError(res.status, text);
        }
        return res.json();
    }

    public async post<T>(endpoint: string, body: unknown, auth: boolean = true): Promise<T> {
        const res = await fetch(this.buildUrl(endpoint), {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', ...this.authHeader(auth) },
            body: JSON.stringify(body),
        });
        if (!res.ok) {
            const text = await safeText(res);
            this.handle401IfAuthed(res.status, auth, text);
            throw new ApiError(res.status, text);
        }
        return res.json();
    }

    public async patch<T>(endpoint: string, body: unknown, auth: boolean = true): Promise<T> {
        const res = await fetch(this.buildUrl(endpoint), {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json', ...this.authHeader(auth) },
            body: JSON.stringify(body),
        });
        if (!res.ok) {
            const text = await safeText(res);
            this.handle401IfAuthed(res.status, auth, text);
            throw new ApiError(res.status, text);
        }
        return parseJson<T>(res);
    }

    public async put<T>(endpoint: string, body: unknown, auth: boolean = true): Promise<T> {
        const res = await fetch(this.buildUrl(endpoint), {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json', ...this.authHeader(auth) },
            body: JSON.stringify(body),
        });
        if (!res.ok) {
            const text = await safeText(res);
            this.handle401IfAuthed(res.status, auth, text);
            throw new ApiError(res.status, text);
        }
        return parseJson<T>(res);
    }

    public async delete<T>(endpoint: string, auth: boolean = true): Promise<T> {
        const res = await fetch(this.buildUrl(endpoint), {
            method: 'DELETE',
            headers: { 'Content-Type': 'application/json', ...this.authHeader(auth) },
        });
        if (!res.ok) {
            const text = await safeText(res);
            this.handle401IfAuthed(res.status, auth, text);
            throw new ApiError(res.status, text);
        }
        return parseJson<T>(res);
    }
}

export class ApiError extends Error {
    constructor(public status: number, message: string) {
        super(message);
    }
}

async function safeText(res: Response): Promise<string> {
    try {
        return await res.text();
    } catch {
        return res.statusText;
    }
}

/**
 * Parse a JSON response body, tolerating an empty body (e.g. a 204 No Content
 * from DELETE) by resolving to undefined instead of throwing on `res.json()`.
 */
async function parseJson<T>(res: Response): Promise<T> {
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    if (!text) return undefined as T;
    return JSON.parse(text) as T;
}
