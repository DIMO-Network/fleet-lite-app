import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService, ApiError } from '../services/api-service.ts';
import { SettingsService } from '../services/settings-service.ts';
import { logout } from '../utils/token.ts';

interface CreatedTenant {
    id: string;
    name: string;
}

/**
 * Shown when a logged-in user belongs to no tenant. Collects a DIMO developer
 * license (client ID + API key) and creates the tenant; the backend validates
 * the credentials against DIMO before persisting. On success we route into the
 * new tenant (`#/<id>/`).
 */
@customElement('onboard-tenant-view')
export class OnboardTenantView extends LitElement {
    @state() private name = '';
    @state() private clientId = '';
    @state() private apiKey = '';
    @state() private submitting = false;
    @state() private error = '';
    @state() private provisioning = false;
    @state() private showCopyBanner = false;

    private messageListener: ((e: MessageEvent) => void) | null = null;
    private popup: Window | null = null;

    disconnectedCallback() {
        super.disconnectedCallback();
        this.teardownListener();
    }

    private teardownListener() {
        if (this.messageListener) {
            window.removeEventListener('message', this.messageListener);
            this.messageListener = null;
        }
    }

    private async openProvisionPopup() {
        this.provisioning = true;
        this.error = '';
        try {
            const settings = await SettingsService.getInstance().fetchPublicSettings();
            const redirectUri = location.origin + '/login.html';
            const url = `${settings.loginUrl}?clientId=${settings.clientId}` +
                `&redirectUri=${encodeURIComponent(redirectUri)}` +
                `&entryState=PROVISION_DEVELOPER_LICENSE` +
                `&brandName=fleet-lite-app`;

            this.popup = window.open(url, 'dimo-provision', 'width=500,height=640');

            this.teardownListener();
            this.messageListener = (e: MessageEvent) => this.handleProvisionMessage(e, settings.loginUrl);
            window.addEventListener('message', this.messageListener);
        } catch {
            this.error = msg('Could not load DIMO settings. Please enter credentials manually.');
            this.provisioning = false;
        }
    }

    private handleProvisionMessage(e: MessageEvent, loginUrl: string) {
        try {
            const loginOrigin = new URL(loginUrl).origin;
            if (e.origin !== loginOrigin) return;
        } catch {
            return;
        }

        if (e.data?.eventType !== 'provisionResponse') return;

        const { clientId, privateKey } = e.data as { clientId?: string; privateKey?: string };
        if (!clientId || !privateKey) return;

        this.clientId = clientId;
        this.apiKey = privateKey;
        this.showCopyBanner = true;
        this.provisioning = false;
        this.teardownListener();
        this.popup?.close();
    }

    private async copyApiKey() {
        if (navigator.clipboard) {
            await navigator.clipboard.writeText(this.apiKey);
        }
    }

    private async submit(e: Event) {
        e.preventDefault();
        this.error = '';
        if (!this.clientId.trim() || !this.apiKey.trim()) {
            this.error = msg('Client ID and API key are required.');
            return;
        }
        this.submitting = true;
        try {
            const tenant = await ApiService.getInstance().post<CreatedTenant>('/tenants', {
                name: this.name.trim(),
                clientId: this.clientId.trim(),
                apiKey: this.apiKey.trim(),
            });
            location.hash = `/${tenant.id}/`;
        } catch (err) {
            this.error = extractMessage(err) || msg('Could not create the fleet.');
        } finally {
            this.submitting = false;
        }
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                position: relative;
                display: flex;
                align-items: center;
                justify-content: center;
                width: 100%;
                height: 100vh;
                background: var(--background);
                color: var(--on-surface);
                overflow: auto;
            }
            .topbar {
                position: absolute;
                top: 0;
                left: 0;
                right: 0;
                display: flex;
                justify-content: flex-end;
                padding: var(--stack-md) var(--margin-desktop);
            }
            @media (max-width: 768px) {
                .topbar { padding: var(--stack-md) var(--margin-mobile); }
            }
            .logout-btn {
                display: inline-flex;
                align-items: center;
                gap: 8px;
                background: none;
                border: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
                border-radius: var(--radius-full);
                padding: 8px 16px;
                font: var(--type-body-sm);
                cursor: pointer;
                transition: color 0.15s ease, border-color 0.15s ease;
                width: auto;
                margin: 0;
            }
            .logout-btn:hover { color: var(--primary); border-color: var(--outline); }
            .logout-btn .material-symbols-outlined { font-size: 18px; }
            .card {
                width: min(480px, 92vw);
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: 16px;
                padding: 32px;
            }
            h1 {
                font-size: 24px;
                font-weight: 700;
                margin: 0 0 8px;
            }
            p.sub {
                color: var(--on-surface-variant);
                font-size: 14px;
                line-height: 1.5;
                margin: 0 0 24px;
            }
            .provision-btn {
                display: flex;
                align-items: center;
                justify-content: center;
                gap: 8px;
                width: 100%;
                padding: 12px;
                background: var(--secondary-container);
                color: var(--on-secondary-container);
                border: none;
                border-radius: 999px;
                font-size: 14px;
                font-weight: 600;
                cursor: pointer;
                margin-bottom: 20px;
                transition: opacity 0.15s ease;
            }
            .provision-btn:hover { opacity: 0.85; }
            .provision-btn[disabled] { opacity: 0.6; cursor: default; }
            .provision-btn .material-symbols-outlined { font-size: 18px; }
            .divider {
                display: flex;
                align-items: center;
                gap: 12px;
                margin-bottom: 20px;
                color: var(--on-surface-variant);
                font-size: 12px;
            }
            .divider::before, .divider::after {
                content: '';
                flex: 1;
                border-top: 1px solid var(--outline-variant);
            }
            label {
                display: block;
                font-size: 12px;
                letter-spacing: 0.04em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin: 16px 0 6px;
            }
            input {
                width: 100%;
                box-sizing: border-box;
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: 8px;
                padding: 12px 14px;
                color: var(--on-surface);
                font-size: 14px;
                font-family: var(--font-mono);
            }
            input:focus {
                outline: none;
                border-color: var(--secondary-container);
            }
            .api-key-row {
                position: relative;
            }
            .api-key-row input {
                padding-right: 44px;
            }
            .copy-inline-btn {
                position: absolute;
                right: 10px;
                top: 50%;
                transform: translateY(-50%);
                background: none;
                border: none;
                color: var(--on-surface-variant);
                cursor: pointer;
                padding: 4px;
                width: auto;
                margin: 0;
                border-radius: 4px;
            }
            .copy-inline-btn:hover { color: var(--primary); }
            .copy-inline-btn .material-symbols-outlined { font-size: 18px; display: block; }
            .copy-banner {
                display: flex;
                align-items: flex-start;
                gap: 10px;
                margin-top: 12px;
                padding: 12px 14px;
                background: color-mix(in srgb, var(--secondary-container) 30%, transparent);
                border: 1px solid var(--secondary-container);
                border-radius: 8px;
                font-size: 13px;
                line-height: 1.4;
                color: var(--on-surface);
            }
            .copy-banner .material-symbols-outlined { font-size: 18px; color: var(--secondary); flex-shrink: 0; margin-top: 1px; }
            .copy-banner-text { flex: 1; }
            .copy-banner-btn {
                display: inline-flex;
                align-items: center;
                gap: 4px;
                margin-top: 6px;
                background: none;
                border: 1px solid var(--secondary-container);
                border-radius: 999px;
                color: var(--secondary);
                padding: 4px 10px;
                font-size: 12px;
                font-weight: 600;
                cursor: pointer;
                width: auto;
            }
            .copy-banner-btn:hover { background: var(--secondary-container); }
            .copy-banner-btn .material-symbols-outlined { font-size: 14px; }
            button[type="submit"] {
                width: 100%;
                margin-top: 24px;
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                border-radius: 999px;
                padding: 14px;
                font-size: 14px;
                font-weight: 700;
                cursor: pointer;
            }
            button[type="submit"][disabled] {
                opacity: 0.6;
                cursor: default;
            }
            .error {
                margin-top: 16px;
                color: var(--error);
                font-size: 13px;
            }
            .hint {
                color: var(--on-surface-variant);
                font-size: 12px;
                margin-top: 4px;
            }
        `,
    ];

    render() {
        return html`
            <div class="topbar">
                <button class="logout-btn" type="button" @click=${() => logout()}>
                    <span class="material-symbols-outlined">logout</span>
                    ${msg('Log out')}
                </button>
            </div>
            <form class="card" @submit=${this.submit}>
                <h1>${msg('Set up your fleet')}</h1>
                <p class="sub">
                    ${msg(`You're not part of a fleet yet. Create one with your DIMO developer
                    license — the client ID and API key from your DIMO developer console.`)}
                </p>

                <button
                    type="button"
                    class="provision-btn"
                    ?disabled=${this.provisioning}
                    @click=${this.openProvisionPopup}
                >
                    <span class="material-symbols-outlined">key</span>
                    ${this.provisioning ? msg('Opening DIMO…') : msg('Get credentials from DIMO')}
                </button>

                <div class="divider">${msg('or enter manually')}</div>

                <label for="name">${msg('Fleet name')}</label>
                <input
                    id="name"
                    .value=${this.name}
                    @input=${(e: Event) => (this.name = (e.target as HTMLInputElement).value)}
                    placeholder="${msg('My Fleet (optional)')}"
                />

                <label for="clientId">${msg('DIMO client ID')}</label>
                <input
                    id="clientId"
                    .value=${this.clientId}
                    @input=${(e: Event) => (this.clientId = (e.target as HTMLInputElement).value)}
                    placeholder="0x…"
                    autocomplete="off"
                />

                <label for="apiKey">${msg('DIMO API key')}</label>
                <div class="api-key-row">
                    <input
                        id="apiKey"
                        type="password"
                        .value=${this.apiKey}
                        @input=${(e: Event) => (this.apiKey = (e.target as HTMLInputElement).value)}
                        placeholder="${msg('developer API key')}"
                        autocomplete="off"
                    />
                    ${this.apiKey ? html`
                        <button type="button" class="copy-inline-btn" title="${msg('Copy API key')}" @click=${this.copyApiKey}>
                            <span class="material-symbols-outlined">content_copy</span>
                        </button>
                    ` : ''}
                </div>
                <div class="hint">${msg(`Stored encrypted; used to read your fleet's vehicles and telemetry.`)}</div>

                ${this.showCopyBanner ? html`
                    <div class="copy-banner">
                        <span class="material-symbols-outlined">warning</span>
                        <div class="copy-banner-text">
                            ${msg('Save your API key now — it cannot be retrieved after this step.')}
                            <br />
                            <button type="button" class="copy-banner-btn" @click=${this.copyApiKey}>
                                <span class="material-symbols-outlined">content_copy</span>
                                ${msg('Copy API key')}
                            </button>
                        </div>
                    </div>
                ` : ''}

                ${this.error ? html`<div class="error">${this.error}</div>` : ''}

                <button type="submit" ?disabled=${this.submitting}>
                    ${this.submitting ? msg('Creating…') : msg('Create fleet')}
                </button>
            </form>
        `;
    }
}

// API errors arrive as a JSON body string (`{"code":400,"message":"…"}`).
// Surface just the human message.
function extractMessage(err: unknown): string {
    const raw = err instanceof ApiError ? err.message : err instanceof Error ? err.message : '';
    try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed.message === 'string') return parsed.message;
    } catch {
        // not JSON — fall through
    }
    return raw;
}

declare global {
    interface HTMLElementTagNameMap {
        'onboard-tenant-view': OnboardTenantView;
    }
}
