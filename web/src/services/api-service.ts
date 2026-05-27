import { isLocalhost } from '../utils/utils.ts';
import { logout } from '../utils/token.ts';

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
        return token ? { Authorization: `Bearer ${token}` } : {};
    }

    private handle401IfAuthed(status: number, auth: boolean): void {
        // The api treats both unsigned JWT and missing-header as 401 (sometimes
        // 400 from the middleware before our handler sees it). Either way,
        // clear local auth state and bounce — keeping a dead token causes
        // infinite redirect loops on the next page load.
        if (auth && (status === 401 || status === 400)) {
            logout();
        }
    }

    public async get<T>(endpoint: string, auth: boolean = true): Promise<T> {
        const res = await fetch(this.buildUrl(endpoint), {
            method: 'GET',
            headers: { 'Content-Type': 'application/json', ...this.authHeader(auth) },
        });
        if (!res.ok) {
            this.handle401IfAuthed(res.status, auth);
            throw new ApiError(res.status, await safeText(res));
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
            this.handle401IfAuthed(res.status, auth);
            throw new ApiError(res.status, await safeText(res));
        }
        return res.json();
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
