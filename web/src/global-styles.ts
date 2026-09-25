import { css } from 'lit';

/*
 * DIMO Fleet design tokens.
 *
 * The visual language follows the DIMO Driver mobile app: Euclid Circular A,
 * cool blue-black surfaces, the sky→mint DIMO gradient, and generous radii.
 *
 * Token roles:
 *  - `--primary` is the high-emphasis *ink* (headings, key values). It is not
 *    the action color.
 *  - `--accent` / `--brand-gradient` are the action + "live" color: primary
 *    buttons, active states, selected controls, online markers.
 *  - `--accent-ink` is the accent when it has to be read as text on a surface
 *    (mint on white fails contrast, so light mode deepens it).
 *  - `--focus-ring` is the keyboard-focus outline color; light mode uses a
 *    deep teal so the ring stays >=3:1 on both white sheets and the canvas.
 *  - `--type-label-caps` is kept by name for compatibility, but it is now a
 *    sentence-case sans label, not uppercase mono.
 */
export const sharedStyles = css`
    :host {
        /* ---------------- Typography ---------------- */
        --font-headline: 'Euclid Circular A', system-ui, -apple-system, 'Segoe UI', sans-serif;
        --font-body: 'Euclid Circular A', system-ui, -apple-system, 'Segoe UI', sans-serif;
        /* Identifiers (VIN, token, plate) use the brand face with tabular figures. */
        --font-mono: var(--font-body);

        --type-headline-xl: 600 32px/40px var(--font-headline);
        --type-headline-lg: 600 26px/32px var(--font-headline);
        --type-headline-md: 600 20px/28px var(--font-headline);
        --type-body-lg: 400 17px/26px var(--font-body);
        --type-body-md: 400 15px/22px var(--font-body);
        --type-body-sm: 400 14px/20px var(--font-body);
        --type-label: 500 12px/16px var(--font-body);
        --type-label-caps: var(--type-label);
        --type-data-display: 600 40px/44px var(--font-headline);

        /* ---------------- Spacing ---------------- */
        --sidebar-width: 244px;
        --container-max-width: 1440px;
        --gutter: 24px;
        --margin-desktop: 40px;
        --margin-mobile: 16px;
        --stack-sm: 8px;
        --stack-md: 16px;
        --stack-lg: 32px;

        /* ---------------- Radii ----------------
         * Hierarchy, not one radius everywhere: chips/inputs < cards < sheets. */
        --radius-sm: 6px;
        --radius-md: 10px;
        --radius-lg: 16px;
        --radius-xl: 20px;
        --radius-2xl: 28px;
        --radius-full: 9999px;

        color: var(--on-surface);
        font: var(--type-body-md);
        font-feature-settings: 'tnum' 1;
        -webkit-font-smoothing: antialiased;
        -moz-osx-font-smoothing: grayscale;
    }

    *,
    *::before,
    *::after {
        box-sizing: border-box;
        margin: 0;
        padding: 0;
    }

    *:focus-visible {
        outline: 2px solid var(--focus-ring);
        outline-offset: 2px;
    }

    ::selection {
        background: var(--accent-soft-strong);
        color: var(--on-surface);
    }

    /* Material Symbols must be redeclared inside each Shadow DOM */
    .material-symbols-outlined {
        font-family: 'Material Symbols Outlined';
        font-weight: normal;
        font-style: normal;
        font-size: 24px;
        line-height: 1;
        letter-spacing: normal;
        text-transform: none;
        display: inline-block;
        white-space: nowrap;
        word-wrap: normal;
        direction: ltr;
        font-variation-settings: 'wght' 350;
        -webkit-font-smoothing: antialiased;
        -moz-osx-font-smoothing: grayscale;
        text-rendering: optimizeLegibility;
        font-feature-settings: 'liga';
    }

    .material-symbols-outlined.filled {
        font-variation-settings: 'FILL' 1, 'wght' 400;
    }

    /* ---------------- Type utility classes ---------------- */
    .t-headline-xl { font: var(--type-headline-xl); letter-spacing: -0.02em; }
    .t-headline-lg { font: var(--type-headline-lg); letter-spacing: -0.015em; }
    .t-headline-md { font: var(--type-headline-md); letter-spacing: -0.01em; }
    .t-body-lg     { font: var(--type-body-lg); }
    .t-body-md     { font: var(--type-body-md); }
    .t-body-sm     { font: var(--type-body-sm); }
    .t-label-caps  { font: var(--type-label); color: var(--on-surface-variant); }
    .t-data        { font: var(--type-data-display); letter-spacing: -0.03em; }

    /* ---------------- Form controls (baseline; components may override) ---------------- */
    :where(input, select, textarea) {
        font: var(--type-body-sm);
        color: var(--on-surface);
    }
    :where(select) {
        appearance: none;
        -webkit-appearance: none;
        background-color: var(--surface-container-high);
        background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%238a8d8d' stroke-width='2.5' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E");
        background-repeat: no-repeat;
        background-position: right 12px center;
        padding-right: 34px !important;
        border: 1px solid var(--outline-variant);
        border-radius: var(--radius-md);
        cursor: pointer;
    }
    :where(input:not([type='checkbox']):not([type='radio']):not([type='range']):not([type='color']), select, textarea):focus-visible {
        outline: none;
        border-color: var(--focus-ring);
        box-shadow: 0 0 0 3px var(--accent-soft);
    }
    :where(input[type='checkbox'], input[type='radio']) {
        accent-color: var(--accent);
    }

    /* ---------------- Buttons ---------------- */
    button {
        font: inherit;
        color: inherit;
        background: none;
        border: none;
        cursor: pointer;
    }

    .btn-primary {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: 8px;
        min-height: 40px;
        padding: 0 18px;
        border-radius: var(--radius-full);
        background: var(--brand-gradient);
        color: var(--on-accent);
        font: 600 14px/20px var(--font-body);
        transition: filter 0.15s ease, box-shadow 0.15s ease;
    }
    .btn-primary:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }
    .btn-primary:disabled { filter: grayscale(1) opacity(0.5); box-shadow: none; cursor: not-allowed; }

    .btn-secondary {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: 8px;
        min-height: 40px;
        padding: 0 16px;
        border-radius: var(--radius-full);
        background: var(--surface-container-high);
        color: var(--on-surface);
        border: 1px solid var(--outline-variant);
        font: 500 14px/20px var(--font-body);
        transition: background 0.15s ease, border-color 0.15s ease;
    }
    .btn-secondary:hover { background: var(--surface-container-highest); border-color: var(--outline); }

    .btn-ghost {
        color: var(--on-surface-variant);
        padding: 8px;
        border-radius: var(--radius-full);
        transition: background 0.15s ease, color 0.15s ease;
    }
    .btn-ghost:hover { background: var(--surface-container-high); color: var(--on-surface); }

    /* ---------------- Card ---------------- */
    .card {
        background: var(--surface-container-low);
        border: 1px solid var(--outline-variant);
        border-radius: var(--radius-lg);
        padding: var(--gutter);
    }

    /* ---------------- Glass panel (used by the map overlay list) ---------------- */
    .glass-panel {
        background: var(--glass-bg);
        backdrop-filter: blur(20px) saturate(1.4);
        -webkit-backdrop-filter: blur(20px) saturate(1.4);
    }

    /* ---------------- Scrollbar ---------------- */
    .custom-scrollbar { scrollbar-width: thin; scrollbar-color: var(--outline-variant) transparent; }
    .custom-scrollbar::-webkit-scrollbar { width: 6px; }
    .custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
    .custom-scrollbar::-webkit-scrollbar-thumb {
        background-color: var(--outline-variant);
        border-radius: 10px;
    }

    @media (prefers-reduced-motion: reduce) {
        *, *::before, *::after {
            transition-duration: 0.01ms !important;
            animation-duration: 0.01ms !important;
            animation-iteration-count: 1 !important;
        }
    }
`;

/**
 * Global document-level styles. Imported for side-effect from src/index.ts;
 * these apply to anything in light DOM (e.g. <body>, the <app-root> host).
 * @font-face lives here: faces declared on the document are usable from every
 * shadow root.
 */
const documentStyles = `
    @font-face { font-family: 'Euclid Circular A'; font-weight: 400; font-style: normal; font-display: swap; src: url('/assets/fonts/EuclidCircularA-Regular.woff2') format('woff2'); }
    @font-face { font-family: 'Euclid Circular A'; font-weight: 500; font-style: normal; font-display: swap; src: url('/assets/fonts/EuclidCircularA-Medium.woff2') format('woff2'); }
    @font-face { font-family: 'Euclid Circular A'; font-weight: 600; font-style: normal; font-display: swap; src: url('/assets/fonts/EuclidCircularA-Semibold.woff2') format('woff2'); }
    @font-face { font-family: 'Euclid Circular A'; font-weight: 700; font-style: normal; font-display: swap; src: url('/assets/fonts/EuclidCircularA-Bold.woff2') format('woff2'); }

    :root {
        --top-bar-height: 72px;
        color-scheme: dark;

        /* ---------------- DIMO brand (from the Driver app palette) ---------------- */
        --dimo-sky: #8CD0FF;
        --dimo-mint: #46F1E4;
        --brand-gradient: linear-gradient(105deg, #8CD0FF 0%, #46F1E4 100%);
        /* Soft ambient glow for full-bleed brand moments (sign-in, onboarding). */
        --brand-glow: radial-gradient(60% 50% at 35% 40%, rgba(140, 208, 255, 0.12), transparent 70%), radial-gradient(55% 50% at 65% 60%, rgba(70, 241, 228, 0.10), transparent 70%);
        /* Single-series chart fill. */
        --data-1: #8CD0FF;
        /* Modal backdrop. */
        --scrim: rgba(8, 9, 10, 0.62);

        /* ---------------- Accent (actions, active, live) ---------------- */
        --accent: #46F1E4;
        --accent-ink: #46F1E4;
        --on-accent: #06201E;
        --accent-soft: rgba(70, 241, 228, 0.12);
        --accent-soft-strong: rgba(70, 241, 228, 0.28);
        --accent-glow: 0 0 0 1px rgba(70, 241, 228, 0.35), 0 6px 24px -6px rgba(70, 241, 228, 0.45);
        /* Keyboard focus outline (>=3:1 against every surface). */
        --focus-ring: #46F1E4;

        /* ---------------- Status ---------------- */
        --positive: #36DF71;
        --warning: #FFAC60;
        --negative: #FF6060;
        --favorite: #FFCD29;

        /* ---------------- App frame ---------------- */
        --canvas: #0E0F11;
        --nav-hover: #1C1F22;
        --nav-active: #24272B;
        --sheet-border: rgba(255, 255, 255, 0.06);
        /* Hairlines drawn directly on the canvas (sidebar, onboarding). */
        --canvas-divider: #45494E;
        --shadow-float: 0 16px 48px -12px rgba(0, 0, 0, 0.6), 0 0 0 1px rgba(255, 255, 255, 0.06);
        --shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.2);
        /* Modals, menus and other panels that float above the page. */
        --surface-overlay: #1C1F22;

        /* ---------------- Surface / Material 3 roles ---------------- */
        --surface: #16181B;
        --surface-dim: #111214;
        --surface-bright: #3A3E42;
        --surface-container-lowest: #0E0F11;
        --surface-container-low: #1C1F22;
        --surface-container: #212428;
        --surface-container-high: #272A2E;
        --surface-container-highest: #303438;
        --surface-variant: #272A2E;
        --surface-tint: #A0A3A2;
        --background: #16181B;
        --on-background: #EDEEEE;
        --on-surface: #EDEEEE;
        --on-surface-variant: #A0A3A2;
        --inverse-surface: #EDEEEE;
        --inverse-on-surface: #16181B;

        /* ---------------- Outlines ---------------- */
        --outline: #5C6063;
        --outline-variant: #373B40;

        /* ---------------- Primary = high-emphasis ink ---------------- */
        --primary: #F6F7F7;
        --on-primary: #111214;
        --primary-container: #E6E8E8;
        --on-primary-container: #3A3E42;
        --inverse-primary: #3A3E42;
        --primary-fixed: #E6E8E8;
        --primary-fixed-dim: #C4C7C7;
        --on-primary-fixed: #111214;
        --on-primary-fixed-variant: #3A3E42;

        /* ---------------- Secondary (warm highlight / warnings) ---------------- */
        --secondary: #FFAC60;
        --on-secondary: #4A2000;
        --secondary-container: #E8730C;
        --on-secondary-container: #2A1200;
        --secondary-fixed: #FCDEC4;
        --secondary-fixed-dim: #FFAC60;
        --on-secondary-fixed: #341100;
        --on-secondary-fixed-variant: #793100;

        /* ---------------- Tertiary (online / healthy = DIMO mint) ---------------- */
        --tertiary: #F6F7F7;
        --on-tertiary: #06201E;
        --tertiary-container: #46F1E4;
        --on-tertiary-container: #0E4B45;
        --tertiary-fixed: #8CFFF5;
        --tertiary-fixed-dim: #46F1E4;
        --on-tertiary-fixed: #06201E;
        --on-tertiary-fixed-variant: #17645D;

        /* ---------------- Error ---------------- */
        --error: #FF6060;
        --on-error: #330000;
        --error-container: #402321;
        --on-error-container: #FFCCCC;

        /* ---------------- Glass ---------------- */
        --glass-bg: rgba(22, 24, 27, 0.78);

        /* ---------------- Driver-behaviour series (dark steps, validated) ---------------- */
        --bhv-braking: #e66767;
        --bhv-cornering: #9085e9;
        --bhv-acceleration: #199e70;
    }

    :root[data-theme="light"] {
        color-scheme: light;

        /* ---------------- Driver-behaviour series (light steps, validated) ---------------- */
        --bhv-braking: #e34948;
        --bhv-cornering: #4a3aa7;
        --bhv-acceleration: #1baf7a;

        --data-1: #2B82D5;
        --scrim: rgba(19, 20, 23, 0.32);
        --brand-glow: radial-gradient(60% 50% at 35% 40%, rgba(140, 208, 255, 0.28), transparent 70%), radial-gradient(55% 50% at 65% 60%, rgba(70, 241, 228, 0.22), transparent 70%);

        --accent: #22C7BA;
        /* >=4.5:1 on white, surface-container-*, the canvas and accent-soft(-strong) over each. */
        --accent-ink: #07635C;
        --on-accent: #06201E;
        --accent-soft: rgba(34, 199, 186, 0.14);
        --accent-soft-strong: rgba(34, 199, 186, 0.3);
        --accent-glow: 0 0 0 1px rgba(34, 199, 186, 0.35), 0 6px 20px -8px rgba(34, 199, 186, 0.55);
        /* 5.2:1 on white, 4.3:1 on the canvas. */
        --focus-ring: #0B7A72;

        --positive: #1B8842;
        --warning: #B75B0A;
        --negative: #C70000;
        --favorite: #C99A00;

        --canvas: #E7E9E9;
        --nav-hover: rgba(255, 255, 255, 0.55);
        --nav-active: #FFFFFF;
        --sheet-border: rgba(19, 20, 23, 0.06);
        --canvas-divider: #A0A3A2;
        --shadow-float: 0 16px 40px -14px rgba(19, 20, 23, 0.22), 0 0 0 1px rgba(19, 20, 23, 0.06);
        --shadow-sm: 0 1px 2px rgba(19, 20, 23, 0.12);
        --surface-overlay: #FFFFFF;

        /* ---------------- Surface / Material 3 roles ---------------- */
        --surface: #FFFFFF;
        --surface-dim: #F1F2F2;
        --surface-bright: #FFFFFF;
        --surface-container-lowest: #FFFFFF;
        --surface-container-low: #F6F7F7;
        --surface-container: #F0F1F1;
        --surface-container-high: #E9EBEB;
        --surface-container-highest: #DFE2E2;
        --surface-variant: #E9EBEB;
        --surface-tint: #5E6163;
        --background: #FFFFFF;
        --on-background: #131417;
        --on-surface: #131417;
        --on-surface-variant: #5E6163;
        --inverse-surface: #16181B;
        --inverse-on-surface: #EDEEEE;

        /* ---------------- Outlines ---------------- */
        --outline: #A0A3A2;
        --outline-variant: #D2D6D7;

        /* ---------------- Primary = high-emphasis ink ---------------- */
        --primary: #131417;
        --on-primary: #FFFFFF;
        --primary-container: #232729;
        --on-primary-container: #E6E8E8;
        --inverse-primary: #C4C7C7;

        --secondary: #B75B0A;
        --on-secondary: #FFFFFF;
        --secondary-container: #FCDEC4;
        --on-secondary-container: #4A2000;

        --tertiary-container: #22C7BA;
        --tertiary-fixed-dim: #1EA398;

        --error: #C70000;
        --on-error: #FFFFFF;
        --error-container: #FFF0F0;
        --on-error-container: #7A0000;

        /* ---------------- Glass ---------------- */
        --glass-bg: rgba(255, 255, 255, 0.82);
    }

    html, body {
        margin: 0;
        background: var(--canvas);
        color: var(--on-surface);
        font-family: 'Euclid Circular A', system-ui, -apple-system, 'Segoe UI', sans-serif;
        height: 100%;
        -webkit-font-smoothing: antialiased;
    }
    body { overflow: hidden; }
`;

const styleEl = document.createElement('style');
styleEl.textContent = documentStyles;
document.head.appendChild(styleEl);
