import { ApiService } from './api-service.ts';

/** How long a license's redirect list is reused before it is read again. */
const CACHE_TTL_MS = 60_000;

/**
 * Reads a DIMO developer license's registered redirect URIs from identity-api,
 * so a "Grant permissions" link can return to a URI that license will accept
 * (see pickGrantRedirectUri).
 *
 * Cached per client id for a minute: long enough that moving between vehicles
 * of one fleet doesn't re-query, short enough that an admin who follows the
 * setup hint and registers a URI sees the link on the next load rather than
 * the next page reload. A failed lookup is not cached, so the next banner tries
 * again.
 */
export class LicenseService {
    private static instance: LicenseService;
    private readonly cache = new Map<string, { at: number; uris: Promise<string[]> }>();

    public static getInstance(): LicenseService {
        if (!LicenseService.instance) {
            LicenseService.instance = new LicenseService();
        }
        return LicenseService.instance;
    }

    /** Registered redirect URIs for `clientId`. Rejects when identity-api can't answer. */
    public redirectUris(clientId: string): Promise<string[]> {
        const key = clientId.toLowerCase();
        const hit = this.cache.get(key);
        if (hit && Date.now() - hit.at < CACHE_TTL_MS) return hit.uris;

        const uris = this.load(clientId);
        this.cache.set(key, { at: Date.now(), uris });
        uris.catch(() => {
            if (this.cache.get(key)?.uris === uris) this.cache.delete(key);
        });
        return uris;
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
