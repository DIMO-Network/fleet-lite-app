/** base64url of `value` as JSON (UTF-8), with the padding JWTs omit. */
export function b64url(value: unknown): string {
    let binary = '';
    for (const byte of new TextEncoder().encode(JSON.stringify(value))) binary += String.fromCharCode(byte);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** An unsigned JWT carrying `claims`, for tests of code that only decodes. */
export function fakeJwt(claims: Record<string, unknown>): string {
    return `${b64url({ alg: 'RS256', typ: 'JWT' })}.${b64url(claims)}.signature`;
}

/** Seconds since the epoch, `offset` seconds from now. */
export function epoch(offset = 0): number {
    return Math.floor(Date.now() / 1000) + offset;
}
