import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { themeService } from '../services/theme-service.ts';
import { sharedStyles } from '../global-styles.ts';
import { logout } from '../utils/token.ts';

type NavKey = 'vehicles' | 'stats' | 'groups' | 'geofences' | 'glovebox' | 'settings';

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
                padding: var(--stack-md);
                background: var(--surface-container-low);
                border-right: 1px solid var(--outline-variant);
                flex-shrink: 0;
                z-index: 50;
                position: relative;
                overflow: visible;
                transition: width 0.2s ease, padding 0.2s ease;
            }
            :host([collapsed]) {
                width: 56px;
                padding: var(--stack-md) 0;
            }
            @media (max-width: 768px) {
                :host { display: none; }
            }

            .collapse-toggle {
                position: absolute;
                right: -12px;
                top: 50%;
                transform: translateY(-50%);
                width: 24px;
                height: 48px;
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-left: none;
                border-radius: 0 var(--radius-md) var(--radius-md) 0;
                display: flex;
                align-items: center;
                justify-content: center;
                cursor: pointer;
                z-index: 51;
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
                padding: 0;
            }
            .collapse-toggle:hover {
                background: var(--surface-container-highest);
                color: var(--primary);
            }
            .collapse-toggle .material-symbols-outlined {
                font-size: 18px;
            }

            .brand {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: var(--stack-md) 8px;
                margin-bottom: 32px;
            }
            :host([collapsed]) .brand {
                justify-content: center;
                padding: var(--stack-md) 0;
                gap: 0;
            }
            :host([collapsed]) .brand h1,
            :host([collapsed]) .brand p {
                display: none;
            }
            .brand img {
                width: 40px;
                height: 40px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                object-fit: cover;
            }
            .brand h1 {
                font: var(--type-headline-md);
                color: var(--primary);
                letter-spacing: -0.02em;
            }
            .brand p {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }

            nav.items { flex: 1; display: flex; flex-direction: column; gap: 8px; }

            a.nav-item {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 12px 16px;
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                text-decoration: none;
                transition: background 0.15s ease, color 0.15s ease;
            }
            a.nav-item:hover {
                background: var(--surface-container-high);
                color: var(--primary);
            }
            a.nav-item.active {
                background: var(--surface-container-highest);
                color: var(--primary);
            }
            a.nav-item.active .material-symbols-outlined {
                font-variation-settings: 'FILL' 1;
            }
            :host([collapsed]) a.nav-item {
                justify-content: center;
                padding: 12px 0;
                gap: 0;
            }
            :host([collapsed]) a.nav-item span.label {
                display: none;
            }
            a.nav-item span.label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
            }

            .footer {
                margin-top: auto;
                padding-top: 16px;
                border-top: 1px solid var(--outline-variant);
                display: flex;
                flex-direction: column;
                gap: 8px;
            }
            button.theme-toggle {
                width: 100%;
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 12px 16px;
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
            }
            button.theme-toggle:hover {
                background: var(--surface-container-high);
                color: var(--primary);
            }
            :host([collapsed]) button.theme-toggle {
                justify-content: center;
                padding: 12px 0;
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
            <div class="brand">
                <img src="/assets/logo.png" alt="${msg('DIMO')}" />
                <div>
                    <h1>${msg('DIMO Dashboard')}</h1>
                    <p>${msg('Precision Telemetry')}</p>
                </div>
            </div>
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
                        ${this.theme === 'dark' ? msg('Light Mode') : msg('Dark Mode')}
                    </span>
                </button>
                <a class="nav-item" href="#/${this.tenantId}/settings"
                   title=${this.collapsed ? msg('Support') : ''}>
                    <span class="material-symbols-outlined">help</span>
                    <span class="label">${msg('Support')}</span>
                </a>
                <a class="nav-item" href="#"
                   title=${this.collapsed ? msg('Sign Out') : ''}
                   @click=${(e: Event) => { e.preventDefault(); logout(); }}>
                    <span class="material-symbols-outlined">logout</span>
                    <span class="label">${msg('Sign Out')}</span>
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
