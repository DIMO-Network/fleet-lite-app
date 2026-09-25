import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, state } from 'lit/decorators.js';
import { SettingsService } from '../services/settings-service.ts';
import { buildLoginUrl } from '../utils/dimo-permissions.ts';

@customElement('login-element')
export class LoginElement extends LitElement {
    @state() private loginUrl = '';
    @state() private noClient = false;

    // Tokens (--brand-gradient, --on-accent, …) come from :root: global-styles.ts
    // in the app, and the mirrored :root block in login.html / accept-invite.html.
    static styles = css`
        :host { display: block; }
        *, *::before, *::after { box-sizing: border-box; }
        #loginLink {
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 10px;
            width: 100%;
            min-height: 48px;
            padding: 0 24px;
            background: var(--brand-gradient);
            color: var(--on-accent);
            border-radius: var(--radius-full, 9999px);
            font: 600 15px/20px var(--font-body, inherit);
            text-decoration: none;
            transition: filter 0.15s ease, box-shadow 0.15s ease;
        }
        #loginLink:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }
        #loginLink:focus-visible { outline: 2px solid var(--focus-ring); outline-offset: 3px; }
        #loginLink svg {
            width: 18px;
            height: 18px;
            transition: transform 0.15s ease;
        }
        #loginLink:hover svg { transform: translateX(2px); }
        @media (prefers-reduced-motion: reduce) {
            #loginLink, #loginLink svg { transition: none; }
        }
        .no-client {
            text-align: center;
            color: var(--on-surface-variant);
            font: 400 14px/20px var(--font-body, inherit);
            padding: 16px;
            border-radius: var(--radius-md, 10px);
            background: var(--surface-container-low);
        }
        .no-client h3 {
            color: var(--primary);
            font: 600 15px/22px var(--font-body, inherit);
            margin: 0 0 6px;
        }
        .no-client p { margin: 0; }
        .no-client code {
            font: 500 13px/18px var(--font-body, inherit);
            color: var(--on-surface);
        }
        .no-client a {
            color: var(--accent-ink);
            text-decoration: none;
            font-weight: 500;
        }
        .no-client a:hover { text-decoration: underline; }
    `;

    async connectedCallback() {
        super.connectedCallback();
        try {
            const settings = await SettingsService.getInstance().fetchPublicSettings();
            if (settings.clientId && settings.clientId.length === 42 && settings.clientId !== '0x0000000000000000000000000000000000000000') {
                this.loginUrl = buildLoginUrl({
                    loginUrl: settings.loginUrl,
                    clientId: settings.clientId,
                });
            } else {
                this.noClient = true;
            }
        } catch (e) {
            console.error('Failed to load public settings', e);
            this.noClient = true;
        }
    }

    render() {
        if (this.noClient) {
            return html`
                <div class="no-client">
                    <h3>${msg('No Client ID configured')}</h3>
                    ${msg(html`<p>Set <code>DIMO_AUTH_CLIENT_ID</code> in the API <code>settings.yaml</code> and register
                    <code>${location.origin}/login.html</code> as a redirect URI in the
                    <a href="https://console.dimo.org">DIMO Developer Console</a>.</p>`)}
                </div>
            `;
        }
        return html`<a id="loginLink" href=${this.loginUrl}>
            ${msg('Sign in with DIMO')}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"
                stroke-linejoin="round" aria-hidden="true"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
        </a>`;
    }
}
