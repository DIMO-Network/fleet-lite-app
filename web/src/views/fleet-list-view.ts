import { LitElement, html, css, nothing } from 'lit';
import { customElement, state, property } from 'lit/decorators.js';
import { msg, str } from '@lit/localize';
import { sharedStyles } from '../global-styles.ts';
import { hiddenVehiclesService } from '../services/hidden-vehicles-service.ts';
import { ApiService } from '../services/api-service.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import { Vehicle, VehicleCard, VehiclesResponse } from '../types/vehicle.ts';
import '../elements/tenant-switcher.ts';

type SortKey = 'status' | 'name' | 'tokenId';
type SortDir = 'asc' | 'desc';

@customElement('fleet-list-view')
export class FleetListView extends LitElement {
    @property({ type: String }) tenantId = '';
    @state() private vehicles: VehicleCard[] = [];
    @state() private lastLocations: Record<string, { lat: number; lon: number }> = {};
    @state() private loading = true;
    @state() private errorMessage: string | null = null;
    @state() private searchQuery = '';
    @state() private selectedGroupId = '';
    @state() private sortKey: SortKey = 'status';
    @state() private sortDir: SortDir = 'desc';
    @state() private refreshing = false;
    @state() private hiddenVehicles = new Set<string>();
    @state() private showHidden = false;
    private unsubscribeHidden: (() => void) | null = null;

    private loadGeneration = 0;
    private static readonly LOCATIONS_CHUNK_SIZE = 1;
    private static readonly LOCATIONS_PARALLEL = 3;

    private formatTitle(v: Vehicle): string {
        const d = v.definition;
        const parts = [d.year ? String(d.year) : '', d.make, d.model].filter(Boolean);
        return parts.length ? parts.join(' ') : `Vehicle #${v.tokenId}`;
    }

    private toCard(v: Vehicle): VehicleCard {
        const hasSynthetic = !!(v.syntheticDevice && v.syntheticDevice.tokenId > 0);
        const hasAftermarket = !!(v.aftermarketDevice && v.aftermarketDevice.tokenId > 0);
        const integrated = hasSynthetic || hasAftermarket;
        const integration = hasAftermarket
            ? `Aftermarket #${v.aftermarketDevice!.tokenId}`
            : hasSynthetic
                ? `Synthetic #${v.syntheticDevice.tokenId}`
                : '';
        return {
            tokenId: String(v.tokenId),
            make: v.definition.make,
            title: this.formatTitle(v),
            location: integration,
            seenAt: `Token #${v.tokenId}`,
            online: integrated,
            errorMessage: integrated ? undefined : msg('No DIMO integration — pair a device to stream telemetry'),
            isFavorite: v.isFavorite ?? false,
            groups: v.groups ?? [],
            licensePlate: v.licensePlate,
            vin: v.vin || undefined,
        };
    }

    private sortCards(cards: VehicleCard[]): VehicleCard[] {
        const statusRank = (v: VehicleCard) => {
            if (!v.online) return 0;
            if (v.noPermissions) return 1;
            return 2;
        };
        return [...cards].sort((a, b) => {
            const favDiff = Number(!!b.isFavorite) - Number(!!a.isFavorite);
            if (favDiff !== 0) return favDiff;
            let cmp = 0;
            if (this.sortKey === 'status') {
                cmp = statusRank(a) - statusRank(b);
            } else if (this.sortKey === 'name') {
                cmp = a.title.localeCompare(b.title);
            } else if (this.sortKey === 'tokenId') {
                cmp = Number(a.tokenId) - Number(b.tokenId);
            }
            return this.sortDir === 'asc' ? cmp : -cmp;
        });
    }

    private groupOptions() {
        const map = new Map<string, { id: string; name: string; color: string }>();
        for (const v of this.vehicles) {
            for (const g of v.groups ?? []) {
                if (!map.has(g.id)) map.set(g.id, g);
            }
        }
        return [...map.values()].sort((a, b) => a.name.localeCompare(b.name));
    }

    private visibleCards(): VehicleCard[] {
        let cards = this.sortCards(this.vehicles);
        if (this.selectedGroupId) {
            cards = cards.filter((c) => c.groups?.some((g) => g.id === this.selectedGroupId));
        }
        const q = this.searchQuery.trim().toLowerCase();
        if (q) {
            cards = cards.filter((c) =>
                c.title.toLowerCase().includes(q)
                || c.tokenId.includes(q)
                || c.location.toLowerCase().includes(q)
            );
        }
        if (this.showHidden) {
            const visible = cards.filter((c) => !this.hiddenVehicles.has(c.tokenId));
            const hidden = cards.filter((c) => this.hiddenVehicles.has(c.tokenId));
            return [...visible, ...hidden];
        }
        return cards.filter((c) => !this.hiddenVehicles.has(c.tokenId));
    }

    private async loadData(force = false) {
        if (!force) {
            const cached = FleetCache.get(this.tenantId);
            if (cached) {
                this.vehicles = cached.vehicles;
                this.lastLocations = cached.locations;
                this.loading = false;
                return;
            }
        }

        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            const cards = (res.vehicles || []).map((v) => this.toCard(v));
            cards.sort((a, b) => Number(!!b.isFavorite) - Number(!!a.isFavorite));
            this.vehicles = cards;
            this.loading = false;
        } catch (e) {
            this.loading = false;
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to load vehicles');
            return;
        }

        this.lastLocations = {};
        const gen = ++this.loadGeneration;
        const ids = this.vehicles.map((v) => v.tokenId);
        const chunks: string[][] = [];
        for (let i = 0; i < ids.length; i += FleetListView.LOCATIONS_CHUNK_SIZE) {
            chunks.push(ids.slice(i, i + FleetListView.LOCATIONS_CHUNK_SIZE));
        }

        const noPermSet = new Set<string>();
        let nextChunk = 0;
        const worker = async () => {
            while (nextChunk < chunks.length) {
                if (gen !== this.loadGeneration) return;
                const batch = chunks[nextChunk++];
                try {
                    const locRes = await TelemetryService.getInstance().fleetLocations(force, batch);
                    if (gen !== this.loadGeneration) return;
                    for (const id of locRes.noPermissions ?? []) noPermSet.add(id);
                    this.lastLocations = { ...this.lastLocations, ...locRes.locations };
                } catch {
                    // keep going on batch failure
                }
            }
        };
        await Promise.all(
            Array.from({ length: Math.min(FleetListView.LOCATIONS_PARALLEL, chunks.length) }, () => worker()),
        );
        if (gen !== this.loadGeneration) return;

        if (noPermSet.size > 0) {
            this.vehicles = this.vehicles.map((v) =>
                noPermSet.has(v.tokenId) ? { ...v, noPermissions: true } : v
            );
        }

        FleetCache.set(this.tenantId, { vehicles: this.vehicles, locations: this.lastLocations });
    }

    willUpdate(changed: Map<string, unknown>) {
        if (changed.has('tenantId') && this.tenantId && !this.loading) {
            this.loading = true;
            this.errorMessage = null;
            this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
            void this.loadData();
        }
    }

    async connectedCallback() {
        super.connectedCallback();
        this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        this.unsubscribeHidden = hiddenVehiclesService.subscribe(() => {
            this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        });
        await this.loadData();
    }

    override disconnectedCallback() {
        this.unsubscribeHidden?.();
        this.unsubscribeHidden = null;
        super.disconnectedCallback();
    }

    private statusClass(v: VehicleCard): string {
        if (!v.online) return 'status-red';
        if (v.noPermissions) return 'status-amber';
        return 'status-green';
    }

    private formatLocation(tokenId: string): string {
        const loc = this.lastLocations[tokenId];
        if (!loc) return '—';
        return `${loc.lat.toFixed(4)}, ${loc.lon.toFixed(4)}`;
    }

    private setSort(key: SortKey) {
        if (this.sortKey === key) {
            this.sortDir = this.sortDir === 'asc' ? 'desc' : 'asc';
        } else {
            this.sortKey = key;
            this.sortDir = key === 'status' ? 'desc' : 'asc';
        }
    }

    private sortIcon(key: SortKey) {
        if (this.sortKey !== key) {
            return html`<span class="material-symbols-outlined sort-icon muted">unfold_more</span>`;
        }
        return this.sortDir === 'asc'
            ? html`<span class="material-symbols-outlined sort-icon">arrow_upward</span>`
            : html`<span class="material-symbols-outlined sort-icon">arrow_downward</span>`;
    }

    private renderRow(v: VehicleCard) {
        const isHidden = this.hiddenVehicles.has(v.tokenId);
        return html`
            <tr class=${isHidden ? 'hidden-row' : ''}
                @click=${() => { if (!isHidden) location.hash = `#/${this.tenantId}/vehicles/${v.tokenId}`; }}>
                <td class="col-status">
                    <span class="status-dot ${this.statusClass(v)}"></span>
                </td>
                <td class="col-vehicle">
                    <div class="vehicle-cell">
                        <div class="vehicle-name">
                            <span class="title">
                                ${v.isFavorite ? html`<span class="material-symbols-outlined star-icon">star</span>` : nothing}
                                ${v.title}
                            </span>
                            ${v.noPermissions ? html`
                                <span class="no-perm-badge">
                                    <span class="material-symbols-outlined">lock</span>
                                    ${msg('No telemetry access')}
                                </span>
                            ` : nothing}
                        </div>
                    </div>
                </td>
                <td class="col-identifier">
                    ${v.licensePlate ? html`
                        <span class="identifier-plate">
                            <span class="material-symbols-outlined">directions_car</span>${v.licensePlate}
                        </span>
                    ` : nothing}
                    ${v.vin ? html`
                        <span class="identifier-vin">${v.vin}</span>
                    ` : nothing}
                    ${!v.licensePlate && !v.vin ? html`
                        <a class="upload-id-btn"
                           href="#/${this.tenantId}/glovebox/${v.tokenId}"
                           title=${msg('Upload vehicle documents to identify this vehicle')}>
                            <span class="material-symbols-outlined">inventory_2</span>
                        </a>
                    ` : nothing}
                </td>
                <td class="col-location mono">${this.formatLocation(v.tokenId)}</td>
                <td class="col-groups">
                    ${(v.groups ?? []).map((g) => html`
                        <span class="group-chip"
                            style="background:${g.color}22;border-color:${g.color}55;color:${g.color}">
                            ${g.name}
                        </span>
                    `)}
                </td>
                <td class="col-token mono">#${v.tokenId}</td>
                <td class="col-action">
                    ${isHidden ? html`
                        <button class="unhide-row-btn" title="${msg('Unhide vehicle')}"
                            @click=${(e: Event) => { e.stopPropagation(); hiddenVehiclesService.unhide(this.tenantId, v.tokenId); }}>
                            <span class="material-symbols-outlined">visibility</span>
                        </button>
                    ` : html`
                        <div class="action-cell">
                            <button class="hide-row-btn" title="${msg('Hide vehicle')}"
                                @click=${(e: Event) => { e.stopPropagation(); hiddenVehiclesService.hide(this.tenantId, v.tokenId); }}>
                                <span class="material-symbols-outlined">visibility_off</span>
                            </button>
                            <a href="#/${this.tenantId}/vehicles/${v.tokenId}"
                               @click=${(e: Event) => e.stopPropagation()}>
                                <span class="material-symbols-outlined">chevron_right</span>
                            </a>
                        </div>
                    `}
                </td>
            </tr>
        `;
    }

    private renderControls() {
        const groupOpts = this.groupOptions();
        return html`
            <div class="controls">
                <div class="search-wrap">
                    <span class="material-symbols-outlined">search</span>
                    <input
                        type="search"
                        placeholder="${msg('Search vehicles…')}"
                        .value=${this.searchQuery}
                        @input=${(e: Event) => { this.searchQuery = (e.target as HTMLInputElement).value; }}
                    />
                    ${this.searchQuery ? html`
                        <button class="clear-btn" @click=${() => { this.searchQuery = ''; }}>
                            <span class="material-symbols-outlined">close</span>
                        </button>
                    ` : nothing}
                </div>
                ${groupOpts.length > 0 ? html`
                    <select class="group-select"
                        @change=${(e: Event) => { this.selectedGroupId = (e.target as HTMLSelectElement).value; }}>
                        <option value="">${msg('All groups')}</option>
                        ${groupOpts.map((g) => html`
                            <option value=${g.id} ?selected=${g.id === this.selectedGroupId}>${g.name}</option>
                        `)}
                    </select>
                ` : nothing}
                ${this.hiddenVehicles.size > 0 ? html`
                    <button
                        class="show-hidden-btn ${this.showHidden ? 'active' : ''}"
                        title=${this.showHidden ? msg('Hide hidden vehicles') : msg('Show hidden vehicles')}
                        @click=${() => { this.showHidden = !this.showHidden; }}
                    >
                        <span class="material-symbols-outlined">visibility_off</span>
                        <span class="hidden-count">${this.hiddenVehicles.size}</span>
                    </button>
                ` : nothing}
                <span class="vehicle-count">
                    ${this.loading ? '' : msg(str`${this.visibleCards().length} vehicles`)}
                </span>
                <button
                    class="refresh-btn ${this.refreshing ? 'spinning' : ''}"
                    title="${msg('Refresh')}"
                    ?disabled=${this.refreshing}
                    @click=${async () => {
                        this.refreshing = true;
                        this.errorMessage = null;
                        await this.loadData(true);
                        this.refreshing = false;
                    }}
                >
                    <span class="material-symbols-outlined">refresh</span>
                </button>
            </div>
        `;
    }

    private renderBody() {
        if (this.loading) {
            return html`<div class="state-msg">${msg('Loading vehicles…')}</div>`;
        }
        if (this.errorMessage) {
            return html`<div class="state-msg error">${this.errorMessage}</div>`;
        }
        const cards = this.visibleCards();
        if (cards.length === 0) {
            return html`<div class="state-msg">${
                this.searchQuery.trim() ? msg('No vehicles match your search.') : msg('No vehicles found.')
            }</div>`;
        }
        return html`
            <div class="table-wrap custom-scrollbar">
                <table>
                    <thead>
                        <tr>
                            <th class="col-status sortable" @click=${() => this.setSort('status')}>
                                ${msg('Status')}${this.sortIcon('status')}
                            </th>
                            <th class="col-vehicle sortable" @click=${() => this.setSort('name')}>
                                ${msg('Vehicle')}${this.sortIcon('name')}
                            </th>
                            <th class="col-identifier">${msg('Identifier')}</th>
                            <th class="col-location">${msg('Last Location')}</th>
                            <th class="col-groups">${msg('Groups')}</th>
                            <th class="col-token sortable" @click=${() => this.setSort('tokenId')}>
                                ${msg('Token')}${this.sortIcon('tokenId')}
                            </th>
                            <th class="col-action"></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${cards.map((c) => this.renderRow(c))}
                    </tbody>
                </table>
            </div>
        `;
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: 100%;
                height: 100%;
                overflow: hidden;
                background: var(--background);
            }

            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                flex-shrink: 0;
                height: var(--top-bar-height, 80px);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
                background: var(--background);
                border-bottom: 1px solid var(--outline-variant);
            }
            header.top-bar .left { display: flex; align-items: center; gap: 32px; }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            header.top-bar nav { display: flex; gap: 24px; }
            header.top-bar nav a {
                text-decoration: none;
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                padding-bottom: 4px;
            }
            header.top-bar nav a.active {
                color: var(--primary);
                border-bottom: 2px solid var(--primary);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }

            /* ── Controls bar ─────────────────────────────────────── */
            .controls {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 16px var(--gutter);
                border-bottom: 1px solid var(--outline-variant);
                flex-shrink: 0;
            }
            .search-wrap {
                display: flex;
                align-items: center;
                gap: 8px;
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                padding: 8px 12px;
                flex: 1;
                max-width: 360px;
            }
            .search-wrap .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); }
            .search-wrap input {
                background: none;
                border: none;
                outline: none;
                color: var(--on-surface);
                font: var(--type-body-md);
                flex: 1;
            }
            .clear-btn {
                background: none;
                border: none;
                padding: 0;
                color: var(--on-surface-variant);
                cursor: pointer;
                display: flex;
            }
            .clear-btn .material-symbols-outlined { font-size: 16px; }
            .group-select {
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface);
                font: var(--type-body-sm);
                padding: 8px 12px;
                outline: none;
                cursor: pointer;
            }
            .vehicle-count {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-left: auto;
            }
            .refresh-btn {
                background: none;
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                padding: 8px;
                cursor: pointer;
                display: flex;
                align-items: center;
                transition: color 0.15s;
            }
            .refresh-btn:hover { color: var(--primary); }
            .refresh-btn.spinning .material-symbols-outlined {
                animation: spin 0.8s linear infinite;
            }
            @keyframes spin { to { transform: rotate(360deg); } }

            /* ── Table ────────────────────────────────────────────── */
            .table-wrap {
                flex: 1;
                overflow: auto;
            }
            table {
                width: 100%;
                border-collapse: collapse;
                font: var(--type-body-sm);
                color: var(--on-surface);
            }
            thead {
                position: sticky;
                top: 0;
                z-index: 2;
                background: var(--surface-container-low);
            }
            th {
                padding: 12px 16px;
                text-align: left;
                font: var(--type-label-caps);
                letter-spacing: 0.06em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                border-bottom: 1px solid var(--outline-variant);
                white-space: nowrap;
                user-select: none;
            }
            th.sortable { cursor: pointer; }
            th.sortable:hover { color: var(--primary); }
            .sort-icon {
                font-size: 14px;
                vertical-align: middle;
                margin-left: 4px;
                opacity: 0.9;
            }
            .sort-icon.muted { opacity: 0.35; }

            tbody tr {
                border-bottom: 1px solid var(--outline-variant);
                cursor: pointer;
                transition: background 0.1s;
            }
            tbody tr:hover { background: var(--surface-container); }
            tbody tr:last-child { border-bottom: none; }

            td {
                padding: 14px 16px;
                vertical-align: middle;
            }

            /* ── Column widths ────────────────────────────────────── */
            .col-status   { width: 48px; text-align: center; }
            .col-vehicle  { min-width: 200px; }
            .col-identifier { width: 180px; }
            .col-location { width: 180px; }
            .col-groups   { width: 180px; }
            .col-token    { width: 100px; }
            .col-action   { width: 48px; text-align: center; }

            /* ── Status dot ───────────────────────────────────────── */
            .status-dot {
                display: inline-block;
                width: 8px;
                height: 8px;
                border-radius: 50%;
            }
            .status-green  { background: var(--tertiary-container); box-shadow: 0 0 6px var(--tertiary-container); }
            .status-amber  { background: #ffb432; box-shadow: 0 0 6px #ffb43255; }
            .status-red    { background: var(--error); opacity: 0.6; }

            /* ── Vehicle cell ─────────────────────────────────────── */
            .vehicle-cell {
                display: flex;
                align-items: center;
                gap: 12px;
            }
            .vehicle-name {
                display: flex;
                flex-direction: column;
                gap: 4px;
            }
            .vehicle-name .title {
                display: flex;
                align-items: center;
                gap: 4px;
                font: var(--type-body-md);
                color: var(--on-surface);
            }
            .star-icon { font-size: 14px; color: #f5c84b; }
            .no-perm-badge {
                display: inline-flex;
                align-items: center;
                gap: 3px;
                font: var(--type-label-caps);
                letter-spacing: 0.04em;
                color: #ffb432;
                font-size: 10px;
            }
            .no-perm-badge .material-symbols-outlined { font-size: 11px; }

            /* ── Identifier cell ─────────────────────────────────── */
            .identifier-plate {
                display: inline-flex;
                align-items: center;
                gap: 4px;
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-sm);
                padding: 3px 8px;
                font: var(--type-label-caps);
                letter-spacing: 0.04em;
                color: var(--on-surface-variant);
                white-space: nowrap;
            }
            .identifier-plate .material-symbols-outlined { font-size: 14px; }

            .identifier-vin {
                display: block;
                font-family: var(--font-mono);
                font-size: 11px;
                color: var(--on-surface-variant);
                margin-top: 4px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                max-width: 148px;
            }

            .upload-id-btn {
                display: inline-flex;
                align-items: center;
                color: var(--on-surface-variant);
                opacity: 0.4;
                text-decoration: none;
                transition: opacity 0.15s, color 0.15s;
            }
            .upload-id-btn:hover { opacity: 1; color: var(--primary); }
            .upload-id-btn .material-symbols-outlined { font-size: 20px; }

            /* ── Location ─────────────────────────────────────────── */
            .mono {
                font-family: var(--font-mono);
                font-size: 12px;
                color: var(--on-surface-variant);
            }

            /* ── Group chips ──────────────────────────────────────── */
            .col-groups { display: table-cell; }
            .group-chip {
                display: inline-block;
                border: 1px solid;
                border-radius: var(--radius-sm);
                padding: 2px 8px;
                font: var(--type-label-caps);
                letter-spacing: 0.04em;
                font-size: 10px;
                margin: 2px 2px 2px 0;
                white-space: nowrap;
            }

            /* ── Action cell ──────────────────────────────────────── */
            .col-action { width: 80px; }
            .action-cell {
                display: flex;
                align-items: center;
                justify-content: center;
                gap: 4px;
            }
            .col-action a {
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: color 0.15s;
            }
            tbody tr:hover .col-action a { color: var(--primary); }

            .hide-row-btn {
                background: none;
                border: none;
                padding: 4px;
                color: var(--on-surface-variant);
                cursor: pointer;
                display: flex;
                align-items: center;
                border-radius: var(--radius-sm);
                opacity: 0;
                transition: opacity 0.15s, color 0.15s;
            }
            tbody tr:hover .hide-row-btn { opacity: 1; }
            .hide-row-btn:hover { color: var(--error); }
            .hide-row-btn .material-symbols-outlined { font-size: 18px; }

            .unhide-row-btn {
                background: none;
                border: none;
                padding: 4px;
                color: var(--primary);
                cursor: pointer;
                display: flex;
                align-items: center;
                justify-content: center;
                border-radius: var(--radius-sm);
                transition: color 0.15s;
            }
            .unhide-row-btn:hover { color: var(--secondary); }
            .unhide-row-btn .material-symbols-outlined { font-size: 18px; }

            tbody tr.hidden-row { opacity: 0.45; cursor: default; }
            tbody tr.hidden-row:hover { opacity: 0.7; background: var(--surface-container); }

            .show-hidden-btn {
                display: flex;
                align-items: center;
                gap: 6px;
                background: none;
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface-variant);
                font: var(--type-body-sm);
                padding: 8px 12px;
                cursor: pointer;
                transition: background 0.15s, color 0.15s, border-color 0.15s;
                white-space: nowrap;
            }
            .show-hidden-btn:hover { color: var(--primary); border-color: var(--primary); }
            .show-hidden-btn.active {
                background: var(--primary-container);
                color: var(--on-primary-container);
                border-color: var(--primary);
            }
            .show-hidden-btn .material-symbols-outlined { font-size: 18px; }
            .show-hidden-btn .hidden-count {
                font-weight: 700;
                font-size: 12px;
            }

            /* ── Empty / loading state ────────────────────────────── */
            .state-msg {
                padding: 64px var(--gutter);
                text-align: center;
                color: var(--on-surface-variant);
                font: var(--type-body-md);
            }
            .state-msg.error { color: var(--error); }

            /* ── Scrollbar ────────────────────────────────────────── */
            .custom-scrollbar::-webkit-scrollbar { width: 6px; height: 6px; }
            .custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
            .custom-scrollbar::-webkit-scrollbar-thumb {
                background: var(--surface-container-highest);
                border-radius: 3px;
            }
        `,
    ];

    render() {
        return html`
            <header class="top-bar">
                <div class="left">
                    <h2>${msg('Fleet Overview')}</h2>
                    <nav>
                        <a href="#/${this.tenantId}/">${msg('Map View')}</a>
                        <a href="#/${this.tenantId}/stats" class="active">${msg('List View')}</a>
                    </nav>
                </div>
                <div class="right">
                    <tenant-switcher .currentTenantId=${this.tenantId}></tenant-switcher>
                </div>
            </header>
            ${this.renderControls()}
            ${this.renderBody()}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'fleet-list-view': FleetListView;
    }
}
