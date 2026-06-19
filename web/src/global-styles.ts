import { css } from 'lit';

export const sharedStyles = css`
    :host {
        /* ---------------- Typography ---------------- */
        --font-headline: 'Inter', sans-serif;
        --font-body: 'Inter', sans-serif;
        --font-mono: 'JetBrains Mono', monospace;

        --type-headline-xl: 700 40px/48px var(--font-headline);
        --type-headline-lg: 600 32px/40px var(--font-headline);
        --type-headline-md: 600 24px/32px var(--font-headline);
        --type-body-lg: 400 18px/28px var(--font-body);
        --type-body-md: 400 16px/24px var(--font-body);
        --type-body-sm: 400 14px/20px var(--font-body);
        --type-label-caps: 600 12px/16px var(--font-mono);
        --type-data-display: 500 48px/56px var(--font-headline);

        /* ---------------- Spacing ---------------- */
        --sidebar-width: 280px;
        --container-max-width: 1440px;
        --gutter: 24px;
        --margin-desktop: 40px;
        --margin-mobile: 16px;
        --stack-sm: 8px;
        --stack-md: 16px;
        --stack-lg: 32px;

        /* ---------------- Radii ---------------- */
        --radius-sm: 0.25rem;
        --radius-md: 0.5rem;
        --radius-lg: 0.75rem;
        --radius-xl: 1rem;
        --radius-2xl: 1.5rem;
        --radius-full: 9999px;

        color: var(--on-surface);
        font: var(--type-body-md);
    }

    *,
    *::before,
    *::after {
        box-sizing: border-box;
        margin: 0;
        padding: 0;
    }

    *:focus-visible {
        outline: 1px solid var(--primary);
        outline-offset: 2px;
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
        -webkit-font-smoothing: antialiased;
        -moz-osx-font-smoothing: grayscale;
        text-rendering: optimizeLegibility;
        font-feature-settings: 'liga';
    }

    .material-symbols-outlined.filled {
        font-variation-settings: 'FILL' 1;
    }

    /* ---------------- Type utility classes ---------------- */
    .t-headline-xl { font: var(--type-headline-xl); letter-spacing: -0.02em; }
    .t-headline-lg { font: var(--type-headline-lg); letter-spacing: -0.01em; }
    .t-headline-md { font: var(--type-headline-md); }
    .t-body-lg     { font: var(--type-body-lg); }
    .t-body-md     { font: var(--type-body-md); }
    .t-body-sm     { font: var(--type-body-sm); }
    .t-label-caps  { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; }
    .t-data        { font: var(--type-data-display); letter-spacing: -0.03em; }

    /* ---------------- Buttons ---------------- */
    button {
        font: inherit;
        color: inherit;
        background: none;
        border: none;
        cursor: pointer;
    }

    .btn-primary {
        background: var(--primary);
        color: var(--on-primary);
        padding: 12px 16px;
        border-radius: var(--radius-md);
        font: var(--type-label-caps);
        letter-spacing: 0.05em;
        text-transform: uppercase;
        transition: opacity 0.15s ease;
    }
    .btn-primary:hover { opacity: 0.9; }

    .btn-secondary {
        background: transparent;
        color: var(--primary);
        border: 1px solid var(--primary);
        padding: 12px 16px;
        border-radius: var(--radius-md);
        font: var(--type-label-caps);
        letter-spacing: 0.05em;
        text-transform: uppercase;
    }

    .btn-ghost {
        color: var(--on-surface-variant);
        padding: 8px;
        border-radius: var(--radius-full);
        transition: background 0.15s ease;
    }
    .btn-ghost:hover { background: var(--surface-container-high); color: var(--primary); }

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
        backdrop-filter: blur(12px);
        -webkit-backdrop-filter: blur(12px);
    }

    /* ---------------- Scrollbar ---------------- */
    .custom-scrollbar::-webkit-scrollbar { width: 6px; }
    .custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
    .custom-scrollbar::-webkit-scrollbar-thumb {
        background-color: var(--outline-variant);
        border-radius: 10px;
    }
`;

/**
 * Global document-level styles. Imported for side-effect from src/index.ts;
 * these apply to anything in light DOM (e.g. <body>, the <app-root> host).
 */
const documentStyles = `
    :root {
        --top-bar-height: 80px;

        /* ---------------- Surface / Material 3 roles ---------------- */
        --surface: #131313;
        --surface-dim: #131313;
        --surface-bright: #393939;
        --surface-container-lowest: #0e0e0e;
        --surface-container-low: #1c1b1b;
        --surface-container: #201f1f;
        --surface-container-high: #2a2a2a;
        --surface-container-highest: #353534;
        --surface-variant: #353534;
        --surface-tint: #c6c6c7;
        --background: #131313;
        --on-background: #e5e2e1;
        --on-surface: #e5e2e1;
        --on-surface-variant: #c4c7c8;
        --inverse-surface: #e5e2e1;
        --inverse-on-surface: #313030;

        /* ---------------- Outlines ---------------- */
        --outline: #8e9192;
        --outline-variant: #444748;

        /* ---------------- Primary (mono white) ---------------- */
        --primary: #ffffff;
        --on-primary: #2f3131;
        --primary-container: #e2e2e2;
        --on-primary-container: #636565;
        --inverse-primary: #5d5f5f;
        --primary-fixed: #e2e2e2;
        --primary-fixed-dim: #c6c6c7;
        --on-primary-fixed: #1a1c1c;
        --on-primary-fixed-variant: #454747;

        /* ---------------- Secondary (kinetic orange) ---------------- */
        --secondary: #ffb691;
        --on-secondary: #552000;
        --secondary-container: #ea6b18;
        --on-secondary-container: #4a1b00;
        --secondary-fixed: #ffdbcb;
        --secondary-fixed-dim: #ffb691;
        --on-secondary-fixed: #341100;
        --on-secondary-fixed-variant: #793100;

        /* ---------------- Tertiary (status green) ---------------- */
        --tertiary: #ffffff;
        --on-tertiary: #003827;
        --tertiary-container: #86f8c8;
        --on-tertiary-container: #007352;
        --tertiary-fixed: #86f8c8;
        --tertiary-fixed-dim: #69dbad;
        --on-tertiary-fixed: #002115;
        --on-tertiary-fixed-variant: #005139;

        /* ---------------- Error ---------------- */
        --error: #ffb4ab;
        --on-error: #690005;
        --error-container: #93000a;
        --on-error-container: #ffdad6;

        /* ---------------- Glass ---------------- */
        --glass-bg: rgba(28, 27, 27, 0.85);
    }

    :root[data-theme="light"] {
        /* ---------------- Surface / Material 3 roles ---------------- */
        --surface: #f8f8f8;
        --surface-dim: #efefef;
        --surface-bright: #ffffff;
        --surface-container-lowest: #ffffff;
        --surface-container-low: #f2f2f2;
        --surface-container: #ebebeb;
        --surface-container-high: #e2e2e2;
        --surface-container-highest: #d9d9d9;
        --surface-variant: #e0e0e0;
        --surface-tint: #5d5f5f;
        --background: #f8f8f8;
        --on-background: #1a1a1a;
        --on-surface: #1a1a1a;
        --on-surface-variant: #444748;
        --inverse-surface: #2f3131;
        --inverse-on-surface: #f0f0f0;

        /* ---------------- Outlines ---------------- */
        --outline: #6e7172;
        --outline-variant: #c4c7c8;

        /* ---------------- Primary (mono black in light) ---------------- */
        --primary: #1a1a1a;
        --on-primary: #ffffff;
        --primary-container: #2f3131;
        --on-primary-container: #e0e0e0;
        --inverse-primary: #c6c6c7;

        /* ---------------- Glass ---------------- */
        --glass-bg: rgba(248, 248, 248, 0.85);
    }

    html, body {
        margin: 0;
        background: var(--surface);
        color: var(--on-surface);
        font-family: 'Inter', sans-serif;
        height: 100%;
    }
    body { overflow: hidden; }
`;

const styleEl = document.createElement('style');
styleEl.textContent = documentStyles;
document.head.appendChild(styleEl);
