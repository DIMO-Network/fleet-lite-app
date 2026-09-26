/**
 * Decode a JWT's payload without verifying it, or null when it isn't one.
 *
 * JWTs are base64url (`-` and `_`, no padding), which `atob` rejects, so the
 * alphabet is mapped back to standard base64 first. Browser-side only ever
 * reads its own session this way; the API is what verifies signatures.
 */
export function decodeTokenClaims(token: string | null | undefined): Record<string, unknown> | null {
    if (!token) return null;
    const parts = token.split('.');
    if (parts.length !== 3) return null;
    try {
        const b64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
        const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
        const json = new TextDecoder().decode(Uint8Array.from(atob(padded), (c) => c.charCodeAt(0)));
        const claims: unknown = JSON.parse(json);
        return claims && typeof claims === 'object' && !Array.isArray(claims)
            ? (claims as Record<string, unknown>)
            : null;
    } catch {
        return null;
    }
}

/**
 * Returns true if the JWT is missing or expired (or malformed), false otherwise.
 * Pure browser decode — no network.
 */
export function isTokenExpired(token: string | null | undefined): boolean {
    const claims = decodeTokenClaims(token);
    if (!claims) return true;
    const exp = typeof claims.exp === 'number' ? claims.exp : 0;
    const now = Math.floor(Date.now() / 1000);
    return exp < now;
}

/**
 * Whether DIMO issued `token` to the developer license `clientId`.
 *
 * DIMO signs every app's tokens with the same keys and records the requesting
 * license in `aud`, so a token handed to this app's pages is only a session
 * for *this* app when `aud` names this app's client id. A "Grant permissions"
 * round trip comes back with the grantor's token for the *fleet's* license;
 * that token is not a sign-in here. The API enforces the same rule.
 */
export function isTokenIssuedTo(token: string | null | undefined, clientId: string): boolean {
    const claims = decodeTokenClaims(token);
    if (!claims || !clientId) return false;
    const aud = claims.aud;
    const audiences = typeof aud === 'string' ? [aud] : Array.isArray(aud) ? aud : [];
    const want = clientId.toLowerCase();
    return audiences.some((a) => typeof a === 'string' && a.toLowerCase() === want);
}

/**
 * Returns the JWT claims object, or null if the token is missing/malformed.
 */
export function getTokenClaims(): Record<string, unknown> | null {
    return decodeTokenClaims(localStorage.getItem('token'));
}

/**
 * Clear auth state and bounce to /login.html. Use this whenever the server
 * tells us our token is invalid (401) — letting the user keep a dead token
 * in localStorage causes infinite redirect loops.
 */
export function logout(): void {
    localStorage.removeItem('token');
    localStorage.removeItem('email');
    window.location.replace('/login.html');
}
