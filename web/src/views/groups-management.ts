import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { TenantService } from '../services/tenant-service.ts';
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
    @state() private searchQuery = '';
    /** True for limited members: the view is a read-only list of their groups. */
    @state() private readOnly = false;

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
            // Limited members get a read-only view (the API rejects mutations too).
            try {
                const access = await TenantService.getInstance().fetchMyAccess();
                this.readOnly = access.allowedGroupIds !== null;
            } catch {
                this.readOnly = false;
            }
        } catch (e) {
            console.error('Failed to load groups', e);
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to load groups');
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
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to delete group');
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
                position: sticky; top: 0; z-index: 40; flex-shrink: 0;
                display: flex; align-items: center; justify-content: space-between;
                height: var(--top-bar-height); padding: 0 var(--gutter);
                /* Same tone as the sheet, so it reads as transparent while still
                   covering cards that scroll under it. */
                background: var(--background);
            }
            header.top-bar h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            .header-actions { display: flex; align-items: center; gap: 20px; }
            .group-total { display: inline-flex; align-items: baseline; gap: 6px; white-space: nowrap; }
            .group-total .num { font: 600 15px/22px var(--font-headline); color: var(--primary); }
            .group-total .lbl { font: var(--type-label); color: var(--on-surface-variant); }
            .new-btn { padding-left: 14px; }
            .new-btn .material-symbols-outlined { font-size: 20px; }

            .canvas {
                flex: 1; width: 100%; max-width: 1200px;
                padding: 4px var(--gutter) var(--stack-lg);
            }

            .toolbar {
                display: flex;
                align-items: center;
                gap: 12px;
                flex-wrap: wrap;
                margin-bottom: 20px;
            }
            /* Same search treatment as the vehicles panel on the map. */
            .search-wrap {
                display: flex;
                align-items: center;
                gap: 8px;
                height: 40px;
                padding: 0 12px;
                flex: 1;
                max-width: 360px;
                border-radius: var(--radius-md);
                background: var(--surface-container-high);
                transition: box-shadow 0.15s ease;
            }
            .search-wrap:focus-within { box-shadow: 0 0 0 2px var(--focus-ring); }
            .search-wrap > .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); }
            .search-wrap input {
                background: none;
                border: none;
                outline: none;
                color: var(--on-surface);
                font: var(--type-body-sm);
                flex: 1;
                min-width: 0;
            }
            /* The pill draws the ring; drop the global input focus halo. */
            .search-wrap input:focus-visible { box-shadow: none; }
            .search-wrap input::placeholder { color: var(--on-surface-variant); }
            .search-wrap input::-webkit-search-cancel-button { display: none; }
            .clear-btn {
                padding: 2px;
                color: var(--on-surface-variant);
                border-radius: var(--radius-full);
                display: inline-flex;
            }
            .clear-btn:hover { color: var(--primary); background: var(--surface-container-highest); }
            .clear-btn .material-symbols-outlined { font-size: 16px; }

            .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(272px, 1fr)); gap: 16px; }

            /* Tonal card; the group color lives in the tile, not the chrome.
               --c is set per card from the group's (arbitrary) hex color. */
            .group-card {
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
                padding: 20px;
                display: flex; flex-direction: column; gap: 20px;
                min-height: 148px;
                transition: background 0.15s ease;
            }
            .group-card:hover { background: var(--surface-container); }
            .group-head { display: flex; align-items: center; gap: 12px; min-height: 36px; }
            .group-head .tile {
                width: 36px; height: 36px; flex-shrink: 0;
                border-radius: var(--radius-md);
                background: color-mix(in srgb, var(--c) 16%, transparent);
                display: flex; align-items: center; justify-content: center;
            }
            .group-head .dot {
                width: 12px; height: 12px; border-radius: var(--radius-full);
                background: var(--c);
                box-shadow: 0 0 10px color-mix(in srgb, var(--c) 55%, transparent);
            }
            .group-head .name {
                font: 600 17px/24px var(--font-headline); letter-spacing: -0.01em; color: var(--primary);
                flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }

            .foot { display: flex; align-items: flex-end; justify-content: space-between; gap: 8px; margin-top: auto; }
            .count { display: flex; align-items: baseline; gap: 8px; }
            .count .n { font: 600 32px/36px var(--font-headline); letter-spacing: -0.03em; color: var(--primary); }
            .count .unit { font: var(--type-body-sm); color: var(--on-surface-variant); }

            /* Quiet icon actions: names live in aria-label + tooltip. */
            .card-actions { display: flex; align-items: center; gap: 2px; margin: 0 -6px -4px 0; }
            .card-actions button {
                width: 34px; height: 34px;
                display: inline-flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .card-actions button:hover,
            .card-actions button:focus-visible { background: var(--surface-container-high); color: var(--on-surface); }
            .card-actions button.danger:hover,
            .card-actions button.danger:focus-visible { background: var(--error-container); color: var(--error); }
            .card-actions .material-symbols-outlined { font-size: 20px; }

            .confirm {
                display: flex; align-items: center; flex-wrap: wrap; gap: 8px;
                margin-top: auto;
                font: var(--type-body-sm); color: var(--on-surface);
            }
            .confirm span { flex: 1 1 100%; }
            .confirm button {
                min-height: 32px; padding: 0 14px;
                border-radius: var(--radius-full);
                font: 500 13px/18px var(--font-body);
                transition: filter 0.15s ease, background 0.15s ease;
            }
            .confirm .yes { background: var(--error-container); color: var(--error); font-weight: 600; }
            .confirm .yes:hover { filter: brightness(1.15); }
            .confirm .no { background: var(--surface-container-high); color: var(--on-surface); }
            .confirm .no:hover { background: var(--surface-container-highest); }

            .empty-state { color: var(--on-surface-variant); font: var(--type-body-md); padding: 64px 24px; text-align: center; }
            .empty-state.error { color: var(--error); }
            .empty-state .material-symbols-outlined {
                font-size: 28px; display: flex; align-items: center; justify-content: center;
                width: 56px; height: 56px; margin: 0 auto 16px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
            }
        `,
    ];

    private get visibleGroups(): FleetGroup[] {
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) return this.groups;
        return this.groups.filter((g) => g.name.toLowerCase().includes(q));
    }

    private renderCard(g: FleetGroup) {
        const confirming = this.confirmingDeleteId === g.id;
        const count = g.vehicleCount ?? 0;
        return html`
            <div class="group-card" style="--c:${g.color}">
                <div class="group-head">
                    <span class="tile"><span class="dot"></span></span>
                    <span class="name" title=${g.name}>${g.name}</span>
                </div>
                ${confirming && !this.readOnly
                    ? html`<div class="confirm">
                        <span>${msg(str`Delete “${g.name}”?`)}</span>
                        <button class="yes" @click=${() => this.onDelete(g)}>${msg('Delete')}</button>
                        <button class="no" @click=${() => { this.confirmingDeleteId = null; }}>${msg('Cancel')}</button>
                    </div>`
                    : html`<div class="foot">
                        <div class="count">
                            <span class="n">${count}</span>
                            <span class="unit">${count === 1 ? msg('vehicle') : msg('vehicles')}</span>
                        </div>
                        ${this.readOnly
                            ? nothing
                            : html`<div class="card-actions">
                                <button aria-label=${msg('Vehicles')} title=${msg('Vehicles')} @click=${() => { this.managing = g; }}>
                                    <span class="material-symbols-outlined">directions_car</span>
                                </button>
                                <button aria-label=${msg('Color')} title=${msg('Color')} @click=${() => { this.editing = g; }}>
                                    <span class="material-symbols-outlined">palette</span>
                                </button>
                                <button class="danger" aria-label=${msg('Delete')} title=${msg('Delete')} @click=${() => { this.confirmingDeleteId = g.id; }}>
                                    <span class="material-symbols-outlined">delete</span>
                                </button>
                            </div>`}
                    </div>`}
            </div>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <h2>${msg('Fleet Groups')}</h2>
                <div class="header-actions">
                    ${this.loading
                        ? nothing
                        : html`<span class="group-total">
                            <span class="num">${this.groups.length}</span>
                            <span class="lbl">${this.groups.length === 1 ? msg('group total') : msg('groups total')}</span>
                        </span>`}
                    ${!this.readOnly
                        ? html`<button class="btn-primary new-btn" @click=${() => { this.creating = true; }}>
                            <span class="material-symbols-outlined">add</span> ${msg('New group')}
                        </button>`
                        : nothing}
                </div>
            </header>

            <div class="canvas">
                ${this.groups.length > 0
                    ? html`
                        <div class="toolbar">
                            <div class="search-wrap">
                                <span class="material-symbols-outlined">search</span>
                                <input
                                    type="search"
                                    placeholder="${msg('Search groups…')}"
                                    .value=${this.searchQuery}
                                    @input=${(e: Event) => { this.searchQuery = (e.target as HTMLInputElement).value; }}
                                />
                                ${this.searchQuery ? html`
                                    <button class="clear-btn" @click=${() => { this.searchQuery = ''; }}>
                                        <span class="material-symbols-outlined">close</span>
                                    </button>
                                ` : nothing}
                            </div>
                        </div>
                      `
                    : nothing}
                ${this.loading
                    ? html`<p class="empty-state">${msg('Loading groups…')}</p>`
                    : this.errorMessage
                        ? html`<p class="empty-state error">${this.errorMessage}</p>`
                        : this.groups.length === 0
                            ? html`<div class="empty-state">
                                <span class="material-symbols-outlined">workspaces</span>
                                ${msg('No groups yet. Create one to organize your fleet.')}
                            </div>`
                            : this.visibleGroups.length === 0
                                ? html`<div class="empty-state">
                                    <span class="material-symbols-outlined">search_off</span>
                                    ${msg('No groups match your search.')}
                                </div>`
                                : html`<div class="grid">${this.visibleGroups.map((g) => this.renderCard(g))}</div>`}
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
