import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { JobTimeoutError, SharingService } from '../services/sharing-service.ts';
import { shortWallet } from '../utils/share-blocker.ts';

/** One existing on-chain grant, read back from identity-api. */
interface ExistingShare {
    grantee: string;
    expiresAt: string;
}

/**
 * Drop the grants that are no longer in force.
 *
 * Revoking does not delete the SACD record — it writes a *zeroed* one, with
 * permissions 0 and expiration 0 — so identity-api keeps returning the grantee
 * afterwards, with an expiry at the Unix epoch. Without this filter a
 * successful revoke leaves the row exactly where it was and the feature reads
 * as "revoke doesn't work".
 *
 * It is the right list for shares that simply ran out, too. An expired grant is
 * not a current grant, and this list is headed "Already shared with".
 *
 * A missing or unparseable expiresAt is KEPT: formatExpiry reads that as "no
 * expiry", and hiding a live indefinite grant because one field was malformed
 * is much the worse mistake — it would tell somebody nobody has access when
 * somebody does.
 */
function liveGrants(nodes: ExistingShare[]): ExistingShare[] {
    const now = Date.now();
    return nodes.filter((n) => {
        if (!n?.grantee) return false;
        if (!n.expiresAt) return true;
        const when = new Date(n.expiresAt).getTime();
        return Number.isNaN(when) || when > now;
    });
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
 * It also opens for vehicles that cannot be shared, with blockedReason set. That
 * is the whole point of the blocked mode: the fleet list used to gate the icon
 * and hide the reason in a tooltip, so the answer to "why can't I share this
 * one?" was unreachable on touch and easy to miss anywhere else.
 *
 * Props:
 *   - tokenId: the vehicle being shared.
 *   - vehicleTitle: for the heading.
 *   - blockedReason: why sharing is unavailable; empty when it is available.
 *   - owner: the owner wallet the caller already knows, as a fallback.
 *   - myWallet: the signed-in wallet, to mark the owner as the reader.
 * Events:
 *   - close: dismissed.
 *   - shared: a grant landed on chain; the caller may want to refetch.
 *   - revoked: a grant was withdrawn on chain; likewise.
 */
@customElement('share-vehicle-modal')
export class ShareVehicleModal extends LitElement {
    @property({ type: Number }) tokenId!: number;
    @property({ type: String }) vehicleTitle = '';
    /**
     * Why sharing is unavailable, already localized, or empty when it is
     * available. The caller composes it (shareBlockReason) so the row tooltip
     * and this banner are literally the same sentence and cannot drift.
     */
    @property({ type: String }) blockedReason = '';
    /** Owner wallet as the caller knows it — used until the chain answers. */
    @property({ type: String }) owner = '';
    @property({ type: String }) myWallet = '';

    @state() private grantee = '';
    @state() private durationDays = 365;
    @state() private submitting = false;
    @state() private errorMessage = '';
    @state() private successMessage = '';
    /**
     * Neither an error nor a success — "we stopped waiting and do not know".
     * It has its own slot because collapsing it into either of the other two
     * would state an outcome nobody has observed.
     */
    @state() private noticeMessage = '';
    @state() private existing: ExistingShare[] = [];
    @state() private loadingExisting = true;
    /**
     * The grantee whose row is asking "Revoke?", or empty. Confirmation is
     * inline and in-component on purpose: `confirm()` blocks the page and, in a
     * modal that is already polling a job, a blocking dialog is the wrong tool.
     * One at a time — arming a second row disarms the first.
     */
    @state() private confirmingRevoke = '';
    /** The grantee whose revoke is in flight, or empty. */
    @state() private revoking = '';
    /** Owner as identity-api reports it; empty until it answers, or if it fails. */
    @state() private chainOwner = '';

    /** Sharing can't proceed, so every control that would attempt it is off. */
    private get blocked(): boolean {
        return this.blockedReason.length > 0;
    }

    /**
     * The owner to show. The chain is authoritative — a transfer lands there
     * before it reaches the card the caller built — but it arrives a round trip
     * late and may not arrive at all, and an owner row that blinks in late is
     * still better than one that blocks the modal.
     */
    private get ownerAddress(): string {
        return this.chainOwner || this.owner;
    }

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
        void this.loadFromChain();
    }

    /**
     * Read the vehicle's owner and current grants from identity-api, through
     * the same proxy the rest of the app uses.
     *
     * Chain state is the source of truth here — nothing is read back from our
     * own database, which never records a share. Best-effort: failing to list
     * existing grants must not stop somebody making a new one, and a blocked
     * modal still runs it because the owner is most of the explanation.
     *
     * One query for both fields: they come from the same node, and a blocked
     * vehicle would otherwise pay two round trips to render one address.
     */
    private async loadFromChain() {
        this.loadingExisting = true;
        const query = `{
            vehicle(tokenId: ${this.tokenId}) {
                owner
                sacds(first: 15) {
                    nodes { grantee expiresAt }
                }
            }
        }`;
        try {
            const res = await ApiService.getInstance().post<{
                data?: { vehicle?: { owner?: string; sacds?: { nodes?: ExistingShare[] } } };
            }>('/identity/proxy', { query });
            this.existing = liveGrants(res.data?.vehicle?.sacds?.nodes ?? []);
            this.chainOwner = res.data?.vehicle?.owner ?? '';
        } catch {
            this.existing = [];
            // chainOwner stays empty so the caller's value keeps rendering.
        } finally {
            this.loadingExisting = false;
        }
    }

    /**
     * Withdraw one grant, then re-read the list from chain.
     *
     * The confirm step is spent by getting here: a second click on an armed row
     * is the confirmation, and this is what it runs.
     */
    private async revoke(grantee: string) {
        // One job at a time. The controls are disabled to match, so this is the
        // second lock rather than the only one.
        if (this.revoking || this.submitting || this.blocked) return;

        this.revoking = grantee;
        this.confirmingRevoke = '';
        this.errorMessage = '';
        this.successMessage = '';
        this.noticeMessage = '';
        try {
            const svc = SharingService.getInstance();
            const jobId = await svc.revoke(this.tokenId, grantee);
            await svc.waitForRevoke(this.tokenId, jobId);

            this.successMessage = msg('Access revoked.');
            this.dispatchEvent(new CustomEvent('revoked', { bubbles: true, composed: true }));
            await this.loadFromChain();
        } catch (err) {
            if (err instanceof JobTimeoutError) {
                // We stopped waiting; the chain did not stop working. Saying
                // "revoked" here would be a claim about a job still running,
                // and saying "failed" would have somebody re-issue a grant they
                // have already withdrawn. The list is re-read either way — it
                // is the honest answer to "did it land?" and it costs one query.
                this.noticeMessage = err.message;
                await this.loadFromChain();
            } else {
                this.errorMessage =
                    err instanceof Error ? err.message : msg('The access could not be revoked.');
            }
        } finally {
            this.revoking = '';
        }
    }

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private async submit() {
        // The button is disabled while blocked; this is the second lock, so a
        // stale blockedReason update can't leave a live share behind it.
        if (!this.granteeIsValid || this.submitting || this.blocked) return;

        this.submitting = true;
        this.errorMessage = '';
        this.successMessage = '';
        this.noticeMessage = '';
        this.confirmingRevoke = '';
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
            await this.loadFromChain();
        } catch (err) {
            this.errorMessage = err instanceof Error ? err.message : msg('The share could not be completed.');
        } finally {
            this.submitting = false;
        }
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
                /* The height cap is only half a constraint without this: anything
                   the body cannot fit has to be clipped to the rounded box, not
                   painted over the page behind it. */
                overflow: hidden;
            }
            /* Fixed furniture. The close button and Close/Share are the ways out
               of this modal, so they are never somewhere you have to scroll to
               find, however many grants the vehicle already has. */
            .head, .foot { flex: none; }
            /* The one scrolling region. min-height:0 is what lets it give: a
               column flex item won't shrink below its own content by default,
               which is how the shared-with rows used to push the footer out
               through the bottom of the card. The negative margin bleeds it to
               the card's edge and the padding puts the gutter back, so the
               scrollbar rides the border and focus rings still have room. */
            .body {
                flex: 1 1 auto; min-height: 0; overflow-y: auto;
                margin: 0 -24px; padding: 0 24px;
            }
            .card h2 { font: var(--type-headline-md); margin-bottom: 4px; }
            /* The treatment every other secondary identifier in the app already
               gets: --type-label-caps is the mono face, so this matches the
               owner row's label here and the token line under a vehicle row
               elsewhere without inventing a style for it. */
            .card .token-id {
                font: var(--type-label-caps); letter-spacing: 0.05em;
                text-transform: uppercase; color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 20px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                background: none; border: none; color: var(--on-surface-variant); padding: 4px; cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            .owner-row {
                display: flex; align-items: center; justify-content: space-between; gap: 12px;
                padding: 8px 10px; margin-bottom: 20px;
                background: var(--surface-container-low); border-radius: var(--radius-md);
                font: var(--type-body-sm);
            }
            .owner-row .lbl {
                font: var(--type-label-caps); letter-spacing: 0.05em;
                text-transform: uppercase; color: var(--on-surface-variant);
            }
            .owner-row .who { font-family: monospace; }

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
            /* Same treatment the confirm button already had, so a blocked modal
               reads as one form that is uniformly off rather than a mix of live
               and dead controls. */
            input[type='text']:disabled,
            .durations button:disabled { opacity: 0.5; cursor: not-allowed; }
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
            /* No height cap of its own. The card body scrolls now, and a second
               scroller nested in the first only fights it for the wheel and
               hides grants behind a scrollbar nobody goes looking for. */
            .existing ul { list-style: none; display: flex; flex-direction: column; gap: 6px; }
            .existing li {
                display: flex; align-items: center; justify-content: space-between;
                gap: 12px; padding: 8px 10px;
                background: var(--surface-container-low); border-radius: var(--radius-md);
                font: var(--type-body-sm);
            }
            /* The address is the row's identity, so it is the part that gives
               when the row is tight — the expiry and the control keep their
               size and the wallet ellipsises. It is already abbreviated and
               carries the full value in its title. */
            .existing li .who { font-family: monospace; flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; }
            .existing li .when { color: var(--on-surface-variant); flex: none; }
            .existing .empty { color: var(--on-surface-variant); font: var(--type-body-sm); }

            /* Revoke lives in the row it acts on. Two steps, both here: the
               first click arms the row, the second runs it. Nothing about it
               leaves the list, so there is never a dialog on top of a dialog
               and never a question about which grant is being withdrawn. */
            .existing li .act { flex: none; display: flex; align-items: center; gap: 6px; }
            .existing li .act button {
                padding: 3px 8px; cursor: pointer; font: var(--type-body-sm);
                background: transparent; color: var(--on-surface-variant);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-sm);
            }
            .existing li .act button:hover:not(:disabled) { color: var(--on-surface); border-color: var(--on-surface-variant); }
            .existing li .act button:disabled { opacity: 0.5; cursor: not-allowed; }
            /* The destructive half of the confirm is the only thing here that
               wears the error colour — the arming click is not destructive and
               must not read as though it were. */
            .existing li .act button.danger { color: var(--error); border-color: rgba(255, 180, 171, 0.4); }
            .existing li .act button.danger:hover:not(:disabled) { border-color: var(--error); }
            .existing li .ask { color: var(--error); flex: none; }
            /* Disabled-and-greyed says "you can't", not "it's working". The
               pulse is what distinguishes a job in flight from a control that
               is merely off, and it stops for anyone who has asked motion to. */
            .existing li .act button.busy { opacity: 1; animation: revoke-pulse 1.4s ease-in-out infinite; }
            @keyframes revoke-pulse {
                0%, 100% { opacity: 0.45; }
                50% { opacity: 1; }
            }
            @media (prefers-reduced-motion: reduce) {
                .existing li .act button.busy { animation: none; opacity: 0.7; }
            }

            .banner {
                padding: 12px; border-radius: var(--radius-md);
                font: var(--type-body-sm); margin-top: 16px;
            }
            .banner.error {
                background: rgba(255, 180, 171, 0.04);
                border: 1px solid rgba(255, 180, 171, 0.2); color: var(--error);
            }
            /* A blocked reason is a precondition, not the outcome of pressing
               anything, so it is read before the form instead of in the
               submit-error slot down by the footer. */
            .banner.lead { margin-top: 0; margin-bottom: 20px; }
            .banner.success {
                background: rgba(140, 255, 180, 0.04);
                border: 1px solid rgba(140, 255, 180, 0.2); color: var(--on-surface);
            }
            /* "We stopped waiting" is not an outcome. It is deliberately neither
               red nor green — either colour would answer a question that is
               still open. */
            .banner.notice {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant); color: var(--on-surface-variant);
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

    /**
     * The owner line, or nothing at all. An "Owner: —" row while the lookup is
     * in flight would read as "this vehicle has no owner", which is one of the
     * blocked reasons and would contradict the banner beside it.
     */
    private renderOwner() {
        const address = this.ownerAddress;
        if (!address) return nothing;

        const short = shortWallet(address);
        const isMine = !!this.myWallet && address.toLowerCase() === this.myWallet.toLowerCase();
        return html`
            <div class="owner-row">
                <span class="lbl">${msg('Owner')}</span>
                <span class="who" title=${address}>${isMine ? msg(str`${short} (you)`) : short}</span>
            </div>
        `;
    }

    /**
     * One row of "Already shared with", in whichever of its three states it is
     * in: at rest, armed, or running.
     *
     * Armed keeps the address on screen and replaces only the expiry, because
     * the question "revoke this?" is unanswerable if you can no longer see
     * which grant "this" is. Cancel is listed after Yes but is also what the
     * row falls back to — clicking Revoke on another row disarms this one.
     */
    private renderGrant(s: ExistingShare) {
        const armed = this.confirmingRevoke === s.grantee;
        const busy = this.revoking === s.grantee;
        // A share in flight, another row's revoke in flight, or a vehicle that
        // cannot be signed for at all: all three make this control a no-op, so
        // it is off rather than merely unhelpful.
        const off = this.submitting || this.blocked || (!!this.revoking && !busy);

        return html`
            <li>
                <span class="who" title=${s.grantee}>${shortWallet(s.grantee)}</span>
                ${armed && !busy
                    ? html`<span class="ask">${msg('Revoke?')}</span>`
                    : html`<span class="when">${this.formatExpiry(s.expiresAt)}</span>`}
                <span class="act">
                    ${busy
                        ? html`<button class="busy" disabled>${msg('Revoking…')}</button>`
                        : armed
                          ? html`
                                <button
                                    class="danger"
                                    ?disabled=${off}
                                    @click=${() => void this.revoke(s.grantee)}
                                >
                                    ${msg('Yes')}
                                </button>
                                <button @click=${() => (this.confirmingRevoke = '')}>
                                    ${msg('Cancel')}
                                </button>
                            `
                          : html`
                                <button
                                    ?disabled=${off}
                                    aria-label=${msg(str`Revoke access for ${shortWallet(s.grantee)}`)}
                                    @click=${() => (this.confirmingRevoke = s.grantee)}
                                >
                                    ${msg('Revoke')}
                                </button>
                            `}
                </span>
            </li>
        `;
    }

    render() {
        const showInvalid = this.grantee.trim().length > 0 && !this.granteeIsValid;
        // A revoke in flight closes the share form too. Both are the same
        // signer on the same account, and two jobs in flight against one
        // vehicle is a race the customer would have to untangle from the list.
        const inputsOff = this.submitting || this.blocked || !!this.revoking;

        return html`
            <div class="card" role="dialog" aria-modal="true" aria-label=${msg('Share vehicle')}>
                <button class="close" @click=${this.dispatchClose} aria-label=${msg('Close')}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <div class="head">
                    <h2>${msg('Share vehicle')}</h2>
                    <!-- The grant is irreversible once it is on chain, so the id
                         of the vehicle it will be written against is stated
                         before the form rather than left to the title, which a
                         fleet of identical models does not disambiguate. -->
                    <p class="token-id">${msg(str`Token #${this.tokenId}`)}</p>
                    <p class="sub">
                        ${this.vehicleTitle
                            ? msg(str`Give another wallet access to ${this.vehicleTitle}.`)
                            : msg('Give another wallet access to this vehicle.')}
                    </p>
                </div>

                <div class="body custom-scrollbar">
                    ${this.blocked
                        ? html`<div class="banner error lead" role="alert">${this.blockedReason}</div>`
                        : nothing}
                    ${this.renderOwner()}

                    <label for="grantee">${msg('Wallet address')}</label>
                    <input
                        id="grantee"
                        type="text"
                        class=${showInvalid ? 'invalid' : ''}
                        placeholder="0x…"
                        .value=${this.grantee}
                        ?disabled=${inputsOff}
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
                                    ?disabled=${inputsOff}
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
                                        ${this.existing.map((s) => this.renderGrant(s))}
                                    </ul>
                                `}
                    </div>
                </div>

                <!-- The submit outcome rides with the buttons, not with the scroll:
                     an error is no use reported somewhere the grants list has
                     already pushed out of sight. -->
                <div class="foot">
                    ${this.errorMessage
                        ? html`<div class="banner error">${this.errorMessage}</div>`
                        : nothing}
                    ${this.successMessage
                        ? html`<div class="banner success">${this.successMessage}</div>`
                        : nothing}
                    ${this.noticeMessage
                        ? html`<div class="banner notice">${this.noticeMessage}</div>`
                        : nothing}

                    <div class="footer">
                        <button class="cancel" ?disabled=${this.submitting || !!this.revoking} @click=${this.dispatchClose}>
                            ${msg('Close')}
                        </button>
                        <button
                            class="confirm"
                            ?disabled=${!this.granteeIsValid || inputsOff}
                            @click=${this.submit}
                        >
                            ${this.submitting ? msg('Sharing…') : msg('Share')}
                        </button>
                    </div>
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
