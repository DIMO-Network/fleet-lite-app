import { LitElement, html, css, unsafeCSS, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { msg } from '@lit/localize';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { PrefsService, TripMechanism } from '../services/prefs-service.ts';
import { formatDistance, formatSpeed } from '../utils/units.ts';
import { tripSignal, tripDistanceKm, tripTimeShort, formatDwell } from '../utils/trips.ts';
import { Trip } from '../types/telemetry.ts';
import { GeofenceCrossing } from '../types/geofence.ts';
import './trip-replay-modal.ts';

/** One selectable trips window. */
interface Period {
    from: Date;
    to: Date;
}

const PERIOD_DAYS = 14;
const PERIOD_COUNT = 6;

/** Detection methods offered in the picker, in display order ('auto' first). */
const TRIP_MECHANISMS: readonly TripMechanism[] = [
    'auto', 'ignitionDetection', 'frequencyAnalysis', 'changePointDetection', 'idling', 'refuel', 'recharge',
];

/**
 * Trips panel for the vehicle details screen: an embedded mini-map showing
 * the vehicle's live position, a bi-weekly period picker, and the detected
 * trips for that window. Clicking a trip draws its route on the mini-map;
 * "Back to live" (or clicking the trip again) returns to the live position.
 * Self-contained — owns its own map, fetches its own data.
 */
@customElement('vehicle-trips-panel')
export class VehicleTripsPanel extends LitElement {
    @property({ type: String }) tokenId = '';

    @state() private trips: Trip[] = [];
    @state() private tripsLoading = false;
    @state() private tripsError = false;
    @state() private permissionsRequired = false;
    @state() private selectedTrip: Trip | null = null;
    @state() private routeLoading = false;
    @state() private periodIndex = 0;
    // Geofences the selected trip crossed (Phase 2 detection, entry 1).
    @state() private tripGeofences: GeofenceCrossing[] = [];
    @state() private geofencesLoading = false;
    // Trip open in the full-screen replay modal (null = closed).
    @state() private replayTrip: Trip | null = null;

    private map: L.Map | null = null;
    private tileLayer: L.TileLayer | null = null;
    private liveMarker: L.CircleMarker | null = null;
    private routeLayer: L.Polyline | null = null;
    private endpointLayers: L.CircleMarker[] = [];
    private geofenceMarkers: L.CircleMarker[] = [];
    private resizeObserver: ResizeObserver | null = null;
    private unsubscribePrefs: (() => void) | null = null;
    // Guards stale async results after period/vehicle switches.
    private loadGeneration = 0;

    private static readonly LIVE_STYLE: L.CircleMarkerOptions = { radius: 8, fillColor: '#69dbad', color: '#ffffff', weight: 2, opacity: 0.9, fillOpacity: 0.85 };

    private boundOnThemeChange = (e: Event) => {
        const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
        if (!this.map) return;
        this.tileLayer?.remove();
        this.tileLayer = this.buildTileLayer(theme);
        this.tileLayer.addTo(this.map);
    };

    private buildTileLayer(theme: 'dark' | 'light'): L.TileLayer {
        const url = theme === 'light'
            ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
            : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
        return L.tileLayer(url, {
            attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
            subdomains: 'abcd',
            maxZoom: 19,
        });
    }

    connectedCallback() {
        super.connectedCallback();
        this.unsubscribePrefs = PrefsService.getInstance().subscribe(() => this.requestUpdate());
        window.addEventListener('theme-change', this.boundOnThemeChange);
    }

    disconnectedCallback() {
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        this.resizeObserver?.disconnect();
        this.map?.remove();
        this.map = null;
        this.unsubscribePrefs?.();
        super.disconnectedCallback();
    }

    willUpdate(changed: Map<string, unknown>) {
        if (changed.has('tokenId') && this.tokenId) {
            this.periodIndex = 0;
            this.clearRoute();
            void this.loadTrips();
            void this.loadLivePosition();
        }
    }

    override firstUpdated() {
        const el = this.renderRoot.querySelector<HTMLElement>('.map');
        if (!el) return;
        this.map = L.map(el, { zoomControl: true, attributionControl: true }).setView([39.5, -98.35], 4);
        this.tileLayer = this.buildTileLayer(themeService.current);
        this.tileLayer.addTo(this.map);
        // Shadow-DOM-hosted maps often initialize before layout settles.
        this.resizeObserver = new ResizeObserver(() => this.map?.invalidateSize());
        this.resizeObserver.observe(el);
    }

    /** The selectable windows: "Last 2 weeks", then bi-weekly ranges back. */
    private periods(): Period[] {
        const out: Period[] = [];
        const now = Date.now();
        for (let i = 0; i < PERIOD_COUNT; i++) {
            out.push({
                from: new Date(now - (i + 1) * PERIOD_DAYS * 24 * 3600 * 1000),
                to: new Date(now - i * PERIOD_DAYS * 24 * 3600 * 1000),
            });
        }
        return out;
    }

    private periodLabel(p: Period, index: number): string {
        if (index === 0) return msg('Last 2 weeks');
        const fmt = (d: Date) => d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
        return `${fmt(p.from)} – ${fmt(p.to)}`;
    }

    private async loadLivePosition() {
        const tokenId = this.tokenId;
        if (!tokenId) return;
        try {
            const res = await TelemetryService.getInstance().fleetLocations(false, [tokenId]);
            if (this.tokenId !== tokenId) return;
            const loc = res.locations?.[tokenId];
            if (!loc || !this.map) return;
            this.liveMarker?.remove();
            this.liveMarker = L.circleMarker([loc.lat, loc.lon], VehicleTripsPanel.LIVE_STYLE).addTo(this.map);
            // Don't fight a route view the user may already be looking at.
            if (!this.selectedTrip) this.map.setView([loc.lat, loc.lon], 12);
        } catch {
            // No live position — map stays at the default view.
        }
    }

    private async loadTrips() {
        const tokenId = this.tokenId;
        if (!tokenId) return;
        const gen = ++this.loadGeneration;
        this.tripsLoading = true;
        this.tripsError = false;
        this.trips = [];
        this.permissionsRequired = false;
        this.clearRoute();
        try {
            const p = this.periods()[this.periodIndex];
            const mechanism = PrefsService.getInstance().getTripMechanism();
            const res = await TelemetryService.getInstance().segments(Number(tokenId), p.from.toISOString(), p.to.toISOString(), mechanism);
            if (gen !== this.loadGeneration) return;
            this.trips = res.segments || [];
            this.permissionsRequired = !!res.permissionsRequired;
        } catch (e) {
            console.error('trips load failed', e);
            if (gen === this.loadGeneration) this.tripsError = true;
        } finally {
            if (gen === this.loadGeneration) this.tripsLoading = false;
        }
    }

    private onPeriodChange(e: Event) {
        this.periodIndex = Number((e.target as HTMLSelectElement).value);
        void this.loadTrips();
    }

    private onMechanismChange(e: Event) {
        // Persist (so the choice sticks across vehicles/reloads), then refetch
        // this vehicle's trips with the new detector.
        PrefsService.getInstance().setTripMechanism((e.target as HTMLSelectElement).value as TripMechanism);
        void this.loadTrips();
    }

    private mechanismLabel(m: TripMechanism): string {
        switch (m) {
            case 'auto': return msg('Auto');
            case 'ignitionDetection': return msg('Ignition');
            case 'frequencyAnalysis': return msg('Frequency');
            case 'changePointDetection': return msg('Change point');
            case 'idling': return msg('Idling');
            case 'refuel': return msg('Refuel');
            case 'recharge': return msg('Recharge');
        }
    }

    private async selectTrip(trip: Trip) {
        if (this.selectedTrip === trip) {
            this.backToLive();
            return;
        }
        const tokenId = this.tokenId;
        this.selectedTrip = trip;
        this.routeLoading = true;
        this.tripGeofences = [];
        // Geofence crossings load independently of the route so neither blocks the
        // other; the route is the primary view, crossings enrich it.
        void this.loadTripGeofences(trip, tokenId);
        try {
            const res = await TelemetryService.getInstance().tripRoute(
                Number(tokenId), trip.start.timestamp, trip.end.timestamp);
            if (this.selectedTrip !== trip || this.tokenId !== tokenId) return;
            const points = (res.points || []).map((p) => [p.lat, p.lon] as [number, number]);
            if (points.length === 0) {
                points.push(
                    [trip.start.value.latitude, trip.start.value.longitude],
                    [trip.end.value.latitude, trip.end.value.longitude],
                );
            }
            this.drawRoute(points);
        } catch {
            this.backToLive();
        } finally {
            if (this.selectedTrip === trip) this.routeLoading = false;
        }
    }

    private async loadTripGeofences(trip: Trip, tokenId: string) {
        this.geofencesLoading = true;
        try {
            const res = await TelemetryService.getInstance().tripGeofences(
                Number(tokenId), trip.start.timestamp, trip.end.timestamp);
            if (this.selectedTrip !== trip || this.tokenId !== tokenId) return;
            this.tripGeofences = res.geofences || [];
            this.drawGeofenceMarkers();
        } catch {
            if (this.selectedTrip === trip) this.tripGeofences = [];
        } finally {
            if (this.selectedTrip === trip && this.tokenId === tokenId) this.geofencesLoading = false;
        }
    }

    /** Drop a small marker at each pass's entry point, colored per geofence. */
    private drawGeofenceMarkers() {
        if (!this.map) return;
        this.removeGeofenceMarkers();
        for (const g of this.tripGeofences) {
            for (const p of g.passes) {
                const m = L.circleMarker([p.entryLat, p.entryLng], {
                    radius: 6, fillColor: g.color, color: '#ffffff', weight: 2, fillOpacity: 0.95,
                }).addTo(this.map);
                this.geofenceMarkers.push(m);
            }
        }
    }

    private removeGeofenceMarkers() {
        this.geofenceMarkers.forEach((m) => m.remove());
        this.geofenceMarkers = [];
    }

    private drawRoute(points: Array<[number, number]>) {
        if (!this.map) return;
        this.removeRouteLayers();
        this.routeLayer = L.polyline(points, { color: '#f5c84b', weight: 4, opacity: 0.85 }).addTo(this.map);
        this.endpointLayers = [
            L.circleMarker(points[0], { radius: 5, fillColor: '#69dbad', color: '#ffffff', weight: 2, fillOpacity: 1 }).addTo(this.map),
            L.circleMarker(points[points.length - 1], { radius: 5, fillColor: '#f5c84b', color: '#ffffff', weight: 2, fillOpacity: 1 }).addTo(this.map),
        ];
        this.map.fitBounds(this.routeLayer.getBounds(), { padding: [30, 30], maxZoom: 15 });
    }

    private removeRouteLayers() {
        this.routeLayer?.remove();
        this.routeLayer = null;
        this.endpointLayers.forEach((m) => m.remove());
        this.endpointLayers = [];
    }

    private clearRoute() {
        this.removeRouteLayers();
        this.removeGeofenceMarkers();
        this.selectedTrip = null;
        this.routeLoading = false;
        this.tripGeofences = [];
        this.geofencesLoading = false;
    }

    private backToLive() {
        this.clearRoute();
        if (this.map && this.liveMarker) {
            this.map.setView(this.liveMarker.getLatLng(), 12);
        }
    }

    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        css`
            :host { display: block; }
            .card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                overflow: hidden;
            }
            .head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 12px;
                padding: 14px 16px;
                border-bottom: 1px solid var(--outline-variant);
                flex-wrap: wrap;
            }
            .head .title {
                display: flex;
                align-items: center;
                gap: 8px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .head .title .material-symbols-outlined { font-size: 16px; }
            .head .count {
                font: var(--type-body-sm);
                padding: 2px 10px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                color: var(--on-surface);
            }
            .head .controls { display: flex; align-items: center; gap: 10px; margin-left: auto; }
            .head select {
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-md);
                color: var(--on-surface);
                font: var(--type-body-sm);
                padding: 6px 10px;
            }
            .head select:focus { outline: 1px solid var(--primary); }
            .back-live {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                background: none;
                border: 1px solid #f5c84b;
                border-radius: var(--radius-full);
                color: #f5c84b;
                font: var(--type-label-caps);
                font-size: 10px;
                letter-spacing: 0.05em;
                text-transform: uppercase;
                padding: 5px 12px;
                cursor: pointer;
            }
            .back-live .material-symbols-outlined { font-size: 14px; }
            .back-live:hover { background: rgba(245, 200, 75, 0.12); }

            .body { display: grid; grid-template-columns: 1fr 360px; min-height: 380px; }
            @media (max-width: 900px) {
                .body { grid-template-columns: 1fr; }
                .map { height: 280px; }
            }
            .map { min-height: 280px; background: #0d0f12; isolation: isolate; }
            .list {
                border-left: 1px solid var(--outline-variant);
                overflow-y: auto;
                max-height: 380px;
            }
            @media (max-width: 900px) {
                .list { border-left: none; border-top: 1px solid var(--outline-variant); }
            }

            .trip-entry { border-bottom: 1px solid var(--outline-variant); }
            .trip-entry.selected {
                background: var(--surface-container-high);
                box-shadow: inset 3px 0 0 #f5c84b;
            }
            .trip-row-wrap {
                display: flex;
                align-items: stretch;
                transition: background 0.15s ease;
            }
            .trip-entry:not(.selected) .trip-row-wrap:hover { background: var(--surface-container-high); }

            .gf-detail {
                padding: 10px 16px 14px;
                border-top: 1px dashed var(--outline-variant);
                display: flex;
                flex-direction: column;
                gap: 10px;
            }
            .gf-detail.loading {
                flex-direction: row;
                align-items: center;
                gap: 8px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .gf-head {
                display: flex;
                align-items: center;
                gap: 6px;
                font: var(--type-label-caps);
                font-size: 10px;
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .gf-head .material-symbols-outlined { font-size: 14px; }
            .gf-item { display: flex; flex-direction: column; gap: 4px; }
            .gf-name {
                display: flex;
                align-items: center;
                gap: 7px;
                font: var(--type-body-sm);
                font-weight: 600;
                color: var(--on-surface);
            }
            .gf-dot { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
            .gf-pass {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                padding-left: 17px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .gf-times { display: inline-flex; align-items: center; gap: 5px; white-space: nowrap; }
            .gf-times .material-symbols-outlined { font-size: 12px; }
            .gf-meta { display: inline-flex; align-items: center; gap: 10px; white-space: nowrap; }
            .gf-speed { display: inline-flex; align-items: center; gap: 3px; }
            .gf-speed.over {
                color: var(--error);
                font-weight: 600;
            }
            .gf-speed .material-symbols-outlined { font-size: 13px; }
            .spin { animation: gf-spin 1s linear infinite; }
            @keyframes gf-spin { to { transform: rotate(360deg); } }
            .trip-row {
                flex: 1;
                min-width: 0;
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                padding: 12px 16px;
                background: none;
                border: none;
                cursor: pointer;
                text-align: left;
            }
            .replay-btn {
                flex-shrink: 0;
                display: flex;
                align-items: center;
                padding: 0 14px;
                background: none;
                border: none;
                border-left: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
                cursor: pointer;
            }
            .replay-btn:hover { color: #f5c84b; }
            .replay-btn .material-symbols-outlined { font-size: 18px; }
            .trip-row .when .times {
                display: flex;
                align-items: center;
                gap: 6px;
                font: var(--type-body-sm);
                color: var(--on-surface);
                white-space: nowrap;
            }
            .trip-row .when .times .material-symbols-outlined { font-size: 13px; color: var(--on-surface-variant); }
            .trip-row .when .ongoing {
                font: var(--type-label-caps);
                font-size: 9px;
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: #69dbad;
            }
            .trip-row .stats {
                flex-shrink: 0;
                text-align: right;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                white-space: nowrap;
            }
            .trip-row .stats .dist { color: var(--primary); font-weight: 600; }

            .state-row {
                padding: 16px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .state-row.perms { color: #f5c84b; display: flex; gap: 8px; align-items: flex-start; }
            .state-row.perms .material-symbols-outlined { font-size: 16px; margin-top: 1px; }
        `,
    ];

    private renderRow(trip: Trip) {
        const selected = this.selectedTrip === trip;
        const dist = tripDistanceKm(trip);
        const avg = tripSignal(trip, 'speed', 'AVG');
        const max = tripSignal(trip, 'speed', 'MAX');
        const distFv = dist != null ? formatDistance(dist, 1) : null;
        const avgFv = avg != null ? formatSpeed(avg) : null;
        const maxFv = max != null ? formatSpeed(max) : null;
        return html`
            <div class="trip-entry ${selected ? 'selected' : ''}">
                <div class="trip-row-wrap">
                    <button class="trip-row" @click=${() => this.selectTrip(trip)}>
                        <span class="when">
                            <span class="times">
                                ${tripTimeShort(trip.start.timestamp)}
                                <span class="material-symbols-outlined">arrow_forward</span>
                                ${trip.isOngoing ? '' : tripTimeShort(trip.end.timestamp)}
                            </span>
                            ${trip.isOngoing ? html`<span class="ongoing">${msg('Ongoing')}</span>` : ''}
                        </span>
                        <span class="stats">
                            ${selected && this.routeLoading
                                ? html`<span class="material-symbols-outlined" style="font-size:14px;">progress_activity</span>`
                                : html`
                                    ${distFv ? html`<span class="dist">${distFv.value} ${distFv.unit}</span>` : ''}
                                    ${avgFv && maxFv ? html`<br />${avgFv.value}/${maxFv.value} ${maxFv.unit}` : ''}
                                `}
                        </span>
                    </button>
                    <button class="replay-btn" title=${msg('Replay trip')} @click=${() => this.openReplay(trip)}>
                        <span class="material-symbols-outlined">smart_display</span>
                    </button>
                </div>
                ${selected ? this.renderTripGeofences() : nothing}
            </div>
        `;
    }

    /** The "geofences crossed" detail shown under the selected trip. */
    private renderTripGeofences() {
        if (this.geofencesLoading && this.tripGeofences.length === 0) {
            return html`<div class="gf-detail loading">
                <span class="material-symbols-outlined spin">progress_activity</span>${msg('Checking geofences…')}
            </div>`;
        }
        if (this.tripGeofences.length === 0) return nothing;
        return html`
            <div class="gf-detail">
                <div class="gf-head">
                    <span class="material-symbols-outlined">fence</span>${msg('Geofences crossed')}
                </div>
                ${this.tripGeofences.map((g) => html`
                    <div class="gf-item">
                        <div class="gf-name">
                            <span class="gf-dot" style="background:${g.color}"></span>${g.name}
                        </div>
                        ${g.passes.map((p) => {
                            const max = p.maxSpeedKph != null ? formatSpeed(p.maxSpeedKph) : null;
                            return html`
                                <div class="gf-pass">
                                    <span class="gf-times">
                                        ${tripTimeShort(p.enteredAt)}
                                        <span class="material-symbols-outlined">arrow_forward</span>
                                        ${tripTimeShort(p.exitedAt)}
                                    </span>
                                    <span class="gf-meta">
                                        <span class="gf-dwell">${formatDwell(p.dwellS)}</span>
                                        ${max ? html`<span class="gf-speed ${p.speedExceeded ? 'over' : ''}">
                                            ${p.speedExceeded ? html`<span class="material-symbols-outlined">speed</span>` : nothing}${max.value} ${max.unit}
                                        </span>` : nothing}
                                    </span>
                                </div>`;
                        })}
                    </div>
                `)}
            </div>
        `;
    }

    private openReplay(trip: Trip) {
        this.replayTrip = trip;
    }

    private renderList() {
        if (this.tripsLoading) return html`<div class="state-row">${msg('Loading trips…')}</div>`;
        if (this.permissionsRequired) {
            return html`
                <div class="state-row perms">
                    <span class="material-symbols-outlined">lock</span>
                    <span>${msg('Grant DIMO permissions to see live telemetry on this vehicle.')}</span>
                </div>`;
        }
        if (this.tripsError) return html`<div class="state-row">${msg('Failed to load trips — check console for details.')}</div>`;
        if (this.trips.length === 0) return html`<div class="state-row">${msg('No trips in this period.')}</div>`;
        return this.trips.map((t) => this.renderRow(t));
    }

    render() {
        const currentMechanism = PrefsService.getInstance().getTripMechanism();
        return html`
            <div class="card">
                <div class="head">
                    <span class="title"><span class="material-symbols-outlined">route</span>${msg('Trips')}</span>
                    ${!this.tripsLoading && this.trips.length > 0
                        ? html`<span class="count">${this.trips.length}</span>` : nothing}
                    <div class="controls">
                        ${this.selectedTrip
                            ? html`
                                <button class="back-live" @click=${this.backToLive}>
                                    <span class="material-symbols-outlined">my_location</span>${msg('Back to live')}
                                </button>`
                            : nothing}
                        <select class="mechanism" title=${msg('Trip detection method')}
                                @change=${this.onMechanismChange}>
                            ${TRIP_MECHANISMS.map((m) => html`
                                <option value=${m} ?selected=${m === currentMechanism}>${this.mechanismLabel(m)}</option>
                            `)}
                        </select>
                        <select @change=${this.onPeriodChange} .value=${String(this.periodIndex)}>
                            ${this.periods().map((p, i) => html`
                                <option value=${i} ?selected=${i === this.periodIndex}>${this.periodLabel(p, i)}</option>
                            `)}
                        </select>
                    </div>
                </div>
                <div class="body">
                    <div class="map"></div>
                    <div class="list custom-scrollbar">${this.renderList()}</div>
                </div>
            </div>
            ${this.replayTrip
                ? html`<trip-replay-modal
                        .trip=${this.replayTrip}
                        .tokenId=${Number(this.tokenId)}
                        @close=${() => { this.replayTrip = null; }}></trip-replay-modal>`
                : nothing}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'vehicle-trips-panel': VehicleTripsPanel;
    }
}
