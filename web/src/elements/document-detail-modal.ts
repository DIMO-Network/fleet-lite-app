import { LitElement, html, css, nothing } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { DocumentService } from '../services/document-service.ts';
import { DocumentEntry } from '../types/document.ts';
import { categoryLabel } from '../utils/document-categories.ts';

@customElement('document-detail-modal')
export class DocumentDetailModal extends LitElement {
    @property({ attribute: false }) document!: DocumentEntry;
    @property({ type: Number }) tokenId!: number;

    @state() private downloading = false;
    @state() private deleting = false;
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
                max-width: 560px;
                max-height: 90vh;
                overflow-y: auto;
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 24px;
                color: var(--on-surface);
                position: relative;
            }
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

            h2 { font: var(--type-headline-md); margin-bottom: 4px; }
            .sub {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 24px;
            }

            dl { display: grid; grid-template-columns: 140px 1fr; gap: 8px 16px; margin-bottom: 24px; }
            dt {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                padding-top: 2px;
            }
            dd {
                font: var(--type-body-md);
                color: var(--on-surface);
                word-break: break-word;
            }
            dd.empty { color: var(--on-surface-variant); font-style: italic; }
            dd code {
                font-family: var(--font-mono);
                font-size: 12px;
                color: var(--on-surface-variant);
            }

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
            .actions button:disabled { opacity: 0.5; cursor: not-allowed; }
            .actions .primary { background: var(--primary); color: var(--on-primary); }
            .actions .ghost { background: transparent; color: var(--on-surface-variant); border-color: var(--outline-variant); }
            .actions .danger { background: transparent; color: var(--error); border-color: var(--error); }
            .actions .danger:hover { background: rgba(255, 180, 171, 0.08); }

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
            await DocumentService.getInstance().download(this.tokenId, this.document.fileHash);
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
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <button class="close" @click=${this.dispatchClose}>
                    <span class="material-symbols-outlined">close</span>
                </button>
                <h2>${categoryLabel(this.document.type)}</h2>
                <p class="sub">${this.document.type} · ${this.formatTime(this.document.time)}</p>

                ${this.errorMessage ? html`<div class="error-text">${this.errorMessage}</div>` : nothing}

                <dl>
                    <dt>${msg('Document ID')}</dt>
                    <dd><code>${this.document.id}</code></dd>
                    <dt>${msg('File hash')}</dt>
                    <dd><code>${this.document.fileHash || '—'}</code></dd>
                    ${fields.length
                        ? fields.map(([k, v]) => html`<dt>${k}</dt><dd>${v}</dd>`)
                        : html`<dt>${msg('Fields')}</dt><dd class="empty">${msg('No structured fields extracted')}</dd>`
                    }
                </dl>

                <div class="actions">
                    <button class="danger" ?disabled=${this.deleting} @click=${this.onDelete}>
                        ${this.deleting ? msg('Deleting…') : msg('Delete')}
                    </button>
                    <button class="ghost" @click=${this.dispatchClose}>${msg('Close')}</button>
                    <button
                        class="primary"
                        ?disabled=${this.downloading || !this.document.fileHash}
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
