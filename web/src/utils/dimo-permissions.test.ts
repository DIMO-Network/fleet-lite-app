import { describe, expect, it } from 'vitest';
import { buildShareVehiclesUrl, DIMO_VEHICLE_FILE_AGREEMENTS, pickGrantRedirectUri, sameClientId } from './dimo-permissions.ts';

const ORIGIN = 'https://fleets.dimo.co';
const safe = { loginPageSafe: true };
const unsafe = { loginPageSafe: false };

describe('pickGrantRedirectUri', () => {
    it('prefers the root, then /index.html, then the login page', () => {
        const all = [`${ORIGIN}/login.html`, `${ORIGIN}/index.html`, `${ORIGIN}/`];
        expect(pickGrantRedirectUri(all, ORIGIN, safe)).toBe(`${ORIGIN}/`);
        expect(pickGrantRedirectUri(all.slice(0, 2), ORIGIN, safe)).toBe(`${ORIGIN}/index.html`);
        expect(pickGrantRedirectUri([`${ORIGIN}/login.html`], ORIGIN, safe)).toBe(`${ORIGIN}/login.html`);
    });

    it('never uses the login page when the grant license is this app\'s own', () => {
        expect(pickGrantRedirectUri([`${ORIGIN}/login.html`], ORIGIN, unsafe)).toBeNull();
        expect(pickGrantRedirectUri([`${ORIGIN}/login.html`, `${ORIGIN}/`], ORIGIN, unsafe)).toBe(`${ORIGIN}/`);
    });

    it('never returns to the invite page, which keeps any token as an invite', () => {
        expect(pickGrantRedirectUri([`${ORIGIN}/accept-invite.html`], ORIGIN, safe)).toBeNull();
    });

    it('only counts this exact origin', () => {
        expect(pickGrantRedirectUri([
            'http://fleets.dimo.co/',
            'https://fleets.dimo.co.evil.example/',
            'https://evil.example/?next=https://fleets.dimo.co/',
            'not a url',
        ], ORIGIN, safe)).toBeNull();
    });

    it('returns the URI exactly as registered, since DIMO compares exactly', () => {
        expect(pickGrantRedirectUri([`${ORIGIN}/?from=dimo`], ORIGIN, safe)).toBe(`${ORIGIN}/?from=dimo`);
        expect(pickGrantRedirectUri([ORIGIN], ORIGIN, safe)).toBe(ORIGIN);
    });
});

describe('sameClientId', () => {
    it('compares addresses case-insensitively and never matches empty', () => {
        expect(sameClientId('0xAbC', '0xabc')).toBe(true);
        expect(sameClientId('0xabc', '0xabd')).toBe(false);
        expect(sameClientId('', '')).toBe(false);
    });
});

describe('buildShareVehiclesUrl', () => {
    it('sends the chosen redirect, the file agreements and each vehicle', () => {
        const url = new URL(buildShareVehiclesUrl({
            loginUrl: 'https://login.dimo.org',
            clientId: '0x2222222222222222222222222222222222222222',
            redirectUri: `${ORIGIN}/`,
            vehicles: [101, '202'],
        }));
        expect(url.searchParams.get('redirectUri')).toBe(`${ORIGIN}/`);
        expect(url.searchParams.get('entryState')).toBe('VEHICLE_MANAGER');
        expect(url.searchParams.get('permissions')).toBe('11111111');
        expect(JSON.parse(url.searchParams.get('cloudEvent') ?? '')).toEqual(DIMO_VEHICLE_FILE_AGREEMENTS);
        expect(url.searchParams.getAll('vehicles')).toEqual(['101', '202']);
    });
});
