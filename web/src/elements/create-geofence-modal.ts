import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { GeofenceService } from '../services/geofence-service.ts';
import { Geofence, GeoJSONPolygon, GeofenceScope } from '../types/geofence.ts';
import { FleetGroup } from '../types/group.ts';
import { polygonAreaM2, formatArea } from '../utils/geo.ts';

/**
 * create-geofence-modal — create a geofence from a freshly-drawn polygon, or
 * edit an existing one's metadata (name, color, speed limit, scope).
 *
 * Geometry is only set on create (passed in via `pendingGeometry` from the
 * map-draw buffer); editing the shape is out of scope for v1 — delete & redraw.
 *
 * Props:
 *   - geofence?: when set, edit mode for that geofence.
 *   - pendingGeometry?: the drawn polygon, required in create mode.
 *   - groups: the tenant's fleet groups (for the "by group" scope picker).
 * Events:
 *   - close: dismissed.
 *   - saved: { geofence } — created/updated; caller refetches.
 */
const PRESET_COLORS = [
    '#ea6b18', '#ffb691', '#f2c94c', '#27ae60',
    '#2d9cdb', '#9b51e0', '#eb5757', '#8e9192',
];

@customElement('create-geofence-modal')
export class CreateGeofenceModal extends LitElement {
    @property({ attribute: false }) geofence?: Geofence;
    @property({ attribute: false }) pendingGeometry?: GeoJSONPolygon;
    @property({ attribute: false }) groups: FleetGroup[] = [];

    @state() private name = '';
    @state() private color = PRESET_COLORS[0];
    @state() private speedLimit = '';
    @state() private scope: GeofenceScope = 'all';
    @state() private selectedGroupIds = new Set<string>();
    @state() private saving = false;
    @state() private errorMessage = '';

    private get isEdit(): boolean {
        return !!this.geofence;
    }

    connectedCallback() {
        super.connectedCallback();
        if (this.geofence) {
            this.name = this.geofence.name;
            this.color = this.geofence.color;
            this.speedLimit = this.geofence.speedLimitKph != null ? String(this.geofence.speedLimitKph) : '';
            this.scope = this.geofence.scope;
            this.selectedGroupIds = new Set(this.geofence.groupIds || []);
        }
    }

    private get areaM2(): number {
        if (this.isEdit) return this.geofence!.areaM2;
        return this.pendingGeometry ? polygonAreaM2(this.pendingGeometry) : 0;
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
                width: 100%; max-width: 460px; max-height: 86vh; overflow-y: auto;
                background: var(--surface-container); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg); padding: 24px; color: var(--on-surface); position: relative;
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
            .field input[type="text"], .field input[type="number"] {
                background: var(--surface-container-low); color: var(--on-surface);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                padding: 10px 12px; font-family: inherit; font-size: 14px;
            }
            .field input:focus { outline: 1px solid var(--primary); }
            .field .hint { font: var(--type-body-sm); color: var(--on-surface-variant); }
            .suffix-row { display: flex; align-items: center; gap: 8px; }
            .suffix-row input { flex: 1; }
            .suffix-row .unit { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .swatches { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
            .swatch { width: 28px; height: 28px; border-radius: var(--radius-full); border: 2px solid transparent; cursor: pointer; padding: 0; }
            .swatch.selected { border-color: var(--primary); }
            .swatch.custom {
                display: flex; align-items: center; justify-content: center; position: relative;
                background: var(--surface-container-low); border: 1px solid var(--outline-variant); color: var(--on-surface-variant);
            }
            .swatch.custom input { position: absolute; width: 0; height: 0; opacity: 0; }

            .segmented { display: flex; gap: 0; border: 1px solid var(--outline-variant); border-radius: var(--radius-md); overflow: hidden; }
            .segmented button {
                flex: 1; padding: 10px 8px; background: var(--surface-container-low); color: var(--on-surface-variant);
                border: none; border-right: 1px solid var(--outline-variant); cursor: pointer;
                font: var(--type-label-caps); letter-spacing: 0.04em; text-transform: uppercase;
            }
            .segmented button:last-child { border-right: none; }
            .segmented button.active { background: var(--primary); color: var(--on-primary); font-weight: 700; }

            .group-list { display: flex; flex-direction: column; gap: 6px; margin-top: 8px; max-height: 180px; overflow-y: auto; }
            .group-row {
                display: flex; align-items: center; gap: 10px; padding: 8px 10px; cursor: pointer;
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md); background: var(--surface-container-low);
            }
            .group-row.selected { border-color: var(--primary); }
            .group-row .dot { width: 12px; height: 12px; border-radius: var(--radius-full); flex-shrink: 0; }
            .group-row .gname { flex: 1; font: var(--type-body-md); color: var(--on-surface); }
            .group-row .check { color: var(--primary); }

            .meta-line { display: flex; align-items: center; gap: 8px; font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 16px; }
            .meta-line .material-symbols-outlined { font-size: 16px; }

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

    private toggleGroup(id: string) {
        const next = new Set(this.selectedGroupIds);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        this.selectedGroupIds = next;
    }

    private async onSave() {
        const name = this.name.trim();
        if (!name) {
            this.errorMessage = msg('Please enter a geofence name.');
            return;
        }
        if (this.scope === 'group' && this.selectedGroupIds.size === 0) {
            this.errorMessage = msg('Pick at least one group, or choose a different scope.');
            return;
        }
        if (!this.isEdit && !this.pendingGeometry) {
            this.errorMessage = msg('Draw the geofence area on the map first.');
            return;
        }
        const speedRaw = this.speedLimit.trim();
        const speedLimitKph = speedRaw === '' ? undefined : Math.max(1, Math.round(Number(speedRaw)));
        const groupIds = this.scope === 'group' ? [...this.selectedGroupIds] : [];

        this.saving = true;
        this.errorMessage = '';
        try {
            const svc = GeofenceService.getInstance();
            const geofence = this.isEdit
                ? await svc.update(this.geofence!.id, { name, color: this.color, speedLimitKph, scope: this.scope, groupIds })
                : await svc.create({ name, color: this.color, geometry: this.pendingGeometry!, speedLimitKph, scope: this.scope, groupIds });
            this.dispatchEvent(new CustomEvent('saved', { detail: { geofence }, bubbles: true, composed: true }));
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Failed to save geofence');
            this.saving = false;
        }
    }

    private renderSwatch(c: string) {
        const cls = c.toLowerCase() === this.color.toLowerCase() ? 'swatch selected' : 'swatch';
        return html`<button class=${cls} style="background:${c}" title=${c} @click=${() => { this.color = c; }}></button>`;
    }

    private renderScopeButton(scope: GeofenceScope, label: string) {
        return html`<button
            class=${this.scope === scope ? 'active' : ''}
            @click=${() => { this.scope = scope; }}
        >${label}</button>`;
    }

    render() {
        const canSave = !!this.name.trim() && (this.isEdit || !!this.pendingGeometry);
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2>${this.isEdit ? msg('Edit geofence') : msg('New geofence')}</h2>
                <p class="sub">${this.isEdit
                    ? msg('Update the geofence details. Redraw the area by deleting and recreating it.')
                    : msg('Name the area you drew, then choose which vehicles it applies to.')}</p>

                <div class="meta-line">
                    <span class="material-symbols-outlined">crop_free</span>
                    <span>${msg('Area')}: ${formatArea(this.areaM2)}</span>
                </div>

                <div class="field">
                    <label for="name">${msg('Name')}</label>
                    <input id="name" type="text" placeholder="${msg('e.g. Downtown Depot')}"
                        .value=${this.name}
                        @input=${(e: Event) => { this.name = (e.target as HTMLInputElement).value; }} />
                </div>

                <div class="field">
                    <label>${msg('Color')}</label>
                    <div class="swatches">
                        ${PRESET_COLORS.map((c) => this.renderSwatch(c))}
                        <label class="swatch custom" title="${msg('Custom color')}">
                            <span class="material-symbols-outlined" style="font-size:18px;">palette</span>
                            <input type="color" .value=${this.color}
                                @input=${(e: Event) => { this.color = (e.target as HTMLInputElement).value; }} />
                        </label>
                    </div>
                </div>

                <div class="field">
                    <label for="speed">${msg('Speed limit (optional)')}</label>
                    <div class="suffix-row">
                        <input id="speed" type="number" min="1" placeholder="${msg('e.g. 50')}"
                            .value=${this.speedLimit}
                            @input=${(e: Event) => { this.speedLimit = (e.target as HTMLInputElement).value; }} />
                        <span class="unit">${msg('km/h')}</span>
                    </div>
                </div>

                <div class="field">
                    <label>${msg('Applies to')}</label>
                    <div class="segmented">
                        ${this.renderScopeButton('all', msg('All vehicles'))}
                        ${this.renderScopeButton('group', msg('By group'))}
                        ${this.renderScopeButton('manual', msg('Specific'))}
                    </div>
                    ${this.scope === 'group'
                        ? this.groups.length === 0
                            ? html`<span class="hint">${msg('No fleet groups yet — create groups first, or pick another scope.')}</span>`
                            : html`<div class="group-list">
                                ${this.groups.map((g) => {
                                    const sel = this.selectedGroupIds.has(g.id);
                                    return html`<div class=${sel ? 'group-row selected' : 'group-row'} @click=${() => this.toggleGroup(g.id)}>
                                        <span class="dot" style="background:${g.color}"></span>
                                        <span class="gname">${g.name}</span>
                                        ${sel ? html`<span class="material-symbols-outlined check">check</span>` : nothing}
                                    </div>`;
                                })}
                            </div>`
                        : this.scope === 'manual'
                            ? html`<span class="hint">${this.isEdit
                                ? msg('Use the “Vehicles” button on the geofence to assign specific vehicles.')
                                : msg('Save first, then assign specific vehicles with the “Vehicles” button.')}</span>`
                            : html`<span class="hint">${msg('Applies to every vehicle in this fleet.')}</span>`}
                </div>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <div class="actions">
                    <button class="ghost" @click=${this.dispatchClose}>${msg('Cancel')}</button>
                    <button class="primary" ?disabled=${!canSave || this.saving} @click=${this.onSave}>
                        ${this.saving ? msg('Saving…') : this.isEdit ? msg('Save') : msg('Create geofence')}
                    </button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'create-geofence-modal': CreateGeofenceModal;
    }
}
