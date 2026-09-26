import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService, ApiError } from '../services/api-service.ts';
import { SettingsService } from '../services/settings-service.ts';
import { logout } from '../utils/token.ts';
import { themeService } from '../services/theme-service.ts';

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
                display: block;
                width: 100%;
                height: 100vh;
                height: 100dvh;
                overflow: auto;
                background: var(--canvas);
                color: var(--on-surface);
            }
            .stage {
                position: relative;
                isolation: isolate;
                overflow: hidden;
                min-height: 100%;
                display: flex;
                flex-direction: column;
            }
            .topbar {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: var(--stack-md);
                height: var(--top-bar-height, 72px);
                padding: 0 var(--margin-desktop);
                flex-shrink: 0;
            }
            .brand {
                display: flex;
                align-items: center;
                gap: 10px;
            }
            .brand .wordmark { height: 18px; width: auto; display: block; }
            /* The gradient wordmark is drawn for dark backgrounds. */
            .brand .wordmark.on-light { filter: brightness(0) opacity(0.88); }
            .brand .product {
                font: 500 18px/1 var(--font-headline);
                color: var(--on-surface);
                letter-spacing: -0.01em;
                padding-left: 10px;
                border-left: 1px solid var(--canvas-divider);
            }
            .logout-btn {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                min-height: 36px;
                padding: 0 14px 0 12px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                font: 500 14px/20px var(--font-body);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .logout-btn:hover { background: var(--nav-hover); color: var(--on-surface); }
            .logout-btn .material-symbols-outlined { font-size: 18px; }

            .center {
                flex: 1;
                display: flex;
                align-items: center;
                justify-content: center;
                padding: var(--stack-md) var(--margin-mobile) 56px;
            }
            .hero { position: relative; width: min(460px, 100%); }
            /* Same soft sky→mint glow as the sign-in page. */
            .hero::before {
                content: '';
                position: absolute;
                z-index: -1;
                inset: -200px -300px;
                background:
                    radial-gradient(closest-side at 36% 40%, color-mix(in srgb, var(--dimo-sky) 12%, transparent), transparent),
                    radial-gradient(closest-side at 64% 62%, color-mix(in srgb, var(--dimo-mint) 10%, transparent), transparent);
                pointer-events: none;
            }
            .card {
                background: var(--glass-bg);
                -webkit-backdrop-filter: blur(24px) saturate(1.4);
                backdrop-filter: blur(24px) saturate(1.4);
                box-shadow: var(--shadow-float);
                border-radius: var(--radius-2xl);
                padding: 36px;
            }
            @media (prefers-reduced-motion: no-preference) {
                .card { animation: rise 0.5s cubic-bezier(0.2, 0.7, 0.2, 1) both; }
                @keyframes rise { from { opacity: 0; transform: translateY(8px); } }
            }
            h1 {
                font: var(--type-headline-lg);
                letter-spacing: -0.02em;
                color: var(--primary);
                margin-bottom: 8px;
            }
            p.sub {
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                margin-bottom: 28px;
            }
            .provision-btn {
                display: flex;
                align-items: center;
                justify-content: center;
                gap: 8px;
                width: 100%;
                min-height: 44px;
                padding: 0 18px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                color: var(--on-surface);
                font: 500 15px/20px var(--font-body);
                transition: background 0.15s ease, border-color 0.15s ease;
            }
            .provision-btn:hover { background: var(--surface-container-highest); border-color: var(--outline); }
            .provision-btn[disabled] { opacity: 0.6; cursor: default; }
            .provision-btn .material-symbols-outlined { font-size: 18px; color: var(--accent-ink); }
            .divider {
                display: flex;
                align-items: center;
                gap: 12px;
                margin: 24px 0 4px;
                font: var(--type-label);
                color: var(--on-surface-variant);
            }
            .divider::before, .divider::after {
                content: '';
                flex: 1;
                border-top: 1px solid var(--outline-variant);
            }
            label {
                display: block;
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin: 18px 0 6px;
            }
            input {
                display: block;
                width: 100%;
                height: 40px;
                padding: 0 12px;
                background: var(--surface-container-high);
                border: 1px solid var(--control-border);
                border-radius: var(--radius-md);
                color: var(--on-surface);
                font: var(--type-body-sm);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            input::placeholder { color: var(--on-surface-variant); opacity: 0.7; }
            input:focus,
            input:focus-visible {
                outline: none;
                border-color: var(--focus-ring);
                box-shadow: 0 0 0 3px var(--accent-soft);
            }
            .api-key-row { position: relative; }
            .api-key-row input { padding-right: 44px; }
            .copy-inline-btn {
                position: absolute;
                right: 6px;
                top: 50%;
                transform: translateY(-50%);
                display: grid;
                place-items: center;
                width: 30px;
                height: 30px;
                border-radius: var(--radius-sm);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .copy-inline-btn:hover { background: var(--surface-container-highest); color: var(--on-surface); }
            .copy-inline-btn .material-symbols-outlined { font-size: 18px; display: block; }
            .hint {
                font: var(--type-label);
                font-weight: 400;
                color: var(--on-surface-variant);
                margin-top: 8px;
            }
            .copy-banner {
                display: flex;
                align-items: flex-start;
                gap: 10px;
                margin-top: 16px;
                padding: 12px 14px;
                background: color-mix(in srgb, var(--warning) 12%, transparent);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                color: var(--on-surface);
            }
            .copy-banner .material-symbols-outlined { font-size: 18px; color: var(--warning); flex-shrink: 0; margin-top: 1px; }
            .copy-banner-text { flex: 1; }
            .copy-banner-btn {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                min-height: 30px;
                margin-top: 10px;
                padding: 0 12px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                color: var(--on-surface);
                font: 500 13px/18px var(--font-body);
                transition: background 0.15s ease;
            }
            .copy-banner-btn:hover { background: var(--surface-container-highest); }
            .copy-banner-btn .material-symbols-outlined { font-size: 14px; color: var(--on-surface-variant); margin: 0; }
            button[type="submit"] {
                display: flex;
                align-items: center;
                justify-content: center;
                width: 100%;
                min-height: 48px;
                margin-top: 28px;
                padding: 0 24px;
                border-radius: var(--radius-full);
                background: var(--brand-gradient);
                color: var(--on-accent);
                font: 600 15px/20px var(--font-body);
                transition: filter 0.15s ease, box-shadow 0.15s ease;
            }
            button[type="submit"]:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }
            button[type="submit"][disabled] {
                filter: grayscale(1) opacity(0.5);
                box-shadow: none;
                cursor: default;
            }
            .error {
                margin-top: 16px;
                padding: 10px 14px;
                border-radius: var(--radius-md);
                background: var(--error-container);
                color: var(--on-error-container);
                font: var(--type-body-sm);
            }

            @media (max-width: 768px) {
                .topbar { padding: 0 var(--margin-mobile); height: 64px; }
                .center { align-items: flex-start; padding-top: 8px; }
                .card { padding: 28px 20px 24px; border-radius: var(--radius-xl); }
                h1 { font: var(--type-headline-md); font-size: 24px; line-height: 30px; }
            }
        `,
    ];

    render() {
        return html`
            <div class="stage">
                <header class="topbar">
                    <div class="brand" role="img" aria-label="DIMO Fleet">
                        <img class="wordmark ${themeService.current === 'light' ? 'on-light' : ''}"
                            src="/assets/dimo-wordmark.png" alt="" />
                        <span class="product">Fleet</span>
                    </div>
                    <button class="logout-btn" type="button" @click=${() => logout()}>
                        <span class="material-symbols-outlined">logout</span>
                        ${msg('Log out')}
                    </button>
                </header>
                <main class="center">
                    <div class="hero">
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
                    </div>
                </main>
            </div>
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
