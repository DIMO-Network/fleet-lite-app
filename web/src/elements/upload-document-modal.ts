import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { DocumentService, fileToBase64 } from '../services/document-service.ts';
import { ExtractResult } from '../types/document.ts';
import { Vehicle } from '../types/vehicle.ts';
import { UPLOAD_CATEGORIES, categoryLabel, COST_ELIGIBLE_CATEGORIES } from '../utils/document-categories.ts';
import { ModalController } from '../utils/modal-controller.ts';

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
    @state() private amount: string = '';
    @state() private errorMessage = '';

    constructor() {
        super();
        new ModalController(this, { close: () => this.dispatchClose() });
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
                background: var(--scrim);
                backdrop-filter: blur(6px);
                -webkit-backdrop-filter: blur(6px);
            }
            .card {
                width: calc(100% - 32px);
                max-width: 520px;
                max-height: 90vh;
                overflow-y: auto;
                background: var(--surface-overlay);
                border-radius: var(--radius-xl);
                box-shadow: var(--shadow-float);
                padding: 24px;
                color: var(--on-surface);
                position: relative;
                animation: modal-in 0.18s ease-out;
            }
            @keyframes modal-in {
                from { opacity: 0; transform: translateY(8px) scale(0.98); }
            }
            .card h2 {
                font: var(--type-headline-md);
                letter-spacing: -0.01em;
                color: var(--primary);
                padding-right: 40px;
                margin-bottom: 4px;
            }
            .card .sub { font: var(--type-body-sm); color: var(--on-surface-variant); margin-bottom: 24px; }

            .close {
                position: absolute; top: 16px; right: 16px;
                width: 32px; height: 32px; padding: 0;
                display: inline-flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full); color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close .material-symbols-outlined { font-size: 20px; }
            .close:hover { background: var(--surface-container-high); color: var(--on-surface); }

            .drop {
                position: relative;
                display: flex;
                flex-direction: column;
                align-items: center;
                gap: 4px;
                border: 1.5px dashed var(--outline);
                border-radius: var(--radius-lg);
                padding: 32px 24px;
                text-align: center;
                font: var(--type-body-sm);
                color: var(--on-surface);
                cursor: pointer;
                transition: border-color 0.15s ease, background 0.15s ease;
            }
            .drop:hover, .drop.over, .drop:focus-within {
                border-color: var(--control-border-hover);
                background: var(--surface-container-high);
            }
            /* Focusable, just not visible: with display:none the only way to
               choose a file was a pointer. The label keeps the whole zone
               clickable; :focus-within shows keyboard focus on the zone. */
            .drop input {
                position: absolute; width: 1px; height: 1px; opacity: 0;
                overflow: hidden; pointer-events: none;
            }
            .drop:focus-within { outline: 2px solid var(--focus-ring); outline-offset: 2px; }
            .drop .icon {
                width: 48px;
                height: 48px;
                margin-bottom: 12px;
                display: flex;
                align-items: center;
                justify-content: center;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface);
                transition: background 0.15s ease;
            }
            .drop:hover .icon, .drop.over .icon { background: var(--surface-container-highest); }
            .drop .icon .material-symbols-outlined { font-size: 24px; }
            .drop strong { font-weight: 600; color: var(--on-surface); }
            .drop .hint { font: var(--type-label); color: var(--on-surface-variant); margin-top: 4px; }

            .field { display: flex; flex-direction: column; gap: 8px; margin-bottom: 16px; }
            .field label { font: var(--type-label); color: var(--on-surface-variant); }
            .field select, .field input[type="text"] {
                height: 40px;
                padding: 0 12px;
                background-color: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--control-border);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                transition: border-color 0.15s ease, box-shadow 0.15s ease;
            }
            .field input[type="text"]::placeholder { color: var(--on-surface-variant); }
            .field select:hover:not(:focus-visible),
            .field input[type="text"]:hover:not(:focus-visible) { border-color: var(--control-border-hover); }
            .field select:focus-visible, .field input[type="text"]:focus-visible {
                outline: none;
                border-color: var(--focus-ring);
                box-shadow: 0 0 0 3px var(--accent-soft);
            }

            .vin-row {
                display: flex;
                align-items: center;
                gap: 10px;
                min-height: 44px;
                padding: 10px 14px;
                border-radius: var(--radius-md);
                background: var(--accent-soft);
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-bottom: 20px;
            }
            .vin-row > .material-symbols-outlined { font-size: 18px; color: var(--accent-ink); }
            .vin-row .vin { font-weight: 500; color: var(--primary); letter-spacing: 0.02em; }
            .vin-row.no-vin {
                background: color-mix(in srgb, var(--warning) 12%, transparent);
                color: var(--on-surface);
            }
            .vin-row.no-vin > .material-symbols-outlined { color: var(--warning); }

            .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 24px; }
            .actions button {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                min-height: 40px;
                padding: 0 18px;
                border-radius: var(--radius-full);
                font: 600 14px/20px var(--font-body);
                transition: background 0.15s ease, border-color 0.15s ease, filter 0.15s ease, box-shadow 0.15s ease;
            }
            .actions .primary {
                background: var(--btn-primary-bg);
                color: var(--btn-primary-fg);
            }
            .actions .primary:hover:not(:disabled) { background: var(--btn-primary-hover); }
            .actions .primary:disabled { filter: grayscale(1); opacity: 0.5; cursor: not-allowed; }
            .actions .ghost {
                padding: 0 16px;
                font-weight: 500;
                background: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
            }
            .actions .ghost:hover { background: var(--surface-container-highest); border-color: var(--outline); }

            .picked-file {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 12px 14px;
                background: var(--surface-container);
                border-radius: var(--radius-md);
                margin-bottom: 8px;
            }
            .picked-file .file-icon {
                width: 36px;
                height: 36px;
                flex: none;
                display: flex;
                align-items: center;
                justify-content: center;
                border-radius: var(--radius-md);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
            }
            .picked-file .file-icon .material-symbols-outlined { font-size: 20px; }
            .picked-file .name { flex: 1; min-width: 0; font: 500 14px/20px var(--font-body); color: var(--primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
            .picked-file .meta { font: var(--type-label); color: var(--on-surface-variant); flex: none; }

            .status {
                text-align: center;
                padding: 40px 0 32px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .status::before {
                content: '';
                display: block;
                width: 28px;
                height: 28px;
                margin: 0 auto 16px;
                border-radius: var(--radius-full);
                border: 2.5px solid var(--outline-variant);
                border-top-color: var(--primary);
                animation: upload-spin 0.8s linear infinite;
            }
            @keyframes upload-spin { to { transform: rotate(360deg); } }
            .status .big { font: 600 17px/24px var(--font-headline); color: var(--primary); margin-bottom: 4px; }

            .error-text {
                padding: 12px 14px;
                background: var(--error-container);
                color: var(--error);
                border-radius: var(--radius-md);
                font: var(--type-body-sm);
                margin-bottom: 16px;
            }
            .card h2 + .error-text { margin-top: 16px; }
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
        await this.runExtract(f);
    }

    /**
     * Read the document and move to review. Clears any earlier result first:
     * review renders "Reading document…" for as long as there is none, so a
     * stale result would skip the wait and a missing one must mean "running".
     */
    private async runExtract(f: File) {
        this.extractResult = null;
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

    private buildParsedData(): Record<string, unknown> {
        const base = { ...(this.extractResult?.fields || {}) };
        const trimmed = this.amount.trim();
        if (COST_ELIGIBLE_CATEGORIES.has(this.selectedCategory) && trimmed !== '') {
            const parsed = Number(trimmed);
            if (!Number.isNaN(parsed)) {
                base.amount = parsed;
                base.currency = 'USD';
            }
        }
        return base;
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
                parsedData: this.buildParsedData(),
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
            <h2 id="modal-title">${msg('Add a document')}</h2>
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
            <h2 id="modal-title">${msg('Confirm')}</h2>
            <p class="sub">${msg('Pick which vehicle this belongs to and confirm the category.')}</p>

            ${this.file ? html`
                <div class="picked-file">
                    <span class="file-icon"><span class="material-symbols-outlined">description</span></span>
                    <span class="name">${this.file.name}</span>
                    <span class="meta">${(this.file.size / 1024).toFixed(0)} KB</span>
                </div>
            ` : nothing}

            ${vin
                ? html`<div class="vin-row">
                    <span class="material-symbols-outlined">qr_code</span>
                    <span>${msg(html`Detected VIN <span class="vin">${vin}</span>`)}</span>
                </div>`
                : html`<div class="vin-row no-vin">
                    <span class="material-symbols-outlined">info</span>
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

            ${COST_ELIGIBLE_CATEGORIES.has(this.selectedCategory) ? html`
                <div class="field">
                    <label for="amount">${msg('Amount (optional)')}</label>
                    <input
                        id="amount"
                        type="text"
                        inputmode="decimal"
                        placeholder="0.00"
                        .value=${this.amount}
                        @input=${(e: Event) => { this.amount = (e.target as HTMLInputElement).value; }}
                    />
                </div>
            ` : nothing}

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
            <h2 id="modal-title">${msg('Saved')}</h2>
            <p class="sub">${msg('Your document has been saved to DIMO. The list will refresh.')}</p>
            <div class="actions"><button class="primary" @click=${this.dispatchClose}>${msg('Done')}</button></div>
        `;
    }

    /**
     * Try again from the error step. The step that failed decides what to
     * retry: with no extract result, reading failed, so read again — returning
     * to review alone would show "Reading document…" with nothing running,
     * forever. With a result, the upload failed, so go back to review and let
     * the member resubmit without re-reading.
     */
    private retry() {
        if (!this.file) {
            this.step = 'pick';
            this.errorMessage = '';
        } else if (!this.extractResult) {
            void this.runExtract(this.file);
        } else {
            this.step = 'review';
            this.errorMessage = '';
        }
    }

    private renderError() {
        return html`
            <h2 id="modal-title">${msg('Something went wrong')}</h2>
            <div class="error-text">${this.errorMessage || msg('Unknown error')}</div>
            <div class="actions">
                <button class="ghost" @click=${this.dispatchClose}>${msg('Close')}</button>
                <button class="primary" @click=${this.retry}>${msg('Try again')}</button>
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
            <div class="card" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
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
