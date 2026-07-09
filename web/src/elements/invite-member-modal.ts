import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiError } from '../services/api-service.ts';
import { TenantService, Member, ROLE_OWNER, ROLE_MEMBER } from '../services/tenant-service.ts';
import { FleetGroup } from '../types/group.ts';

/**
 * invite-member-modal — invite a member by email with a group-access scope, or
 * (when `member` is set) edit an existing member's scope.
 *
 * Access is either "all groups" or a selected subset; the group list has a
 * frontend search filter because fleets can carry many groups. Owners always
 * get full access, so the group section hides for owner-role invites.
 *
 * Props:
 *   - tenantId: required.
 *   - groups: the tenant's fleet groups (for the picker).
 *   - member?: edit-access mode for that member (no email/role fields).
 * Events:
 *   - close: dismissed.
 *   - saved: { emailSent? } — invite created / access updated; caller reloads.
 */
@customElement('invite-member-modal')
export class InviteMemberModal extends LitElement {
    @property({ type: String }) tenantId = '';
    @property({ attribute: false }) groups: FleetGroup[] = [];
    @property({ attribute: false }) member?: Member;

    @state() private email = '';
    @state() private inviteRole = ROLE_MEMBER;
    @state() private accessMode: 'all' | 'selected' = 'all';
    @state() private selected = new Set<string>();
    @state() private groupQuery = '';
    @state() private saving = false;
    @state() private errorMessage = '';

    private get isEdit(): boolean {
        return !!this.member;
    }

    connectedCallback() {
        super.connectedCallback();
        if (this.member) {
            this.inviteRole = this.member.role;
            if (this.member.allowedGroupIds) {
                this.accessMode = 'selected';
                this.selected = new Set(this.member.allowedGroupIds);
            }
        }
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                position: fixed;
                inset: 0;
                z-index: 100;
                display: flex;
                align-items: center;
                justify-content: center;
                background: rgba(0, 0, 0, 0.6);
                backdrop-filter: blur(4px);
            }
            .card {
                width: 100%;
                max-width: 480px;
                max-height: 90vh;
                overflow-y: auto;
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 24px;
                color: var(--on-surface);
                position: relative;
            }
            .card h2 { font: var(--type-headline-md); margin-bottom: 4px; }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 20px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                background: none; border: none; color: var(--on-surface-variant); padding: 4px; cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            .field { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; }
            .field label {
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant);
            }
            .field input[type="email"], .field select {
                background: var(--surface-container-low); color: var(--on-surface);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                padding: 10px 12px; font-family: inherit; font-size: 14px;
            }
            .field input:focus, .field select:focus { outline: 1px solid var(--primary); }

            .radio-row { display: flex; flex-direction: column; gap: 8px; }
            .radio-row label.option {
                display: flex; align-items: flex-start; gap: 10px;
                font: var(--type-body-md); color: var(--on-surface);
                text-transform: none; letter-spacing: normal; cursor: pointer;
            }
            .radio-row .option-text { display: flex; flex-direction: column; gap: 2px; }
            .radio-row .option-hint { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .group-picker {
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                background: var(--surface-container-low);
                margin-top: 8px;
            }
            .group-search {
                display: flex; align-items: center; gap: 8px;
                padding: 8px 12px; border-bottom: 1px solid var(--outline-variant);
            }
            .group-search .material-symbols-outlined { font-size: 16px; color: var(--on-surface-variant); }
            .group-search input {
                background: none; border: none; outline: none; flex: 1; min-width: 0;
                color: var(--on-surface); font: var(--type-body-sm);
            }
            .group-list { max-height: 200px; overflow-y: auto; padding: 6px 0; }
            .group-row {
                display: flex; align-items: center; gap: 10px;
                padding: 7px 12px; cursor: pointer;
                font: var(--type-body-md); color: var(--on-surface);
            }
            .group-row:hover { background: var(--surface-container-high); }
            .group-row .dot { width: 12px; height: 12px; border-radius: var(--radius-full); flex-shrink: 0; }
            .group-row .gname { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
            .group-empty { padding: 14px 12px; font: var(--type-body-sm); color: var(--on-surface-variant); text-align: center; }
            .selection-count { padding: 8px 12px; font: var(--type-body-sm); color: var(--on-surface-variant); border-top: 1px solid var(--outline-variant); }

            .actions { display: flex; gap: 12px; justify-content: flex-end; margin-top: 8px; }
            .actions button {
                padding: 10px 18px; border-radius: var(--radius-md);
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700;
                border: 1px solid transparent; cursor: pointer;
            }
            .actions .primary { background: var(--primary); color: var(--on-primary); }
            .actions .primary:disabled { opacity: 0.5; cursor: not-allowed; }
            .actions .ghost { background: transparent; color: var(--on-surface-variant); border-color: var(--outline-variant); }

            .error-text {
                padding: 12px; background: rgba(255, 180, 171, 0.04);
                border: 1px solid rgba(255, 180, 171, 0.2); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm); margin-bottom: 16px;
            }
        `,
    ];

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private get filteredGroups(): FleetGroup[] {
        const q = this.groupQuery.trim().toLowerCase();
        if (!q) return this.groups;
        return this.groups.filter((g) => g.name.toLowerCase().includes(q));
    }

    /** null = full access; array = the selected group ids. */
    private get allowedGroupIds(): string[] | null {
        if (this.inviteRole === ROLE_OWNER || this.accessMode === 'all') return null;
        return [...this.selected];
    }

    private toggleGroup(id: string) {
        const next = new Set(this.selected);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        this.selected = next;
    }

    private get canSave(): boolean {
        if (this.accessMode === 'selected' && this.inviteRole !== ROLE_OWNER && this.selected.size === 0) return false;
        if (this.isEdit) return true;
        return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(this.email.trim());
    }

    private async onSave() {
        this.saving = true;
        this.errorMessage = '';
        try {
            const svc = TenantService.getInstance();
            if (this.isEdit) {
                await svc.updateMemberAccess(this.tenantId, this.member!.wallet, this.allowedGroupIds);
                this.dispatchEvent(new CustomEvent('saved', { bubbles: true, composed: true }));
            } else {
                const res = await svc.createInvitation(
                    this.tenantId, this.email.trim().toLowerCase(), this.inviteRole, this.allowedGroupIds,
                );
                this.dispatchEvent(new CustomEvent('saved', {
                    detail: { emailSent: res.emailSent, email: this.email.trim().toLowerCase() },
                    bubbles: true, composed: true,
                }));
            }
        } catch (err) {
            this.errorMessage = extractMessage(err) || msg('Could not save. Please try again.');
            this.saving = false;
        }
    }

    private renderGroupPicker() {
        const filtered = this.filteredGroups;
        return html`
            <div class="group-picker">
                <div class="group-search">
                    <span class="material-symbols-outlined">search</span>
                    <input
                        type="search"
                        placeholder="${msg('Filter groups…')}"
                        .value=${this.groupQuery}
                        @input=${(e: Event) => { this.groupQuery = (e.target as HTMLInputElement).value; }}
                    />
                </div>
                <div class="group-list">
                    ${this.groups.length === 0
                        ? html`<div class="group-empty">${msg('No groups yet — create groups first, or grant access to all groups.')}</div>`
                        : filtered.length === 0
                            ? html`<div class="group-empty">${msg('No groups match your filter.')}</div>`
                            : filtered.map((g) => html`
                                <label class="group-row">
                                    <input
                                        type="checkbox"
                                        .checked=${this.selected.has(g.id)}
                                        @change=${() => this.toggleGroup(g.id)}
                                    />
                                    <span class="dot" style="background:${g.color}"></span>
                                    <span class="gname">${g.name}</span>
                                </label>
                            `)}
                </div>
                <div class="selection-count">${msg(str`${this.selected.size} selected`)}</div>
            </div>
        `;
    }

    render() {
        const showAccess = this.inviteRole !== ROLE_OWNER;
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2>${this.isEdit ? msg('Edit access') : msg('Invite member')}</h2>
                <p class="sub">${this.isEdit
                    ? msg(str`Change which groups ${this.member?.email || this.member?.wallet || ''} can see.`)
                    : msg('Send an email invitation and choose which groups the new member can see.')}</p>

                ${this.isEdit ? nothing : html`
                    <div class="field">
                        <label for="email">${msg('Email')}</label>
                        <input
                            id="email"
                            type="email"
                            placeholder="${msg('teammate@company.com')}"
                            autocomplete="off"
                            .value=${this.email}
                            @input=${(e: Event) => { this.email = (e.target as HTMLInputElement).value; }}
                        />
                    </div>
                    <div class="field">
                        <label for="role">${msg('Role')}</label>
                        <select
                            id="role"
                            .value=${this.inviteRole}
                            @change=${(e: Event) => { this.inviteRole = (e.target as HTMLSelectElement).value; }}
                        >
                            <option value=${ROLE_MEMBER}>${msg('Member')}</option>
                            <option value=${ROLE_OWNER}>${msg('Owner')}</option>
                        </select>
                    </div>
                `}

                ${showAccess ? html`
                    <div class="field">
                        <label>${msg('Group access')}</label>
                        <div class="radio-row">
                            <label class="option">
                                <input
                                    type="radio"
                                    name="access"
                                    .checked=${this.accessMode === 'all'}
                                    @change=${() => { this.accessMode = 'all'; }}
                                />
                                <span class="option-text">
                                    ${msg('All groups')}
                                    <span class="option-hint">${msg('Sees every vehicle, including ones not in any group.')}</span>
                                </span>
                            </label>
                            <label class="option">
                                <input
                                    type="radio"
                                    name="access"
                                    .checked=${this.accessMode === 'selected'}
                                    @change=${() => { this.accessMode = 'selected'; }}
                                />
                                <span class="option-text">
                                    ${msg('Selected groups only')}
                                    <span class="option-hint">${msg('Sees only vehicles in the chosen groups; management views are read-only.')}</span>
                                </span>
                            </label>
                        </div>
                        ${this.accessMode === 'selected' ? this.renderGroupPicker() : nothing}
                    </div>
                ` : html`
                    <p class="sub">${msg('Owners always have access to all groups.')}</p>
                `}

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <div class="actions">
                    <button class="ghost" @click=${this.dispatchClose}>${msg('Cancel')}</button>
                    <button class="primary" ?disabled=${!this.canSave || this.saving} @click=${this.onSave}>
                        ${this.saving ? msg('Saving…') : this.isEdit ? msg('Save access') : msg('Send invite')}
                    </button>
                </div>
            </div>
        `;
    }
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
        'invite-member-modal': InviteMemberModal;
    }
}
