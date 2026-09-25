import { ApiService } from './api-service.ts';

/**
 * Reads a DIMO developer license's registered redirect URIs from identity-api,
 * so a "Grant permissions" link can return to a URI that license will accept
 * (see pickGrantRedirectUri).
 *
 * Cached per client id for the page's lifetime: redirect lists change through
 * an on-chain transaction in the DIMO console, not while somebody is looking at
 * a banner. A failed lookup is not cached, so the next banner tries again.
 */
export class LicenseService {
    private static instance: LicenseService;
    private readonly cache = new Map<string, Promise<string[]>>();

    public static getInstance(): LicenseService {
        if (!LicenseService.instance) {
            LicenseService.instance = new LicenseService();
        }
        return LicenseService.instance;
    }

    /** Registered redirect URIs for `clientId`. Rejects when identity-api can't answer. */
    public redirectUris(clientId: string): Promise<string[]> {
        const key = clientId.toLowerCase();
        let pending = this.cache.get(key);
        if (!pending) {
            pending = this.load(clientId);
            pending.catch(() => this.cache.delete(key));
            this.cache.set(key, pending);
        }
        return pending;
    }

    private async load(clientId: string): Promise<string[]> {
        if (!/^0x[0-9a-fA-F]{40}$/.test(clientId)) throw new Error('not a client id');
        const query = `{
            developerLicense(by: { clientId: "${clientId}" }) {
                redirectURIs(first: 100) { nodes { uri } }
            }
        }`;
        const res = await ApiService.getInstance().post<{
            data?: { developerLicense?: { redirectURIs?: { nodes?: { uri: string }[] } } };
        }>('/identity/proxy', { query }, false);
        const nodes = res.data?.developerLicense?.redirectURIs?.nodes;
        if (!nodes) throw new Error('license not found');
        return nodes.map((n) => n.uri);
    }
}
