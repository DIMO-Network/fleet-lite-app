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

/**
 * Choose which of a license's registered redirect URIs a grant should return
 * to, or null when none of them is on this app's origin.
 *
 * A grant is made to the *fleet's* dev license, not to this app's login
 * license, and each fleet registered its own redirect list. DIMO login requires
 * an exact match and answers a mismatch with a generic "issue with the app's
 * credentials" page — so assuming `/login.html` sends every fleet that
 * registered only its root (license #472 did) to a dead end.
 *
 * Preference: our login page (the page built for DIMO's redirect), then the
 * origin root, then any other path on our origin. Returned exactly as
 * registered, trailing slash and all, because the comparison upstream is
 * exact. Only the same scheme and host count: a lookalike host or plain http
 * is not ours.
 *
 * Landing on a page other than /login.html is safe for a grant: the returned
 * token is simply not read, and the member stays signed in with their own.
 */
export function pickGrantRedirectUri(registered: readonly string[], origin: string): string | null {
    const ours = registered.filter((uri) => {
        try {
            return new URL(uri).origin === origin;
        } catch {
            return false;
        }
    });
    const path = (uri: string) => new URL(uri).pathname;
    return (
        ours.find((uri) => path(uri) === '/login.html') ??
        ours.find((uri) => path(uri) === '/') ??
        ours[0] ??
        null
    );
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
    /** Where DIMO returns afterwards; must be registered on `clientId`. Defaults to dimoRedirectUri(). */
    redirectUri?: string;
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
        redirectUri: opts.redirectUri ?? dimoRedirectUri(),
        entryState: 'VEHICLE_MANAGER',
        permissions: opts.permissions ?? DIMO_PERMISSIONS_ALL,
    });
    for (const v of opts.vehicles ?? []) {
        params.append('vehicles', String(v));
    }
    return `${opts.loginUrl}?${params.toString()}`;
}
