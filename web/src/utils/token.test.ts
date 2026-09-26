import { describe, expect, it } from 'vitest';
import { decodeTokenClaims, isTokenExpired, isTokenIssuedTo } from './token.ts';
import { b64url, epoch, fakeJwt } from '../test/jwt.ts';

const APP = '0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB';

describe('decodeTokenClaims', () => {
    it('reads base64url payloads, including the characters atob rejects', () => {
        // '?', '>' and '~' in these positions encode to '-' / '_' in base64url.
        const claims = { email: 'a?b>c~d@example.com', note: '???>>>~~~', exp: epoch(3600) };
        const token = fakeJwt(claims);
        expect(token.split('.')[1]).toMatch(/[-_]/);
        expect(decodeTokenClaims(token)).toEqual(claims);
    });

    it('reads non-ASCII claims as UTF-8', () => {
        expect(decodeTokenClaims(fakeJwt({ name: 'Méndez' }))?.name).toBe('Méndez');
    });

    it('returns null for anything that is not a three-part JWT with an object payload', () => {
        expect(decodeTokenClaims(null)).toBeNull();
        expect(decodeTokenClaims('')).toBeNull();
        expect(decodeTokenClaims('abc.def')).toBeNull();
        expect(decodeTokenClaims('a.%%%.c')).toBeNull();
        expect(decodeTokenClaims(`x.${b64url([1])}.y`)).toBeNull();
    });
});

describe('isTokenExpired', () => {
    it('is false only for a decodable token with a future exp', () => {
        expect(isTokenExpired(fakeJwt({ exp: epoch(60) }))).toBe(false);
        expect(isTokenExpired(fakeJwt({ exp: epoch(-60) }))).toBe(true);
        expect(isTokenExpired(fakeJwt({}))).toBe(true);
        expect(isTokenExpired('not-a-jwt')).toBe(true);
        expect(isTokenExpired(null)).toBe(true);
    });
});

describe('isTokenIssuedTo', () => {
    it('accepts the audience DIMO issues for this app, in any casing', () => {
        expect(isTokenIssuedTo(fakeJwt({ aud: APP }), APP)).toBe(true);
        expect(isTokenIssuedTo(fakeJwt({ aud: APP.toLowerCase() }), APP)).toBe(true);
        expect(isTokenIssuedTo(fakeJwt({ aud: ['login-with-dimo', APP] }), APP)).toBe(true);
    });

    it("rejects another license's token: a fleet grant's round trip is not a sign-in", () => {
        expect(isTokenIssuedTo(fakeJwt({ aud: '0x1111111111111111111111111111111111111111' }), APP)).toBe(false);
        expect(isTokenIssuedTo(fakeJwt({ aud: 'login-with-dimo' }), APP)).toBe(false);
    });

    it('rejects tokens with no usable audience, and an unknown app id', () => {
        expect(isTokenIssuedTo(fakeJwt({}), APP)).toBe(false);
        expect(isTokenIssuedTo(fakeJwt({ aud: 42 }), APP)).toBe(false);
        expect(isTokenIssuedTo(fakeJwt({ aud: APP }), '')).toBe(false);
        expect(isTokenIssuedTo('garbage', APP)).toBe(false);
    });
});
