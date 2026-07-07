import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { DocumentService, fileToBase64 } from '../services/document-service.ts';
import { ExtractResult } from '../types/document.ts';
import { Vehicle } from '../types/vehicle.ts';
import { UPLOAD_CATEGORIES, categoryLabel } from '../utils/document-categories.ts';

type Step = 'pick' | 'review' | 'submitting' | 'done' | 'error';

/**
 * upload-document-modal — three-step UX:
 *   1. pick:   file chooser
 *   2. review: extracted VIN + category + vehicle dropdown (auto-pick if VIN matches one)
 *   3. done:   confirmation
 *
 * Props:
 *   - vehicles: caller's vehicles, used for the dropdown
 *   - initialTokenId: pre-select this vehicle in the dropdown (e.g. user
 *     opened the modal from a specific vehicle's glovebox)
 *
 * Events:
 *   - close: user dismissed (no side effects)
 *   - uploaded: { tokenId, parsedId, rawId } — caller refetches /documents/list
 */
@customElement('upload-document-modal')
export class UploadDocumentModal extends LitElement {
    @property({ attribute: false }) vehicles: Vehicle[] = [];
    @property({ type: Number }) initialTokenId?: number;

    @state() private step: Step = 'pick';
    @state() private file: File | null = null;
    @state() private extractResult: ExtractResult | null = null;
    @state() private selectedTokenId: number | null = null;
    @state() private selectedCategory: string = 'dimo.document.unknown';
    @state() private errorMessage = '';

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
                max-width: 520px;
                max-height: 90vh;
                overflow-y: auto;
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
                position: absolute;
                top: 16px;
                right: 16px;
                background: none;
                border: none;
                color: var(--on-surface-variant);
                padding: 4px;
                cursor: pointer;
            }
            .close:hover { color: var(--primary); }

            .drop {
                display: block;
                border: 1px dashed var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 32px;
                text-align: center;
                color: var(--on-surface-variant);
                cursor: pointer;
                transition: border-color 0.15s ease, background 0.15s ease;
            }
            .drop:hover, .drop.over {
                border-color: var(--secondary);
                background: rgba(255, 182, 145, 0.04);
            }
            .drop input { display: none; }
            .drop .icon { font-size: 32px; color: var(--secondary); margin-bottom: 8px; }
            .drop .hint { font: var(--type-label-caps); letter-spacing: 0.05em; text-transform: uppercase; margin-top: 8px; }

            .field { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; }
            .field label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .field select, .field input[type="text"] {
                background: var(--surface-container-low);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 10px 12px;
                font-family: inherit;
                font-size: 14px;
            }
            .field select:focus, .field input:focus { outline: 1px solid var(--primary); }

            .vin-row {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 8px 12px;
                border-radius: var(--radius-md);
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-bottom: 16px;
            }
            .vin-row .vin { font-family: var(--font-mono); color: var(--primary); }
            .vin-row.no-vin { background: rgba(255, 180, 171, 0.04); border-color: rgba(255, 180, 171, 0.2); color: var(--error); }

            .actions { display: flex; gap: 12px; justify-content: flex-end; margin-top: 8px; }
            .actions button {
                padding: 10px 18px;
                border-radius: var(--radius-md);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                border: 1px solid transparent;
                cursor: pointer;
            }
            .actions .primary {
                background: var(--primary);
                color: var(--on-primary);
            }
            .actions .primary:disabled { opacity: 0.5; cursor: not-allowed; }
            .actions .ghost {
                background: transparent;
                color: var(--on-surface-variant);
                border-color: var(--outline-variant);
            }

            .picked-file {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 12px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                margin-bottom: 16px;
            }
            .picked-file .name { flex: 1; font: var(--type-body-md); color: var(--primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
            .picked-file .meta { font: var(--type-label-caps); color: var(--on-surface-variant); letter-spacing: 0.05em; }

            .status {
                text-align: center;
                padding: 32px 0;
                color: var(--on-surface-variant);
            }
            .status .big { font: var(--type-headline-md); color: var(--primary); margin-bottom: 8px; }

            .error-text {
                padding: 12px;
                background: rgba(255, 180, 171, 0.04);
                border: 1px solid rgba(255, 180, 171, 0.2);
                color: var(--error);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                margin-bottom: 16px;
            }
        `,
    ];

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private formatTitle(v: Vehicle): string {
        const d = v.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : msg(str`Vehicle #${v.tokenId}`);
    }

    connectedCallback() {
        super.connectedCallback();
        if (this.initialTokenId && this.vehicles.some((v) => v.tokenId === this.initialTokenId)) {
            this.selectedTokenId = this.initialTokenId;
        } else if (this.vehicles.length === 1) {
            this.selectedTokenId = this.vehicles[0].tokenId;
        }
    }

    private async onFilePicked(e: Event) {
        const input = e.target as HTMLInputElement;
        const f = input.files?.[0];
        if (!f) return;
        this.file = f;
        this.step = 'review';
        this.errorMessage = '';
        try {
            const result = await DocumentService.getInstance().extract(f);
            this.extractResult = result;
            if (result.category) {
                this.selectedCategory = result.category;
            }
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Extract failed');
            this.step = 'error';
        }
    }

    private async onConfirm() {
        if (!this.file || !this.selectedTokenId) return;
        this.step = 'submitting';
        this.errorMessage = '';
        try {
            const fileBase64 = await fileToBase64(this.file);
            const res = await DocumentService.getInstance().attest({
                tokenId: this.selectedTokenId,
                category: this.selectedCategory,
                fileBase64,
                mimeType: this.file.type || 'application/octet-stream',
                fileName: this.file.name,
                parsedData: this.extractResult?.fields || {},
            });
            this.step = 'done';
            this.dispatchEvent(new CustomEvent('uploaded', {
                detail: {
                    tokenId: this.selectedTokenId,
                    parsedId: res.parsedSubmission.id,
                    rawId: res.rawSubmission?.id,
                },
                bubbles: true,
                composed: true,
            }));
        } catch (err) {
            console.error(err);
            this.errorMessage = err instanceof Error ? err.message : msg('Upload failed');
            this.step = 'error';
        }
    }

    private renderPick() {
        return html`
            <h2>${msg('Add a document')}</h2>
            <p class="sub">${msg("PDF, JPG, or PNG. We'll read the VIN and other details automatically, then securely save the document to DIMO.")}</p>
            <label class="drop">
                <input type="file" accept="application/pdf,image/jpeg,image/png" @change=${this.onFilePicked} />
                <div class="icon"><span class="material-symbols-outlined">upload_file</span></div>
                <div>${msg(html`Drop a file here, or <strong>click to choose</strong>`)}</div>
                <div class="hint">${msg('PDF · JPG · PNG, max 25 MB')}</div>
            </label>
            <div class="actions">
                <button class="ghost" @click=${this.dispatchClose}>${msg('Cancel')}</button>
            </div>
        `;
    }

    private renderReview() {
        if (!this.extractResult) {
            return html`<div class="status"><div class="big">${msg('Reading document…')}</div><div>${msg('Pulling out the VIN and other details.')}</div></div>`;
        }
        const vin = this.extractResult.vin?.trim();
        const canSubmit = this.selectedTokenId !== null;
        return html`
            <h2>${msg('Confirm')}</h2>
            <p class="sub">${msg('Pick which vehicle this belongs to and confirm the category.')}</p>

            ${this.file ? html`
                <div class="picked-file">
                    <span class="material-symbols-outlined" style="color: var(--secondary);">description</span>
                    <span class="name">${this.file.name}</span>
                    <span class="meta">${(this.file.size / 1024).toFixed(0)} KB</span>
                </div>
            ` : nothing}

            ${vin
                ? html`<div class="vin-row">
                    <span class="material-symbols-outlined" style="font-size:16px;">qr_code</span>
                    <span>${msg(html`Detected VIN <span class="vin">${vin}</span>`)}</span>
                </div>`
                : html`<div class="vin-row no-vin">
                    <span class="material-symbols-outlined" style="font-size:16px;">info</span>
                    <span>${msg('No VIN detected — pick the vehicle manually.')}</span>
                </div>`
            }

            <div class="field">
                <label for="veh">${msg('Vehicle')}</label>
                <select id="veh" @change=${(e: Event) => { this.selectedTokenId = Number((e.target as HTMLSelectElement).value); }}>
                    <option value="" ?selected=${this.selectedTokenId === null}>${msg('Select a vehicle…')}</option>
                    ${this.vehicles.map((v) => html`
                        <option value=${v.tokenId} ?selected=${v.tokenId === this.selectedTokenId}>${this.formatTitle(v)}</option>
                    `)}
                </select>
            </div>

            <div class="field">
                <label for="cat">${msg('Category')}</label>
                <select id="cat" @change=${(e: Event) => { this.selectedCategory = (e.target as HTMLSelectElement).value; }}>
                    ${UPLOAD_CATEGORIES.map((c) => html`
                        <option value=${c.ceType} ?selected=${c.ceType === this.selectedCategory}>${c.label}</option>
                    `)}
                </select>
            </div>

            ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

            <div class="actions">
                <button class="ghost" @click=${this.dispatchClose}>${msg('Cancel')}</button>
                <button class="primary" ?disabled=${!canSubmit} @click=${this.onConfirm}>${msg('Save document')}</button>
            </div>
        `;
    }

    private renderSubmitting() {
        return html`<div class="status"><div class="big">${msg('Saving document…')}</div><div>${msg('Signing and securely storing your document with DIMO.')}</div></div>`;
    }

    private renderDone() {
        return html`
            <h2>${msg('Saved')}</h2>
            <p class="sub">${msg('Your document has been saved to DIMO. The list will refresh.')}</p>
            <div class="actions"><button class="primary" @click=${this.dispatchClose}>${msg('Done')}</button></div>
        `;
    }

    private renderError() {
        return html`
            <h2>${msg('Something went wrong')}</h2>
            <div class="error-text">${this.errorMessage || msg('Unknown error')}</div>
            <div class="actions">
                <button class="ghost" @click=${this.dispatchClose}>${msg('Close')}</button>
                <button class="primary" @click=${() => { this.step = this.file ? 'review' : 'pick'; this.errorMessage = ''; }}>${msg('Try again')}</button>
            </div>
        `;
    }

    render() {
        const body =
            this.step === 'pick'       ? this.renderPick() :
            this.step === 'review'     ? this.renderReview() :
            this.step === 'submitting' ? this.renderSubmitting() :
            this.step === 'done'       ? this.renderDone() :
                                         this.renderError();
        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                ${body}
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'upload-document-modal': UploadDocumentModal;
    }
}

// Silence the unused-import warning until we use it inside the file.
void categoryLabel;
