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
                /* Panel tone. --surface-overlay is a requested token (white in
                   light mode); until it exists this falls back to the card tone. */
                --modal-bg: var(--surface-overlay, var(--surface-container-low));
                position: fixed; inset: 0; z-index: 100;
                display: flex; align-items: center; justify-content: center;
                padding: 16px;
                background: color-mix(in srgb, var(--canvas) 72%, transparent);
                backdrop-filter: blur(8px);
                -webkit-backdrop-filter: blur(8px);
            }
            .card {
                width: 100%; max-width: 520px; max-height: min(80vh, 720px);
                background: var(--modal-bg); border: none;
                border-radius: var(--radius-xl); box-shadow: var(--shadow-float);
                padding: 24px; color: var(--on-surface);
                position: relative; display: flex; flex-direction: column;
            }
            .card h2 {
                font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary);
                margin-bottom: 4px; padding-right: 40px;
                display: flex; align-items: center; gap: 10px;
            }
            .card h2 .dot {
                width: 12px; height: 12px; border-radius: var(--radius-full); flex-shrink: 0;
                background: var(--c);
                box-shadow: 0 0 8px color-mix(in srgb, var(--c) 55%, transparent);
            }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 16px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                width: 36px; height: 36px;
                display: flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .close .material-symbols-outlined { font-size: 20px; }

            /* Same search field as the vehicles panel on the map. */
            .search {
                display: flex; align-items: center; gap: 8px;
                flex-shrink: 0; height: 40px; padding: 0 12px; margin-bottom: 12px;
                border-radius: var(--radius-md);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            .search:focus-within { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
            .search > .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); }
            .search input {
                flex: 1; min-width: 0;
                background: none; border: none;
                color: var(--on-surface); font: var(--type-body-sm);
            }
            .search input:focus-visible { outline: none; box-shadow: none; }
            .search input::placeholder { color: var(--on-surface-variant); }

            .list {
                flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 2px;
                margin: 0 -8px; padding: 0 8px;
            }
            .list::-webkit-scrollbar { width: 6px; }
            .list::-webkit-scrollbar-thumb { background-color: var(--outline-variant); border-radius: 10px; }

            .row {
                display: flex; align-items: center; gap: 12px;
                min-height: 56px; padding: 8px 8px 8px 12px;
                border-radius: var(--radius-md);
                transition: background 0.15s ease;
            }
            .row:hover { background: var(--surface-container); }
            .row .meta { flex: 1; min-width: 0; }
            .row .meta .title {
                font: 500 15px/22px var(--font-body); color: var(--primary);
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }
            /* Identifiers can outrun the row, so they clip rather than wrap
               the card wider — the search box is how you find a specific one. */
            .row .meta .sub2 {
                font: var(--type-label); color: var(--on-surface-variant); margin-top: 2px;
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }

            .toggle {
                width: 36px; height: 36px; display: flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full);
                background: var(--surface-container-high); color: var(--on-surface-variant);
                flex-shrink: 0;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .toggle .material-symbols-outlined { font-size: 20px; }
            .toggle:hover { background: var(--accent-soft); color: var(--accent-ink); }
            .toggle.member { background: var(--accent-soft-strong); color: var(--accent-ink); }
            .toggle:disabled { opacity: 0.5; cursor: progress; }

            .empty-state { color: var(--on-surface-variant); font: var(--type-body-sm); padding: 24px; text-align: center; }
            .error-text {
                padding: 10px 12px; margin-top: 12px;
                background: var(--error-container); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm);
            }
            .footer { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
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
                <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2 style="--c:${this.geofence.color}"><span class="dot"></span>${this.geofence.name}</h2>
                <p class="sub">${this.loading
                    ? msg('Loading assigned vehicles…')
                    : msg(str`${this.memberIds.size} of ${this.vehicles.length} vehicles assigned.`)}</p>

                <div class="search">
                    <span class="material-symbols-outlined">search</span>
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
                    <button class="btn-primary" @click=${this.dispatchClose}>${msg('Done')}</button>
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
