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

/**
 * File access requested alongside the permission bits: SACD `cloudevent`
 * agreements, which login.dimo.org signs into the grant's source document
 * (dimo-login #299) and lists as "Files Requested".
 *
 * Permission bits don't cover documents for other DIMO apps: dimo-app-backend
 * decides document access from these agreements (hasDocumentAgreement). This
 * app's own glovebox reads through token-exchange with RAW_DATA and never
 * needed them — requesting them makes the grant say what it gives.
 *
 * Vehicle documents and their original files, tagged `documents`: the vehicle
 * half of what fleet-tenancy-api's own shares grant (those add the driver's,
 * `dimo.*.driver.*`, which a fleet license reading vehicle data has no use
 * for). `source` is left out on purpose: login.dimo.org fills in the grantor
 * (whose files are shared), which an app can't know before the owner signs in.
 */
export const DIMO_VEHICLE_FILE_AGREEMENTS = [
    { eventType: 'dimo.document.vehicle.*', tags: ['documents'] },
    { eventType: 'dimo.raw.vehicle.*', tags: ['documents'] },
];

/** Redirect URI to hand DIMO. Must exactly match one registered on the dev license. */
export function dimoRedirectUri(): string {
    return location.origin + '/login.html';
}

/** Whether two developer-license client ids name the same license. */
export function sameClientId(a: string, b: string): boolean {
    return !!a && !!b && a.toLowerCase() === b.toLowerCase();
}

/**
 * Choose which of a license's registered redirect URIs a grant should return
 * to, or null when none of them is on this app's origin.
 *
 * A grant is made to the *fleet's* dev license, not to this app's login
 * license, and each fleet registered its own redirect list. DIMO login requires
 * an exact match and answers a mismatch with a generic "issue with the app's
 * credentials" page, so assuming one path sends every fleet that registered a
 * different one to a dead end (license #472 registered only its root).
 *
 * The grant comes back carrying the *grantor's* token for the fleet's license.
 * No page may treat that as a sign-in:
 *   - `/` and `/index.html` ignore it (index.html strips it from the URL);
 *   - `/login.html` stores a token only when DIMO issued it to this app's own
 *     client id (see src/login-redirect.ts; the API checks the same), so the
 *     fleet's token is dropped and the member stays signed in as themselves.
 *     That makes it usable, last, as long as the grant's license is not this
 *     app's own — then the grantor's token *would* be a valid sign-in.
 *     `loginPageSafe` is false in exactly that case.
 *   - accept-invite.html keeps any `token` as a pending invite: never.
 *
 * Returned exactly as registered, trailing slash and all, because the
 * comparison upstream is exact. Only the same scheme and host count: a
 * lookalike host or plain http is not ours.
 */
export function pickGrantRedirectUri(
    registered: readonly string[],
    origin: string,
    opts: { loginPageSafe: boolean },
): string | null {
    const ours = registered.filter((uri) => {
        try {
            return new URL(uri).origin === origin;
        } catch {
            return false;
        }
    });
    const withPath = (p: string) => ours.find((uri) => new URL(uri).pathname === p);
    return (
        withPath('/') ??
        withPath('/index.html') ??
        (opts.loginPageSafe ? withPath('/login.html') : undefined) ??
        null
    );
}

/**
 * Where a grant returns when the license's redirect list couldn't be read.
 * The root is the only guess that is safe for every license (see
 * pickGrantRedirectUri); if it isn't registered DIMO shows its credentials
 * error, which beats a session swap.
 */
export function grantFallbackRedirectUri(): string {
    return location.origin + '/';
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
    /**
     * Where DIMO returns afterwards; must be registered on `clientId`. Choose
     * it with pickGrantRedirectUri, which knows which pages are safe.
     */
    redirectUri: string;
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
        redirectUri: opts.redirectUri,
        entryState: 'VEHICLE_MANAGER',
        permissions: opts.permissions ?? DIMO_PERMISSIONS_ALL,
        cloudEvent: JSON.stringify(DIMO_VEHICLE_FILE_AGREEMENTS),
    });
    for (const v of opts.vehicles ?? []) {
        params.append('vehicles', String(v));
    }
    return `${opts.loginUrl}?${params.toString()}`;
}
