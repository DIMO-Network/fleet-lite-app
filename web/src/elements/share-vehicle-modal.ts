import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { SharingService } from '../services/sharing-service.ts';

/** One existing on-chain grant, read back from identity-api. */
interface ExistingShare {
    grantee: string;
    expiresAt: string;
}

/**
 * How long a share lasts. Zero days means indefinite, which SACD expresses as
 * forty years — it has no never-expires value, and the onboarding mint uses the
 * same convention.
 */
interface DurationOption {
    days: number;
    label: string;
}

/**
 * share-vehicle-modal — grant a wallet on-chain access to one vehicle.
 *
 * This is SACD sharing: a permission grant against the vehicle NFT, signed
 * server-side by the operator's signer on the owner's kernel account. The owner
 * keeps the vehicle and never signs anything.
 *
 * Not to be confused with the tracking links in b2b-fleet-mgr-app's
 * "Share Vehicle Tracking" — that is a time-boxed public URL and has nothing to
 * do with on-chain permissions. The naming has caught people out before.
 *
 * The permission set is fixed (everything except approximate location, which is
 * redundant next to the precise location already granted) — there is no
 * per-permission picker in this version.
 *
 * Props:
 *   - tokenId: the vehicle being shared.
 *   - vehicleTitle: for the heading.
 * Events:
 *   - close: dismissed.
 *   - shared: a grant landed on chain; the caller may want to refetch.
 */
@customElement('share-vehicle-modal')
export class ShareVehicleModal extends LitElement {
    @property({ type: Number }) tokenId!: number;
    @property({ type: String }) vehicleTitle = '';

    @state() private grantee = '';
    @state() private durationDays = 365;
    @state() private submitting = false;
    @state() private errorMessage = '';
    @state() private successMessage = '';
    @state() private existing: ExistingShare[] = [];
    @state() private loadingExisting = true;

    private get durations(): DurationOption[] {
        return [
            { days: 30, label: msg('30 days') },
            { days: 365, label: msg('1 year') },
            { days: 0, label: msg('No expiry') },
        ];
    }

    /**
     * A wallet address, checked before the button enables. Case is not
     * normalised — the backend checksums it — but the shape is, so an obvious
     * typo costs nothing instead of a round trip.
     */
    private get granteeIsValid(): boolean {
        return /^0x[0-9a-fA-F]{40}$/.test(this.grantee.trim());
    }

    connectedCallback() {
        super.connectedCallback();
        void this.loadExisting();
    }

    /**
     * Read the vehicle's current grants from identity-api, through the same
     * proxy the rest of the app uses.
     *
     * Chain state is the source of truth here — nothing is read back from our
     * own database, which never records a share. Best-effort: failing to list
     * existing grants must not stop somebody making a new one.
     */
    private async loadExisting() {
        this.loadingExisting = true;
        const query = `{
            vehicle(tokenId: ${this.tokenId}) {
                sacds(first: 15) {
                    nodes { grantee expiresAt }
                }
            }
        }`;
        try {
            const res = await ApiService.getInstance().post<{
                data?: { vehicle?: { sacds?: { nodes?: ExistingShare[] } } };
            }>('/identity/proxy', { query });
            this.existing = res.data?.vehicle?.sacds?.nodes ?? [];
        } catch {
            this.existing = [];
        } finally {
            this.loadingExisting = false;
        }
    }

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private async submit() {
        if (!this.granteeIsValid || this.submitting) return;

        this.submitting = true;
        this.errorMessage = '';
        this.successMessage = '';
        try {
            const svc = SharingService.getInstance();
            const jobId = await svc.share(this.tokenId, this.grantee.trim(), this.durationDays);
            await svc.waitForShare(this.tokenId, jobId);

            this.successMessage = msg('Shared. The grant is on chain.');
            this.grantee = '';
            this.dispatchEvent(new CustomEvent('shared', { bubbles: true, composed: true }));

            // Identity-api indexes from chain and can lag the receipt by a few
            // seconds, so this refresh may not show the new grant yet. The
            // manual refresh below is the answer to that rather than polling
            // identity-api until it catches up.
            await this.loadExisting();
        } catch (err) {
            this.errorMessage = err instanceof Error ? err.message : msg('The share could not be completed.');
        } finally {
            this.submitting = false;
        }
    }

    private shortAddress(address: string): string {
        return address.length > 12 ? `${address.slice(0, 6)}…${address.slice(-4)}` : address;
    }

    /**
     * SACD expirations are set forty years out to mean "indefinite", so a date
     * far enough away is rendered as no expiry rather than as the year 2066.
     */
    private formatExpiry(expiresAt: string): string {
        if (!expiresAt) return msg('No expiry');
        const when = new Date(expiresAt);
        if (Number.isNaN(when.getTime())) return msg('No expiry');

        const tenYears = new Date();
        tenYears.setFullYear(tenYears.getFullYear() + 10);
        if (when > tenYears) return msg('No expiry');

        return msg(str`Until ${when.toLocaleDateString()}`);
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                position: fixed; inset: 0; z-index: 100;
                display: flex; align-items: center; justify-content: center;
                background: rgba(0, 0, 0, 0.6); backdrop-filter: blur(4px);
            }
            .card {
                width: 100%; max-width: 480px; max-height: 80vh;
                background: var(--surface-container); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg); padding: 24px; color: var(--on-surface);
                position: relative; display: flex; flex-direction: column;
            }
            .card h2 { font: var(--type-headline-md); margin-bottom: 4px; }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 20px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                background: none; border: none; color: var(--on-surface-variant); padding: 4px; cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            label {
                display: block; font: var(--type-label-caps); letter-spacing: 0.05em;
                text-transform: uppercase; color: var(--on-surface-variant); margin-bottom: 8px;
            }
            input[type='text'] {
                width: 100%; box-sizing: border-box;
                background: var(--surface-container-low); color: var(--on-surface);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                padding: 10px 12px; font-family: monospace; font-size: 13px;
            }
            input[type='text']:focus { outline: 1px solid var(--primary); }
            input[type='text'].invalid { border-color: var(--error); }
            .hint { font: var(--type-body-sm); color: var(--on-surface-variant); margin-top: 6px; }
            .hint.bad { color: var(--error); }

            .durations { display: flex; gap: 8px; margin: 20px 0 4px; }
            .durations button {
                flex: 1; padding: 10px; cursor: pointer;
                background: var(--surface-container-low); color: var(--on-surface-variant);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                font: var(--type-body-sm);
            }
            .durations button.selected {
                background: var(--primary); color: var(--on-primary); border-color: var(--primary);
            }

            .existing { margin-top: 20px; border-top: 1px solid var(--outline-variant); padding-top: 16px; }
            .existing h3 {
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase;
                color: var(--on-surface-variant); margin-bottom: 10px;
            }
            .existing ul { list-style: none; display: flex; flex-direction: column; gap: 6px; max-height: 140px; overflow-y: auto; }
            .existing li {
                display: flex; justify-content: space-between; gap: 12px; padding: 8px 10px;
                background: var(--surface-container-low); border-radius: var(--radius-md);
                font: var(--type-body-sm);
            }
            .existing li .who { font-family: monospace; }
            .existing li .when { color: var(--on-surface-variant); }
            .existing .empty { color: var(--on-surface-variant); font: var(--type-body-sm); }

            .banner {
                padding: 12px; border-radius: var(--radius-md);
                font: var(--type-body-sm); margin-top: 16px;
            }
            .banner.error {
                background: rgba(255, 180, 171, 0.04);
                border: 1px solid rgba(255, 180, 171, 0.2); color: var(--error);
            }
            .banner.success {
                background: rgba(140, 255, 180, 0.04);
                border: 1px solid rgba(140, 255, 180, 0.2); color: var(--on-surface);
            }

            .footer { display: flex; justify-content: flex-end; gap: 10px; margin-top: 20px; }
            .footer button {
                padding: 10px 18px; border-radius: var(--radius-md); cursor: pointer;
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700;
                border: 1px solid transparent;
            }
            .footer .cancel {
                background: transparent; color: var(--on-surface-variant); border-color: var(--outline-variant);
            }
            .footer .confirm { background: var(--primary); color: var(--on-primary); }
            .footer .confirm:disabled { opacity: 0.5; cursor: not-allowed; }
        `,
    ];

    render() {
        const showInvalid = this.grantee.trim().length > 0 && !this.granteeIsValid;

        return html`
            <div class="card" role="dialog" aria-modal="true" aria-label=${msg('Share vehicle')}>
                <button class="close" @click=${this.dispatchClose} aria-label=${msg('Close')}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2>${msg('Share vehicle')}</h2>
                <p class="sub">
                    ${this.vehicleTitle
                        ? msg(str`Give another wallet access to ${this.vehicleTitle}.`)
                        : msg('Give another wallet access to this vehicle.')}
                </p>

                <label for="grantee">${msg('Wallet address')}</label>
                <input
                    id="grantee"
                    type="text"
                    class=${showInvalid ? 'invalid' : ''}
                    placeholder="0x…"
                    .value=${this.grantee}
                    ?disabled=${this.submitting}
                    @input=${(e: Event) => (this.grantee = (e.target as HTMLInputElement).value)}
                />
                <p class="hint ${showInvalid ? 'bad' : ''}">
                    ${showInvalid
                        ? msg('That does not look like a wallet address.')
                        : msg('They will be able to see this vehicle’s data and send commands to it.')}
                </p>

                <label style="margin-top:20px">${msg('Access expires')}</label>
                <div class="durations">
                    ${this.durations.map(
                        (d) => html`
                            <button
                                class=${this.durationDays === d.days ? 'selected' : ''}
                                ?disabled=${this.submitting}
                                @click=${() => (this.durationDays = d.days)}
                            >
                                ${d.label}
                            </button>
                        `,
                    )}
                </div>

                <div class="existing">
                    <h3>${msg('Already shared with')}</h3>
                    ${this.loadingExisting
                        ? html`<p class="empty">${msg('Loading…')}</p>`
                        : this.existing.length === 0
                          ? html`<p class="empty">${msg('Nobody yet.')}</p>`
                          : html`
                                <ul>
                                    ${this.existing.map(
                                        (s) => html`
                                            <li>
                                                <span class="who" title=${s.grantee}>${this.shortAddress(s.grantee)}</span>
                                                <span class="when">${this.formatExpiry(s.expiresAt)}</span>
                                            </li>
                                        `,
                                    )}
                                </ul>
                            `}
                </div>

                ${this.errorMessage
                    ? html`<div class="banner error">${this.errorMessage}</div>`
                    : nothing}
                ${this.successMessage
                    ? html`<div class="banner success">${this.successMessage}</div>`
                    : nothing}

                <div class="footer">
                    <button class="cancel" ?disabled=${this.submitting} @click=${this.dispatchClose}>
                        ${msg('Close')}
                    </button>
                    <button
                        class="confirm"
                        ?disabled=${!this.granteeIsValid || this.submitting}
                        @click=${this.submit}
                    >
                        ${this.submitting ? msg('Sharing…') : msg('Share')}
                    </button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'share-vehicle-modal': ShareVehicleModal;
    }
}
