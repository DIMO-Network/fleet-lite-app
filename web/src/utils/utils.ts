export function isLocalhost(): boolean {
    const h = window.location.hostname;
    return h === 'localhost' || h === '127.0.0.1' || h.endsWith('.dimo.org') && h.startsWith('local');
}
