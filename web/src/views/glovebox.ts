import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, state, property } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';
import { DocumentService } from '../services/document-service.ts';
import { DocumentEntry } from '../types/document.ts';
import { categoryLabel, EXPECTED_CE_TYPES } from '../utils/document-categories.ts';
import { documentSummary } from '../utils/document-summary.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import { SettingsService } from '../services/settings-service.ts';
import { buildShareVehiclesUrl, pickGrantRedirectUri } from '../utils/dimo-permissions.ts';
import { LicenseService } from '../services/license-service.ts';
import { TCOCache } from '../services/tco-cache.ts';
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
    /** Registered redirect for grants to the license; see resolveGrantRedirect. */
    @state() private grantRedirect: string | null | undefined = undefined;
    // Login host for the grant link in the permissions banner. Empty until
    // /public/settings resolves; the banner falls back to plain text.
    @state() private loginUrl = '';

    @state() private showUploadModal = false;
    @state() private detailOpen: DocumentEntry | null = null;

    private connected = false;
    private static readonly PREFETCH_PARALLEL = 3;

    async connectedCallback() {
        super.connectedCallback();
        this.connected = true;
        void SettingsService.getInstance()
            .fetchPublicSettings()
            .then((s) => { this.loginUrl = s.loginUrl; })
            .catch(() => { /* banner degrades to text-only */ });
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
            this.resolveGrantRedirect(this.devLicense);
            this.docCounts = new Map(this.docCounts).set(tokenId, this.documents.length);
        } catch (e) {
            console.error('Failed to load documents', e);
        } finally {
            this.loadingDocs = false;
        }
    }

    /**
     * Link that sends the vehicle owner to DIMO's sharing screen for the
     * selected vehicle, asking for every privilege — the glovebox needs
     * RAW_DATA (7), which no permission template grants. Empty while
     * /public/settings is still in flight or the API didn't name a license.
     */
    private grantUrl(): string {
        if (this.grantRedirect === null) return '';
        if (!this.loginUrl || !this.devLicense || !this.selected) return '';
        return buildShareVehiclesUrl({
            loginUrl: this.loginUrl,
            clientId: this.devLicense,
            redirectUri: this.grantRedirect ?? undefined,
            vehicles: [this.selected.tokenId],
        });
    }

    /**
     * Look up where a grant to `license` may return (see pickGrantRedirectUri).
     * undefined = unknown (lookup pending or failed): the link keeps the default
     * redirect. null = the license has nothing on our origin, so a link would
     * only reach DIMO's credentials error; the banner explains the fix instead.
     */
    private resolveGrantRedirect(license: string) {
        this.grantRedirect = undefined;
        if (!license) return;
        LicenseService.getInstance()
            .redirectUris(license)
            .then((uris) => {
                if (this.devLicense === license) this.grantRedirect = pickGrantRedirectUri(uris, location.origin);
            })
            .catch(() => { /* unknown: keep the default redirect */ });
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
        // A new document may add a cost-eligible line item — don't let a
        // stale TCO cache hide it on the next visit to that tab.
        TCOCache.invalidate();
        if (this.selected && e.detail.tokenId === this.selected.tokenId) {
            await this.loadDocs(this.selected.tokenId);
        }
    };

    private openDetail = (doc: DocumentEntry) => { this.detailOpen = doc; };
    private closeDetail = () => { this.detailOpen = null; };
    private onDeleted = async (e: CustomEvent<{ tokenId: number }>) => {
        this.detailOpen = null;
        // A deleted (tombstoned) document must stop counting toward TCO too.
        TCOCache.invalidate();
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
                background: var(--background);
            }

            /* ── Left list panel ────────────────────────────────── */
            .list-panel {
                width: 380px;
                /* A split pane is one of the few places a hairline carries meaning. */
                border-right: 1px solid var(--outline-variant);
                display: flex;
                flex-direction: column;
                height: 100%;
                flex-shrink: 0;
            }
            @media (max-width: 1024px) { .list-panel { width: 40%; } }
            @media (max-width: 768px)  { .list-panel { display: none; } }

            .list-header {
                height: var(--top-bar-height);
                flex-shrink: 0;
                padding: 0 20px 0 var(--gutter);
                display: flex;
                justify-content: space-between;
                align-items: center;
            }
            .list-header h1 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            /* The page's primary action, as a round gradient button. */
            .list-header button {
                width: 36px; height: 36px;
                border-radius: var(--radius-full);
                background: var(--brand-gradient);
                color: var(--on-accent);
                display: flex; align-items: center; justify-content: center;
                transition: filter 0.15s ease, box-shadow 0.15s ease;
            }
            .list-header button .material-symbols-outlined { font-size: 22px; }
            .list-header button:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }
            .list-header button:disabled { filter: grayscale(1) opacity(0.5); box-shadow: none; cursor: not-allowed; }

            .vehicle-list {
                flex: 1;
                overflow-y: auto;
                padding: 4px 12px var(--stack-md);
                display: flex; flex-direction: column; gap: 2px;
            }
            .list-note { padding: 24px 12px; font: var(--type-body-sm); color: var(--on-surface-variant); }

            /* Rows like the vehicles panel; the active one follows the nav idiom. */
            .vehicle-card {
                border-radius: 14px;
                padding: 10px 8px 10px 10px;
                display: flex; align-items: center; gap: 14px;
                cursor: pointer;
                transition: background 0.15s ease;
                color: inherit;
                flex-shrink: 0;
            }
            .vehicle-card:hover { background: var(--surface-container-low); }
            .vehicle-card.active { background: var(--surface-container-high); }

            .vehicle-icon {
                width: 44px; height: 44px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                display: flex; align-items: center; justify-content: center;
                flex-shrink: 0;
                transition: background 0.15s ease;
            }
            .vehicle-icon .material-symbols-outlined { font-size: 22px; color: var(--on-surface-variant); }
            .vehicle-card.active .vehicle-icon { background: var(--accent-soft); }
            .vehicle-card.active .vehicle-icon .material-symbols-outlined { color: var(--accent-ink); }

            .vehicle-meta { flex: 1; min-width: 0; }
            .vehicle-meta h3 {
                font: 600 15px/22px var(--font-headline);
                color: var(--primary);
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }
            .vehicle-meta p {
                font: 400 13px/18px var(--font-body);
                color: var(--on-surface-variant);
                margin-top: 2px;
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }

            .vehicle-card .right-group { display: flex; align-items: center; gap: 8px; }
            .vehicle-card .right-group .material-symbols-outlined { font-size: 20px; color: var(--outline); }
            .vehicle-card.active .right-group .material-symbols-outlined { color: var(--on-surface-variant); }

            /* ── Right detail panel ─────────────────────────────── */
            .detail-panel {
                flex: 1;
                min-width: 0;
                display: flex;
                flex-direction: column;
                height: 100%;
                overflow-y: auto;
            }
            .detail-header {
                padding: 16px var(--gutter) 8px;
                display: flex; align-items: center; gap: 20px;
            }
            /* Top-aligned so it sits where every other page header puts it. */
            .detail-header tenant-switcher { align-self: flex-start; }
            .detail-icon {
                width: 64px; height: 64px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                display: flex; align-items: center; justify-content: center;
                flex-shrink: 0;
            }
            .detail-icon .material-symbols-outlined {
                font-size: 32px;
                color: var(--on-surface-variant);
            }
            .detail-header h2 { font: var(--type-headline-lg); color: var(--primary); letter-spacing: -0.015em; }
            .detail-header p {
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin-top: 4px;
            }

            .detail-body {
                padding: var(--stack-md) var(--gutter) var(--stack-lg);
                max-width: var(--container-max-width);
                width: 100%;
                flex: 1;
                display: flex;
                flex-direction: column;
            }

            .grant-setup { font: var(--type-body-sm); color: var(--on-surface-variant); max-width: 320px; }
            .grant-setup code { font: 500 12px/16px var(--font-body); color: var(--on-surface); }
            .perms-banner {
                display: flex;
                align-items: center;
                gap: 16px;
                padding: 16px 16px 16px 20px;
                background: color-mix(in srgb, var(--warning) 10%, transparent);
                border-radius: var(--radius-lg);
                margin-bottom: var(--stack-lg);
            }
            .perms-banner strong { color: var(--warning); font: 600 15px/22px var(--font-body); }
            .perms-banner p {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 4px;
            }
            .perms-banner code {
                font: 500 12px/16px var(--font-mono);
                color: var(--on-surface);
                background: var(--surface-container-high);
                border-radius: 5px;
                padding: 1px 5px;
                word-break: break-all;
            }
            .perms-banner a.grant {
                flex-shrink: 0;
                min-height: 36px;
                padding: 0 14px 0 16px;
                border-radius: var(--radius-full);
                background: var(--brand-gradient);
                color: var(--on-accent);
                font: 600 13px/18px var(--font-body);
                text-decoration: none;
                display: inline-flex;
                align-items: center;
                gap: 6px;
                transition: filter 0.15s ease, box-shadow 0.15s ease;
            }
            .perms-banner a.grant:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }

            .filter-row { margin-bottom: 28px; display: flex; gap: 8px; }
            /* The (only) filter is selected: toggled-control treatment. */
            .filter-pill {
                min-height: 32px;
                padding: 0 14px;
                background: var(--accent-soft-strong);
                color: var(--accent-ink);
                border-radius: var(--radius-full);
                font: 500 13px/18px var(--font-body);
                display: inline-flex; align-items: center; gap: 6px;
            }
            .filter-pill .sep { opacity: 0.6; }

            /* Missing: actionable prompts, laid out as quiet add-cards. */
            .missing-section { margin-bottom: 40px; }
            .missing-head { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
            .missing-head .dot { width: 6px; height: 6px; background: var(--warning); border-radius: var(--radius-full); }
            .missing-head h3 { font: var(--type-label); color: var(--on-surface-variant); }
            .missing-list {
                display: grid;
                grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
                gap: 12px;
            }
            .missing-item {
                display: flex; align-items: center; gap: 14px;
                padding: 14px 16px 14px 14px;
                border-radius: var(--radius-lg);
                background: var(--surface-container-low);
                cursor: pointer;
                transition: background 0.15s ease;
            }
            .missing-item:hover { background: var(--surface-container); }
            .missing-item .add {
                width: 36px; height: 36px; flex-shrink: 0;
                display: flex; align-items: center; justify-content: center;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
                font-size: 20px;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .missing-item:hover .add { background: var(--accent-soft); color: var(--accent-ink); }
            .missing-item h4 { font: 500 15px/22px var(--font-body); color: var(--primary); }
            .missing-item p { font: 400 13px/18px var(--font-body); color: var(--on-surface-variant); margin-top: 2px; }

            /* Doc groups */
            .group { margin-bottom: 28px; }
            .group-head {
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin-bottom: 8px;
            }
            .doc-row {
                display: flex;
                align-items: center;
                gap: 14px;
                min-height: 64px;
                padding: 12px 12px 12px 14px;
                background: var(--surface-container-low);
                border-radius: var(--radius-md);
                margin-bottom: 4px;
                cursor: pointer;
                transition: background 0.15s ease;
            }
            .doc-row:hover { background: var(--surface-container); }
            .doc-row .file-icon {
                width: 36px; height: 36px;
                border-radius: var(--radius-md);
                background: var(--surface-container-high);
                display: flex; align-items: center; justify-content: center;
                flex-shrink: 0;
            }
            .doc-row .file-icon .material-symbols-outlined { color: var(--on-surface-variant); font-size: 20px; }
            .doc-row .meta { flex: 1; min-width: 0; }
            .doc-row .meta .title {
                font: 500 15px/22px var(--font-body); color: var(--primary);
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }
            .doc-row .meta .when {
                font: var(--type-label); color: var(--on-surface-variant); margin-top: 2px;
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
            }
            .doc-row .material-symbols-outlined.chev { font-size: 20px; color: var(--outline); }
            .doc-row:hover .material-symbols-outlined.chev { color: var(--on-surface-variant); }

            .empty-state {
                flex: 1;
                display: flex; flex-direction: column; align-items: center; justify-content: center;
                text-align: center;
                padding: 48px 0;
                margin-top: auto;
            }
            .empty-state h3 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); margin-bottom: 8px; }
            .empty-state p {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                max-width: 300px;
                margin-bottom: 24px;
            }

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
        // Title and detail come from the document's own extracted fields —
        // vendor, amount, plate — the same way dimo-driver renders them. The
        // old code only looked for a `name` field, which nothing writes, so
        // every row fell back to its category and a group of five service
        // records read as five identical lines.
        const { title, subtitle } = documentSummary(d.type, d.data);
        const when = this.formatTime(d.time);
        return html`
            <div class="doc-row" @click=${() => this.openDetail(d)}>
                <div class="file-icon"><span class="material-symbols-outlined">description</span></div>
                <div class="meta">
                    <div class="title">${title}</div>
                    <div class="when">${subtitle ? `${subtitle} · ${when}` : when}</div>
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
                        title="${msg('Add a document')}"
                        aria-label="${msg('Add a document')}">
                        <span class="material-symbols-outlined">add</span>
                    </button>
                </header>
                <div class="vehicle-list custom-scrollbar">
                    ${this.loadingVehicles
                        ? html`<p class="list-note">${msg('Loading…')}</p>`
                        : this.vehicles.length === 0
                            ? html`<p class="list-note">${msg('No vehicles on this account.')}</p>`
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
                                ${this.grantUrl() ? html`
                                    <a class="grant" href=${this.grantUrl()} target="_blank" rel="noopener">
                                        ${msg('Grant permissions')}
                                        <span class="material-symbols-outlined" style="font-size:16px;">open_in_new</span>
                                    </a>
                                ` : this.grantRedirect === null ? html`<p class="grant-setup">${msg(html`To grant from here, add <code>${location.origin}/login.html</code> to this license’s redirect URIs in the DIMO developer console.`)}</p>` : nothing}
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
                                            <span class="material-symbols-outlined add">add</span>
                                            <div>
                                                <h4>${m.label}</h4>
                                                ${m.blurb ? html`<p>${m.blurb}</p>` : nothing}
                                            </div>
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
                                        <button class="btn-primary" @click=${this.openUpload}>${msg('Add document')}</button>
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
