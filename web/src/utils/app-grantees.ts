import { ApiService } from '../services/api-service.ts';

/**
 * Which grantees are developer licenses — other apps — keyed by lowercased
 * address, with a name to show ("alias (#tokenId)").
 *
 * One batched identity-api query, one alias per grantee. A grantee that is a
 * person's wallet has no license: identity-api answers that alias with null
 * and a NOT_FOUND error beside the data, which is the expected answer here,
 * not a failure. Only a response with no data at all rejects.
 */
export async function lookupAppGrantees(grantees: string[]): Promise<Map<string, string>> {
    const valid = grantees.filter((g) => /^0x[0-9a-fA-F]{40}$/.test(g));
    const apps = new Map<string, string>();
    if (valid.length === 0) return apps;
    const query = `{ ${valid
        .map((g, i) => `g${i}: developerLicense(by: { clientId: "${g}" }) { tokenId alias }`)
        .join(' ')} }`;
    const res = await ApiService.getInstance().post<{
        data?: Record<string, { tokenId: number; alias?: string | null } | null>;
    }>('/identity/proxy', { query });
    if (!res.data) throw new Error('developer license lookup failed');
    valid.forEach((g, i) => {
        const lic = res.data?.[`g${i}`];
        if (lic) apps.set(g.toLowerCase(), lic.alias ? `${lic.alias} (#${lic.tokenId})` : `#${lic.tokenId}`);
    });
    return apps;
}
