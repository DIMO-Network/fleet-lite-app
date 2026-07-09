import { LitElement, html, css } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { getTokenClaims } from '../utils/token.ts';
import { ApiError } from '../services/api-service.ts';
import { TenantService, Member, Invitation, MyAccess, ROLE_OWNER } from '../services/tenant-service.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { FleetGroup } from '../types/group.ts';
import './invite-member-modal.ts';

/**
 * Members management for the current tenant, embedded in the settings view.
 * Any member can see the roster; only an owner sees the add form and the
 * per-member remove control. The caller's own role is derived from the roster
 * (their wallet's row), so no extra request is needed.
 */
@customElement('tenant-members')
export class TenantMembers extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private members: Member[] = [];
    @state() private loading = false;
    @state() private error = '';

    @state() private newWallet = '';
    @state() private adding = false;
    @state() private addError = '';
    @state() private busyWallet = ''; // wallet currently being removed

    @state() private invites: Invitation[] = [];
    @state() private inviteError = '';
    @state() private inviteNotice = '';
    @state() private busyInviteId = ''; // invitation currently being revoked/resent
    @state() private showPast = false; // past-invitations section, collapsed by default
    @state() private groups: FleetGroup[] = []; // for the invite modal + access chips
    @state() private inviteOpen = false; // invite-member modal
    @state() private editingAccess: Member | null = null; // edit-access modal target
    @state() private myAccess: MyAccess | null = null; // non-owner "your access" summary

    static styles = [
        sharedStyles,
        css`
            :host { display: block; }
            .row-group {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                overflow: hidden;
            }
            .member {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 12px;
                padding: 16px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .member:last-child { border-bottom: none; }
            .member .left-group { display: flex; align-items: center; gap: 16px; min-width: 0; }
            .member .right-group { display: flex; align-items: center; gap: 12px; flex-shrink: 0; }
            .member .identity { display: flex; flex-direction: column; min-width: 0; gap: 2px; }
            .member .wallet {
                font: var(--type-body-md);
                font-family: var(--font-mono);
                color: var(--primary);
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            /* When we have an email, show it in the normal UI font, not mono. */
            .member .wallet.email { font-family: inherit; color: var(--on-surface); }
            .member .last-seen { font-size: 12px; color: var(--on-surface-variant); }
            .member .you {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .badge {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                padding: 4px 10px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
            }
            .badge.owner {
                color: var(--tertiary-container);
                border-color: var(--tertiary-container);
            }
            .remove-btn {
                background: none;
                border: none;
                color: var(--on-surface-variant);
                padding: 6px;
                border-radius: var(--radius-full);
                cursor: pointer;
                display: inline-flex;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .remove-btn:hover { background: var(--surface-container-high); color: var(--error); }
            .remove-btn[disabled] { opacity: 0.5; cursor: default; }
            .remove-btn .material-symbols-outlined { font-size: 20px; }

            .add-form {
                display: flex;
                gap: 12px;
                margin-top: var(--stack-md);
            }
            .add-form input {
                flex: 1;
                min-width: 0;
                box-sizing: border-box;
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 12px 14px;
                color: var(--on-surface);
                font: var(--type-body-sm);
                font-family: var(--font-mono);
            }
            .add-form input:focus { outline: none; border-color: var(--secondary-container); }
            .add-form button {
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                border-radius: var(--radius-full);
                padding: 0 22px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                cursor: pointer;
                white-space: nowrap;
            }
            .add-form button[disabled] { opacity: 0.6; cursor: default; }

            .state { padding: 16px; font: var(--type-body-sm); color: var(--on-surface-variant); }
            .error { color: var(--error); font: var(--type-body-sm); margin-top: var(--stack-sm); }
            .notice { color: var(--tertiary-fixed-dim, var(--on-surface-variant)); font: var(--type-body-sm); margin-top: var(--stack-sm); }

            .section-label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin: var(--stack-lg) 0 var(--stack-sm);
            }
            /* Invite-by-email form: email input grows, role select + button hug right. */
            .invite-form { display: flex; gap: 12px; flex-wrap: wrap; }
            .invite-form input[type='email'] {
                flex: 1;
                min-width: 0;
                box-sizing: border-box;
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 12px 14px;
                color: var(--on-surface);
                font: var(--type-body-sm);
            }
            .invite-form input[type='email']:focus { outline: none; border-color: var(--secondary-container); }
            .invite-form select {
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 0 12px;
                color: var(--on-surface);
                font: var(--type-body-sm);
            }
            .invite-form button {
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                border-radius: var(--radius-full);
                padding: 0 22px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                cursor: pointer;
                white-space: nowrap;
            }
            .invite-form button[disabled] { opacity: 0.6; cursor: default; }

            /* Pending-invite rows reuse the .member layout but key off the email. */
            .invite-row .email-id { font: var(--type-body-md); color: var(--on-surface); }
            .invite-row .meta { font-size: 12px; color: var(--on-surface-variant); }
            .text-btn {
                background: none;
                border: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
                padding: 6px 12px;
                border-radius: var(--radius-full);
                cursor: pointer;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .text-btn:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .text-btn.danger:hover { color: var(--error); border-color: var(--error); }
            .text-btn[disabled] { opacity: 0.5; cursor: default; }

            .invite-btn {
                display: inline-flex;
                align-items: center;
                gap: 8px;
                background: var(--primary);
                color: var(--on-primary);
                border: none;
                border-radius: var(--radius-full);
                padding: 12px 22px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                cursor: pointer;
            }
            .invite-btn .material-symbols-outlined { font-size: 18px; }
            .badge.access { text-transform: none; letter-spacing: normal; }
            .access-panel { display: flex; gap: 14px; padding: 16px; align-items: flex-start; }
            .access-text { display: flex; flex-direction: column; gap: 8px; font: var(--type-body-md); color: var(--on-surface); }
            .access-chips { display: flex; flex-wrap: wrap; gap: 6px; }
            .access-chips .none { font: var(--type-body-sm); color: var(--on-surface-variant); }
            .group-chip {
                display: inline-block;
                border: 1px solid;
                border-radius: var(--radius-sm);
                padding: 2px 8px;
                font: var(--type-label-caps);
                letter-spacing: 0.04em;
                font-size: 10px;
                white-space: nowrap;
            }

            /* Collapsible "Past invitations" header: section-label styling on a button. */
            .section-toggle {
                display: flex;
                align-items: center;
                gap: 4px;
                background: none;
                border: none;
                padding: 0;
                cursor: pointer;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin: var(--stack-lg) 0 var(--stack-sm);
            }
            .section-toggle:hover { color: var(--on-surface); }
            .section-toggle .material-symbols-outlined { font-size: 18px; }
            .badge.expired { color: var(--error); border-color: var(--error); }
        `,
    ];

    // Load (and reload) whenever the tenant changes — covers the initial
    // property binding and later tenant switches without a double fetch.
    willUpdate(changed: Map<string, unknown>) {
        if (changed.has('tenantId') && this.tenantId) {
            void this.load();
        }
    }

    private get myWallet(): string {
        const claims = getTokenClaims();
        const addr = typeof claims?.ethereum_address === 'string' ? claims.ethereum_address : '';
        return addr.toLowerCase();
    }

    private get isOwner(): boolean {
        const me = this.myWallet;
        return this.members.some(m => m.wallet.toLowerCase() === me && m.role === ROLE_OWNER);
    }

    private async load() {
        if (!this.tenantId) return;
        this.loading = true;
        this.error = '';
        try {
            this.members = await TenantService.getInstance().fetchMembers(this.tenantId);
            // Groups feed the invite modal's picker and the access chips; for a
            // limited member the API already returns only their groups, which is
            // exactly what the "your access" summary needs. Non-fatal.
            try {
                this.groups = await FleetGroupService.getInstance().list();
            } catch {
                this.groups = [];
            }
            // Pending invites are only shown to owners (who manage them). Load
            // them after members so isOwner is known; failure here is non-fatal.
            if (this.isOwner) {
                try {
                    this.invites = await TenantService.getInstance().fetchInvitations(this.tenantId);
                } catch {
                    this.invites = [];
                }
                this.myAccess = null;
            } else {
                this.invites = [];
                try {
                    this.myAccess = await TenantService.getInstance().fetchMyAccess();
                } catch {
                    this.myAccess = null;
                }
            }
        } catch (err) {
            this.error = extractMessage(err) || msg('Could not load members.');
            this.members = [];
        } finally {
            this.loading = false;
        }
    }

    /** Live pending invites — accepted ones appear in the roster, expired ones under "Past invitations". */
    private get pendingInvites(): Invitation[] {
        return this.invites.filter(i => i.status === 'pending' && !isExpired(i));
    }

    /** Accepted and expired invites, shown in the collapsed history section. */
    private get pastInvites(): Invitation[] {
        return this.invites.filter(i => i.status === 'accepted' || (i.status === 'pending' && isExpired(i)));
    }

    /**
     * Human label for the account that accepted an invite: the roster email if
     * that wallet is (still) a member, else the shortened wallet.
     */
    private accountLabel(wallet?: string): string {
        if (!wallet) return '';
        const member = this.members.find(m => m.wallet.toLowerCase() === wallet.toLowerCase());
        return member?.email || shortWallet(wallet);
    }

    /** The invite modal saved: show the sent/not-sent notice and refresh. */
    private onInviteSaved(e: CustomEvent) {
        const { emailSent, email } = (e.detail ?? {}) as { emailSent?: boolean; email?: string };
        this.inviteOpen = false;
        this.inviteError = '';
        this.inviteNotice = emailSent === false
            ? msg(str`Invitation created for ${email ?? ''}, but the email could not be sent. Use Resend once email delivery is configured.`)
            : msg(str`Invitation sent to ${email ?? ''}.`);
        void this.load();
    }

    /** Human summary of an allowed-groups scope ("All groups" / "N groups"). */
    private accessLabel(allowedGroupIds?: string[]): string {
        if (!allowedGroupIds) return msg('All groups');
        const n = allowedGroupIds.length;
        return n === 1 ? msg('1 group') : msg(str`${n} groups`);
    }

    /** Tooltip listing the group names behind an access chip. */
    private accessTitle(allowedGroupIds?: string[]): string {
        if (!allowedGroupIds) return '';
        const byId = new Map(this.groups.map((g) => [g.id, g.name]));
        return allowedGroupIds.map((id) => byId.get(id) ?? id).join(', ');
    }

    private async revokeInvite(id: string) {
        this.inviteError = '';
        this.inviteNotice = '';
        this.busyInviteId = id;
        try {
            await TenantService.getInstance().revokeInvitation(this.tenantId, id);
            await this.load();
        } catch (err) {
            this.inviteError = extractMessage(err) || msg('Could not revoke invitation.');
        } finally {
            this.busyInviteId = '';
        }
    }

    private async resendInvite(id: string) {
        this.inviteError = '';
        this.inviteNotice = '';
        this.busyInviteId = id;
        try {
            const res = await TenantService.getInstance().resendInvitation(this.tenantId, id);
            this.inviteNotice = res.emailSent === false
                ? msg('Invitation refreshed, but the email could not be sent. Check email delivery configuration.')
                : msg('Invitation re-sent.');
            // Resend renews the expiry, which can move an expired invite back to
            // the pending list — refresh so it lands in the right section.
            await this.load();
        } catch (err) {
            this.inviteError = extractMessage(err) || msg('Could not resend invitation.');
        } finally {
            this.busyInviteId = '';
        }
    }

    private async addMember(e: Event) {
        e.preventDefault();
        this.addError = '';
        const wallet = this.newWallet.trim();
        if (!/^0x[a-fA-F0-9]{40}$/.test(wallet)) {
            this.addError = msg('Enter a valid wallet address (0x…, 40 hex characters).');
            return;
        }
        if (this.members.some(m => m.wallet.toLowerCase() === wallet.toLowerCase())) {
            this.addError = msg('That wallet is already a member.');
            return;
        }
        this.adding = true;
        try {
            await TenantService.getInstance().addMember(this.tenantId, wallet);
            this.newWallet = '';
            await this.load();
        } catch (err) {
            this.addError = extractMessage(err) || msg('Could not add member.');
        } finally {
            this.adding = false;
        }
    }

    private async removeMember(wallet: string) {
        this.error = '';
        this.busyWallet = wallet;
        try {
            await TenantService.getInstance().removeMember(this.tenantId, wallet);
            await this.load();
        } catch (err) {
            this.error = extractMessage(err) || msg('Could not remove member.');
        } finally {
            this.busyWallet = '';
        }
    }

    private renderMember(m: Member) {
        const isSelf = m.wallet.toLowerCase() === this.myWallet;
        const isOwnerRole = m.role === ROLE_OWNER;
        // Owners can remove anyone but themselves (guards against self-lockout).
        const canRemove = this.isOwner && !isSelf;
        return html`
            <div class="member">
                <div class="left-group">
                    <span class="material-symbols-outlined" style="color: var(--on-surface-variant);">
                        ${isOwnerRole ? 'shield_person' : 'person'}
                    </span>
                    <div class="identity">
                        <span class="wallet ${m.email ? 'email' : ''}" title=${m.wallet}>
                            ${m.email || shortWallet(m.wallet)}
                        </span>
                        ${m.lastLoginAt
                            ? html`<span class="last-seen">${msg(str`Last login ${new Date(m.lastLoginAt).toLocaleDateString()}`)}</span>`
                            : ''}
                    </div>
                    ${isSelf ? html`<span class="you">${msg('You')}</span>` : ''}
                </div>
                <div class="right-group">
                    ${!isOwnerRole
                        ? html`<span class="badge access" title=${this.accessTitle(m.allowedGroupIds)}>
                            ${this.accessLabel(m.allowedGroupIds)}
                        </span>`
                        : ''}
                    <span class="badge ${isOwnerRole ? 'owner' : ''}">${m.role}</span>
                    ${this.isOwner && !isOwnerRole
                        ? html`
                            <button
                                class="text-btn"
                                @click=${() => { this.editingAccess = m; }}
                            >${msg('Edit access')}</button>
                          `
                        : ''}
                    ${canRemove
                        ? html`
                            <button
                                class="remove-btn"
                                title="${msg('Remove member')}"
                                ?disabled=${this.busyWallet === m.wallet}
                                @click=${() => this.removeMember(m.wallet)}
                            >
                                <span class="material-symbols-outlined">person_remove</span>
                            </button>
                          `
                        : ''}
                </div>
            </div>
        `;
    }

    render() {
        if (this.loading) {
            return html`<div class="row-group"><div class="state">${msg('Loading members…')}</div></div>`;
        }
        if (this.error && this.members.length === 0) {
            return html`<div class="row-group"><div class="state error">${this.error}</div></div>`;
        }
        return html`
            <div class="row-group">
                ${this.members.length === 0
                    ? html`<div class="state">${msg('No members yet.')}</div>`
                    : this.members.map(m => this.renderMember(m))}
            </div>
            ${this.error && this.members.length > 0 ? html`<div class="error">${this.error}</div>` : ''}
            ${this.isOwner ? this.renderOwnerControls() : this.renderMyAccess()}
            ${this.inviteOpen
                ? html`<invite-member-modal
                    .tenantId=${this.tenantId}
                    .groups=${this.groups}
                    @close=${() => { this.inviteOpen = false; }}
                    @saved=${this.onInviteSaved}
                  ></invite-member-modal>`
                : ''}
            ${this.editingAccess
                ? html`<invite-member-modal
                    .tenantId=${this.tenantId}
                    .groups=${this.groups}
                    .member=${this.editingAccess}
                    @close=${() => { this.editingAccess = null; }}
                    @saved=${() => { this.editingAccess = null; void this.load(); }}
                  ></invite-member-modal>`
                : ''}
        `;
    }

    /**
     * Non-owners see their own access level here: full access, or the list of
     * groups they're limited to (their /fleet/groups response is already
     * scoped, so it carries exactly those groups' names/colors).
     */
    private renderMyAccess() {
        if (!this.myAccess) return html``;
        const limited = this.myAccess.allowedGroupIds !== null;
        return html`
            <div class="section-label">${msg('Your access')}</div>
            <div class="row-group">
                <div class="access-panel">
                    <span class="material-symbols-outlined" style="color: var(--on-surface-variant);">
                        ${limited ? 'lock' : 'lock_open'}
                    </span>
                    <div class="access-text">
                        ${limited
                            ? html`
                                <span>${msg('Limited access to the following groups:')}</span>
                                <div class="access-chips">
                                    ${this.groups.map((g) => html`
                                        <span class="group-chip" style="border-color:${g.color}; color:${g.color}">${g.name}</span>
                                    `)}
                                    ${this.groups.length === 0 ? html`<span class="none">${msg('No groups assigned yet — ask an owner.')}</span>` : ''}
                                </div>
                              `
                            : html`<span>${msg('You have access to all groups and vehicles.')}</span>`}
                    </div>
                </div>
            </div>
        `;
    }

    private renderOwnerControls() {
        const pending = this.pendingInvites;
        const past = this.pastInvites;
        return html`
            <div class="section-label">${msg('Invite by email')}</div>
            <button class="invite-btn" @click=${() => { this.inviteOpen = true; }}>
                <span class="material-symbols-outlined">person_add</span>
                ${msg('Invite member')}
            </button>
            ${this.inviteError ? html`<div class="error">${this.inviteError}</div>` : ''}
            ${this.inviteNotice ? html`<div class="notice">${this.inviteNotice}</div>` : ''}

            ${pending.length > 0
                ? html`
                    <div class="section-label">${msg('Pending invitations')}</div>
                    <div class="row-group">${pending.map(i => this.renderInvite(i))}</div>
                  `
                : ''}
            ${past.length > 0
                ? html`
                    <button
                        class="section-toggle"
                        aria-expanded=${this.showPast}
                        @click=${() => (this.showPast = !this.showPast)}
                    >
                        <span class="material-symbols-outlined">${this.showPast ? 'expand_more' : 'chevron_right'}</span>
                        ${msg(str`Past invitations (${past.length})`)}
                    </button>
                    ${this.showPast
                        ? html`<div class="row-group">${past.map(i => this.renderPastInvite(i))}</div>`
                        : ''}
                  `
                : ''}

            <div class="section-label">${msg('Add by wallet address')}</div>
            <form class="add-form" @submit=${this.addMember}>
                <input
                    placeholder="${msg('0x… wallet address')}"
                    autocomplete="off"
                    .value=${this.newWallet}
                    @input=${(e: Event) => (this.newWallet = (e.target as HTMLInputElement).value)}
                />
                <button type="submit" ?disabled=${this.adding}>
                    ${this.adding ? msg('Adding…') : msg('Add member')}
                </button>
            </form>
            ${this.addError ? html`<div class="error">${this.addError}</div>` : ''}
        `;
    }

    private renderInvite(i: Invitation) {
        const busy = this.busyInviteId === i.id;
        const expires = new Date(i.expiresAt);
        return html`
            <div class="member invite-row">
                <div class="left-group">
                    <span class="material-symbols-outlined" style="color: var(--on-surface-variant);">mail</span>
                    <div class="identity">
                        <span class="email-id">${i.email}</span>
                        <span class="meta" title=${this.accessTitle(i.allowedGroupIds)}>${msg(str`Invited as ${i.role} · ${this.accessLabel(i.allowedGroupIds)} · expires ${expires.toLocaleDateString()}`)}</span>
                    </div>
                </div>
                <div class="right-group">
                    <button
                        class="text-btn"
                        ?disabled=${busy}
                        @click=${() => this.resendInvite(i.id)}
                    >${msg('Resend')}</button>
                    <button
                        class="text-btn danger"
                        ?disabled=${busy}
                        @click=${() => this.revokeInvite(i.id)}
                    >${msg('Revoke')}</button>
                </div>
            </div>
        `;
    }

    /**
     * A past invitation: accepted (shows when and which account connected) or
     * expired (shows when it lapsed; can still be re-sent, which revives it as
     * pending with a fresh link, or revoked to clear it from this list).
     */
    private renderPastInvite(i: Invitation) {
        const busy = this.busyInviteId === i.id;
        const accepted = i.status === 'accepted';
        const acceptedDate = i.acceptedAt ? new Date(i.acceptedAt).toLocaleDateString() : '';
        const expiredDate = new Date(i.expiresAt).toLocaleDateString();
        const account = this.accountLabel(i.inviteeWallet);
        return html`
            <div class="member invite-row">
                <div class="left-group">
                    <span class="material-symbols-outlined" style="color: var(--on-surface-variant);">
                        ${accepted ? 'how_to_reg' : 'event_busy'}
                    </span>
                    <div class="identity">
                        <span class="email-id">${i.email}</span>
                        <span class="meta" title=${i.inviteeWallet ?? ''}>
                            ${accepted
                                ? account
                                    ? msg(str`Accepted ${acceptedDate} · connected as ${account}`)
                                    : msg(str`Accepted ${acceptedDate}`)
                                : msg(str`Expired ${expiredDate}`)}
                        </span>
                    </div>
                </div>
                <div class="right-group">
                    <span class="badge ${accepted ? '' : 'expired'}">
                        ${accepted ? msg('Accepted') : msg('Expired')}
                    </span>
                    ${accepted
                        ? ''
                        : html`
                            <button
                                class="text-btn"
                                ?disabled=${busy}
                                @click=${() => this.resendInvite(i.id)}
                            >${msg('Resend')}</button>
                            <button
                                class="text-btn danger"
                                ?disabled=${busy}
                                @click=${() => this.revokeInvite(i.id)}
                            >${msg('Revoke')}</button>
                          `}
                </div>
            </div>
        `;
    }
}

function isExpired(i: Invitation): boolean {
    return new Date(i.expiresAt).getTime() < Date.now();
}

function shortWallet(w: string): string {
    return w.length > 12 ? `${w.slice(0, 6)}…${w.slice(-4)}` : w;
}

// API errors arrive as a JSON body string (`{"code":400,"message":"…"}`).
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
        'tenant-members': TenantMembers;
    }
}
