interface Rgb {
    r: number;
    g: number;
    b: number;
}

const LIGHT_BADGE = '#f4f1ec';
const DARK_BADGE = '#16151a';

function relativeLuminance({ r, g, b }: Rgb): number {
    const channel = (c: number) => {
        const v = c / 255;
        return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function contrastRatio(a: number, b: number): number {
    const [lighter, darker] = a > b ? [a, b] : [b, a];
    return (lighter + 0.05) / (darker + 0.05);
}

/** Average RGB of an image's non-transparent pixels, sampled at low resolution. */
function averageColor(img: HTMLImageElement): Rgb | null {
    const size = 24;
    const canvas = document.createElement('canvas');
    canvas.width = size;
    canvas.height = size;
    const ctx = canvas.getContext('2d');
    if (!ctx) return null;
    ctx.drawImage(img, 0, 0, size, size);

    let r = 0;
    let g = 0;
    let b = 0;
    let count = 0;
    const { data } = ctx.getImageData(0, 0, size, size);
    for (let i = 0; i < data.length; i += 4) {
        if (data[i + 3] < 32) continue; // skip near-transparent pixels
        r += data[i];
        g += data[i + 1];
        b += data[i + 2];
        count++;
    }
    if (!count) return null;
    return { r: r / count, g: g / count, b: b / count };
}

/**
 * Picks whichever of a light or dark badge background contrasts more with a
 * logo's average color, so marks stay legible whether the artwork itself is
 * light (e.g. a white wordmark) or dark (e.g. a black emblem).
 *
 * Returns null if the image can't be sampled (e.g. zero-size or fully transparent).
 */
export function contrastingBadgeBackground(img: HTMLImageElement): string | null {
    const avg = averageColor(img);
    if (!avg) return null;
    const logoLuminance = relativeLuminance(avg);
    const lightContrast = contrastRatio(logoLuminance, relativeLuminance({ r: 0xf4, g: 0xf1, b: 0xec }));
    const darkContrast = contrastRatio(logoLuminance, relativeLuminance({ r: 0x16, g: 0x15, b: 0x1a }));
    return lightContrast >= darkContrast ? LIGHT_BADGE : DARK_BADGE;
}
