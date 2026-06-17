import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { logout } from '../utils/token.ts';

type NavKey = 'vehicles' | 'stats' | 'groups' | 'glovebox' | 'settings';

// `suffix` is appended to the current tenant prefix (`#/<tenantId>`) to form the
// link, so all nav stays within the active tenant's routes.
// `label` is a thunk, not a precomputed string: msg() must run at render time so
// it picks up the active locale. Evaluating it here at module load would capture
// the source locale before setLocale() runs and never re-localize.
const ITEMS: { key: NavKey; icon: string; label: () => string; suffix: string }[] = [
    { key: 'vehicles', icon: 'directions_car', label: () => msg('Vehicles'), suffix: '/' },
    { key: 'stats',    icon: 'bar_chart',      label: () => msg('Stats'),    suffix: '/stats' },
    { key: 'groups',   icon: 'workspaces',     label: () => msg('Groups'),   suffix: '/groups' },
    { key: 'glovebox', icon: 'inventory_2',    label: () => msg('Glovebox'), suffix: '/glovebox' },
    { key: 'settings', icon: 'settings',       label: () => msg('Settings'), suffix: '/settings' },
];

@customElement('side-nav')
export class SideNav extends LitElement {
    @property({ type: String }) active: NavKey = 'vehicles';
    @property({ type: String }) tenantId = '';

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
            }
            @media (max-width: 768px) {
                :host { display: none; }
            }

            .brand {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: var(--stack-md) 8px;
                margin-bottom: 32px;
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
        `,
    ];

    private item(key: NavKey, icon: string, label: string, suffix: string) {
        const cls = this.active === key ? 'nav-item active' : 'nav-item';
        const href = `#/${this.tenantId}${suffix}`;
        return html`
            <a class=${cls} href=${href}>
                <span class="material-symbols-outlined">${icon}</span>
                <span class="label">${label}</span>
            </a>
        `;
    }

    render() {
        return html`
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
                <a class="nav-item" href="#/${this.tenantId}/settings">
                    <span class="material-symbols-outlined">help</span>
                    <span class="label">${msg('Support')}</span>
                </a>
                <a class="nav-item" href="#" @click=${(e: Event) => { e.preventDefault(); logout(); }}>
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
