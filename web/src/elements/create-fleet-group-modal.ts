import { LitElement, html, css, nothing } from 'lit';
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
const PRESET_COLORS = [
    '#ea6b18', '#ffb691', '#f2c94c', '#27ae60',
    '#2d9cdb', '#9b51e0', '#eb5757', '#8e9192',
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
                max-width: 440px;
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 24px;
                color: var(--on-surface);
                position: relative;
            }
            .card h2 { font: var(--type-headline-md); margin-bottom: 4px; }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 24px; }
            .close {
                position: absolute; top: 16px; right: 16px;
                background: none; border: none; color: var(--on-surface-variant); padding: 4px; cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            .field { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; }
            .field label {
                font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; color: var(--on-surface-variant);
            }
            .field input[type="text"] {
                background: var(--surface-container-low); color: var(--on-surface);
                border: 1px solid var(--outline-variant); border-radius: var(--radius-md);
                padding: 10px 12px; font-family: inherit; font-size: 14px;
            }
            .field input:focus { outline: 1px solid var(--primary); }
            .field input:disabled { opacity: 0.5; cursor: not-allowed; }
            .field .hint { font: var(--type-body-sm); color: var(--on-surface-variant); }

            .swatches { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
            .swatch {
                width: 28px; height: 28px; border-radius: var(--radius-full);
                border: 2px solid transparent; cursor: pointer; padding: 0;
            }
            .swatch.selected { border-color: var(--primary); }
            .swatch.custom {
                display: flex; align-items: center; justify-content: center;
                background: var(--surface-container-low); border: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
            }
            .swatch.custom input { position: absolute; width: 0; height: 0; opacity: 0; }

            .preview {
                display: flex; align-items: center; gap: 10px;
                padding: 10px 12px; margin-bottom: 16px;
                background: var(--surface-container-low); border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
            }
            .preview .dot { width: 16px; height: 16px; border-radius: var(--radius-full); flex-shrink: 0; }
            .preview .text { font: var(--type-body-md); color: var(--primary); }

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

    private async onSave() {
        const name = this.name.trim();
        if (!this.isEdit && !name) {
            this.errorMessage = 'Please enter a group name.';
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
            this.errorMessage = err instanceof Error ? err.message : 'Failed to save group';
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
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2>${this.isEdit ? 'Edit group' : 'New group'}</h2>
                <p class="sub">${this.isEdit
                    ? 'Update the color. Group names can’t be changed.'
                    : 'Name the group and pick a color. You can assign vehicles next.'}</p>

                <div class="field">
                    <label for="name">Name</label>
                    <input
                        id="name"
                        type="text"
                        placeholder="e.g. East Coast"
                        .value=${this.name}
                        ?disabled=${this.isEdit}
                        @input=${(e: Event) => { this.name = (e.target as HTMLInputElement).value; }}
                    />
                    ${this.isEdit ? html`<span class="hint">Name is locked after creation.</span>` : nothing}
                </div>

                <div class="field">
                    <label>Color</label>
                    <div class="swatches">
                        ${PRESET_COLORS.map((c) => this.renderSwatch(c))}
                        <label class="swatch custom" title="Custom color">
                            <span class="material-symbols-outlined" style="font-size:18px;">palette</span>
                            <input
                                type="color"
                                .value=${this.color}
                                @input=${(e: Event) => { this.color = (e.target as HTMLInputElement).value; }}
                            />
                        </label>
                    </div>
                </div>

                <div class="preview">
                    <span class="dot" style="background:${this.color}"></span>
                    <span class="text">${name || 'Group preview'}</span>
                </div>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <div class="actions">
                    <button class="ghost" @click=${this.dispatchClose}>Cancel</button>
                    <button class="primary" ?disabled=${!canSave || this.saving} @click=${this.onSave}>
                        ${this.saving ? 'Saving…' : this.isEdit ? 'Save' : 'Create group'}
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
