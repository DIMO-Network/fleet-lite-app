import { LitElement, html, css } from 'lit';
import { customElement } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';

interface Row { icon: string; label: string; }

const PRIVACY_ROWS: Row[] = [
    { icon: 'directions_car', label: 'Manage Subscription' },
    { icon: 'vpn_key',        label: 'Passkey' },
    { icon: 'language',       label: 'Language' },
    { icon: 'developer_mode', label: 'Advanced' },
    { icon: 'straighten',     label: 'Measurement Units' },
    { icon: 'mail',           label: 'Email Preferences' },
    { icon: 'logout',         label: 'Log out' },
];

const SUPPORT_ROWS: Row[] = [
    { icon: 'support_agent', label: 'FAQ' },
];

@customElement('account-settings-view')
export class AccountSettingsView extends LitElement {
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

            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                height: 80px;
                width: 100%;
                background: var(--background);
                border-bottom: 1px solid var(--outline-variant);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--margin-desktop);
            }
            @media (max-width: 768px) {
                header.top-bar { display: none; }
            }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }
            .icon-btn {
                color: var(--on-surface-variant);
                background: none;
                border: none;
                padding: 8px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease;
            }
            .icon-btn:hover { background: var(--surface-container-high); color: var(--primary); }
            .avatar {
                width: 32px;
                height: 32px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                overflow: hidden;
            }
            .avatar img { width: 100%; height: 100%; object-fit: cover; }

            .canvas {
                flex: 1;
                width: 100%;
                max-width: 800px;
                margin: 0 auto;
                padding: var(--stack-lg) var(--gutter);
            }
            @media (max-width: 768px) {
                .canvas { padding: var(--stack-lg) var(--margin-mobile); }
            }

            .mobile-profile { display: none; }
            @media (max-width: 768px) {
                .mobile-profile {
                    display: block;
                    margin-bottom: var(--stack-lg);
                }
                .mobile-profile h1 {
                    font: var(--type-headline-xl);
                    letter-spacing: -0.02em;
                    color: var(--primary);
                    margin-bottom: 8px;
                }
                .mobile-profile .wallet {
                    display: flex;
                    align-items: center;
                    gap: 8px;
                    color: var(--on-surface-variant);
                    margin-bottom: 4px;
                    font: var(--type-body-md);
                }
                .mobile-profile .email { font: var(--type-body-md); color: var(--on-surface-variant); }
            }

            .section { margin-bottom: 32px; }
            .section-title {
                font: var(--type-body-md);
                font-weight: 500;
                color: var(--on-surface-variant);
                margin-bottom: 16px;
                padding: 0 8px;
            }

            .row-group {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                overflow: hidden;
            }
            .row {
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px;
                cursor: pointer;
                border-bottom: 1px solid var(--outline-variant);
                color: inherit;
                text-decoration: none;
                transition: background 0.15s ease;
            }
            .row:last-child { border-bottom: none; }
            .row:hover { background: var(--surface-container-high); }
            .row .left-group { display: flex; align-items: center; gap: 16px; }
            .row .label {
                font: var(--type-body-lg);
                color: var(--primary);
            }
            .row .material-symbols-outlined.muted { color: var(--on-surface-variant); }
            .row:hover .left-group > .material-symbols-outlined { color: var(--primary); }
        `,
    ];

    private renderRow(r: Row) {
        return html`
            <a class="row" href="#">
                <div class="left-group">
                    <span class="material-symbols-outlined muted">${r.icon}</span>
                    <span class="label">${r.label}</span>
                </div>
                <span class="material-symbols-outlined muted">chevron_right</span>
            </a>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <h2>Account</h2>
                <div class="right">
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <div class="avatar">
                        <img alt="User profile"
                             src="https://lh3.googleusercontent.com/aida-public/AB6AXuCq6qWKURsjqwSIJCKjZAmUcVFqT9SNg8mTq8fW242Gn57aMJoxPyNcyU6lHWIrYnzQxXNf5_pIr1S-8d8rkPyNBAXEKykdqU5lGv5ZJfcs0ewK6-xDbmEdPA6YxjJ4K40St9VgnpafSWDI7qNo-bL7Gbhss7D3_VFiHmsNG_nl3lIRZmutx5u1jXGT-5yxwdpM6MM0qyutbkf5kwwLNG3GfXXSG0EvYM8fA6g7UpvV1v1nseFlv6C5vbhDGk33tHgj392rOE5Us7o" />
                    </div>
                </div>
            </header>

            <div class="canvas">
                <div class="mobile-profile">
                    <h1>Account</h1>
                    <div class="wallet">
                        <span>0x59...da40</span>
                        <button class="icon-btn"><span class="material-symbols-outlined" style="font-size: 16px;">content_copy</span></button>
                    </div>
                    <div class="email">james@dimo.zone</div>
                </div>

                <div class="section">
                    <h3 class="section-title">Privacy & Account</h3>
                    <div class="row-group">
                        ${PRIVACY_ROWS.map(r => this.renderRow(r))}
                    </div>
                </div>

                <div class="section">
                    <h3 class="section-title">Support</h3>
                    <div class="row-group">
                        ${SUPPORT_ROWS.map(r => this.renderRow(r))}
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
