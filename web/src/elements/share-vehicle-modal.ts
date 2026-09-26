import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { JobTimeoutError, JobWaitAbortedError, SharingService } from '../services/sharing-service.ts';
import { shortWallet } from '../utils/share-blocker.ts';
import { lookupAppGrantees } from '../utils/app-grantees.ts';
import { sameClientId } from '../utils/dimo-permissions.ts';
import {
    extraPermissions,
    hasUnrecognisedPermissions,
    missingStandardPermissions,
    remainingShareDays,
    SacdPermission,
} from '../utils/sacd-permissions.ts';

/** One existing on-chain grant, read back from identity-api. */
interface ExistingShare {
    grantee: string;
    expiresAt: string;
    /** SACD permission mask as a hex string. */
    permissions?: string;
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
 * per-permission picker in this version. Existing grants below that set can be
 * upgraded to it in place (see upgrade()).
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
 *   - fleetLicense: the license this fleet reads its vehicles with (GET
 *     /me/access); its grant is shown as the fleet's and never offered for
 *     revoke, upgrade or re-share.
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
    /**
     * The fleet's own license. Revoking its grant would cut the fleet off the
     * vehicle, and re-sharing to it could shorten that access, so this modal
     * does neither — and the API refuses both.
     */
    @property({ type: String }) fleetLicense = '';

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
    /**
     * The grantee whose row is asking "Upgrade?", or empty. Same inline,
     * one-at-a-time confirm as revoke — and arming either disarms the other,
     * so a row never asks two questions at once.
     */
    @state() private confirmingUpgrade = '';
    /** The grantee whose upgrade is in flight, or empty. */
    @state() private upgrading = '';
    /**
     * Grantees upgraded while this modal is open (lowercased). identity-api
     * indexes from chain and can lag the job's receipt by seconds, so the
     * re-read right after an upgrade may still return the old mask; without
     * this the row would still say "Limited" and offer a second, redundant
     * upgrade. Scoped to this modal's lifetime — a later open re-reads chain.
     */
    @state() private upgraded = new Set<string>();
    /** Owner as identity-api reports it; empty until it answers, or if it fails. */
    @state() private chainOwner = '';
    /**
     * Grantees that are other apps' developer licenses (see lookupAppGrantees),
     * or null until known. An app's grant is scoped by its developer on
     * purpose: Upgrade would hand it remote commands, credentials and raw data,
     * so apps are never offered one — and while it is unknown which grantees
     * are apps, nobody is.
     */
    @state() private appGrantees: Map<string, string> | null = null;

    /**
     * Stops the status polling (not the jobs) when the modal goes away, so a
     * closed modal doesn't keep polling and re-reading chain for two minutes.
     */
    private abort = new AbortController();

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
        this.abort = new AbortController();
        void this.loadFromChain();
    }

    disconnectedCallback() {
        this.abort.abort();
        super.disconnectedCallback();
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
                sacds(first: 50) {
                    nodes { grantee permissions expiresAt }
                }
            }
        }`;
        try {
            const res = await ApiService.getInstance().post<{
                data?: { vehicle?: { owner?: string; sacds?: { nodes?: ExistingShare[] } } };
            }>('/identity/proxy', { query });
            this.existing = liveGrants(res.data?.vehicle?.sacds?.nodes ?? []);
            this.chainOwner = res.data?.vehicle?.owner ?? '';
            void this.loadAppGrantees(this.existing.map((e) => e.grantee));
        } catch {
            this.existing = [];
            // chainOwner stays empty so the caller's value keeps rendering.
        } finally {
            this.loadingExisting = false;
        }
    }

    private async loadAppGrantees(grantees: string[]) {
        try {
            this.appGrantees = await lookupAppGrantees(grantees);
        } catch {
            this.appGrantees = null;
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
        if (this.revoking || this.upgrading || this.submitting || this.blocked) return;

        this.revoking = grantee;
        this.confirmingRevoke = '';
        this.confirmingUpgrade = '';
        this.errorMessage = '';
        this.successMessage = '';
        this.noticeMessage = '';
        try {
            const svc = SharingService.getInstance();
            const jobId = await svc.revoke(this.tokenId, grantee);
            await svc.waitForRevoke(this.tokenId, jobId, this.abort.signal);

            this.successMessage = msg('Access revoked.');
            this.dispatchEvent(new CustomEvent('revoked', { bubbles: true, composed: true }));
            await this.loadFromChain();
        } catch (err) {
            if (err instanceof JobWaitAbortedError) return;
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

    /**
     * Re-share an existing grant with the standard permission set.
     *
     * There is no separate upgrade endpoint because none is needed: SACD keeps
     * one record per grantee and setPermissions overwrites it, so sharing again
     * to the same wallet replaces the old mask with the one fleet-tenancy-api
     * signs for every share. The grant's expiry is carried over — an upgrade
     * must not quietly shorten somebody's access, or make a 30-day share
     * permanent.
     */
    private async upgrade(s: ExistingShare) {
        if (this.upgrading || this.revoking || this.submitting || this.blocked) return;

        this.upgrading = s.grantee;
        this.confirmingUpgrade = '';
        this.confirmingRevoke = '';
        this.errorMessage = '';
        this.successMessage = '';
        this.noticeMessage = '';
        try {
            const svc = SharingService.getInstance();
            const jobId = await svc.share(this.tokenId, s.grantee, remainingShareDays(s.expiresAt));
            await svc.waitForShare(this.tokenId, jobId, this.abort.signal);

            this.upgraded = new Set(this.upgraded).add(s.grantee.toLowerCase());
            this.successMessage = msg('Access upgraded.');
            this.dispatchEvent(new CustomEvent('shared', { bubbles: true, composed: true }));
            await this.loadFromChain();
        } catch (err) {
            if (err instanceof JobWaitAbortedError) return;
            if (err instanceof JobTimeoutError) {
                // Same reasoning as revoke: we stopped waiting, the job did not.
                // Treat the grantee as upgraded so the row doesn't offer a
                // second share job while the first is still running.
                this.upgraded = new Set(this.upgraded).add(s.grantee.toLowerCase());
                this.noticeMessage = err.message;
                await this.loadFromChain();
            } else {
                this.errorMessage =
                    err instanceof Error ? err.message : msg('The upgrade could not be completed.');
            }
        } finally {
            this.upgrading = '';
        }
    }

    /** Names for the permissions a grant can be missing, in the customer's terms. */
    private permissionLabel(p: SacdPermission): string {
        switch (p) {
            case SacdPermission.NonLocationTelemetry: return msg('vehicle data');
            case SacdPermission.Commands: return msg('remote commands');
            case SacdPermission.CurrentLocation: return msg('current location');
            case SacdPermission.AllTimeLocation: return msg('location history');
            case SacdPermission.Credentials: return msg('vehicle credentials');
            case SacdPermission.Streams: return msg('live streams');
            case SacdPermission.RawData: return msg('raw data');
            default: return msg('approximate location');
        }
    }

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private async submit() {
        // The button is disabled while blocked; this is the second lock, so a
        // stale blockedReason update can't leave a live share behind it.
        if (!this.granteeIsValid || this.submitting || this.upgrading || this.revoking || this.blocked) return;

        this.submitting = true;
        this.errorMessage = '';
        this.successMessage = '';
        this.noticeMessage = '';
        this.confirmingRevoke = '';
        this.confirmingUpgrade = '';
        try {
            const svc = SharingService.getInstance();
            const jobId = await svc.share(this.tokenId, this.grantee.trim(), this.durationDays);
            await svc.waitForShare(this.tokenId, jobId, this.abort.signal);

            this.successMessage = msg('Shared. The grant is on chain.');
            this.grantee = '';
            this.dispatchEvent(new CustomEvent('shared', { bubbles: true, composed: true }));

            // Identity-api indexes from chain and can lag the receipt by a few
            // seconds, so this refresh may not show the new grant yet. The
            // manual refresh below is the answer to that rather than polling
            // identity-api until it catches up.
            await this.loadFromChain();
        } catch (err) {
            if (err instanceof JobWaitAbortedError) return;
            if (err instanceof JobTimeoutError) {
                // Unknown, not failed — the same reasoning as revoke and
                // upgrade. The list is the honest answer to "did it land?".
                this.noticeMessage = err.message;
                await this.loadFromChain();
            } else {
                this.errorMessage = err instanceof Error ? err.message : msg('The share could not be completed.');
            }
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
                background: var(--scrim);
                backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px);
            }
            .card {
                width: calc(100% - 32px); max-width: 480px; max-height: 80vh;
                background: var(--surface-overlay);
                border-radius: var(--radius-xl); box-shadow: var(--shadow-float);
                padding: 24px; color: var(--on-surface);
                position: relative; display: flex; flex-direction: column;
                /* The height cap is only half a constraint without this: anything
                   the body cannot fit has to be clipped to the rounded box, not
                   painted over the page behind it. */
                overflow: hidden;
                animation: modal-in 0.18s ease-out;
            }
            @keyframes modal-in {
                from { opacity: 0; transform: translateY(8px) scale(0.98); }
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
            .card h2 {
                font: var(--type-headline-md); letter-spacing: -0.01em;
                color: var(--primary); padding-right: 40px; margin-bottom: 2px;
            }
            /* The same secondary-identifier treatment the token line under a
               vehicle row gets elsewhere, so it reads as meta, not as a title. */
            .card .token-id {
                font: var(--type-label); color: var(--on-surface-variant);
                margin-bottom: 8px;
            }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 20px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                width: 32px; height: 32px; padding: 0;
                display: inline-flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full); color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close .material-symbols-outlined { font-size: 20px; }
            .close:hover:not(:disabled) { background: var(--surface-container-high); color: var(--on-surface); }
            /* Off while a job runs, like the footer Close: closing mid-job left
               nobody watching it and reopened the modal with its guards reset. */
            .close:disabled { opacity: 0.4; cursor: not-allowed; }

            .owner-row {
                display: flex; align-items: center; justify-content: space-between; gap: 12px;
                min-height: 44px; padding: 0 14px; margin-bottom: 20px;
                background: var(--surface-container); border-radius: var(--radius-md);
                font: var(--type-body-sm);
            }
            .owner-row .lbl { font: var(--type-label); color: var(--on-surface-variant); }
            .owner-row .who { color: var(--on-surface); font-weight: 500; }

            label {
                display: block; font: var(--type-label); color: var(--on-surface-variant); margin-bottom: 8px;
            }
            input[type='text'] {
                width: 100%; height: 40px; padding: 0 12px;
                background-color: var(--surface-container-high); color: var(--on-surface);
                border: 1px solid var(--control-border); border-radius: var(--radius-md);
                font: var(--type-body-sm);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            input[type='text']::placeholder { color: var(--on-surface-variant); }
            input[type='text']:hover:not(:disabled):not(:focus-visible) { border-color: var(--control-border-hover); }
            input[type='text']:focus-visible {
                outline: none; border-color: var(--focus-ring); box-shadow: 0 0 0 3px var(--accent-soft);
            }
            input[type='text'].invalid,
            input[type='text'].invalid:hover { border-color: var(--error); }
            /* Same treatment the confirm button already had, so a blocked modal
               reads as one form that is uniformly off rather than a mix of live
               and dead controls. */
            input[type='text']:disabled,
            .durations button:disabled { opacity: 0.5; cursor: not-allowed; }
            .hint { font: var(--type-body-sm); color: var(--on-surface-variant); margin-top: 8px; }
            .hint.bad { color: var(--error); }
            .hint.warn { color: var(--warning); }

            .durations { display: flex; gap: 8px; margin: 0 0 4px; }
            .durations button {
                flex: 1; min-height: 40px; padding: 0 12px;
                background: var(--surface-container-high); color: var(--on-surface-variant);
                border-radius: var(--radius-md);
                font: 500 14px/20px var(--font-body);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .durations button:hover:not(:disabled):not(.selected) {
                background: var(--surface-container-highest); color: var(--on-surface);
            }
            .durations button.selected {
                background: var(--selected-bg); color: var(--selected-fg);
            }

            .existing { margin-top: 28px; }
            .existing h3 {
                font: var(--type-label); color: var(--on-surface-variant); margin-bottom: 8px;
            }
            /* No height cap of its own. The card body scrolls now, and a second
               scroller nested in the first only fights it for the wheel and
               hides grants behind a scrollbar nobody goes looking for. */
            .existing ul { list-style: none; display: flex; flex-direction: column; gap: 4px; }
            .existing li {
                display: flex; align-items: center; justify-content: space-between;
                gap: 12px; min-height: 48px; padding: 0 8px 0 14px;
                background: var(--surface-container); border-radius: var(--radius-md);
                font: var(--type-body-sm);
            }
            /* The address is the row's identity, so it is the part that gives
               when the row is tight — the expiry and the control keep their
               size and the wallet ellipsises. It is already abbreviated and
               carries the full value in its title. */
            .existing li .who {
                font-weight: 500; color: var(--on-surface);
                flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
            }
            .existing li .when { color: var(--on-surface-variant); flex: none; }
            .existing .empty { color: var(--on-surface-variant); font: var(--type-body-sm); }

            /* Revoke lives in the row it acts on. Two steps, both here: the
               first click arms the row, the second runs it. Nothing about it
               leaves the list, so there is never a dialog on top of a dialog
               and never a question about which grant is being withdrawn. */
            .existing li .act { flex: none; display: flex; align-items: center; gap: 6px; }
            .existing li .act button {
                min-height: 32px; padding: 0 12px; font: 500 13px/18px var(--font-body);
                background: var(--surface-container-high); color: var(--on-surface);
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .existing li .act button:hover:not(:disabled) { background: var(--surface-container-highest); }
            .existing li .act button:disabled { opacity: 0.5; cursor: not-allowed; }
            /* The destructive half of the confirm is the only thing here that
               wears the error colour — the arming click is not destructive and
               must not read as though it were. */
            .existing li .act button.danger { background: var(--error-container); color: var(--error); }
            .existing li .act button.danger:hover:not(:disabled) {
                background: color-mix(in srgb, var(--error) 18%, var(--error-container));
            }
            .existing li .ask { color: var(--error); font-weight: 500; flex: none; }
            .existing li .ask.up { color: var(--on-surface); }

            /* A grant below the standard set says so on its own line, in words:
               a tooltip would be unreachable on touch, and "limited" alone
               does not say what the grantee cannot do. */
            .existing li.limited { flex-wrap: wrap; padding-top: 8px; padding-bottom: 10px; row-gap: 2px; }
            .existing li .missing {
                flex-basis: 100%; order: 3;
                font: var(--type-label); color: var(--warning);
            }
            /* While asking, the line explains what Yes grants — that is a
               statement, not a warning, so it drops the warning colour. */
            .existing li .missing.confirm,
            .existing li .missing.note { color: var(--on-surface-variant); }
            .existing li .act button.go {
                background: var(--btn-primary-bg); color: var(--btn-primary-fg); font-weight: 600;
            }
            .existing li .act button.go:hover:not(:disabled) { background: var(--btn-primary-hover); }
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
                padding: 12px 14px; border-radius: var(--radius-md);
                font: var(--type-body-sm); margin-top: 16px;
            }
            .banner.error { background: var(--error-container); color: var(--error); }
            /* A blocked reason is a precondition, not the outcome of pressing
               anything, so it is read before the form instead of in the
               submit-error slot down by the footer. */
            .banner.lead { margin-top: 0; margin-bottom: 20px; }
            .banner.success { background: var(--accent-soft); color: var(--on-surface); }
            /* "We stopped waiting" is not an outcome. It is deliberately neither
               red nor green — either colour would answer a question that is
               still open. */
            .banner.notice { background: var(--surface-container-high); color: var(--on-surface-variant); }

            .footer { display: flex; justify-content: flex-end; gap: 8px; margin-top: 24px; }
            .footer button {
                display: inline-flex; align-items: center; justify-content: center;
                min-height: 40px; padding: 0 18px; border-radius: var(--radius-full);
                font: 600 14px/20px var(--font-body);
                transition: background 0.15s ease, border-color 0.15s ease, filter 0.15s ease, box-shadow 0.15s ease;
            }
            .footer .cancel {
                padding: 0 16px; font-weight: 500;
                background: var(--surface-container-high); color: var(--on-surface);
                border: 1px solid var(--outline-variant);
            }
            .footer .cancel:hover:not(:disabled) { background: var(--surface-container-highest); border-color: var(--outline); }
            .footer .confirm { background: var(--btn-primary-bg); color: var(--btn-primary-fg); }
            .footer .confirm:hover:not(:disabled) { background: var(--btn-primary-hover); }
            .footer button:disabled { opacity: 0.5; cursor: not-allowed; }
            .footer .confirm:disabled { filter: grayscale(1); }
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
     * One row of "Already shared with", in whichever of its states it is in:
     * at rest, armed (revoke or upgrade), or running.
     *
     * A grant missing part of the standard permission set — typically one made
     * before remote commands joined the default, or by another app — says what
     * it is missing and offers Upgrade, which re-shares with the standard set.
     *
     * Armed keeps the address on screen and replaces only the expiry, because
     * the question "revoke this?" is unanswerable if you can no longer see
     * which grant "this" is. Cancel is listed after Yes but is also what the
     * row falls back to — clicking Revoke on another row disarms this one.
     */
    private renderGrant(s: ExistingShare) {
        const armed = this.confirmingRevoke === s.grantee;
        const busy = this.revoking === s.grantee;
        const upArmed = this.confirmingUpgrade === s.grantee;
        const upBusy = this.upgrading === s.grantee;
        // Another app's developer license: its developer scoped this grant, and
        // Upgrade would hand it remote commands, credentials and raw data. Only
        // offered once it is known the grantee is not an app.
        const app = this.appGrantees?.get(s.grantee.toLowerCase());
        // The fleet's own access: shown, but not this modal's to change.
        const fleets = sameClientId(s.grantee, this.fleetLicense);
        const upgradeable = this.appGrantees !== null && !app && !fleets;
        // null = the mask could not be read; offer nothing rather than guess.
        const missing = missingStandardPermissions(s.permissions);
        const limited = upgradeable && !!missing && missing.length > 0 && !this.upgraded.has(s.grantee.toLowerCase());
        const missingList = limited ? missing.map((p) => this.permissionLabel(p)).join(', ') : '';
        // A re-share writes exactly the standard mask, so anything beyond it
        // on this grant is lost. Say so before they confirm.
        const lostList = limited ? extraPermissions(s.permissions).map((p) => this.permissionLabel(p)).join(', ') : '';
        // A share in flight, another row's job in flight, or a vehicle that
        // cannot be signed for at all: all make these controls a no-op, so
        // they are off rather than merely unhelpful.
        const jobElsewhere = (!!this.revoking && !busy) || (!!this.upgrading && !upBusy);
        const off = this.submitting || this.blocked || jobElsewhere;

        let middle;
        if (armed && !busy) middle = html`<span class="ask">${msg('Revoke?')}</span>`;
        else if (upArmed && !upBusy) middle = html`<span class="ask up">${msg('Upgrade?')}</span>`;
        else middle = html`<span class="when">${this.formatExpiry(s.expiresAt)}</span>`;

        let actions;
        if (fleets) {
            actions = nothing;
        } else if (busy) {
            actions = html`<button class="busy" disabled>${msg('Revoking…')}</button>`;
        } else if (upBusy) {
            actions = html`<button class="busy upgrade" disabled>${msg('Upgrading…')}</button>`;
        } else if (armed) {
            actions = html`
                <button class="danger" ?disabled=${off} @click=${() => void this.revoke(s.grantee)}>
                    ${msg('Yes')}
                </button>
                <button @click=${() => (this.confirmingRevoke = '')}>${msg('Cancel')}</button>
            `;
        } else if (upArmed) {
            actions = html`
                <button class="go" ?disabled=${off} @click=${() => void this.upgrade(s)}>
                    ${msg('Yes')}
                </button>
                <button @click=${() => (this.confirmingUpgrade = '')}>${msg('Cancel')}</button>
            `;
        } else {
            actions = html`
                ${limited
                    ? html`
                          <button
                              class="upgrade"
                              ?disabled=${off}
                              aria-label=${msg(str`Upgrade access for ${shortWallet(s.grantee)}`)}
                              @click=${() => {
                                  this.confirmingRevoke = '';
                                  this.confirmingUpgrade = s.grantee;
                              }}
                          >
                              ${msg('Upgrade')}
                          </button>
                      `
                    : nothing}
                <button
                    ?disabled=${off}
                    aria-label=${msg(str`Revoke access for ${shortWallet(s.grantee)}`)}
                    @click=${() => {
                        this.confirmingUpgrade = '';
                        this.confirmingRevoke = s.grantee;
                    }}
                >
                    ${msg('Revoke')}
                </button>
            `;
        }

        return html`
            <li class=${limited || app || fleets ? 'limited' : ''}>
                <span class="who" title=${s.grantee}>${shortWallet(s.grantee)}</span>
                ${middle}
                <span class="act">${actions}</span>
                ${fleets
                    ? html`<span class="missing note">${msg('This fleet’s own access. It isn’t managed here.')}</span>`
                    : app
                      ? html`<span class="missing note">${msg(str`App: ${app}`)}</span>`
                      : limited
                        ? html`<span class="missing ${upArmed ? 'confirm' : ''}">
                              ${upArmed
                                  ? html`${lostList
                                        ? msg(str`Adds ${missingList}. Removes ${lostList}.`)
                                        : msg(str`Adds ${missingList}.`)}
                                    ${hasUnrecognisedPermissions(s.permissions)
                                        ? msg('It also removes permissions this app doesn’t recognise.')
                                        : nothing}
                                    ${msg('Documents are included, and the expiry stays the same.')}`
                                  : msg(str`Limited access: missing ${missingList}`)}
                          </span>`
                        : nothing}
            </li>
        `;
    }

    render() {
        const showInvalid = this.grantee.trim().length > 0 && !this.granteeIsValid;
        // SACD keeps one record per grantee, so sharing to a wallet that has
        // one replaces it — duration and all, which can shorten a grant.
        const current = this.granteeIsValid
            ? this.existing.find((e) => e.grantee.toLowerCase() === this.grantee.trim().toLowerCase())
            : undefined;
        const toFleet = this.granteeIsValid && sameClientId(this.grantee.trim(), this.fleetLicense);
        // A revoke in flight closes the share form too. Both are the same
        // signer on the same account, and two jobs in flight against one
        // vehicle is a race the customer would have to untangle from the list.
        const inputsOff = this.submitting || this.blocked || !!this.revoking || !!this.upgrading;

        return html`
            <div class="card" role="dialog" aria-modal="true" aria-label=${msg('Share vehicle')}>
                <button class="close" ?disabled=${this.submitting || !!this.revoking || !!this.upgrading} @click=${this.dispatchClose} aria-label=${msg('Close')}>
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
                    <p class="hint ${showInvalid || toFleet ? 'bad' : current ? 'warn' : ''}">
                        ${showInvalid
                            ? msg('That does not look like a wallet address.')
                            : toFleet
                              ? msg('That is this fleet’s own license. Its access isn’t managed here.')
                              : current
                              ? msg(str`This wallet already has access (${this.formatExpiry(current.expiresAt)}). Sharing again replaces it with the standard access for the duration below.`)
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
                        <button class="cancel" ?disabled=${this.submitting || !!this.revoking || !!this.upgrading} @click=${this.dispatchClose}>
                            ${msg('Close')}
                        </button>
                        <button
                            class="confirm"
                            ?disabled=${!this.granteeIsValid || toFleet || inputsOff}
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
