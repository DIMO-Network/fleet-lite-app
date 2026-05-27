import { LitElement, html, css } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { SettingsService } from '../services/settings-service.ts';

@customElement('login-element')
export class LoginElement extends LitElement {
    @state() private loginUrl = '';
    @state() private noClient = false;

    static styles = css`
        :host { display: block; }
        a {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            width: 100%;
            padding: 14px 24px;
            background: #ffffff;
            color: #2f3131;
            border-radius: 8px;
            font-family: 'JetBrains Mono', monospace;
            font-size: 12px;
            font-weight: 600;
            letter-spacing: 0.08em;
            text-transform: uppercase;
            text-decoration: none;
            transition: opacity 0.2s;
        }
        a:hover { opacity: 0.85; }
        .no-client {
            text-align: center;
            color: #c4c7c8;
            font-size: 14px;
            line-height: 1.6;
        }
        .no-client h3 { color: #ffffff; font-size: 16px; margin-bottom: 8px; }
        .no-client a {
            display: inline;
            width: auto;
            padding: 0;
            background: none;
            color: #ffb691;
            font-size: 14px;
            text-transform: none;
            letter-spacing: normal;
        }
    `;

    async connectedCallback() {
        super.connectedCallback();
        try {
            const settings = await SettingsService.getInstance().fetchPublicSettings();
            if (settings.clientId && settings.clientId.length === 42 && settings.clientId !== '0x0000000000000000000000000000000000000000') {
                const redirectUri = location.origin + '/login.html';
                this.loginUrl = `${settings.loginUrl}?clientId=${settings.clientId}&redirectUri=${encodeURIComponent(redirectUri)}&entryState=EMAIL_INPUT&forceEmail=true`;
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
                    <h3>No Client ID configured</h3>
                    <p>Set <code>DIMO_AUTH_CLIENT_ID</code> in the API <code>settings.yaml</code> and register
                    <code>${location.origin}/login.html</code> as a redirect URI in the
                    <a href="https://console.dimo.org">DIMO Developer Console</a>.</p>
                </div>
            `;
        }
        return html`<a id="loginLink" href=${this.loginUrl}>Sign in with DIMO</a>`;
    }
}
