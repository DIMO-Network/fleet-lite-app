/**
 * Login-with-DIMO permission requests.
 *
 * `permissions` is a POSITIONAL binary string, not a list of names — one
 * character per entry of the SDK's `Permissions` enum, in declaration order
 * (see `permissionsToBinary` in login-with-dimo/sdk/src/utils/permission.ts).
 * Reordering these silently grants the wrong privileges.
 *
 *   pos 1  NONLOCATION_TELEMETRY   privilege 1
 *   pos 2  COMMANDS                privilege 2
 *   pos 3  CURRENT_LOCATION        privilege 3
 *   pos 4  ALLTIME_LOCATION        privilege 4
 *   pos 5  CREDENTIALS             privilege 5  — VIN; telemetry-api needs it
 *   pos 6  STREAMS                 privilege 6
 *   pos 7  RAW_DATA                privilege 7  — glovebox documents via fetch-api
 *   pos 8  APPROXIMATE_LOCATION    privilege 8
 *
 * Do NOT swap this for `permissionTemplateId`: template 1 resolves to
 * `11111100`, which omits RAW_DATA, so the glovebox can never load its
 * document list no matter how the grant screen is presented.
 */
export const DIMO_PERMISSIONS_ALL = '11111111';

/** Redirect URI to hand DIMO. Must exactly match one registered on the dev license. */
export function dimoRedirectUri(): string {
    return location.origin + '/login.html';
}

export interface DimoGrantUrlOptions {
    /** Base login host, from GET /public/settings (`loginUrl`). */
    loginUrl: string;
    /** Dev license client id the grant is made to. */
    clientId: string;
    /** Positional binary string; defaults to all eight privileges. */
    permissions?: string;
}

export interface ShareVehiclesUrlOptions extends DimoGrantUrlOptions {
    /** Token ids to narrow the vehicle picker to. Omit to offer the whole garage. */
    vehicles?: Array<number | string>;
}

/**
 * Sign-in URL. Passing `permissions` puts DIMO into `VEHICLE_MANAGER`, so the
 * user authenticates and grants vehicle access in one pass — matching what the
 * SDK's own LoginWithDimo button does when handed permissions.
 */
export function buildLoginUrl(opts: DimoGrantUrlOptions): string {
    const params = new URLSearchParams({
        clientId: opts.clientId,
        redirectUri: dimoRedirectUri(),
        entryState: 'VEHICLE_MANAGER',
        permissions: opts.permissions ?? DIMO_PERMISSIONS_ALL,
        forceEmail: 'true',
    });
    return `${opts.loginUrl}?${params.toString()}`;
}

/**
 * URL for the vehicle-sharing screen on its own, for an already-signed-in user
 * fixing up a vehicle we lack privileges on. Re-granting overwrites that
 * grantee's existing SACD record rather than adding a second one.
 */
export function buildShareVehiclesUrl(opts: ShareVehiclesUrlOptions): string {
    const params = new URLSearchParams({
        clientId: opts.clientId,
        redirectUri: dimoRedirectUri(),
        entryState: 'VEHICLE_MANAGER',
        permissions: opts.permissions ?? DIMO_PERMISSIONS_ALL,
    });
    for (const v of opts.vehicles ?? []) {
        params.append('vehicles', String(v));
    }
    return `${opts.loginUrl}?${params.toString()}`;
}
