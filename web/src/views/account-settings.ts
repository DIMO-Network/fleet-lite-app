import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { msg } from '@lit/localize';
import { sharedStyles } from '../global-styles.ts';
import { getTokenClaims, logout } from '../utils/token.ts';
import { PrefsService, localeLabel } from '../services/prefs-service.ts';
import { setLocale } from '../localization.ts';
import { unitsLabel } from '../utils/units.ts';
import '../elements/tenant-members.ts';

// A row is either a navigation link (href) or an in-place action (onClick).
interface Row { icon: string; label: string; trailing?: string; onClick?: () => void; href?: string; }

@customElement('account-settings-view')
export class AccountSettingsView extends LitElement {
    @property({ type: String }) tenantId = '';
    private unsubscribePrefs: (() => void) | null = null;

    connectedCallback() {
        super.connectedCallback();
        this.unsubscribePrefs = PrefsService.getInstance().subscribe(() => this.requestUpdate());
    }

    disconnectedCallback() {
        this.unsubscribePrefs?.();
        super.disconnectedCallback();
    }
    private get wallet(): string {
        const claims = getTokenClaims();
        const addr = typeof claims?.ethereum_address === 'string' ? claims.ethereum_address : '';
        if (!addr) return '';
        return `${addr.slice(0, 6)}…${addr.slice(-4)}`;
    }

    private get email(): string {
        return localStorage.getItem('email') || '';
    }

    private privacyRows(): Row[] {
        const prefs = PrefsService.getInstance();
        return [
            { icon: 'directions_car', label: msg('Manage Subscription') },
            {
                icon: 'language',
                label: msg('Language'),
                trailing: localeLabel(prefs.getLocale()),
                onClick: () => {
                    const next = prefs.toggleLocale();
                    setLocale(next).then(() => window.location.reload());
                },
            },
            // Replaces a dead 'Advanced' placeholder. What a customer actually
            // needs from here is the answer to "what have I paid for, and why
            // is a vehicle missing" — which is the memberships page.
            {
                icon: 'card_membership',
                label: msg('Memberships'),
                href: `#/${this.tenantId}/memberships`,
            },
            {
                icon: 'straighten',
                label: msg('Measurement Units'),
                trailing: unitsLabel(prefs.getUnits()),
                onClick: () => { prefs.toggleUnits(); },
            },
            { icon: 'logout',         label: msg('Log out'), onClick: () => logout() },
        ];
    }

    private supportRows(): Row[] {
        return [
            { icon: 'support_agent', label: msg('FAQ') },
        ];
    }

    private copyWallet = async () => {
        const claims = getTokenClaims();
        const addr = typeof claims?.ethereum_address === 'string' ? claims.ethereum_address : '';
        if (addr && navigator.clipboard) {
            try { await navigator.clipboard.writeText(addr); } catch { /* noop */ }
        }
    };
    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: 100%;
                height: 100%;
                overflow-y: auto;
                background: var(--background);
            }
            :host > * { flex-shrink: 0; }

            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                height: var(--top-bar-height);
                background: var(--background);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
            }
            @media (max-width: 768px) {
                header.top-bar { display: none; }
            }
            header.top-bar h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            header.top-bar .right { display: flex; align-items: center; gap: 12px; }
            .icon-btn {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                width: 40px;
                height: 40px;
                color: var(--on-surface-variant);
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .icon-btn .material-symbols-outlined { font-size: 22px; }
            .icon-btn:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .avatar {
                width: 32px;
                height: 32px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                overflow: hidden;
            }
            .avatar img { width: 100%; height: 100%; object-fit: cover; }

            .canvas {
                flex: 1;
                width: 100%;
                max-width: 800px;
                margin: 0 auto;
                padding: var(--stack-md) var(--gutter) 48px;
            }
            @media (max-width: 768px) {
                .canvas { padding: var(--stack-lg) var(--margin-mobile); }
            }

            .profile-block {
                margin-bottom: var(--stack-lg);
                padding: 20px 24px;
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
            }
            .profile-block .wallet {
                display: flex;
                align-items: center;
                gap: 6px;
                color: var(--primary);
                font: 600 17px/24px var(--font-headline);
                letter-spacing: 0.01em;
            }
            .profile-block .wallet button {
                display: inline-flex;
                padding: 6px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .profile-block .wallet button:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .profile-block .email { font: var(--type-body-sm); color: var(--on-surface-variant); margin-top: 2px; }
            .profile-block .empty { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .section { margin-bottom: 32px; }
            .section-title {
                font: 600 15px/22px var(--font-headline);
                color: var(--primary);
                margin-bottom: 12px;
                padding: 0 4px;
            }

            .row-group {
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
                overflow: hidden;
            }
            .row {
                position: relative;
                display: flex;
                align-items: center;
                justify-content: space-between;
                min-height: 56px;
                padding: 0 12px 0 16px;
                cursor: pointer;
                color: inherit;
                text-decoration: none;
                transition: background 0.15s ease;
            }
            /* Inset divider, starting under the label (not the icon). */
            .row + .row::before {
                content: '';
                position: absolute;
                top: 0;
                left: 54px;
                right: 0;
                height: 1px;
                background: var(--outline-variant);
            }
            .row:hover { background: var(--surface-container); }
            .row .left-group { display: flex; align-items: center; gap: 16px; }
            .row .right-group { display: flex; align-items: center; gap: 8px; }
            .row .trailing {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .row .label {
                font: 500 15px/22px var(--font-body);
                color: var(--on-surface);
            }
            .row .material-symbols-outlined { font-size: 22px; }
            .row .material-symbols-outlined.muted { color: var(--on-surface-variant); }
            .row .right-group > .material-symbols-outlined { font-size: 20px; opacity: 0.7; }
            .row:hover .left-group > .material-symbols-outlined { color: var(--on-surface); }
        `,
    ];

    private renderRow(r: Row) {
        // A row with an href navigates natively (hash routing picks it up); a
        // row with an onClick acts in place and must not move the page.
        const onClick = r.onClick
            ? (e: Event) => { e.preventDefault(); r.onClick?.(); }
            : undefined;
        return html`
            <a class="row" href=${r.href ?? '#'} @click=${onClick}>
                <div class="left-group">
                    <span class="material-symbols-outlined muted">${r.icon}</span>
                    <span class="label">${r.label}</span>
                </div>
                <div class="right-group">
                    ${r.trailing ? html`<span class="trailing">${r.trailing}</span>` : ''}
                    <span class="material-symbols-outlined muted">chevron_right</span>
                </div>
            </a>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <h2>${msg('Account')}</h2>
                <div class="right">
                    <tenant-switcher .currentTenantId=${this.tenantId}></tenant-switcher>
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <div class="avatar">
                        <img alt="${msg('User profile')}"
                             src="https://lh3.googleusercontent.com/aida-public/AB6AXuCq6qWKURsjqwSIJCKjZAmUcVFqT9SNg8mTq8fW242Gn57aMJoxPyNcyU6lHWIrYnzQxXNf5_pIr1S-8d8rkPyNBAXEKykdqU5lGv5ZJfcs0ewK6-xDbmEdPA6YxjJ4K40St9VgnpafSWDI7qNo-bL7Gbhss7D3_VFiHmsNG_nl3lIRZmutx5u1jXGT-5yxwdpM6MM0qyutbkf5kwwLNG3GfXXSG0EvYM8fA6g7UpvV1v1nseFlv6C5vbhDGk33tHgj392rOE5Us7o" />
                    </div>
                </div>
            </header>

            <div class="canvas">
                <div class="profile-block">
                    ${this.wallet
                        ? html`
                            <div class="wallet">
                                <span>${this.wallet}</span>
                                <button @click=${this.copyWallet} title="${msg('Copy wallet address')}">
                                    <span class="material-symbols-outlined" style="font-size: 16px;">content_copy</span>
                                </button>
                            </div>
                            ${this.email ? html`<div class="email">${this.email}</div>` : ''}
                        `
                        : html`<div class="empty">${msg('Not signed in.')}</div>`
                    }
                </div>

                <div class="section">
                    <h3 class="section-title">${msg('Privacy & Account')}</h3>
                    <div class="row-group">
                        ${this.privacyRows().map(r => this.renderRow(r))}
                    </div>
                </div>

                <div class="section">
                    <h3 class="section-title">${msg('Fleet Members')}</h3>
                    <tenant-members .tenantId=${this.tenantId}></tenant-members>
                </div>

                <div class="section">
                    <h3 class="section-title">${msg('Support')}</h3>
                    <div class="row-group">
                        ${this.supportRows().map(r => this.renderRow(r))}
                    </div>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'account-settings-view': AccountSettingsView;
    }
}
