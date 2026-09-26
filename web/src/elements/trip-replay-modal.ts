import { LitElement, html, css, nothing, unsafeCSS } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { msg } from '@lit/localize';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import dayjs from 'dayjs';
import { sharedStyles } from '../global-styles.ts';
import { themeService } from '../services/theme-service.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { Trip, TripWaypoint } from '../types/telemetry.ts';
import { tripDistanceKm, tripDurationMs, tripSignal } from '../utils/trips.ts';
import { formatDistance, formatSpeed } from '../utils/units.ts';
import { buildTileLayer, MAP_COLORS, tripMapStyles } from '../utils/fleet-map.ts';
import { behaviorColor, isBehaviorEvent } from '../utils/behavior-events.ts';
import { ModalController } from '../utils/modal-controller.ts';

interface EventFlag {
    name: string;
    pct: number;
}

const MAX_WAYPOINTS = 500;

// Map markers can't read CSS variables. The vehicle is the brand mint; route
// and endpoints come from tripMapStyles() so they track the tile theme.
const POSITION_STYLE: L.CircleMarkerOptions = { radius: 8, fillColor: MAP_COLORS.mint, color: MAP_COLORS.ink, weight: 2.5, fillOpacity: 1 };

function downsample(pts: TripWaypoint[]): TripWaypoint[] {
    if (pts.length <= MAX_WAYPOINTS) return pts;
    const step = Math.ceil(pts.length / MAX_WAYPOINTS);
    const out: TripWaypoint[] = [];
    for (let i = 0; i < pts.length; i += step) out.push(pts[i]);
    if (out[out.length - 1] !== pts[pts.length - 1]) out.push(pts[pts.length - 1]);
    return out;
}

function fmtDuration(seconds: number): string {
    const h = Math.floor(seconds / 3600);
    const m = Math.round((seconds % 3600) / 60);
    return h > 0 ? (m > 0 ? `${h}h ${m}m` : `${h}h`) : `${m}m`;
}

/**
 * Full-screen modal that replays a single trip: animates a marker along the
 * vehicle's sampled GPS waypoints, drawing the polyline as it goes, with a
 * scrubber that ticks driving-behavior events. Self-contained — fetches its
 * own waypoints + events from /telemetry/:tokenId/replay. Adapts main's
 * nested `Trip` (segments) model via the shared trip utils.
 */
@customElement('trip-replay-modal')
export class TripReplayModal extends LitElement {
    constructor() {
        super();
        new ModalController(this, { close: () => this.dispatchClose() });
    }

    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
        css`
            :host {
                position: fixed;
                inset: 0;
                z-index: 1000;
                display: flex;
                align-items: center;
                justify-content: center;
                padding: 16px;
                background: var(--scrim);
                backdrop-filter: blur(6px);
                -webkit-backdrop-filter: blur(6px);
            }
            .card {
                width: min(100%, 820px);
                max-height: calc(100vh - 32px);
                max-height: calc(100dvh - 32px);
                display: flex;
                flex-direction: column;
                /* The map shrinks first; scrolling is only a last resort on
                   viewports too short for even the minimum map. */
                overflow-x: hidden;
                overflow-y: auto;
                /* Sections pad themselves; cancel the shared .card padding + border. */
                padding: 0;
                border: none;
                background: var(--surface);
                border-radius: var(--radius-xl);
                box-shadow: var(--shadow-float);
                color: var(--on-surface);
                position: relative;
            }
            .replay-header {
                display: flex;
                align-items: flex-start;
                justify-content: space-between;
                gap: 12px;
                padding: 20px 16px 16px 24px;
                flex-shrink: 0;
            }
            .replay-title {
                font: var(--type-headline-md);
                letter-spacing: -0.01em;
                color: var(--primary);
            }
            .replay-subtitle {
                display: block;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 2px;
            }
            .close {
                display: inline-flex;
                color: var(--on-surface-variant);
                padding: 6px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close:hover { background: var(--surface-container-high); color: var(--primary); }
            /* Up to 360px tall, giving way (down to 200px) so the stats and
               playback controls always fit on short laptop and phone screens. */
            .map-wrapper {
                position: relative;
                flex: 0 1 360px;
                min-height: 200px;
                display: flex;
                flex-direction: column;
                margin: 0 16px;
                border-radius: var(--radius-lg);
                overflow: hidden;
                isolation: isolate;
                background: var(--surface-container-lowest);
            }
            #map { flex: 1 1 auto; width: 100%; min-height: 0; background: var(--surface-container-lowest); }
            #map .leaflet-bar {
                border: none;
                border-radius: var(--radius-md);
                box-shadow: var(--shadow-float);
                overflow: hidden;
            }
            #map .leaflet-bar a {
                width: 32px;
                height: 32px;
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
                color: var(--on-surface);
                border-bottom: 1px solid var(--outline-variant);
                font: 500 18px/32px var(--font-body);
            }
            #map .leaflet-bar a:last-child { border-bottom: none; }
            #map .leaflet-bar a:hover { background: var(--surface-container-high); color: var(--primary); }
            #map .leaflet-control-attribution {
                background: var(--glass-bg);
                color: var(--on-surface-variant);
                font-size: 9px;
                opacity: 0.7;
            }
            #map .leaflet-control-attribution a { color: var(--on-surface-variant); }
            .sparse-msg {
                position: absolute;
                bottom: 12px;
                left: 50%;
                transform: translateX(-50%);
                z-index: 500;
                background: var(--glass-bg);
                backdrop-filter: blur(16px);
                -webkit-backdrop-filter: blur(16px);
                box-shadow: var(--shadow-float);
                border-radius: var(--radius-full);
                padding: 6px 14px;
                font: var(--type-label);
                color: var(--warning);
                pointer-events: none;
                white-space: nowrap;
            }
            .map-state {
                flex: 1 1 auto;
                display: flex;
                align-items: center;
                justify-content: center;
                padding: 24px;
                text-align: center;
                font: var(--type-body-md);
                color: var(--on-surface-variant);
            }
            .map-state.error { color: var(--error); }

            /* Trip stats as tonal tiles rather than a ruled strip. */
            .stats-bar {
                display: grid;
                grid-template-columns: repeat(4, 1fr);
                gap: 8px;
                padding: 12px 16px 0;
                flex-shrink: 0;
            }
            @media (max-width: 600px) {
                .stats-bar { grid-template-columns: repeat(2, 1fr); }
            }
            .stat {
                padding: 12px 14px;
                border-radius: var(--radius-md);
                background: var(--surface-container-low);
                min-width: 0;
            }
            .stat-label {
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin-bottom: 2px;
            }
            .stat-value {
                font: 600 22px/28px var(--font-headline);
                letter-spacing: -0.02em;
                color: var(--primary);
            }
            .stat-value .unit {
                font: var(--type-label);
                letter-spacing: 0;
                color: var(--on-surface-variant);
                margin-left: 3px;
            }
            .controls {
                display: flex;
                align-items: center;
                gap: 10px;
                padding: 16px 16px 20px 20px;
                flex-shrink: 0;
            }
            .progress-bar {
                flex: 1;
                position: relative;
                height: 4px;
                margin-right: 6px;
                overflow: visible;
            }
            /* Phones: the progress bar takes its own row above the buttons. */
            @media (max-width: 480px) {
                .controls { flex-wrap: wrap; row-gap: 14px; padding: 16px 16px 16px; }
                .progress-bar { flex: 1 0 100%; margin: 6px 0 0; }
                .time-display { margin-right: auto; }
            }
            .progress-track {
                height: 100%;
                background: var(--surface-container-highest);
                border-radius: var(--radius-full);
                overflow: hidden;
            }
            .progress-fill {
                height: 100%;
                background: var(--progress-fill);
                border-radius: var(--radius-full);
            }
            .event-tick {
                position: absolute;
                top: -4px;
                width: 3px;
                height: 12px;
                background: var(--tick-color);
                border-radius: var(--radius-full);
                transform: translateX(-50%);
                cursor: default;
            }
            .event-tick-tooltip {
                display: none;
                position: absolute;
                bottom: 18px;
                left: 50%;
                transform: translateX(-50%);
                background: var(--surface-bright);
                box-shadow: var(--shadow-float);
                border-radius: var(--radius-sm);
                padding: 4px 8px;
                font: var(--type-label);
                color: var(--on-surface);
                white-space: nowrap;
                pointer-events: none;
                /* The event name arrives upper-cased; render it sentence case. */
                text-transform: lowercase;
            }
            .event-tick-tooltip::first-letter { text-transform: uppercase; }
            .event-tick:hover .event-tick-tooltip { display: block; }
            .time-display {
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface-variant);
                white-space: nowrap;
            }
            .ctrl-btn {
                width: 36px;
                height: 36px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                display: flex;
                align-items: center;
                justify-content: center;
                color: var(--on-surface);
                flex-shrink: 0;
                transition: background 0.15s ease, filter 0.15s ease, box-shadow 0.15s ease;
            }
            .ctrl-btn .material-symbols-outlined { font-size: 20px; }
            .ctrl-btn:hover { background: var(--surface-container-highest); }
            .ctrl-btn.primary {
                width: 40px;
                height: 40px;
                background: var(--btn-primary-bg);
                color: var(--btn-primary-fg);
            }
            .ctrl-btn.primary .material-symbols-outlined { font-variation-settings: 'FILL' 1; }
            .ctrl-btn.primary:hover { background: var(--btn-primary-hover); }
            .speed-select {
                height: 36px;
                padding: 0 12px;
                border-radius: var(--radius-full);
                font: 500 13px/18px var(--font-body);
                color: var(--on-surface);
            }
        `,
    ];

    @property({ attribute: false }) trip!: Trip;
    @property({ type: Number }) tokenId!: number;

    @state() private waypoints: TripWaypoint[] = [];
    @state() private isPlaying = false;
    @state() private speedMultiplier: 1 | 2 | 4 = 1;
    @state() private loading = true;
    @state() private fetchError = '';
    @state() private isSparse = false;
    @state() private eventFlags: EventFlag[] = [];

    private currentStep = 0;
    private map?: L.Map;
    private tileLayer: L.TileLayer | null = null;

    private boundOnThemeChange = (e: Event) => {
        const { theme } = (e as CustomEvent<{ theme: 'dark' | 'light' }>).detail;
        if (!this.map) return;
        this.tileLayer?.remove();
        this.tileLayer = buildTileLayer(theme);
        this.tileLayer.addTo(this.map);
        const trip = tripMapStyles(theme);
        this.routeLines.forEach((l) => l.setStyle({ color: trip.route }));
        this.startMarker?.setStyle(trip.start);
        this.endMarker?.setStyle(trip.end);
    };

    private positionMarker?: L.CircleMarker;
    private drawnPolyline?: L.Polyline;
    // Route lines + endpoints, restyled when the theme flips.
    private routeLines: L.Polyline[] = [];
    private startMarker?: L.CircleMarker;
    private endMarker?: L.CircleMarker;
    // The map's height flexes with the viewport; keep Leaflet's size in sync.
    private resizeObserver?: ResizeObserver;
    private animationInterval?: number;
    private mapInitTimer?: number;
    private connected = false;


    // Trip metadata derived from main's nested segments `Trip` shape.
    private get startTs(): string { return this.trip.start.timestamp; }
    private get endTs(): string { return this.trip.end.timestamp; }
    private get durationSec(): number { return Math.round(tripDurationMs(this.trip) / 1000); }
    private get distanceKm(): number | undefined { return tripDistanceKm(this.trip); }
    private get avgSpeedKph(): number | undefined { return tripSignal(this.trip, 'speed', 'AVG'); }
    private get maxSpeedKph(): number | undefined { return tripSignal(this.trip, 'speed', 'MAX'); }

    override connectedCallback() {
        super.connectedCallback();
        this.connected = true;
        window.addEventListener('theme-change', this.boundOnThemeChange);
        void this.fetchRoute();
    }

    override disconnectedCallback() {
        super.disconnectedCallback();
        this.connected = false;
        window.removeEventListener('theme-change', this.boundOnThemeChange);
        if (this.mapInitTimer !== undefined) { clearTimeout(this.mapInitTimer); this.mapInitTimer = undefined; }
        this.stopAnim();
        this.resizeObserver?.disconnect();
        this.resizeObserver = undefined;
        this.map?.remove();
        this.map = undefined;
    }

    private dispatchClose() {
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    private async fetchRoute() {
        this.loading = true;
        this.fetchError = '';
        try {
            const resp = await TelemetryService.getInstance().tripReplay(this.tokenId, this.startTs, this.endTs);
            if (!this.connected) return;

            if (resp.permissionsRequired) {
                this.fetchError = msg('Grant DIMO permissions on this vehicle to see trip replay.');
                this.loading = false;
                return;
            }

            const raw = [...resp.waypoints].sort((a, b) => a.timestamp.localeCompare(b.timestamp));
            this.waypoints = downsample(raw);

            if (this.waypoints.length < 2) {
                this.isSparse = true;
                this.loading = false;
                await this.updateComplete;
                if (!this.connected) return;
                this.initFallbackMap();
                return;
            }

            const startMs = new Date(this.startTs).getTime();
            const endMs = new Date(this.endTs).getTime();
            const range = endMs - startMs;
            this.eventFlags = range <= 0 ? [] : resp.events
                .filter((e) => isBehaviorEvent(e.name))
                .map((e) => ({
                    name: e.name,
                    pct: Math.min(100, Math.max(0, (new Date(e.timestamp).getTime() - startMs) / range * 100)),
                }));

            this.loading = false;
            await this.updateComplete;
            if (!this.connected) return;
            this.initMap();
        } catch (e) {
            this.fetchError = e instanceof Error ? e.message : msg('Failed to load GPS data');
            this.loading = false;
        }
    }

    private initFallbackMap() {
        const el = this.shadowRoot?.getElementById('map');
        if (!el) return;
        const sLat = this.trip.start.value.latitude;
        const sLng = this.trip.start.value.longitude;
        const eLat = this.trip.end.value.latitude;
        const eLng = this.trip.end.value.longitude;

        this.map = L.map(el as HTMLElement);
        this.tileLayer = buildTileLayer(themeService.current);
        this.tileLayer.addTo(this.map);
        this.observeMapSize(el as HTMLElement);
        const trip = tripMapStyles(themeService.current);

        this.routeLines = [
            L.polyline([[sLat, sLng], [eLat, eLng]], { color: trip.route, dashArray: '6,6', opacity: 0.6, weight: 2 }).addTo(this.map),
        ];
        this.startMarker = L.circleMarker([sLat, sLng], trip.start)
            .bindPopup(msg('Start')).addTo(this.map);
        this.endMarker = L.circleMarker([eLat, eLng], trip.end)
            .bindPopup(msg('End')).addTo(this.map);

        try { this.map.fitBounds([[sLat, sLng], [eLat, eLng]], { padding: [40, 40] }); } catch { /* ignore */ }
        this.mapInitTimer = window.setTimeout(() => {
            this.mapInitTimer = undefined;
            this.map?.invalidateSize();
        }, 100);
    }

    private initMap() {
        const el = this.shadowRoot?.getElementById('map');
        if (!el || this.waypoints.length < 2) return;

        const bounds = this.waypoints.map((w) => [w.lat, w.lng] as [number, number]);

        this.map = L.map(el as HTMLElement);
        this.tileLayer = buildTileLayer(themeService.current);
        this.tileLayer.addTo(this.map);
        this.observeMapSize(el as HTMLElement);
        const trip = tripMapStyles(themeService.current);

        this.drawnPolyline = L.polyline([], { color: trip.route, weight: 4, opacity: 0.95 });
        this.routeLines = [
            L.polyline(bounds, { color: trip.route, opacity: 0.35, weight: 2, dashArray: '4,3' }).addTo(this.map),
            this.drawnPolyline.addTo(this.map),
        ];

        this.startMarker = L.circleMarker(bounds[0], trip.start)
            .bindPopup(msg('Start')).addTo(this.map);
        this.endMarker = L.circleMarker(bounds[bounds.length - 1], trip.end)
            .bindPopup(msg('End')).addTo(this.map);

        this.positionMarker = L.circleMarker(bounds[0], POSITION_STYLE).addTo(this.map);

        try { this.map.fitBounds(bounds as L.LatLngBoundsLiteral, { padding: [40, 40] }); } catch { /* ignore */ }
        this.mapInitTimer = window.setTimeout(() => {
            this.mapInitTimer = undefined;
            this.map?.invalidateSize();
            this.startAnim();
        }, 150);
    }

    private observeMapSize(el: HTMLElement) {
        this.resizeObserver?.disconnect();
        this.resizeObserver = new ResizeObserver(() => this.map?.invalidateSize());
        this.resizeObserver.observe(el);
    }

    private startAnim() {
        if (this.animationInterval !== undefined) clearInterval(this.animationInterval);
        this.animationInterval = window.setInterval(() => this.tick(), Math.floor(50 / this.speedMultiplier));
        this.isPlaying = true;
    }

    private stopAnim() {
        if (this.animationInterval !== undefined) { clearInterval(this.animationInterval); this.animationInterval = undefined; }
        this.isPlaying = false;
    }

    private tick() {
        if (this.currentStep >= this.waypoints.length - 1) { this.stopAnim(); return; }
        this.currentStep++;
        const wp = this.waypoints[this.currentStep];
        this.positionMarker?.setLatLng([wp.lat, wp.lng]);
        this.drawnPolyline?.addLatLng([wp.lat, wp.lng]);
        this.requestUpdate();
    }

    private togglePlay() {
        if (this.isPlaying) { this.stopAnim(); }
        else { if (this.currentStep >= this.waypoints.length - 1) this.doReset(); this.startAnim(); }
    }

    private doReset() {
        this.stopAnim();
        this.currentStep = 0;
        this.drawnPolyline?.setLatLngs([]);
        if (this.waypoints.length > 0) this.positionMarker?.setLatLng([this.waypoints[0].lat, this.waypoints[0].lng]);
        this.requestUpdate();
    }

    private onSpeedChange(e: Event) {
        this.speedMultiplier = Number((e.target as HTMLSelectElement).value) as 1 | 2 | 4;
        if (this.isPlaying) { this.stopAnim(); this.startAnim(); }
    }

    private get progressPct(): number {
        return this.waypoints.length > 1 ? (this.currentStep / (this.waypoints.length - 1)) * 100 : 0;
    }
    private get currentClock(): string {
        return this.waypoints.length ? dayjs(this.waypoints[this.currentStep].timestamp).format('HH:mm') : '';
    }
    private get endClock(): string {
        return this.endTs ? dayjs(this.endTs).format('HH:mm') : '';
    }

    override render() {
        const distFmt = formatDistance(this.distanceKm, 1);
        const avgFmt = formatSpeed(this.avgSpeedKph);
        const maxFmt = formatSpeed(this.maxSpeedKph);
        const hasControls = !this.isSparse && !this.loading && !this.fetchError;

        return html`
            <div class="card" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1" @click=${(e: Event) => e.stopPropagation()}>
                <div class="replay-header">
                    <div>
                        <span class="replay-title" id="modal-title">${msg('Trip Replay')}</span>
                        <span class="replay-subtitle">
                            ${dayjs(this.startTs).format('MMM D · HH:mm')} → ${this.endClock} · ${fmtDuration(this.durationSec)}
                        </span>
                    </div>
                    <button class="close" aria-label=${msg('Close')} @click=${this.dispatchClose}>
                        <span class="material-symbols-outlined">close</span>
                    </button>
                </div>

                <div class="map-wrapper">
                    ${this.loading
                        ? html`<div class="map-state">${msg('Loading GPS data…')}</div>`
                        : this.fetchError
                            ? html`<div class="map-state error">${this.fetchError}</div>`
                            : html`<div id="map"></div>`}
                    ${this.isSparse ? html`<div class="sparse-msg">${msg('GPS data sparse — showing start and end only')}</div>` : nothing}
                </div>

                <div class="stats-bar">
                    <div class="stat">
                        <div class="stat-label">${msg('Distance')}</div>
                        <div class="stat-value">${distFmt.value}<span class="unit">${distFmt.unit}</span></div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">${msg('Duration')}</div>
                        <div class="stat-value">${fmtDuration(this.durationSec)}</div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">${msg('Avg speed')}</div>
                        <div class="stat-value">${avgFmt.value}<span class="unit">${avgFmt.unit}</span></div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">${msg('Max speed')}</div>
                        <div class="stat-value">${maxFmt.value}<span class="unit">${maxFmt.unit}</span></div>
                    </div>
                </div>

                ${hasControls ? html`
                    <div class="controls">
                        <div class="progress-bar">
                            <div class="progress-track">
                                <div class="progress-fill" style="width:${this.progressPct}%"></div>
                            </div>
                            ${this.eventFlags.map((flag) => html`
                                <div class="event-tick" style="left:${flag.pct}%;--tick-color:${behaviorColor(flag.name)}">
                                    <div class="event-tick-tooltip">
                                        ${flag.name.replace(/^[^.]+\./, '').replace(/([A-Z])/g, ' $1').trim().toUpperCase()}
                                    </div>
                                </div>
                            `)}
                        </div>
                        <span class="time-display">${this.currentClock} / ${this.endClock}</span>
                        <button class="ctrl-btn primary" @click=${this.togglePlay} title=${this.isPlaying ? msg('Pause') : msg('Play')}>
                            <span class="material-symbols-outlined">${this.isPlaying ? 'pause' : 'play_arrow'}</span>
                        </button>
                        <button class="ctrl-btn" @click=${this.doReset} title=${msg('Reset')}>
                            <span class="material-symbols-outlined">replay</span>
                        </button>
                        <select class="speed-select" @change=${this.onSpeedChange} title=${msg('Playback speed')}>
                            <option value="1" ?selected=${this.speedMultiplier === 1}>1×</option>
                            <option value="2" ?selected=${this.speedMultiplier === 2}>2×</option>
                            <option value="4" ?selected=${this.speedMultiplier === 4}>4×</option>
                        </select>
                    </div>
                ` : nothing}
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'trip-replay-modal': TripReplayModal;
    }
}
