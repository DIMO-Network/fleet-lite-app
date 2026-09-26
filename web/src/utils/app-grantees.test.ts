import { afterEach, describe, expect, it, vi } from 'vitest';

const post = vi.fn();
vi.mock('../services/api-service.ts', () => ({ ApiService: { getInstance: () => ({ post }) } }));

const { lookupAppGrantees } = await import('./app-grantees.ts');

const APP = '0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB';
const WALLET = '0xDE9651e2b884f19e7F4a45F23387114c3D5757EE';

describe('lookupAppGrantees', () => {
    afterEach(() => post.mockReset());

    it('names the grantees that are developer licenses and leaves wallets out', async () => {
        // The shape identity-api returns: a license for g0, null (with a
        // NOT_FOUND error alongside) for a plain wallet.
        post.mockResolvedValue({
            data: { g0: { tokenId: 163, alias: 'dimo' }, g1: null },
            errors: [{ message: `No developer license with client id ${WALLET}.`, path: ['g1'] }],
        });
        const apps = await lookupAppGrantees([APP, WALLET]);
        expect([...apps.entries()]).toEqual([[APP.toLowerCase(), 'dimo (#163)']]);
        expect(post.mock.calls[0][1].query).toContain(`g1: developerLicense(by: { clientId: "${WALLET}" })`);
    });

    it('falls back to the token id when a license has no alias', async () => {
        post.mockResolvedValue({ data: { g0: { tokenId: 472, alias: null } } });
        expect((await lookupAppGrantees([APP])).get(APP.toLowerCase())).toBe('#472');
    });

    it('never puts anything but an address into the query', async () => {
        post.mockResolvedValue({ data: {} });
        await lookupAppGrantees(['0x" } evil: x { "', APP]);
        expect(post.mock.calls[0][1].query).not.toContain('evil');
    });

    it('asks nothing for no grantees, and rejects a response without data', async () => {
        expect((await lookupAppGrantees([])).size).toBe(0);
        expect(post).not.toHaveBeenCalled();
        post.mockResolvedValue({ errors: [{ message: 'down' }] });
        await expect(lookupAppGrantees([APP])).rejects.toThrow();
    });
});
