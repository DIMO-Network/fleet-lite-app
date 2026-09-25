import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { themeService } from '../services/theme-service.ts';
import { sharedStyles } from '../global-styles.ts';
import { logout } from '../utils/token.ts';

type NavKey = 'vehicles' | 'stats' | 'groups' | 'geofences' | 'glovebox' | 'tco' | 'charging' | 'settings';

// `suffix` is appended to the current tenant prefix (`#/<tenantId>`) to form the
// link, so all nav stays within the active tenant's routes.
// `label` is a thunk, not a precomputed string: msg() must run at render time so
// it picks up the active locale. Evaluating it here at module load would capture
// the source locale before setLocale() runs and never re-localize.
const ITEMS: { key: NavKey; icon: string; label: () => string; suffix: string }[] = [
    { key: 'vehicles', icon: 'directions_car', label: () => msg('Vehicles'), suffix: '/' },
    { key: 'stats',    icon: 'bar_chart',      label: () => msg('Stats'),    suffix: '/stats' },
    { key: 'groups',   icon: 'workspaces',     label: () => msg('Groups'),   suffix: '/groups' },
    { key: 'geofences', icon: 'fence',         label: () => msg('Geofences'), suffix: '/geofences' },
    { key: 'glovebox', icon: 'inventory_2',    label: () => msg('Glovebox'), suffix: '/glovebox' },
    { key: 'tco',      icon: 'payments',       label: () => msg('TCO'),      suffix: '/tco' },
    { key: 'charging', icon: 'ev_station',     label: () => msg('Charging'), suffix: '/charging' },
    { key: 'settings', icon: 'settings',       label: () => msg('Settings'), suffix: '/settings' },
];

@customElement('side-nav')
export class SideNav extends LitElement {
    @property({ type: String }) active: NavKey = 'vehicles';
    @property({ type: String }) tenantId = '';
    @property({ type: Boolean, reflect: true }) collapsed = false;
    @state() private theme: 'dark' | 'light' = 'dark';

    private boundOnThemeChange = (e: Event) => {
        this.theme = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail.theme;
    };

    connectedCallback() {
        super.connectedCallback();
        this.collapsed = localStorage.getItem('sidebar-collapsed') === 'true';
        themeService.init();
        this.theme = themeService.current;
        window.addEventListener('theme-change', this.boundOnThemeChange);
    }

    disconnectedCallback() {
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        super.disconnectedCallback();
    }

    private toggle() {
        this.collapsed = !this.collapsed;
        localStorage.setItem('sidebar-collapsed', String(this.collapsed));
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: var(--sidebar-width);
                height: 100vh;
                /* padding is set by app-root: its shared reset (* { padding: 0 })
                   matches this host from the outer scope and would beat :host. */
                background: var(--canvas);
                flex-shrink: 0;
                z-index: 50;
                position: relative;
                overflow: visible;
                transition: width 0.2s ease, padding 0.2s ease;
            }
            :host([collapsed]) {
                width: 64px;
            }
            @media (max-width: 768px) {
                :host { display: none; }
            }

            .collapse-toggle {
                position: absolute;
                right: -14px;
                top: 26px;
                width: 28px;
                height: 28px;
                /* Straddles the canvas and the sheet, so it floats rather than
                   using a surface fill that matches the light canvas. */
                background: var(--surface-overlay);
                border: 1px solid var(--canvas-divider);
                box-shadow: var(--shadow-sm);
                border-radius: var(--radius-full);
                display: flex;
                align-items: center;
                justify-content: center;
                cursor: pointer;
                z-index: 51;
                color: var(--on-surface-variant);
                opacity: 0;
                transition: opacity 0.15s ease, background 0.15s ease, color 0.15s ease;
                padding: 0;
            }
            :host(:hover) .collapse-toggle,
            .collapse-toggle:focus-visible,
            :host([collapsed]) .collapse-toggle { opacity: 1; }
            .collapse-toggle:hover {
                background: var(--surface-container-highest);
                color: var(--on-surface);
            }
            .collapse-toggle .material-symbols-outlined {
                font-size: 18px;
            }

            .brand {
                display: flex;
                align-items: center;
                gap: 10px;
                height: 32px;
                padding: 0 10px;
                margin-bottom: 28px;
                text-decoration: none;
            }
            .brand .wordmark {
                height: 18px;
                width: auto;
                display: block;
            }
            /* The gradient wordmark is drawn for dark backgrounds; on the light
               canvas render it as solid ink. */
            .brand .wordmark.on-light { filter: brightness(0) opacity(0.88); }
            .brand .product {
                font: 500 18px/1 var(--font-headline);
                color: var(--on-surface);
                letter-spacing: -0.01em;
                padding-left: 10px;
                border-left: 1px solid var(--canvas-divider);
            }
            .brand .mark {
                display: none;
                width: 32px;
                height: 32px;
                border-radius: var(--radius-full);
            }
            :host([collapsed]) .brand {
                justify-content: center;
                padding: 0;
            }
            :host([collapsed]) .brand .wordmark,
            :host([collapsed]) .brand .product {
                display: none;
            }
            :host([collapsed]) .brand .mark { display: block; }

            nav.items { flex: 1; display: flex; flex-direction: column; gap: 2px; }

            a.nav-item {
                position: relative;
                display: flex;
                align-items: center;
                gap: 12px;
                height: 40px;
                padding: 0 12px;
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                text-decoration: none;
                transition: background 0.15s ease, color 0.15s ease;
            }
            a.nav-item .material-symbols-outlined { font-size: 22px; }
            a.nav-item:hover {
                background: var(--nav-hover);
                color: var(--on-surface);
            }
            a.nav-item.active {
                background: var(--nav-active);
                color: var(--primary);
                box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
            }
            /* Selection reads from the raised pill and the filled glyph; the
               icon stays ink so the nav doesn't carry a second accent color. */
            a.nav-item.active .material-symbols-outlined {
                font-variation-settings: 'FILL' 1, 'wght' 400;
                color: var(--primary);
            }
            :host([collapsed]) a.nav-item {
                justify-content: center;
                padding: 0;
                gap: 0;
            }
            :host([collapsed]) a.nav-item span.label {
                display: none;
            }
            a.nav-item span.label {
                font: 500 14px/20px var(--font-body);
            }

            .footer {
                margin-top: auto;
                padding-top: 12px;
                display: flex;
                flex-direction: column;
                gap: 2px;
            }
            button.theme-toggle {
                width: 100%;
                display: flex;
                align-items: center;
                gap: 12px;
                height: 40px;
                padding: 0 12px;
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
                font: 500 14px/20px var(--font-body);
            }
            button.theme-toggle .material-symbols-outlined { font-size: 22px; }
            button.theme-toggle:hover {
                background: var(--nav-hover);
                color: var(--on-surface);
            }
            :host([collapsed]) button.theme-toggle {
                justify-content: center;
                padding: 0;
                gap: 0;
            }
            :host([collapsed]) button.theme-toggle span.label {
                display: none;
            }
        `,
    ];

    private item(key: NavKey, icon: string, label: string, suffix: string) {
        const cls = this.active === key ? 'nav-item active' : 'nav-item';
        const href = `#/${this.tenantId}${suffix}`;
        return html`
            <a class=${cls} href=${href} title=${this.collapsed ? label : ''}>
                <span class="material-symbols-outlined">${icon}</span>
                <span class="label">${label}</span>
            </a>
        `;
    }

    render() {
        return html`
            <button class="collapse-toggle" @click=${this.toggle}
                title=${this.collapsed ? msg('Expand sidebar') : msg('Collapse sidebar')}>
                <span class="material-symbols-outlined">
                    ${this.collapsed ? 'chevron_right' : 'chevron_left'}
                </span>
            </button>
            <!-- Product name is a brand, so it is not localized. -->
            <a class="brand" href="#/${this.tenantId}/" aria-label="DIMO Fleet">
                <img class="wordmark ${this.theme === 'light' ? 'on-light' : ''}" src="/assets/dimo-wordmark.png" alt="" />
                <span class="product">Fleet</span>
                <img class="mark" src="/assets/dimo-mark.png" alt="" />
            </a>
            <nav class="items">
                ${ITEMS.map(i => this.item(i.key, i.icon, i.label(), i.suffix))}
            </nav>
            <div class="footer">
                <button
                    class="theme-toggle"
                    title=${this.theme === 'dark' ? msg('Switch to light mode') : msg('Switch to dark mode')}
                    @click=${() => themeService.toggle()}
                >
                    <span class="material-symbols-outlined">
                        ${this.theme === 'dark' ? 'light_mode' : 'dark_mode'}
                    </span>
                    <span class="label">
                        ${this.theme === 'dark' ? msg('Light mode') : msg('Dark mode')}
                    </span>
                </button>
                <a class="nav-item" href="#/${this.tenantId}/settings"
                   title=${this.collapsed ? msg('Support') : ''}>
                    <span class="material-symbols-outlined">help</span>
                    <span class="label">${msg('Support')}</span>
                </a>
                <a class="nav-item" href="#"
                   title=${this.collapsed ? msg('Sign out') : ''}
                   @click=${(e: Event) => { e.preventDefault(); logout(); }}>
                    <span class="material-symbols-outlined">logout</span>
                    <span class="label">${msg('Sign out')}</span>
                </a>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'side-nav': SideNav;
    }
}
