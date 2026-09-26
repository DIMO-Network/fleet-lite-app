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
// User-selectable data colors (stored on the geofence as a hex string), drawn
// from the DIMO palette and ordered around the hue wheel. Saved geofences may
// carry any hex — these are only the presets.
const PRESET_COLORS = [
    '#2B82D5', '#8CD0FF', '#46F1E4', '#36DF71',
    '#FFCD29', '#FFAC60', '#FF6060', '#957CDB',
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
                --modal-bg: var(--surface-overlay);
                position: fixed; inset: 0; z-index: 100;
                display: flex; align-items: center; justify-content: center;
                padding: 16px;
                background: var(--scrim);
                backdrop-filter: blur(8px);
                -webkit-backdrop-filter: blur(8px);
            }
            .card {
                width: 100%; max-width: 480px; max-height: calc(100vh - 32px); overflow-y: auto;
                background: var(--modal-bg); border: none;
                border-radius: var(--radius-xl); box-shadow: var(--shadow-float);
                padding: 24px; color: var(--on-surface); position: relative;
            }
            .card h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); margin-bottom: 4px; padding-right: 40px; }
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

            .field { display: flex; flex-direction: column; gap: 8px; margin-bottom: 20px; }
            .field > label { font: var(--type-label); color: var(--on-surface-variant); }
            .field input[type="text"], .field input[type="number"] {
                height: 40px;
                padding: 0 12px;
                background: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--control-border);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            .field input::placeholder { color: var(--on-surface-variant); }
            .field input:focus-visible {
                outline: none;
                border-color: var(--focus-ring);
                box-shadow: 0 0 0 3px var(--accent-soft);
            }
            .field .hint { font: var(--type-label); color: var(--on-surface-variant); }
            .suffix-row { display: flex; align-items: center; gap: 10px; }
            .suffix-row input { flex: 1; min-width: 0; }
            .suffix-row .unit { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .swatches { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
            .swatch {
                position: relative;
                width: 28px; height: 28px; border-radius: var(--radius-full);
                border: none; cursor: pointer; padding: 0;
                transition: transform 0.12s ease, box-shadow 0.12s ease;
            }
            .swatch:hover { transform: scale(1.08); }
            /* Ink ring with a gap: legible on every swatch hue, incl. mint. */
            .swatch.selected { box-shadow: 0 0 0 2px var(--modal-bg), 0 0 0 4px var(--primary); }
            .swatch.custom {
                display: flex; align-items: center; justify-content: center;
                background: var(--surface-container-high); color: var(--on-surface-variant);
            }
            .swatch.custom:hover { color: var(--on-surface); background: var(--surface-container-highest); }
            .swatch.custom .material-symbols-outlined { font-size: 16px; }
            .swatch.custom input { position: absolute; width: 0; height: 0; opacity: 0; }

            /* Segmented pill, same as the view switch in the page header. */
            .segmented {
                display: flex; gap: 2px; padding: 3px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
            }
            .segmented button {
                flex: 1; padding: 7px 10px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                font: 500 13px/18px var(--font-body);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .segmented button:hover { color: var(--on-surface); }
            .segmented button.active {
                color: var(--primary);
                background: var(--surface-bright);
                box-shadow: var(--shadow-sm);
            }

            .group-list { display: flex; flex-direction: column; gap: 2px; margin-top: 4px; max-height: 188px; overflow-y: auto; }
            .group-row {
                display: flex; align-items: center; gap: 12px; min-height: 44px; padding: 0 12px; cursor: pointer;
                border-radius: var(--radius-md); background: var(--surface-container);
                transition: background 0.15s ease;
            }
            .group-row:hover { background: var(--surface-container-high); }
            .group-row.selected { background: var(--surface-container-high); box-shadow: inset 0 0 0 1.5px var(--primary); }
            .group-row .dot { width: 10px; height: 10px; border-radius: var(--radius-full); flex-shrink: 0; }
            .group-row .gname { flex: 1; font: var(--type-body-sm); color: var(--on-surface); }
            .group-row.selected .gname { color: var(--primary); font-weight: 500; }
            .group-row .check { color: var(--primary); font-size: 20px; }

            .meta-line {
                display: inline-flex; align-items: center; gap: 6px;
                padding: 4px 10px 4px 8px; margin-bottom: 20px;
                border-radius: var(--radius-sm);
                background: var(--surface-container-high);
                font: var(--type-label); color: var(--on-surface-variant);
            }
            .meta-line .material-symbols-outlined { font-size: 16px; }

            .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 24px; }

            .error-text {
                padding: 10px 12px; margin-top: 16px;
                background: var(--error-container); color: var(--error);
                border-radius: var(--radius-md); font: var(--type-body-sm);
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
            aria-pressed=${this.scope === scope}
            @click=${() => { this.scope = scope; }}
        >${label}</button>`;
    }

    render() {
        const canSave = !!this.name.trim() && (this.isEdit || !!this.pendingGeometry);
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
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
                            <span class="material-symbols-outlined">palette</span>
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
                    <button class="btn-secondary" @click=${this.dispatchClose}>${msg('Cancel')}</button>
                    <button class="btn-primary" ?disabled=${!canSave || this.saving} @click=${this.onSave}>
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
