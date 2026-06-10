import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService, ApiError } from '../services/api-service.ts';
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
            button {
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
            button[disabled] {
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
                <input
                    id="apiKey"
                    type="password"
                    .value=${this.apiKey}
                    @input=${(e: Event) => (this.apiKey = (e.target as HTMLInputElement).value)}
                    placeholder="${msg('developer API key')}"
                    autocomplete="off"
                />
                <div class="hint">${msg(`Stored encrypted; used to read your fleet's vehicles and telemetry.`)}</div>

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
