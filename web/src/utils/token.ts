/**
 * Returns true if the JWT is missing or expired (or malformed), false otherwise.
 * Pure browser decode — no network.
 */
export function isTokenExpired(token: string | null | undefined): boolean {
    if (!token) return true;
    try {
        const parts = token.split('.');
        if (parts.length !== 3) return true;
        const payload = JSON.parse(atob(parts[1]));
        const exp = typeof payload?.exp === 'number' ? payload.exp : 0;
        const now = Math.floor(Date.now() / 1000);
        return exp < now;
    } catch {
        return true;
    }
}

/**
 * Returns the JWT claims object, or null if the token is missing/malformed.
 */
export function getTokenClaims(): Record<string, unknown> | null {
    const t = localStorage.getItem('token');
    if (!t) return null;
    try {
        const parts = t.split('.');
        if (parts.length !== 3) return null;
        return JSON.parse(atob(parts[1]));
    } catch {
        return null;
    }
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
