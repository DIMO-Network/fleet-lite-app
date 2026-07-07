import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, state, property } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';
import { DocumentService } from '../services/document-service.ts';
import { DocumentEntry } from '../types/document.ts';
import { categoryLabel, EXPECTED_CE_TYPES, CE_TYPE_TO_LABEL } from '../utils/document-categories.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import '../elements/upload-document-modal.ts';
import '../elements/document-detail-modal.ts';

function vehicleTitle(v: Vehicle): string {
    const d = v.definition;
    const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
    return parts.length ? parts.join(' ') : `Vehicle #${v.tokenId}`;
}

interface MissingItem {
    ceType: string;
    label: string;
    blurb: string;
}

// Thunks, not precomputed strings: msg() must run at render time to pick up the
// active locale (a module-load value would freeze the source locale before
// setLocale() runs).
const MISSING_BLURBS: Record<string, () => string> = {
    'dimo.document.vehicle.insurance':    () => msg('Track renewals'),
    'dimo.document.vehicle.registration': () => msg('Track expiration'),
    'dimo.document.vehicle.inspection':   () => msg('Track next inspection'),
};

@customElement('glovebox-view')
export class GloveboxView extends LitElement {
    @property({ type: String }) tenantId = '';
    @property({ type: String }) initialTokenId = '';
    @state() private vehicles: Vehicle[] = [];
    @state() private selected: Vehicle | null = null;
    @state() private loadingVehicles = true;

    @state() private documents: DocumentEntry[] = [];
    @state() private loadingDocs = false;
    // Record counts for vehicles the user has already viewed, keyed by
    // tokenId — kept around so a card doesn't revert to the generic "View
    // documents" placeholder once its count is known, even after the user
    // navigates to another vehicle and back.
    @state() private docCounts = new Map<number, number>();
    @state() private permissionsRequired = false;
    @state() private devLicense = '';

    @state() private showUploadModal = false;
    @state() private detailOpen: DocumentEntry | null = null;

    private connected = false;
    private static readonly PREFETCH_PARALLEL = 3;

    async connectedCallback() {
        super.connectedCallback();
        this.connected = true;
        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            this.vehicles = res.vehicles || [];
            const initial = this.initialTokenId
                ? this.vehicles.find(v => String(v.tokenId) === this.initialTokenId)
                : null;
            this.selected = initial ?? this.vehicles[0] ?? null;
            if (this.selected) {
                await this.loadDocs(this.selected.tokenId);
            }
        } catch (e) {
            console.error('Failed to load vehicles', e);
        } finally {
            this.loadingVehicles = false;
        }
        // Fire-and-forget: fill in record counts for the rest of the fleet in
        // the background so cards show a real count on first paint instead of
        // the generic "View documents" placeholder until clicked.
        void this.prefetchDocCounts();
    }

    disconnectedCallback() {
        this.connected = false;
        super.disconnectedCallback();
    }

    private async prefetchDocCounts() {
        const targets = this.vehicles.filter((v) => !this.docCounts.has(v.tokenId));
        let next = 0;
        const worker = async () => {
            while (next < targets.length) {
                if (!this.connected) return;
                const v = targets[next++];
                try {
                    const res = await DocumentService.getInstance().list(v.tokenId);
                    if (!this.connected) return;
                    this.docCounts = new Map(this.docCounts).set(v.tokenId, (res.documents || []).length);
                } catch {
                    // Leave uncached — the card falls back to "View documents".
                }
            }
        };
        await Promise.all(
            Array.from({ length: Math.min(GloveboxView.PREFETCH_PARALLEL, targets.length) }, () => worker()),
        );
    }

    private async loadDocs(tokenId: number) {
        this.loadingDocs = true;
        this.documents = [];
        this.permissionsRequired = false;
        this.devLicense = '';
        try {
            const res = await DocumentService.getInstance().list(tokenId);
            this.documents = res.documents || [];
            this.permissionsRequired = !!res.permissionsRequired;
            this.devLicense = res.devLicense || '';
            this.docCounts = new Map(this.docCounts).set(tokenId, this.documents.length);
        } catch (e) {
            console.error('Failed to load documents', e);
        } finally {
            this.loadingDocs = false;
        }
    }

    private async selectVehicle(v: Vehicle) {
        if (this.selected?.tokenId === v.tokenId) return;
        this.selected = v;
        await this.loadDocs(v.tokenId);
    }

    private get missing(): MissingItem[] {
        const presentTypes = new Set(this.documents.map((d) => d.type));
        return EXPECTED_CE_TYPES
            .filter((t) => !presentTypes.has(t))
            .map((t) => ({
                ceType: t,
                label: msg(str`Add ${categoryLabel(t).toLowerCase()}`),
                blurb: MISSING_BLURBS[t]?.() ?? '',
            }));
    }

    private get groupedDocs(): Array<{ label: string; docs: DocumentEntry[] }> {
        const groups = new Map<string, DocumentEntry[]>();
        for (const d of this.documents) {
            const label = categoryLabel(d.type);
            if (!groups.has(label)) groups.set(label, []);
            groups.get(label)!.push(d);
        }
        // Order: friendly-label asc; docs within a group: newest first.
        return Array.from(groups.entries())
            .sort((a, b) => a[0].localeCompare(b[0]))
            .map(([label, docs]) => ({
                label,
                docs: docs.slice().sort((a, b) => b.time.localeCompare(a.time)),
            }));
    }

    private openUpload = () => { this.showUploadModal = true; };
    private closeUpload = () => { this.showUploadModal = false; };
    private onUploaded = async (e: CustomEvent<{ tokenId: number }>) => {
        this.showUploadModal = false;
        FleetCache.invalidate();
        if (this.selected && e.detail.tokenId === this.selected.tokenId) {
            await this.loadDocs(this.selected.tokenId);
        }
    };

    private openDetail = (doc: DocumentEntry) => { this.detailOpen = doc; };
    private closeDetail = () => { this.detailOpen = null; };
    private onDeleted = async (e: CustomEvent<{ tokenId: number }>) => {
        this.detailOpen = null;
        if (this.selected && e.detail.tokenId === this.selected.tokenId) {
            await this.loadDocs(this.selected.tokenId);
        }
    };

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: row;
                width: 100%;
                height: 100%;
                overflow: hidden;
                position: relative;
            }

            /* ── Left list panel ────────────────────────────────── */
            .list-panel {
                width: 400px;
                border-right: 1px solid var(--outline-variant);
                display: flex;
                flex-direction: column;
                height: 100%;
                flex-shrink: 0;
            }
            @media (max-width: 1024px) { .list-panel { width: 40%; } }
            @media (max-width: 768px)  { .list-panel { display: none; } }

            .list-header {
                height: var(--top-bar-height, 80px);
                flex-shrink: 0;
                padding: 0 var(--margin-desktop);
                border-bottom: 1px solid var(--outline-variant);
                background: var(--glass-bg);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                position: sticky;
                top: 0;
                z-index: 10;
                display: flex;
                justify-content: space-between;
                align-items: center;
            }
            .list-header h1 { font: var(--type-headline-md); color: var(--primary); }
            .list-header button {
                width: 40px; height: 40px;
                border-radius: var(--radius-full);
                background: none; border: none;
                color: var(--primary);
                display: flex; align-items: center; justify-content: center;
                transition: background 0.15s ease;
                cursor: pointer;
            }
            .list-header button:hover { background: var(--surface-container); }

            .vehicle-list {
                flex: 1;
                overflow-y: auto;
                padding: var(--stack-md) var(--margin-mobile);
                display: flex; flex-direction: column; gap: 12px;
            }

            .vehicle-card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 12px;
                display: flex; align-items: center; gap: 16px;
                cursor: pointer;
                transition: background 0.15s ease;
                color: inherit;
            }
            .vehicle-card:hover { background: var(--surface-container); }
            .vehicle-card.active { background: var(--surface-container-highest); }

            .vehicle-icon {
                width: 48px; height: 48px;
                border-radius: var(--radius-full);
                background: var(--surface-container-highest);
                border: 1px solid var(--outline-variant);
                display: flex; align-items: center; justify-content: center;
                flex-shrink: 0;
            }
            .vehicle-icon .material-symbols-outlined { color: var(--on-surface-variant); }
            .vehicle-card.active .vehicle-icon .material-symbols-outlined { color: var(--primary); }

            .vehicle-meta { flex: 1; min-width: 0; }
            .vehicle-meta h3 {
                font: var(--type-body-md);
                font-weight: 700;
                color: var(--primary);
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }
            .vehicle-meta p {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }

            .vehicle-card .right-group { display: flex; align-items: center; gap: 8px; }
            .vehicle-card .right-group .material-symbols-outlined { color: var(--on-surface-variant); }

            /* ── Right detail panel ─────────────────────────────── */
            .detail-panel {
                flex: 1;
                display: flex;
                flex-direction: column;
                height: 100%;
                overflow-y: auto;
                background: var(--background);
            }
            .detail-header {
                padding: 32px var(--gutter) 24px var(--gutter);
                display: flex; align-items: center; gap: 24px;
                border-bottom: 1px solid rgba(68, 71, 72, 0.3);
            }
            .detail-icon {
                width: 80px; height: 80px;
                border-radius: var(--radius-full);
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                display: flex; align-items: center; justify-content: center;
                box-shadow: 0 0 15px rgba(255, 255, 255, 0.05);
                flex-shrink: 0;
            }
            .detail-icon .material-symbols-outlined {
                font-size: 40px;
                color: var(--on-surface-variant);
                font-variation-settings: 'wght' 200;
            }
            .detail-header h2 { font: var(--type-headline-lg); color: var(--primary); letter-spacing: -0.01em; }
            .detail-header p {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-top: 4px;
            }

            .detail-body {
                padding: var(--stack-lg) var(--gutter);
                max-width: var(--container-max-width);
                margin: 0 auto;
                width: 100%;
                flex: 1;
                display: flex;
                flex-direction: column;
            }

            .perms-banner {
                display: flex;
                align-items: center;
                gap: 16px;
                padding: 16px;
                background: rgba(255, 182, 145, 0.06);
                border: 1px solid rgba(255, 182, 145, 0.2);
                border-radius: var(--radius-md);
                margin-bottom: var(--stack-lg);
            }
            .perms-banner strong { color: var(--secondary); font: var(--type-body-md); font-weight: 600; }
            .perms-banner p {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 4px;
                line-height: 1.5;
            }
            .perms-banner code {
                font-family: var(--font-mono);
                font-size: 11px;
                color: var(--secondary);
            }
            .perms-banner a.grant {
                flex-shrink: 0;
                background: var(--primary);
                color: var(--on-primary);
                padding: 10px 16px;
                border-radius: var(--radius-md);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                text-decoration: none;
                display: inline-flex;
                align-items: center;
                gap: 4px;
            }
            .perms-banner a.grant:hover { opacity: 0.9; }

            .filter-row { margin-bottom: var(--stack-lg); display: flex; gap: 8px; }
            .filter-pill {
                padding: 8px 16px;
                background: var(--primary);
                color: var(--on-primary);
                border-radius: var(--radius-full);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                display: inline-flex; align-items: center; gap: 4px;
                border: none; cursor: pointer;
            }
            .filter-pill .sep { opacity: 0.6; }

            /* Missing rail */
            .missing-section { margin-bottom: 48px; }
            .missing-head { display: flex; align-items: center; gap: 8px; margin-bottom: 16px; }
            .missing-head .dot { width: 4px; height: 4px; background: var(--secondary); border-radius: var(--radius-full); }
            .missing-head h3 {
                font: var(--type-label-caps);
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .missing-list {
                border-left: 2px solid var(--surface-container-high);
                padding-left: 24px;
                display: flex; flex-direction: column; gap: 24px;
            }
            .missing-item { cursor: pointer; }
            .missing-item h4 {
                font: var(--type-body-lg);
                color: var(--primary);
                transition: color 0.15s ease;
            }
            .missing-item:hover h4 { color: var(--secondary); }
            .missing-item p { font: var(--type-body-sm); color: var(--on-surface-variant); margin-top: 4px; }

            /* Doc groups */
            .group { margin-bottom: 32px; }
            .group-head {
                font: var(--type-label-caps);
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 12px;
            }
            .doc-row {
                display: flex;
                align-items: center;
                gap: 16px;
                padding: 12px 16px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                margin-bottom: 8px;
                cursor: pointer;
                transition: background 0.15s ease, border-color 0.15s ease;
            }
            .doc-row:hover { background: var(--surface-container); border-color: var(--outline); }
            .doc-row .file-icon {
                width: 36px; height: 36px;
                border-radius: var(--radius-md);
                background: var(--surface-container-highest);
                display: flex; align-items: center; justify-content: center;
                flex-shrink: 0;
            }
            .doc-row .file-icon .material-symbols-outlined { color: var(--secondary); font-size: 18px; }
            .doc-row .meta { flex: 1; min-width: 0; }
            .doc-row .meta .title { font: var(--type-body-md); color: var(--primary); }
            .doc-row .meta .when { font: var(--type-label-caps); letter-spacing: 0.05em; color: var(--on-surface-variant); margin-top: 4px; }
            .doc-row .material-symbols-outlined.chev { color: var(--on-surface-variant); }

            .empty-state {
                flex: 1;
                display: flex; flex-direction: column; align-items: center; justify-content: center;
                text-align: center;
                padding: 48px 0;
                margin-top: auto;
            }
            .empty-state h3 { font: var(--type-headline-md); color: var(--primary); margin-bottom: 8px; }
            .empty-state p {
                font: var(--type-label-caps);
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                max-width: 280px;
                margin-bottom: 32px;
                line-height: 1.6;
            }
            .empty-state .add-doc {
                padding: 12px 24px;
                border-radius: var(--radius-full);
                border: 1px solid var(--primary);
                color: var(--primary);
                background: none;
                font: var(--type-body-md);
                font-weight: 500;
                cursor: pointer;
                transition: background 0.3s ease, color 0.3s ease;
            }
            .empty-state .add-doc:hover { background: var(--primary); color: var(--on-primary); }

            .docs-loading {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                padding: 24px 0;
                text-align: center;
            }
        `,
    ];

    private recordLabel(count: number) {
        return msg(str`${count} Record${count === 1 ? '' : 's'}`);
    }

    private renderCardCount(v: Vehicle) {
        if (v.tokenId === this.selected?.tokenId) {
            return this.loadingDocs ? msg('Loading…') : this.recordLabel(this.documents.length);
        }
        const cached = this.docCounts.get(v.tokenId);
        return cached !== undefined ? this.recordLabel(cached) : msg('View documents');
    }

    private renderListCard(v: Vehicle) {
        const cls = v.tokenId === this.selected?.tokenId ? 'vehicle-card active' : 'vehicle-card';
        return html`
            <div class=${cls} @click=${() => this.selectVehicle(v)}>
                <div class="vehicle-icon">
                    <span class="material-symbols-outlined">directions_car</span>
                </div>
                <div class="vehicle-meta">
                    <h3>${vehicleTitle(v)}</h3>
                    <p>${this.renderCardCount(v)}</p>
                </div>
                <div class="right-group">
                    <span class="material-symbols-outlined">chevron_right</span>
                </div>
            </div>
        `;
    }

    private formatTime(iso: string): string {
        try { return new Date(iso).toLocaleDateString(); } catch { return iso; }
    }

    private renderDocRow(d: DocumentEntry) {
        const fields = (d.data as { data?: { fields?: Record<string, unknown> }; fields?: Record<string, unknown> } | null) ?? {};
        const inner = fields.data?.fields ?? fields.fields ?? {};
        const name = (typeof (inner as Record<string, unknown>).name === 'string')
            ? (inner as { name: string }).name
            : (CE_TYPE_TO_LABEL[d.type] ?? d.type);
        return html`
            <div class="doc-row" @click=${() => this.openDetail(d)}>
                <div class="file-icon"><span class="material-symbols-outlined">description</span></div>
                <div class="meta">
                    <div class="title">${name}</div>
                    <div class="when">${this.formatTime(d.time)}</div>
                </div>
                <span class="material-symbols-outlined chev">chevron_right</span>
            </div>
        `;
    }

    render() {
        const total = this.documents.length;
        const missing = this.missing;
        const groups = this.groupedDocs;
        return html`
            <section class="list-panel">
                <header class="list-header">
                    <h1>${msg('Glovebox')}</h1>
                    <button
                        ?disabled=${!this.selected}
                        @click=${this.openUpload}
                        title="${msg('Add a document')}">
                        <span class="material-symbols-outlined">add</span>
                    </button>
                </header>
                <div class="vehicle-list custom-scrollbar">
                    ${this.loadingVehicles
                        ? html`<p style="padding:24px;color:var(--on-surface-variant);">${msg('Loading…')}</p>`
                        : this.vehicles.length === 0
                            ? html`<p style="padding:24px;color:var(--on-surface-variant);">${msg('No vehicles on this account.')}</p>`
                            : this.vehicles.map((v) => this.renderListCard(v))
                    }
                </div>
            </section>

            <section class="detail-panel">
                <div class="detail-header">
                    <div class="detail-icon">
                        <span class="material-symbols-outlined">directions_car</span>
                    </div>
                    <div>
                        <h2>${this.selected ? vehicleTitle(this.selected) : msg('Select a vehicle')}</h2>
                        <p>${this.selected ? this.recordLabel(total) : msg('Pick a vehicle from the list')}</p>
                    </div>
                    <tenant-switcher .currentTenantId=${this.tenantId} style="margin-left:auto;"></tenant-switcher>
                </div>

                ${this.selected ? html`
                    <div class="detail-body">
                        ${this.permissionsRequired ? html`
                            <div class="perms-banner">
                                <div>
                                    <strong>${msg('Grant DIMO permissions to view documents on this vehicle.')}</strong>
                                    <p>
                                        ${msg(html`The fleet-lite dev license <code>${this.devLicense}</code>
                                        needs data-sharing permission for this vehicle before its document list can load.
                                        Documents you upload here are still saved — you just can't list or download them yet.`)}
                                    </p>
                                </div>
                                <a class="grant" href="https://console.dimo.org" target="_blank" rel="noopener">
                                    ${msg('Open DIMO console')}
                                    <span class="material-symbols-outlined" style="font-size:14px;">open_in_new</span>
                                </a>
                            </div>
                        ` : nothing}
                        <div class="filter-row">
                            <button class="filter-pill">${msg('All')} <span class="sep">•</span> ${total}</button>
                        </div>

                        ${missing.length ? html`
                            <div class="missing-section">
                                <div class="missing-head">
                                    <span class="dot"></span>
                                    <h3>${msg('Missing')}</h3>
                                </div>
                                <div class="missing-list">
                                    ${missing.map((m) => html`
                                        <div class="missing-item" @click=${this.openUpload}>
                                            <h4>${m.label}</h4>
                                            ${m.blurb ? html`<p>${m.blurb}</p>` : nothing}
                                        </div>
                                    `)}
                                </div>
                            </div>
                        ` : nothing}

                        ${this.loadingDocs
                            ? html`<div class="docs-loading">${msg('Loading documents…')}</div>`
                            : groups.length === 0
                                ? html`
                                    <div class="empty-state">
                                        <h3>${msg('No records yet.')}</h3>
                                        <p>${msg('— upload anything: receipt, insurance pdf, reg card')}</p>
                                        <button class="add-doc" @click=${this.openUpload}>${msg('Add document')}</button>
                                    </div>
                                `
                                : groups.map((g) => html`
                                    <div class="group">
                                        <div class="group-head">${g.label}</div>
                                        ${g.docs.map((d) => this.renderDocRow(d))}
                                    </div>
                                `)
                        }
                    </div>
                ` : nothing}
            </section>

            ${this.showUploadModal && this.selected
                ? html`<upload-document-modal
                        .vehicles=${this.vehicles}
                        .initialTokenId=${this.selected.tokenId}
                        @close=${this.closeUpload}
                        @uploaded=${this.onUploaded}>
                    </upload-document-modal>`
                : nothing
            }

            ${this.detailOpen && this.selected
                ? html`<document-detail-modal
                        .document=${this.detailOpen}
                        .tokenId=${this.selected.tokenId}
                        @close=${this.closeDetail}
                        @deleted=${this.onDeleted}>
                    </document-detail-modal>`
                : nothing
            }
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'glovebox-view': GloveboxView;
    }
}
