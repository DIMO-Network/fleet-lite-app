import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const redirectUris = vi.fn<(clientId: string) => Promise<string[]>>();
vi.mock('../services/license-service.ts', () => ({
    LicenseService: { getInstance: () => ({ redirectUris }) },
}));

const { resolveGrantRedirect } = await import('./grant-link.ts');

const APP = '0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB';
const FLEET = '0x2222222222222222222222222222222222222222';

describe('resolveGrantRedirect', () => {
    beforeEach(() => vi.stubGlobal('location', { origin: 'https://fleets.dimo.co' }));
    afterEach(() => {
        vi.unstubAllGlobals();
        redirectUris.mockReset();
    });

    it("returns a fleet license's login page when that is all it registered", async () => {
        redirectUris.mockResolvedValue(['https://fleets.dimo.co/login.html']);
        await expect(resolveGrantRedirect(FLEET, APP)).resolves.toBe('https://fleets.dimo.co/login.html');
    });

    it("never returns this app's own license to the login page", async () => {
        redirectUris.mockResolvedValue(['https://fleets.dimo.co/login.html']);
        await expect(resolveGrantRedirect(APP.toLowerCase(), APP)).resolves.toBeNull();
    });

    it('falls back to the root when the redirect list cannot be read', async () => {
        redirectUris.mockRejectedValue(new Error('identity-api down'));
        await expect(resolveGrantRedirect(FLEET, APP)).resolves.toBe('https://fleets.dimo.co/');
    });
});
