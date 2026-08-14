import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { FleetGroup } from '../types/group.ts';
import { Vehicle } from '../types/vehicle.ts';

/**
 * manage-group-vehicles-modal — toggle which vehicles belong to a group.
 *
 * Membership is derived from each vehicle's `groups` (populated by /vehicles).
 * Toggling a row calls add/remove immediately and updates local state, so the
 * write-path attestation fires per change. A `changed` event is dispatched on
 * close so the parent refetches counts/membership.
 *
 * Props:
 *   - group: the group being managed.
 *   - vehicles: all of the tenant's vehicles.
 * Events:
 *   - close: dismissed.
 *   - changed: at least one membership changed; caller should refetch.
 */
@customElement('manage-group-vehicles-modal')
export class ManageGroupVehiclesModal extends LitElement {
    @property({ attribute: false }) group!: FleetGroup;
    @property({ attribute: false }) vehicles: Vehicle[] = [];

    @state() private memberIds = new Set<number>();
    @state() private busy = new Set<number>();
    @state() private query = '';
    @state() private errorMessage = '';
    private changed = false;

    /**
     * Membership as it was when the modal opened, which is what the list is
     * ordered by. Deliberately not memberIds: ordering on live membership would
     * make a row jump to the top the instant you add it, shifting everything
     * under the cursor mid-click. Frozen here, the list stays put while you work
     * and comes back re-sorted the next time you open it.
     */
    private initialMemberIds = new Set<number>();

    connectedCallback() {
        super.connectedCallback();
        const members = new Set<number>();
        for (const v of this.vehicles) {
            if ((v.groups || []).some((g) => g.id === this.group.id)) {
                members.add(v.tokenId);
            }
        }
        this.memberIds = members;
        this.initialMemberIds = new Set(members);
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
                width: 100%; max-width: 520px; max-height: 80vh;
                background: var(--surface-container); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg); padding: 24px; color: var(--on-surface);
                position: relative; display: flex; flex-direction: column;
            }
            .card h2 { font: var(--type-headline-md); margin-bottom: 4px; display: flex; align-items: center; gap: 10px; }
            .card h2 .dot { width: 14px; height: 14px; border-radius: var(--radius-full); }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 16px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                background: none; border: none; color: var(--on-surface-variant); padding: 4px; cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            .search input {
                width: 100%; box-sizing: border-box;
                background: var(--surface-container-low); color: var(--on-surface);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                padding: 10px 12px; font-family: inherit; font-size: 14px; margin-bottom: 12px;
            }
            .search input:focus { outline: 1px solid var(--primary); }

            .list { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
            .list::-webkit-scrollbar { width: 6px; }
            .list::-webkit-scrollbar-thumb { background-color: var(--outline-variant); border-radius: 10px; }

            .row {
                display: flex; align-items: center; gap: 12px;
                padding: 12px; border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md); background: var(--surface-container-low);
            }
            .row .meta { flex: 1; min-width: 0; }
            .row .meta .title { font: var(--type-body-md); color: var(--primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
            /* Token · VIN · plate can outrun the row, so it clips rather than wraps
               the card wider — the search box is how you find a specific one. */
            .row .meta .sub2 {
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase;
                color: var(--on-surface-variant); margin-top: 2px;
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }

            .toggle {
                width: 40px; height: 40px; display: flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full); border: 1px solid var(--outline-variant);
                background: transparent; color: var(--on-surface-variant); cursor: pointer; flex-shrink: 0;
            }
            .toggle.member { background: var(--primary); color: var(--on-primary); border-color: var(--primary); }
            .toggle:disabled { opacity: 0.5; cursor: progress; }

            .empty-state { color: var(--on-surface-variant); font: var(--type-body-sm); padding: 24px; text-align: center; }
            .error-text {
                padding: 12px; background: rgba(255, 180, 171, 0.04);
                border: 1px solid rgba(255, 180, 171, 0.2); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm); margin: 12px 0 0;
            }
            .footer { display: flex; justify-content: flex-end; margin-top: 16px; }
            .footer button {
                padding: 10px 18px; border-radius: var(--radius-md);
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700;
                border: 1px solid transparent; background: var(--primary); color: var(--on-primary); cursor: pointer;
            }
        `,
    ];

    private vehicleTitle(v: Vehicle): string {
        const d = v.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : msg(str`Vehicle #${v.tokenId}`);
    }

    /**
     * The identifiers an operator actually recognises a vehicle by. Token id is
     * always there; VIN and plate come from the registration attestation and are
     * absent when unknown, so they are dropped rather than rendered blank.
     */
    private vehicleIdentifiers(v: Vehicle): string {
        return [msg(str`Token #${v.tokenId}`), v.vin, v.licensePlate]
            .filter(Boolean)
            .join(' · ');
    }

    /** The same fields the row shows, lowercased for substring matching. */
    private searchHaystack(v: Vehicle): string {
        return [this.vehicleTitle(v), String(v.tokenId), v.vin, v.licensePlate]
            .filter(Boolean)
            .join(' ')
            .toLowerCase();
    }

    private dispatchClose() {
        if (this.changed) {
            this.dispatchEvent(new CustomEvent('changed', { bubbles: true, composed: true }));
        }
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private async toggle(v: Vehicle) {
        const tokenId = v.tokenId;
        if (this.busy.has(tokenId)) return;
        const isMember = this.memberIds.has(tokenId);

        this.busy = new Set(this.busy).add(tokenId);
        this.errorMessage = '';
        try {
            const svc = FleetGroupService.getInstance();
            if (isMember) {
                await svc.removeVehicle(tokenId, this.group.id);
                this.memberIds.delete(tokenId);
            } else {
                await svc.addVehicle(tokenId, this.group.id);
                this.memberIds.add(tokenId);
            }
            this.memberIds = new Set(this.memberIds);
            this.changed = true;
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Failed to update membership');
        } finally {
            const next = new Set(this.busy);
            next.delete(tokenId);
            this.busy = next;
        }
    }

    render() {
        const q = this.query.trim().toLowerCase();
        const matched = q
            ? this.vehicles.filter((v) => this.searchHaystack(v).includes(q))
            : this.vehicles;
        // Members first, so opening the modal answers "what's in this group?"
        // without scrolling. Array.sort is stable, so each side keeps the order
        // it came in with.
        const filtered = [...matched].sort(
            (a, b) => Number(this.initialMemberIds.has(b.tokenId)) - Number(this.initialMemberIds.has(a.tokenId)),
        );

        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2><span class="dot" style="background:${this.group.color}"></span>${this.group.name}</h2>
                <p class="sub">${msg(str`${this.memberIds.size} of ${this.vehicles.length} vehicles in this group.`)}</p>

                <div class="search">
                    <input
                        type="text"
                        placeholder="${msg('Search by name, VIN, plate or token…')}"
                        .value=${this.query}
                        @input=${(e: Event) => { this.query = (e.target as HTMLInputElement).value; }}
                    />
                </div>

                <div class="list">
                    ${filtered.length === 0
                        ? html`<p class="empty-state">${msg('No vehicles match.')}</p>`
                        : filtered.map((v) => {
                            const member = this.memberIds.has(v.tokenId);
                            return html`
                                <div class="row">
                                    <div class="meta">
                                        <div class="title">${this.vehicleTitle(v)}</div>
                                        <div class="sub2">${this.vehicleIdentifiers(v)}</div>
                                    </div>
                                    <button
                                        class=${member ? 'toggle member' : 'toggle'}
                                        ?disabled=${this.busy.has(v.tokenId)}
                                        title=${member ? msg('Remove from group') : msg('Add to group')}
                                        @click=${() => this.toggle(v)}
                                    >
                                        <span class="material-symbols-outlined">${member ? 'check' : 'add'}</span>
                                    </button>
                                </div>
                            `;
                        })}
                </div>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <div class="footer">
                    <button @click=${this.dispatchClose}>${msg('Done')}</button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'manage-group-vehicles-modal': ManageGroupVehiclesModal;
    }
}
