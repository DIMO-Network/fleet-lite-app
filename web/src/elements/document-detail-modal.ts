import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { DocumentService } from '../services/document-service.ts';
import { DocumentEntry } from '../types/document.ts';
import { categoryLabel } from '../utils/document-categories.ts';
import { ModalController } from '../utils/modal-controller.ts';

@customElement('document-detail-modal')
export class DocumentDetailModal extends LitElement {
    @property({ attribute: false }) document!: DocumentEntry;
    @property({ type: Number }) tokenId!: number;

    @state() private downloading = false;
    @state() private deleting = false;
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
                max-width: 560px;
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
            .close {
                position: absolute; top: 16px; right: 16px;
                width: 32px; height: 32px; padding: 0;
                display: inline-flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full); color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close .material-symbols-outlined { font-size: 20px; }
            .close:hover { background: var(--surface-container-high); color: var(--on-surface); }

            h2 {
                font: var(--type-headline-md);
                letter-spacing: -0.01em;
                color: var(--primary);
                padding-right: 40px;
                margin-bottom: 4px;
            }
            .sub {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-bottom: 20px;
            }

            /* Key/value sheet: one tonal block with hairline row dividers, so a
               long list of extracted fields scans as a table, not a wall. */
            dl {
                display: grid;
                grid-template-columns: minmax(96px, 140px) 1fr;
                padding: 4px 16px;
                margin-bottom: 8px;
                background: var(--surface-container);
                border-radius: var(--radius-lg);
            }
            dt, dd {
                padding: 11px 0;
                border-top: 1px solid var(--outline-variant);
            }
            dt:first-of-type, dt:first-of-type + dd { border-top: none; }
            dt {
                font: var(--type-label);
                color: var(--on-surface-variant);
                padding-right: 16px;
                padding-top: 13px;
            }
            dd {
                font: var(--type-body-sm);
                color: var(--on-surface);
                word-break: break-word;
            }
            dd.empty { color: var(--on-surface-variant); }
            dd code {
                font-family: var(--font-mono);
                font-size: 13px;
                color: var(--on-surface-variant);
                word-break: break-all;
            }

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
            .actions button:disabled { opacity: 0.5; cursor: not-allowed; }
            .actions .primary { background: var(--btn-primary-bg); color: var(--btn-primary-fg); }
            .actions .primary:hover:not(:disabled) { background: var(--btn-primary-hover); }
            .actions .primary:disabled { filter: grayscale(1); }
            .actions .ghost {
                padding: 0 16px;
                font-weight: 500;
                background: var(--surface-container-high);
                color: var(--on-surface);
                border: 1px solid var(--outline-variant);
            }
            .actions .ghost:hover { background: var(--surface-container-highest); border-color: var(--outline); }
            /* Destructive sits apart from the way out and the main action. */
            .actions .danger {
                margin-right: auto;
                padding: 0 16px;
                font-weight: 500;
                background: var(--error-container);
                color: var(--error);
            }
            .actions .danger:hover:not(:disabled) {
                background: color-mix(in srgb, var(--error) 18%, var(--error-container));
            }

            .error-text {
                padding: 12px 14px;
                background: var(--error-container);
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

    private formatTime(iso: string): string {
        if (!iso) return '';
        try {
            return new Date(iso).toLocaleString();
        } catch {
            return iso;
        }
    }

    /**
     * Flatten the CE's `data` field (which has the shape produced by the
     * Extract API: `{type, data:{fields:{...}}}` or `{fields:{...}}`) into a
     * list of [key, value] pairs to render as a definition list. Skip noisy
     * structural keys like `type` and any value that isn't a string/number.
     */
    private extractFields(): Array<[string, string]> {
        const data = this.document.data;
        if (!data || typeof data !== 'object') return [];
        const rec = data as Record<string, unknown>;
        const inner =
            (rec.data && typeof rec.data === 'object'
                ? ((rec.data as Record<string, unknown>).fields as Record<string, unknown> | undefined)
                : undefined) ??
            (rec.fields as Record<string, unknown> | undefined) ??
            rec;
        if (!inner || typeof inner !== 'object') return [];
        return Object.entries(inner)
            .filter(([k]) => k !== 'type')
            .map(([k, v]) => [k, typeof v === 'object' ? JSON.stringify(v) : String(v)] as [string, string]);
    }

    private async onDownload() {
        this.downloading = true;
        this.errorMessage = '';
        try {
            await DocumentService.getInstance().download(this.tokenId, this.document.rawId ?? '');
        } catch (e) {
            this.errorMessage = e instanceof Error ? e.message : msg('Download failed');
        } finally {
            this.downloading = false;
        }
    }

    private async onDelete() {
        if (!confirm(msg("Delete this document? It will be removed from your list. The file itself stays stored on DIMO's infrastructure."))) {
            return;
        }
        this.deleting = true;
        this.errorMessage = '';
        try {
            await DocumentService.getInstance().delete(this.document.id, this.tokenId);
            this.dispatchEvent(new CustomEvent('deleted', {
                detail: { id: this.document.id, tokenId: this.tokenId },
                bubbles: true,
                composed: true,
            }));
        } catch (e) {
            this.errorMessage = e instanceof Error ? e.message : msg('Delete failed');
            this.deleting = false;
        }
    }

    render() {
        const fields = this.extractFields();
        return html`
            <div class="card" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2 id="modal-title">${categoryLabel(this.document.type)}</h2>
                <p class="sub">${this.document.type} · ${this.formatTime(this.document.time)}</p>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}
                ${this.document.isReadOnly
                    ? html`<p class="sub">${this.document.isThirdParty
                          ? msg('Added through another DIMO app. You can view and download it here; only the app that added it can remove it.')
                          : msg('This vehicle is shared with you. You can view, download and add documents, but not delete them.')}</p>`
                    : nothing}

                <dl>
                    <dt>${msg('Document ID')}</dt>
                    <dd><code>${this.document.id}</code></dd>
                    <dt>${msg('File ID')}</dt>
                    <dd><code>${this.document.rawId || '—'}</code></dd>
                    ${this.document.uploadedBy
                        ? html`<dt>${msg('Added by')}</dt><dd><code>${this.document.uploadedBy}</code></dd>`
                        : nothing}
                    ${fields.length
                        ? fields.map(([k, v]) => html`<dt>${k}</dt><dd>${v}</dd>`)
                        : html`<dt>${msg('Fields')}</dt><dd class="empty">${msg('No structured fields extracted')}</dd>`
                    }
                </dl>

                <div class="actions">
                    ${this.document.isReadOnly
                        ? nothing
                        : html`<button class="danger" ?disabled=${this.deleting} @click=${this.onDelete}>
                              ${this.deleting ? msg('Deleting…') : msg('Delete')}
                          </button>`}
                    <button class="ghost" @click=${this.dispatchClose}>${msg('Close')}</button>
                    <button
                        class="primary"
                        ?disabled=${this.downloading || !this.document.rawId}
                        @click=${this.onDownload}>
                        ${this.downloading ? msg('Downloading…') : msg('Download')}
                    </button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'document-detail-modal': DocumentDetailModal;
    }
}
