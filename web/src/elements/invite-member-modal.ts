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
                background: color-mix(in srgb, var(--canvas) 70%, transparent);
                backdrop-filter: blur(6px);
                -webkit-backdrop-filter: blur(6px);
            }
            .card {
                width: calc(100% - 32px);
                max-width: 480px;
                max-height: 90vh;
                overflow-y: auto;
                background: var(--surface-overlay);
                border-radius: var(--radius-xl);
                box-shadow: var(--shadow-float);
                padding: 24px;
                color: var(--on-surface);
                position: relative;
                animation: modal-in 0.18s ease-out;
            }
            @keyframes modal-in {
                from { opacity: 0; transform: translateY(8px) scale(0.98); }
            }
            .card h2 {
                font: var(--type-headline-md);
                letter-spacing: -0.01em;
                color: var(--primary);
                padding-right: 40px;
                margin-bottom: 4px;
            }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 24px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                width: 32px; height: 32px; padding: 0;
                display: inline-flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full); color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close .material-symbols-outlined { font-size: 20px; }
            .close:hover { background: var(--surface-container-high); color: var(--on-surface); }

            .field { display: flex; flex-direction: column; gap: 8px; margin-bottom: 20px; }
            .field > label { font: var(--type-label); color: var(--on-surface-variant); }
            .field input[type="email"], .field select {
                height: 40px;
                padding: 0 12px;
                background-color: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            .field input[type="email"]::placeholder { color: var(--on-surface-variant); }
            .field input[type="email"]:hover:not(:focus-visible),
            .field select:hover:not(:focus-visible) { border-color: var(--outline); }
            .field input[type="email"]:focus-visible, .field select:focus-visible {
                outline: none;
                border-color: var(--accent);
                box-shadow: 0 0 0 3px var(--accent-soft);
            }

            /* Access scope reads as two option cards; the chosen one takes the
               accent tint so the choice is visible without finding the dot. */
            .radio-row { display: flex; flex-direction: column; gap: 6px; }
            .radio-row label.option {
                display: flex; align-items: flex-start; gap: 12px;
                padding: 12px 14px;
                border-radius: var(--radius-md);
                background: var(--surface-container);
                font: 500 14px/20px var(--font-body); color: var(--on-surface);
                cursor: pointer;
                transition: background 0.15s ease, box-shadow 0.15s ease;
            }
            .radio-row label.option:hover { background: var(--surface-container-high); }
            .radio-row label.option:has(input:checked) {
                background: var(--accent-soft);
                box-shadow: inset 0 0 0 1px var(--accent-soft-strong);
            }
            .radio-row label.option input { margin-top: 3px; flex: none; }
            .radio-row .option-text { display: flex; flex-direction: column; gap: 2px; }
            .radio-row .option-hint { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .group-picker {
                border-radius: var(--radius-md);
                background: var(--surface-container);
                margin-top: 6px;
                overflow: hidden;
            }
            .group-search {
                display: flex; align-items: center; gap: 8px;
                height: 40px; padding: 0 12px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .group-search .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); }
            .group-search input {
                background: none; border: none; outline: none; flex: 1; min-width: 0;
                color: var(--on-surface); font: var(--type-body-sm);
            }
            .group-search input::placeholder { color: var(--on-surface-variant); }
            .group-search input:focus-visible { box-shadow: none; }
            .group-search:focus-within { box-shadow: inset 0 -2px 0 var(--accent); }
            .group-list { max-height: 200px; overflow-y: auto; padding: 4px; }
            .group-row {
                display: flex; align-items: center; gap: 10px;
                min-height: 36px; padding: 0 10px; cursor: pointer;
                border-radius: var(--radius-sm);
                font: var(--type-body-sm); color: var(--on-surface);
                transition: background 0.15s ease;
            }
            .group-row:hover { background: var(--surface-container-high); }
            .group-row .dot { width: 8px; height: 8px; border-radius: var(--radius-full); flex-shrink: 0; }
            .group-row .gname { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
            .group-empty { padding: 14px 12px; font: var(--type-body-sm); color: var(--on-surface-variant); text-align: center; }
            .selection-count {
                padding: 8px 12px; font: var(--type-label); color: var(--on-surface-variant);
                border-top: 1px solid var(--outline-variant);
            }

            .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 24px; }
            .actions button {
                display: inline-flex; align-items: center; justify-content: center;
                min-height: 40px; padding: 0 18px;
                border-radius: var(--radius-full);
                font: 600 14px/20px var(--font-body);
                transition: background 0.15s ease, border-color 0.15s ease, filter 0.15s ease, box-shadow 0.15s ease;
            }
            .actions .primary { background: var(--brand-gradient); color: var(--on-accent); }
            .actions .primary:hover:not(:disabled) { filter: brightness(1.06); box-shadow: var(--accent-glow); }
            .actions .primary:disabled { filter: grayscale(1); opacity: 0.5; cursor: not-allowed; }
            .actions .ghost {
                padding: 0 16px; font-weight: 500;
                background: var(--surface-container-high); color: var(--on-surface);
                border: 1px solid var(--outline-variant);
            }
            .actions .ghost:hover { background: var(--surface-container-highest); border-color: var(--outline); }

            .error-text {
                padding: 12px 14px; background: var(--error-container); color: var(--error);
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
                            <option value=${ROLE_MEMBER} ?selected=${this.inviteRole === ROLE_MEMBER}>${msg('Member')}</option>
                            <option value=${ROLE_OWNER} ?selected=${this.inviteRole === ROLE_OWNER}>${msg('Owner')}</option>
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
