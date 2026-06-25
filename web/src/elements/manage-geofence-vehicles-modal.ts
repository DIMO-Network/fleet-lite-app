import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { GeofenceService } from '../services/geofence-service.ts';
import { Geofence } from '../types/geofence.ts';
import { Vehicle } from '../types/vehicle.ts';

/**
 * manage-geofence-vehicles-modal — toggle which vehicles a manual-scope geofence
 * applies to. The current set is fetched from /fleet/geofences/:id/vehicles on
 * open (geofence membership isn't embedded in /vehicles). Toggling calls
 * add/remove immediately; a `changed` event on close prompts the caller to
 * refetch counts.
 *
 * Props:
 *   - geofence: the manual-scope geofence being managed.
 *   - vehicles: all of the tenant's vehicles.
 */
@customElement('manage-geofence-vehicles-modal')
export class ManageGeofenceVehiclesModal extends LitElement {
    @property({ attribute: false }) geofence!: Geofence;
    @property({ attribute: false }) vehicles: Vehicle[] = [];

    @state() private memberIds = new Set<number>();
    @state() private busy = new Set<number>();
    @state() private query = '';
    @state() private loading = true;
    @state() private errorMessage = '';
    private changed = false;

    connectedCallback() {
        super.connectedCallback();
        void this.loadMembers();
    }

    private async loadMembers() {
        this.loading = true;
        try {
            const ids = await GeofenceService.getInstance().vehicles(this.geofence.id);
            this.memberIds = new Set(ids);
            this.errorMessage = '';
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Failed to load assigned vehicles');
        } finally {
            this.loading = false;
        }
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
            .row .meta .sub2 { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant); margin-top: 2px; }

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
            const svc = GeofenceService.getInstance();
            if (isMember) {
                await svc.removeVehicle(tokenId, this.geofence.id);
                this.memberIds.delete(tokenId);
            } else {
                await svc.addVehicle(tokenId, this.geofence.id);
                this.memberIds.add(tokenId);
            }
            this.memberIds = new Set(this.memberIds);
            this.changed = true;
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Failed to update assignment');
        } finally {
            const next = new Set(this.busy);
            next.delete(tokenId);
            this.busy = next;
        }
    }

    render() {
        const q = this.query.trim().toLowerCase();
        const filtered = q
            ? this.vehicles.filter((v) => this.vehicleTitle(v).toLowerCase().includes(q) || String(v.tokenId).includes(q))
            : this.vehicles;

        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2><span class="dot" style="background:${this.geofence.color}"></span>${this.geofence.name}</h2>
                <p class="sub">${this.loading
                    ? msg('Loading assigned vehicles…')
                    : msg(str`${this.memberIds.size} of ${this.vehicles.length} vehicles assigned.`)}</p>

                <div class="search">
                    <input type="text" placeholder="${msg('Search vehicles…')}"
                        .value=${this.query}
                        @input=${(e: Event) => { this.query = (e.target as HTMLInputElement).value; }} />
                </div>

                <div class="list">
                    ${this.loading
                        ? html`<p class="empty-state">${msg('Loading…')}</p>`
                        : filtered.length === 0
                            ? html`<p class="empty-state">${msg('No vehicles match.')}</p>`
                            : filtered.map((v) => {
                                const member = this.memberIds.has(v.tokenId);
                                return html`
                                    <div class="row">
                                        <div class="meta">
                                            <div class="title">${this.vehicleTitle(v)}</div>
                                            <div class="sub2">${msg(str`Token #${v.tokenId}`)}</div>
                                        </div>
                                        <button
                                            class=${member ? 'toggle member' : 'toggle'}
                                            ?disabled=${this.busy.has(v.tokenId)}
                                            title=${member ? msg('Remove from geofence') : msg('Add to geofence')}
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
        'manage-geofence-vehicles-modal': ManageGeofenceVehiclesModal;
    }
}
