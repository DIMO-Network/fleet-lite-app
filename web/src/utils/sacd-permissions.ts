/**
 * Reading SACD permission masks as identity-api returns them (`Sacd.permissions`,
 * a hex string), to tell a grant that is missing part of the standard set.
 *
 * The encoding mirrors fleet-tenancy-api's internal/sharing/permissions.go:
 * permission n occupies bits 2n and 2n+1, "11" means granted, and the two low
 * bits are reserved. Keep the numbering in step with that file — it is the one
 * that signs.
 */

export const SacdPermission = {
    NonLocationTelemetry: 1,
    Commands: 2,
    CurrentLocation: 3,
    AllTimeLocation: 4,
    Credentials: 5,
    Streams: 6,
    RawData: 7,
    ApproximateLocation: 8,
} as const;
export type SacdPermission = (typeof SacdPermission)[keyof typeof SacdPermission];

/**
 * What every share made through this app grants: fleet-tenancy-api's
 * DefaultPermissions — everything except APPROXIMATE_LOCATION, which is
 * redundant next to the precise location already granted. A re-share writes
 * exactly this set, so it is also the bar a grant is measured against.
 */
export const STANDARD_SHARE_PERMISSIONS: readonly SacdPermission[] = [
    SacdPermission.NonLocationTelemetry,
    SacdPermission.Commands,
    SacdPermission.CurrentLocation,
    SacdPermission.AllTimeLocation,
    SacdPermission.Credentials,
    SacdPermission.Streams,
    SacdPermission.RawData,
];

/** Parse identity-api's hex mask. Unparseable input reads as no permissions. */
export function parsePermissionMask(hex: string | null | undefined): bigint {
    if (!hex) return 0n;
    try {
        return BigInt(hex.startsWith('0x') || hex.startsWith('0X') ? hex : `0x${hex}`);
    } catch {
        return 0n;
    }
}

export function hasPermission(mask: bigint, p: SacdPermission): boolean {
    return ((mask >> BigInt(2 * p)) & 0b11n) === 0b11n;
}

/**
 * The standard permissions this grant does not have, in enum order. Empty means
 * the grant is already at (or above) what a re-share would write.
 *
 * A missing or malformed mask returns null rather than "everything missing":
 * offering an upgrade on a grant we could not read would be a guess dressed up
 * as a finding.
 */
export function missingStandardPermissions(hex: string | null | undefined): SacdPermission[] | null {
    if (!hex) return null;
    const mask = parsePermissionMask(hex);
    if (mask === 0n) return null;
    return STANDARD_SHARE_PERMISSIONS.filter((p) => !hasPermission(mask, p));
}

/**
 * Permissions this grant has that the standard set does not — what a re-share
 * would take away, since it overwrites the whole mask. Today that can only be
 * APPROXIMATE_LOCATION; anything newer than the enum knows is ignored rather
 * than named wrongly.
 */
export function extraPermissions(hex: string | null | undefined): SacdPermission[] {
    const mask = parsePermissionMask(hex);
    return (Object.values(SacdPermission) as SacdPermission[]).filter(
        (p) => !STANDARD_SHARE_PERMISSIONS.includes(p) && hasPermission(mask, p),
    );
}

/** Expiries further out than this are SACD's "indefinite" (set forty years out). */
const INDEFINITE_AFTER_YEARS = 10;

/**
 * The share duration that reproduces a grant's current expiry, for re-sharing
 * without silently shortening or extending it. Zero means indefinite, as the
 * share endpoint reads it.
 *
 * Rounds up to whole days — the endpoint takes days — so a re-share can add at
 * most a day, never cut one off.
 */
export function remainingShareDays(expiresAt: string | null | undefined, now: number = Date.now()): number {
    if (!expiresAt) return 0;
    const when = new Date(expiresAt).getTime();
    if (Number.isNaN(when)) return 0;

    const indefinite = new Date(now);
    indefinite.setFullYear(indefinite.getFullYear() + INDEFINITE_AFTER_YEARS);
    if (when > indefinite.getTime()) return 0;

    return Math.max(1, Math.ceil((when - now) / 86_400_000));
}

/**
 * Whether the grant carries bits beyond the permissions this app knows (8, in
 * bits 2-17). A re-share writes exactly the standard mask, so those would be
 * removed too — unnamed, but not unmentioned.
 */
export function hasUnrecognisedPermissions(hex: string | null | undefined): boolean {
    return parsePermissionMask(hex) >> 18n !== 0n;
}
