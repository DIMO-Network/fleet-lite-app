import { isTokenExpired, isTokenIssuedTo } from './utils/token.ts';

/**
 * What login.html should do with the query DIMO (or anyone) sent it to.
 *
 * - `none`: a plain visit; show the sign-in page, or skip it for a live session.
 * - `logout`: clear the session.
 * - `sign-in`: a token DIMO issued to this app — store it as the session.
 * - `not-a-sign-in`: a token for some other license, expired, or not a token.
 *   Most often a "Grant permissions" round trip, which comes back with the
 *   grantor's token for the *fleet's* license. Storing it would sign this
 *   browser in as the grantor, so it is dropped and the existing session, if
 *   any, carries on.
 * - `unverified`: a token arrived but this app's client id could not be read,
 *   so there is nothing to check it against. Not stored; the page says so.
 */
export type LoginRedirect =
    | { kind: 'none' }
    | { kind: 'logout' }
    | { kind: 'sign-in'; token: string; email: string; emailMissing: boolean }
    | { kind: 'not-a-sign-in' }
    | { kind: 'unverified' };

/**
 * Classify login.html's query string. `appClientId` is this app's Login with
 * DIMO client id from /public/settings, or null when that could not be read.
 *
 * `emailMissing` keeps the page's existing rule: DIMO sends `email=` empty when
 * the account shared none, and only then is the user asked to type one.
 */
export function classifyLoginRedirect(search: string, appClientId: string | null): LoginRedirect {
    const params = new URLSearchParams(search);
    if (params.get('logout') === 'true') return { kind: 'logout' };

    const token = params.get('token');
    if (token === null) return { kind: 'none' };
    if (!appClientId) return { kind: 'unverified' };
    if (isTokenExpired(token) || !isTokenIssuedTo(token, appClientId)) return { kind: 'not-a-sign-in' };

    const email = params.get('email') ?? '';
    return { kind: 'sign-in', token, email, emailMissing: params.has('email') && !email };
}
