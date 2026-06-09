import { LitElement, html, css, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { FleetGroup } from '../types/group.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';
import '../elements/create-fleet-group-modal.ts';
import '../elements/manage-group-vehicles-modal.ts';

/**
 * groups-management-view — manage the tenant's fleet groups: create, recolor,
 * delete, and assign vehicles. Reached at #/:tenantId/groups.
 */
@customElement('groups-management-view')
export class GroupsManagementView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private groups: FleetGroup[] = [];
    @state() private vehicles: Vehicle[] = [];
    @state() private loading = true;
    @state() private errorMessage: string | null = null;

    @state() private editing: FleetGroup | null = null;
    @state() private creating = false;
    @state() private managing: FleetGroup | null = null;
    @state() private confirmingDeleteId: string | null = null;

    connectedCallback() {
        super.connectedCallback();
        void this.load();
    }

    private async load() {
        this.loading = true;
        try {
            const [groups, vehiclesRes] = await Promise.all([
                FleetGroupService.getInstance().list(),
                ApiService.getInstance().get<VehiclesResponse>('/vehicles'),
            ]);
            this.groups = groups;
            this.vehicles = vehiclesRes.vehicles || [];
            this.errorMessage = null;
        } catch (e) {
            console.error('Failed to load groups', e);
            this.errorMessage = e instanceof Error ? e.message : 'Failed to load groups';
        } finally {
            this.loading = false;
        }
    }

    private async onDelete(group: FleetGroup) {
        try {
            await FleetGroupService.getInstance().delete(group.id);
            this.confirmingDeleteId = null;
            await this.load();
        } catch (e) {
            console.error('Failed to delete group', e);
            this.errorMessage = e instanceof Error ? e.message : 'Failed to delete group';
        }
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex; flex-direction: column;
                width: 100%; height: 100%; overflow-y: auto; background: var(--background);
            }
            header.top-bar {
                position: sticky; top: 0; z-index: 40;
                display: flex; align-items: center; justify-content: space-between;
                height: 80px; padding: 0 var(--gutter);
                background: var(--background); border-bottom: 1px solid var(--outline-variant);
            }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            .new-btn {
                display: flex; align-items: center; gap: 8px;
                background: var(--primary); color: var(--on-primary);
                border: none; padding: 10px 16px; border-radius: var(--radius-md);
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; font-weight: 700; cursor: pointer;
            }
            .canvas { flex: 1; width: 100%; max-width: 880px; margin: 0 auto; padding: var(--stack-lg) var(--gutter); box-sizing: border-box; }

            .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 16px; }

            .group-card {
                background: var(--surface-container-low); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg); padding: 16px; display: flex; flex-direction: column; gap: 12px;
            }
            .group-head { display: flex; align-items: center; gap: 12px; }
            .group-head .dot { width: 18px; height: 18px; border-radius: var(--radius-full); flex-shrink: 0; }
            .group-head .name { font: var(--type-body-lg); font-weight: 600; color: var(--primary); flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
            .count { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .card-actions { display: flex; gap: 8px; flex-wrap: wrap; }
            .card-actions button {
                display: flex; align-items: center; gap: 6px;
                background: transparent; color: var(--on-surface-variant);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                padding: 8px 10px; font: var(--type-label-caps); letter-spacing: 0.04em; text-transform: uppercase; cursor: pointer;
                transition: color 0.15s ease, border-color 0.15s ease;
            }
            .card-actions button:hover { color: var(--primary); border-color: var(--primary); }
            .card-actions button.danger:hover { color: var(--error); border-color: var(--error); }
            .card-actions .material-symbols-outlined { font-size: 16px; }

            .confirm { display: flex; align-items: center; gap: 8px; font: var(--type-body-sm); color: var(--error); }
            .confirm button {
                border: none; border-radius: var(--radius-sm); padding: 6px 10px;
                font: var(--type-label-caps); letter-spacing: 0.04em; text-transform: uppercase; cursor: pointer;
            }
            .confirm .yes { background: var(--error); color: var(--on-primary); }
            .confirm .no { background: var(--surface-container-high); color: var(--on-surface); }

            .empty-state { color: var(--on-surface-variant); font: var(--type-body-md); padding: 48px 24px; text-align: center; }
            .empty-state.error { color: var(--error); }
            .empty-state .material-symbols-outlined { font-size: 40px; display: block; margin-bottom: 12px; opacity: 0.6; }
        `,
    ];

    private renderCard(g: FleetGroup) {
        const confirming = this.confirmingDeleteId === g.id;
        return html`
            <div class="group-card">
                <div class="group-head">
                    <span class="dot" style="background:${g.color}"></span>
                    <span class="name">${g.name}</span>
                </div>
                <span class="count">${g.vehicleCount ?? 0} ${(g.vehicleCount ?? 0) === 1 ? 'vehicle' : 'vehicles'}</span>
                ${confirming
                    ? html`<div class="confirm">
                        <span>Delete “${g.name}”?</span>
                        <button class="yes" @click=${() => this.onDelete(g)}>Delete</button>
                        <button class="no" @click=${() => { this.confirmingDeleteId = null; }}>Cancel</button>
                    </div>`
                    : html`<div class="card-actions">
                        <button @click=${() => { this.managing = g; }}>
                            <span class="material-symbols-outlined">directions_car</span> Vehicles
                        </button>
                        <button @click=${() => { this.editing = g; }}>
                            <span class="material-symbols-outlined">palette</span> Color
                        </button>
                        <button class="danger" @click=${() => { this.confirmingDeleteId = g.id; }}>
                            <span class="material-symbols-outlined">delete</span> Delete
                        </button>
                    </div>`}
            </div>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <h2>Fleet Groups</h2>
                <button class="new-btn" @click=${() => { this.creating = true; }}>
                    <span class="material-symbols-outlined">add</span> New group
                </button>
            </header>

            <div class="canvas">
                ${this.loading
                    ? html`<p class="empty-state">Loading groups…</p>`
                    : this.errorMessage
                        ? html`<p class="empty-state error">${this.errorMessage}</p>`
                        : this.groups.length === 0
                            ? html`<div class="empty-state">
                                <span class="material-symbols-outlined">workspaces</span>
                                No groups yet. Create one to organize your fleet.
                            </div>`
                            : html`<div class="grid">${this.groups.map((g) => this.renderCard(g))}</div>`}
            </div>

            ${this.creating
                ? html`<create-fleet-group-modal
                    @close=${() => { this.creating = false; }}
                    @saved=${() => { this.creating = false; void this.load(); }}
                  ></create-fleet-group-modal>`
                : nothing}

            ${this.editing
                ? html`<create-fleet-group-modal
                    .group=${this.editing}
                    @close=${() => { this.editing = null; }}
                    @saved=${() => { this.editing = null; void this.load(); }}
                  ></create-fleet-group-modal>`
                : nothing}

            ${this.managing
                ? html`<manage-group-vehicles-modal
                    .group=${this.managing}
                    .vehicles=${this.vehicles}
                    @close=${() => { this.managing = null; }}
                    @changed=${() => { void this.load(); }}
                  ></manage-group-vehicles-modal>`
                : nothing}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'groups-management-view': GroupsManagementView;
    }
}
