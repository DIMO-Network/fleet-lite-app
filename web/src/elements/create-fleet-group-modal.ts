import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { FleetGroupService } from '../services/fleet-group-service.ts';
import { FleetGroup } from '../types/group.ts';

/**
 * create-fleet-group-modal — create a new group, or edit an existing one.
 *
 * In edit mode the name is immutable (disabled): renaming would fan out a
 * re-attest to every member, so the UI locks it (matches kaufmann/b2b). Only the
 * color is editable on an existing group.
 *
 * Props:
 *   - group?: when set, the modal is in edit mode for that group.
 * Events:
 *   - close: dismissed, no side effects.
 *   - saved: { group } — created/updated; caller refetches the list.
 */
// User-selectable data colors (stored on the group as a hex string), drawn
// from the DIMO palette and ordered around the hue wheel. Saved groups may
// carry any hex — these are only the presets.
const PRESET_COLORS = [
    '#2B82D5', '#8CD0FF', '#46F1E4', '#36DF71',
    '#FFCD29', '#FFAC60', '#FF6060', '#957CDB',
];

@customElement('create-fleet-group-modal')
export class CreateFleetGroupModal extends LitElement {
    @property({ attribute: false }) group?: FleetGroup;

    @state() private name = '';
    @state() private color = PRESET_COLORS[0];
    @state() private saving = false;
    @state() private errorMessage = '';

    private get isEdit(): boolean {
        return !!this.group;
    }

    connectedCallback() {
        super.connectedCallback();
        if (this.group) {
            this.name = this.group.name;
            this.color = this.group.color;
        }
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                /* Panel tone. --surface-overlay is a requested token (white in
                   light mode); until it exists this falls back to the card tone. */
                --modal-bg: var(--surface-overlay, var(--surface-container-low));
                position: fixed;
                inset: 0;
                z-index: 100;
                display: flex;
                align-items: center;
                justify-content: center;
                padding: 16px;
                background: color-mix(in srgb, var(--canvas) 72%, transparent);
                backdrop-filter: blur(8px);
                -webkit-backdrop-filter: blur(8px);
            }
            .card {
                width: 100%;
                max-width: 440px;
                max-height: calc(100vh - 32px);
                overflow-y: auto;
                background: var(--modal-bg);
                border: none;
                border-radius: var(--radius-xl);
                box-shadow: var(--shadow-float);
                padding: 24px;
                color: var(--on-surface);
                position: relative;
            }
            .card h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); margin-bottom: 4px; padding-right: 40px; }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 24px; }
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
            .field input[type="text"] {
                height: 40px;
                padding: 0 12px;
                background: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            .field input::placeholder { color: var(--on-surface-variant); }
            .field input:focus-visible {
                outline: none;
                border-color: var(--accent);
                box-shadow: 0 0 0 3px var(--accent-soft);
            }
            .field input:disabled { opacity: 0.55; cursor: not-allowed; }
            .field .hint { font: var(--type-label); color: var(--on-surface-variant); }

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
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
            }
            .swatch.custom:hover { color: var(--on-surface); background: var(--surface-container-highest); }
            .swatch.custom .material-symbols-outlined { font-size: 16px; }
            .swatch.custom input { position: absolute; width: 0; height: 0; opacity: 0; }

            /* Preview of the group as it will read elsewhere (group chip tint).
               --c is the chosen color. */
            .preview {
                display: flex; align-items: center; gap: 10px;
                padding: 12px 14px; margin-bottom: 8px;
                background: color-mix(in srgb, var(--c) 12%, var(--surface-container));
                border-radius: var(--radius-md);
            }
            .preview .dot {
                width: 10px; height: 10px; border-radius: var(--radius-full); flex-shrink: 0;
                background: var(--c);
                box-shadow: 0 0 8px color-mix(in srgb, var(--c) 55%, transparent);
            }
            .preview .text { font: 500 15px/22px var(--font-body); color: var(--primary); }

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

    private async onSave() {
        const name = this.name.trim();
        if (!this.isEdit && !name) {
            this.errorMessage = msg('Please enter a group name.');
            return;
        }
        this.saving = true;
        this.errorMessage = '';
        try {
            const svc = FleetGroupService.getInstance();
            const group = this.isEdit
                ? await svc.update(this.group!.id, { color: this.color })
                : await svc.create(name, this.color);
            this.dispatchEvent(new CustomEvent('saved', { detail: { group }, bubbles: true, composed: true }));
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Failed to save group');
            this.saving = false;
        }
    }

    private renderSwatch(c: string) {
        const cls = c.toLowerCase() === this.color.toLowerCase() ? 'swatch selected' : 'swatch';
        return html`<button
            class=${cls}
            style="background:${c}"
            title=${c}
            @click=${() => { this.color = c; }}
        ></button>`;
    }

    render() {
        const name = this.name.trim();
        const canSave = this.isEdit || !!name;
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2>${this.isEdit ? msg('Edit group') : msg('New group')}</h2>
                <p class="sub">${this.isEdit
                    ? msg('Update the color. Group names can’t be changed.')
                    : msg('Name the group and pick a color. You can assign vehicles next.')}</p>

                <div class="field">
                    <label for="name">${msg('Name')}</label>
                    <input
                        id="name"
                        type="text"
                        placeholder="${msg('e.g. East Coast')}"
                        .value=${this.name}
                        ?disabled=${this.isEdit}
                        @input=${(e: Event) => { this.name = (e.target as HTMLInputElement).value; }}
                    />
                    ${this.isEdit ? html`<span class="hint">${msg('Name is locked after creation.')}</span>` : nothing}
                </div>

                <div class="field">
                    <label>${msg('Color')}</label>
                    <div class="swatches">
                        ${PRESET_COLORS.map((c) => this.renderSwatch(c))}
                        <label class="swatch custom" title="${msg('Custom color')}">
                            <span class="material-symbols-outlined">palette</span>
                            <input
                                type="color"
                                .value=${this.color}
                                @input=${(e: Event) => { this.color = (e.target as HTMLInputElement).value; }}
                            />
                        </label>
                    </div>
                </div>

                <div class="preview" style="--c:${this.color}">
                    <span class="dot"></span>
                    <span class="text">${name || msg('Group preview')}</span>
                </div>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <div class="actions">
                    <button class="btn-secondary" @click=${this.dispatchClose}>${msg('Cancel')}</button>
                    <button class="btn-primary" ?disabled=${!canSave || this.saving} @click=${this.onSave}>
                        ${this.saving ? msg('Saving…') : this.isEdit ? msg('Save') : msg('Create group')}
                    </button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'create-fleet-group-modal': CreateFleetGroupModal;
    }
}
