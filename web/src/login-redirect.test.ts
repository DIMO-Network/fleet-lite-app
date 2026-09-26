import { describe, expect, it } from 'vitest';
import { classifyLoginRedirect } from './login-redirect.ts';
import { epoch, fakeJwt } from './test/jwt.ts';

const APP = '0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB';
const FLEET = '0x2222222222222222222222222222222222222222';

const query = (params: Record<string, string>) => '?' + new URLSearchParams(params).toString();
const ours = fakeJwt({ aud: APP, ethereum_address: '0xAAA', exp: epoch(3600) });

describe('classifyLoginRedirect', () => {
    it('treats a visit with no token as a plain visit', () => {
        expect(classifyLoginRedirect('', APP)).toEqual({ kind: 'none' });
        expect(classifyLoginRedirect('?utm=mail', APP)).toEqual({ kind: 'none' });
    });

    it('honours logout before anything else', () => {
        expect(classifyLoginRedirect(query({ logout: 'true', token: ours }), APP)).toEqual({ kind: 'logout' });
    });

    it('signs in with a token DIMO issued to this app', () => {
        expect(classifyLoginRedirect(query({ token: ours, email: 'a@b.co' }), APP)).toEqual({
            kind: 'sign-in', token: ours, email: 'a@b.co', emailMissing: false,
        });
    });

    it('asks for an email only when DIMO sent it empty', () => {
        expect(classifyLoginRedirect(query({ token: ours, email: '' }), APP)).toMatchObject({ kind: 'sign-in', emailMissing: true });
        expect(classifyLoginRedirect(query({ token: ours }), APP)).toMatchObject({ kind: 'sign-in', emailMissing: false });
    });

    it("does not sign in with the grantor's token from a fleet grant", () => {
        const grantors = fakeJwt({ aud: FLEET, ethereum_address: '0xOWNER', exp: epoch(3600) });
        expect(classifyLoginRedirect(query({ token: grantors, email: 'owner@fleet.co' }), APP)).toEqual({ kind: 'not-a-sign-in' });
    });

    it('does not store expired or malformed tokens', () => {
        const expired = fakeJwt({ aud: APP, exp: epoch(-60) });
        expect(classifyLoginRedirect(query({ token: expired }), APP)).toEqual({ kind: 'not-a-sign-in' });
        expect(classifyLoginRedirect(query({ token: 'nonsense' }), APP)).toEqual({ kind: 'not-a-sign-in' });
    });

    it('refuses to decide without this app\'s client id', () => {
        expect(classifyLoginRedirect(query({ token: ours }), null)).toEqual({ kind: 'unverified' });
    });
});
