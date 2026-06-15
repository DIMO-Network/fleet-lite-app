/**
 * Maps a vehicle definition's `make` (e.g. "Mercedes-Benz", "Land Rover") to the
 * URL of its OEM logo under src/assets/oem-logos/, which is keyed by a lowercase,
 * hyphenated slug of the brand name (e.g. "mercedes-benz.png", "land-rover.png").
 *
 * The asset may not exist for every make DIMO returns — callers should fall back
 * to a generic icon on the <img>'s `error` event rather than pre-checking existence.
 */
export function brandLogoUrl(make: string): string | null {
    const slug = make
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '');
    if (!slug) return null;
    return `/assets/oem-logos/${slug}.png`;
}
