import { LitElement, html, css, nothing } from 'lit';
import { customElement, state, property } from 'lit/decorators.js';
import { msg, str } from '@lit/localize';
import { sharedStyles } from '../global-styles.ts';
import { hiddenVehiclesService } from '../services/hidden-vehicles-service.ts';
import { ApiService } from '../services/api-service.ts';
import { FleetCache } from '../services/fleet-cache.ts';
import { Vehicle, VehicleCard, VehiclesResponse } from '../types/vehicle.ts';
import { seedLocationsFromDb, fetchFleetLocations } from '../utils/fleet-map.ts';
import { shareBlockReason } from '../utils/share-blocker.ts';
import { TenantService } from '../services/tenant-service.ts';
import '../elements/tenant-switcher.ts';
import '../elements/share-vehicle-modal.ts';

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
    /**
     * Whether the signed-in member holds manage_vehicles. Purely to avoid
     * offering an action that would be refused — the API enforces it, and so
     * does fleet-tenancy-api behind that.
     */
    @state() private canShareVehicles = false;
    /** The signed-in wallet, so a blocked share can say "owned by you". */
    @state() private myWallet = '';
    @state() private fleetLicense = '';
    @state() private shareTarget: VehicleCard | null = null;
    private unsubscribeHidden: (() => void) | null = null;

    private loadGeneration = 0;

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
        // A vehicle whose metadata has not synced yet arrives with every field
        // zeroed, which is indistinguishable from one with no device paired.
        // Say which it is: "no integration" is a state the customer can act on
        // and this is not one, so showing it here would send them to pair a
        // device that is already paired.
        const errorMessage = v.metadataPending
            ? msg('Details still syncing — this vehicle was added recently')
            : integrated
                ? undefined
                : msg('No DIMO integration — pair a device to stream telemetry');
        return {
            tokenId: String(v.tokenId),
            make: v.definition.make,
            title: this.formatTitle(v),
            location: integration,
            seenAt: `Token #${v.tokenId}`,
            online: integrated,
            errorMessage,
            isFavorite: v.isFavorite ?? false,
            groups: v.groups ?? [],
            licensePlate: v.licensePlate,
            vin: v.vin || undefined,
            canShare: v.canShare ?? false,
            shareBlocker: v.shareBlocker,
            owner: v.owner,
            metadataPending: v.metadataPending ?? false,
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
                || (c.licensePlate?.toLowerCase().includes(q) ?? false)
                || (c.vin?.toLowerCase().includes(q) ?? false)
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
            // Cold load: render instantly from the last persisted snapshot
            // while the /vehicles fetch below revalidates. Paint-only — the
            // fresh response replaces everything shown here.
            const tid = this.tenantId;
            const persisted = await FleetCache.loadPersisted(tid);
            if (persisted && tid === this.tenantId) {
                this.vehicles = persisted.vehicles;
                this.lastLocations = persisted.locations;
                this.loading = false;
            }
        }

        let rawVehicles: Vehicle[] = [];
        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            rawVehicles = res.vehicles || [];
            const cards = rawVehicles.map((v) => this.toCard(v));
            cards.sort((a, b) => Number(!!b.isFavorite) - Number(!!a.isFavorite));
            this.vehicles = cards;
            this.loading = false;
        } catch (e) {
            this.loading = false;
            this.errorMessage = e instanceof Error ? e.message : msg('Failed to load vehicles');
            return;
        }

        // Show DB-cached coordinates immediately, then fan out only for the
        // vehicles whose location is stale (older than the freshness window).
        // A fully-fresh fleet makes zero telemetry calls — same shared loader
        // as the map view, so both views generate identical backend load.
        this.lastLocations = seedLocationsFromDb(rawVehicles);
        const gen = ++this.loadGeneration;
        const noPermSet = new Set<string>();
        await fetchFleetLocations({
            vehicles: rawVehicles,
            force,
            isCurrent: () => gen === this.loadGeneration,
            onNoPermissions: (ids) => ids.forEach((id) => noPermSet.add(id)),
            onBatch: (locations) => {
                this.lastLocations = { ...this.lastLocations, ...locations };
            },
        });
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

    /**
     * Preset the group filter from a `?group=<id>` query param on the hash route,
     * so links like the vehicle-detail group chips land here pre-filtered.
     */
    private applyGroupFromHash() {
        const hash = location.hash;
        const qi = hash.indexOf('?');
        if (qi < 0) return;
        const group = new URLSearchParams(hash.slice(qi + 1)).get('group');
        if (group) this.selectedGroupId = group;
    }

    async connectedCallback() {
        super.connectedCallback();
        this.applyGroupFromHash();
        this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        this.unsubscribeHidden = hiddenVehiclesService.subscribe(() => {
            this.hiddenVehicles = hiddenVehiclesService.getHidden(this.tenantId);
        });
        void this.loadShareCapability();
        await this.loadData();
    }

    /**
     * The share control's tooltip. Always present, because the icon is always
     * rendered once the share annotation ran: an explained refusal teaches the
     * user what would make the vehicle shareable, where a hidden icon taught
     * them the feature didn't exist. The same sentence is handed to the modal,
     * which is why it comes from shareBlockReason and not from here.
     */
    private shareTitle(v: VehicleCard): string {
        return shareBlockReason(v, this.canShareVehicles, this.myWallet) ?? msg('Share vehicle');
    }

    /**
     * Whether this member may share at all. Best-effort and deliberately not
     * awaited with the vehicle load: failing to resolve it leaves sharing
     * explained as unavailable, which is the safe direction, and must not
     * delay the fleet list.
     */
    private async loadShareCapability() {
        try {
            const access = await TenantService.getInstance().fetchMyAccess();
            this.canShareVehicles = (access.permissions ?? []).includes('manage_vehicles');
            this.myWallet = access.wallet ?? '';
            this.fleetLicense = access.fleetLicense ?? '';
        } catch {
            this.canShareVehicles = false;
        }
    }

    private openShare(v: VehicleCard) {
        this.shareTarget = v;
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
                           title=${msg('Upload vehicle documents to identify this vehicle')}
                           aria-label=${msg('Upload vehicle documents to identify this vehicle')}>
                            <span class="material-symbols-outlined">inventory_2</span>
                        </a>
                    ` : nothing}
                </td>
                <td class="col-location mono">${this.formatLocation(v.tokenId)}</td>
                <td class="col-groups">
                    ${(v.groups ?? []).map((g) => html`
                        <span class="group-chip" style="--group-color:${g.color}">
                            <span class="dot"></span>${g.name}
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
                            ${v.canShare || v.shareBlocker ? html`
                                <button class="share-row-btn ${this.canShareVehicles && v.canShare ? '' : 'share-row-btn--blocked'}"
                                    title="${this.shareTitle(v)}"
                                    aria-haspopup="dialog"
                                    @click=${(e: Event) => {
                                        e.stopPropagation();
                                        // Opens even when sharing is blocked. The tooltip is the
                                        // only place the reason lived, and a tooltip is invisible
                                        // on touch and to anyone who doesn't hover long enough,
                                        // so the click has to lead somewhere that explains itself.
                                        this.openShare(v);
                                    }}>
                                    <span class="material-symbols-outlined">share</span>
                                </button>
                            ` : nothing}
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

            /* Same header + segmented view switch as fleet-overview, so moving
               between Map View and List View only swaps the body. */
            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                flex-shrink: 0;
                height: var(--top-bar-height);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
                background: var(--background);
            }
            header.top-bar .left { display: flex; align-items: center; gap: 20px; }
            header.top-bar h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            header.top-bar nav {
                display: flex;
                gap: 2px;
                padding: 3px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
            }
            header.top-bar nav a {
                text-decoration: none;
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface-variant);
                padding: 6px 14px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            header.top-bar nav a:hover { color: var(--on-surface); }
            header.top-bar nav a.active {
                color: var(--primary);
                background: var(--surface-bright);
                box-shadow: var(--shadow-sm);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }

            /* ── Controls bar ─────────────────────────────────────── */
            .controls {
                display: flex;
                align-items: center;
                gap: 10px;
                padding: 4px var(--gutter) 16px;
                flex-shrink: 0;
            }
            .search-wrap {
                display: flex;
                align-items: center;
                gap: 8px;
                height: 40px;
                padding: 0 12px;
                background: var(--surface-container-high);
                border-radius: var(--radius-md);
                flex: 1;
                max-width: 360px;
                transition: box-shadow 0.15s ease;
            }
            .search-wrap:focus-within { box-shadow: 0 0 0 2px var(--focus-ring); }
            .search-wrap > .material-symbols-outlined { font-size: 18px; color: var(--on-surface-variant); flex-shrink: 0; }
            .search-wrap input {
                background: none;
                border: none;
                outline: none;
                color: var(--on-surface);
                font: var(--type-body-sm);
                flex: 1;
                min-width: 0;
            }
            .search-wrap input:focus-visible { box-shadow: none; }
            .search-wrap input::placeholder { color: var(--on-surface-variant); }
            .search-wrap input::-webkit-search-cancel-button { display: none; }
            .clear-btn {
                display: inline-flex;
                padding: 2px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
            }
            .clear-btn:hover { color: var(--primary); background: var(--surface-container-highest); }
            .clear-btn .material-symbols-outlined { font-size: 16px; }
            .group-select {
                height: 40px;
                padding: 0 12px;
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface);
            }
            .vehicle-count {
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin-left: auto;
                white-space: nowrap;
            }
            .refresh-btn {
                width: 40px;
                height: 40px;
                flex-shrink: 0;
                display: inline-flex;
                align-items: center;
                justify-content: center;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .refresh-btn .material-symbols-outlined { font-size: 20px; }
            .refresh-btn:hover { background: var(--surface-container-high); color: var(--on-surface); }
            .refresh-btn:disabled { cursor: default; opacity: 0.6; }
            .refresh-btn.spinning .material-symbols-outlined {
                animation: spin 0.8s linear infinite;
            }
            @keyframes spin { to { transform: rotate(360deg); } }

            /* Toggle: tonal pill at rest, accent tint when on. */
            .show-hidden-btn {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                height: 40px;
                padding: 0 14px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
                font: 500 13px/18px var(--font-body);
                white-space: nowrap;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .show-hidden-btn:hover { background: var(--surface-container-highest); color: var(--on-surface); }
            .show-hidden-btn.active,
            .show-hidden-btn.active:hover {
                background: var(--selected-bg);
                color: var(--selected-fg);
            }
            .show-hidden-btn .material-symbols-outlined { font-size: 18px; }
            .show-hidden-btn .hidden-count { font-weight: 600; }

            /* ── Table ────────────────────────────────────────────── */
            .table-wrap {
                flex: 1;
                overflow: auto;
                padding: 0 var(--gutter) var(--gutter);
            }
            table {
                width: 100%;
                border-collapse: collapse;
                font: var(--type-body-sm);
                color: var(--on-surface);
            }
            /* Opaque so rows scroll under it, but the same tone as the page —
               the header reads as unfilled. */
            thead {
                position: sticky;
                top: 0;
                z-index: 2;
                background: var(--background);
            }
            th {
                height: 40px;
                padding: 0 12px;
                text-align: left;
                font: var(--type-label);
                color: var(--on-surface-variant);
                border-bottom: 1px solid var(--outline-variant);
                white-space: nowrap;
                user-select: none;
            }
            th.sortable { cursor: pointer; transition: color 0.15s ease; }
            th.sortable:hover { color: var(--on-surface); }
            .sort-icon {
                font-size: 14px;
                vertical-align: -3px;
                margin-left: 2px;
            }
            .sort-icon.muted { opacity: 0.4; }

            tbody tr {
                border-bottom: 1px solid var(--outline-variant);
                cursor: pointer;
                transition: background 0.12s ease;
            }
            tbody tr:hover { background: var(--surface-container-low); }
            tbody tr:last-child { border-bottom: none; }

            td {
                height: 56px;
                padding: 8px 12px;
                vertical-align: middle;
            }

            /* ── Column widths ────────────────────────────────────── */
            .col-status   { width: 56px; text-align: center; }
            .col-vehicle  { min-width: 200px; }
            .col-identifier { width: 180px; }
            .col-location { width: 170px; }
            .col-groups   { width: 170px; }
            .col-token    { width: 96px; text-align: right; }
            .col-action   { width: 124px; }

            /* ── Status dot ───────────────────────────────────────── */
            .status-dot {
                display: inline-block;
                width: 8px;
                height: 8px;
                border-radius: var(--radius-full);
                vertical-align: middle;
            }
            .status-green  { background: var(--accent); box-shadow: 0 0 8px var(--accent-soft-strong); }
            .status-amber  { background: var(--warning); }
            .status-red    { background: var(--error); }

            /* ── Vehicle cell ─────────────────────────────────────── */
            .vehicle-cell {
                display: flex;
                align-items: center;
                gap: 12px;
            }
            .vehicle-name {
                display: flex;
                flex-direction: column;
                align-items: flex-start;
                gap: 4px;
            }
            .vehicle-name .title {
                display: flex;
                align-items: center;
                gap: 6px;
                font: 500 14px/20px var(--font-body);
                color: var(--primary);
            }
            .star-icon {
                font-size: 15px;
                color: var(--favorite);
                font-variation-settings: 'FILL' 1;
            }
            .no-perm-badge {
                display: inline-flex;
                align-items: center;
                gap: 4px;
                padding: 1px 8px;
                border-radius: var(--radius-sm);
                background: color-mix(in srgb, var(--warning) 14%, transparent);
                color: var(--warning);
                font: 500 11px/16px var(--font-body);
            }
            .no-perm-badge .material-symbols-outlined { font-size: 12px; }

            /* ── Identifier cell ─────────────────────────────────── */
            .identifier-plate {
                display: inline-flex;
                align-items: center;
                gap: 4px;
                padding: 1px 6px;
                border-radius: 5px;
                background: var(--surface-container-highest);
                font: 600 11px/16px var(--font-body);
                letter-spacing: 0.06em;
                color: var(--on-surface);
                white-space: nowrap;
            }
            .identifier-plate .material-symbols-outlined { font-size: 13px; color: var(--on-surface-variant); }
            .identifier-plate + .identifier-vin { margin-top: 4px; }

            .identifier-vin {
                display: block;
                font: 400 12px/16px var(--font-body);
                letter-spacing: 0.02em;
                color: var(--on-surface-variant);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
                max-width: 160px;
            }

            .upload-id-btn {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                width: 32px;
                height: 32px;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                opacity: 0.55;
                text-decoration: none;
                transition: opacity 0.15s ease, color 0.15s ease, background 0.15s ease;
            }
            .upload-id-btn:hover { opacity: 1; color: var(--on-surface); background: var(--surface-container-high); }
            .upload-id-btn .material-symbols-outlined { font-size: 18px; }

            /* ── Location / token ─────────────────────────────────── */
            .mono {
                font: 400 13px/18px var(--font-body);
                color: var(--on-surface-variant);
                white-space: nowrap;
            }

            /* ── Group chips: 14% tint of the group color + a dot ─── */
            .group-chip {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                margin: 2px 4px 2px 0;
                padding: 2px 8px;
                border-radius: var(--radius-sm);
                background: color-mix(in srgb, var(--group-color, var(--outline)) 14%, transparent);
                font: var(--type-label);
                color: var(--on-surface);
                white-space: nowrap;
            }
            .group-chip .dot {
                width: 6px;
                height: 6px;
                border-radius: var(--radius-full);
                background: var(--group-color, var(--outline));
                flex-shrink: 0;
            }

            /* ── Action cell: quiet round icon buttons ────────────── */
            .action-cell {
                display: flex;
                align-items: center;
                justify-content: flex-end;
                gap: 2px;
            }
            .col-action a,
            .hide-row-btn,
            .share-row-btn,
            .unhide-row-btn {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                width: 32px;
                height: 32px;
                flex-shrink: 0;
                border-radius: var(--radius-full);
                color: var(--on-surface-variant);
                text-decoration: none;
                transition: opacity 0.15s ease, color 0.15s ease, background 0.15s ease;
            }
            .col-action .material-symbols-outlined { font-size: 18px; }
            .col-action a .material-symbols-outlined { font-size: 20px; }
            tbody tr:hover .col-action a {
                color: var(--on-surface);
                background: var(--surface-container-high);
            }
            .col-action a:hover { color: var(--primary); }

            .hide-row-btn { opacity: 0; }
            tbody tr:hover .hide-row-btn,
            .hide-row-btn:focus-visible { opacity: 1; }
            .hide-row-btn:hover { color: var(--error); background: var(--error-container); }

            /* Reveals on row hover like the hide button beside it, but stays
               visible on keyboard focus — hover-only would make it reachable by
               tab and invisible while focused. */
            .share-row-btn { opacity: 0; }
            tbody tr:hover .share-row-btn,
            .share-row-btn:focus-visible { opacity: 1; }
            .share-row-btn:hover { color: var(--on-surface); background: var(--surface-container-high); }
            /* Dimmed because sharing this vehicle won't work, but still a live
               button: it opens the modal that says why. No not-allowed cursor —
               the click does something, and pretending otherwise is what stopped
               anyone finding the reason in the first place. */
            tbody tr:hover .share-row-btn--blocked { opacity: 0.4; }
            .share-row-btn--blocked:hover,
            tbody tr:hover .share-row-btn--blocked:hover {
                color: var(--on-surface-variant);
                background: none;
                opacity: 0.7;
            }

            td.col-action { text-align: right; }
            .unhide-row-btn { color: var(--on-surface); }
            .unhide-row-btn:hover { color: var(--accent-ink); background: var(--accent-soft); }

            tbody tr.hidden-row { cursor: default; }
            tbody tr.hidden-row td:not(.col-action) { opacity: 0.45; }
            tbody tr.hidden-row:hover td:not(.col-action) { opacity: 0.7; }

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
                background: var(--outline-variant);
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
            ${this.shareTarget
                ? html`
                      <share-vehicle-modal
                          .tokenId=${Number(this.shareTarget.tokenId)}
                          .vehicleTitle=${this.shareTarget.title}
                          .blockedReason=${shareBlockReason(this.shareTarget, this.canShareVehicles, this.myWallet) ?? ''}
                          .owner=${this.shareTarget.owner ?? ''}
                          .myWallet=${this.myWallet}
                          .fleetLicense=${this.fleetLicense}
                          @close=${() => { this.shareTarget = null; }}
                      ></share-vehicle-modal>
                  `
                : nothing}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'fleet-list-view': FleetListView;
    }
}
