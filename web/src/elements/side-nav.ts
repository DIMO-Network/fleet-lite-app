import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { logout } from '../utils/token.ts';

type NavKey = 'vehicles' | 'stats' | 'glovebox' | 'settings';

const ITEMS: { key: NavKey; icon: string; label: string; href: string }[] = [
    { key: 'vehicles', icon: 'directions_car', label: 'Vehicles', href: '#/' },
    { key: 'stats',    icon: 'bar_chart',      label: 'Stats',    href: '#/stats' },
    { key: 'glovebox', icon: 'inventory_2',    label: 'Glovebox', href: '#/glovebox' },
    { key: 'settings', icon: 'settings',       label: 'Settings', href: '#/settings' },
];

@customElement('side-nav')
export class SideNav extends LitElement {
    @property({ type: String }) active: NavKey = 'vehicles';

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: var(--sidebar-width);
                height: 100vh;
                padding: var(--stack-md) var(--stack-sm);
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
                padding: 0 16px;
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

            .add-vehicle {
                width: 100%;
                background: var(--primary);
                color: var(--on-primary);
                padding: 12px;
                border-radius: var(--radius-md);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                margin-bottom: 24px;
                transition: opacity 0.15s ease;
            }
            .add-vehicle:hover { opacity: 0.9; }

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

    private item(key: NavKey, icon: string, label: string, href: string) {
        const cls = this.active === key ? 'nav-item active' : 'nav-item';
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
                <img src="/assets/dimo-logo-d.png" alt="DIMO" />
                <div>
                    <h1>DIMO Dashboard</h1>
                    <p>Precision Telemetry</p>
                </div>
            </div>
            <button class="add-vehicle">Add Vehicle</button>
            <nav class="items">
                ${ITEMS.map(i => this.item(i.key, i.icon, i.label, i.href))}
            </nav>
            <div class="footer">
                <a class="nav-item" href="#/support">
                    <span class="material-symbols-outlined">help</span>
                    <span class="label">Support</span>
                </a>
                <a class="nav-item" href="#" @click=${(e: Event) => { e.preventDefault(); logout(); }}>
                    <span class="material-symbols-outlined">logout</span>
                    <span class="label">Sign Out</span>
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
