import { describe, expect, it } from 'vitest';
import {
    extraPermissions,
    hasUnrecognisedPermissions,
    missingStandardPermissions,
    remainingShareDays,
    SacdPermission,
} from './sacd-permissions.ts';

// Permission n is granted when bits 2n and 2n+1 are both set.
const mask = (...perms: number[]) => '0x' + perms.reduce((m, p) => m | (0b11n << BigInt(2 * p)), 0n).toString(16);

describe('SACD permission masks', () => {
    it('matches fleet-tenancy-api: the standard set is 0xfffc, everything is 0x3fffc', () => {
        expect(mask(1, 2, 3, 4, 5, 6, 7)).toBe('0xfffc');
        expect(missingStandardPermissions('0xfffc')).toEqual([]);
        expect(missingStandardPermissions('0x3fffc')).toEqual([]);
    });

    it('names what an older grant lacks (commands joined the default later)', () => {
        expect(missingStandardPermissions(mask(1, 3, 4, 5, 6, 7))).toEqual([SacdPermission.Commands]);
    });

    it('offers nothing for a mask it cannot read', () => {
        expect(missingStandardPermissions(undefined)).toBeNull();
        expect(missingStandardPermissions('nonsense')).toBeNull();
        expect(missingStandardPermissions('0x0')).toBeNull();
    });

    it('says what a re-share would remove', () => {
        expect(extraPermissions('0x3fffc')).toEqual([SacdPermission.ApproximateLocation]);
        expect(extraPermissions('0xfffc')).toEqual([]);
    });

    it('notices bits beyond the permissions it knows', () => {
        expect(hasUnrecognisedPermissions('0x3fffc')).toBe(false);
        expect(hasUnrecognisedPermissions('0xc0000')).toBe(true);
        expect(hasUnrecognisedPermissions(undefined)).toBe(false);
    });

    it('carries an expiry over in whole days, indefinite as indefinite', () => {
        const inTenDays = new Date(Date.now() + 10 * 86_400_000 - 60_000).toISOString();
        expect(remainingShareDays(inTenDays)).toBe(10);
        expect(remainingShareDays(new Date(Date.now() + 40 * 365 * 86_400_000).toISOString())).toBe(0);
    });
});
