import { LitElement, html, css, nothing, unsafeCSS } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import L from 'leaflet';
import leafletCss from 'leaflet/dist/leaflet.css?inline';
import dayjs from 'dayjs';
import { sharedStyles } from '../global-styles.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { Trip, TripWaypoint } from '../types/telemetry.ts';
import { formatDistance, formatSpeed } from '../utils/units.ts';

interface EventFlag {
    name: string;
    pct: number;
}

const MAX_WAYPOINTS = 500;
const TILE_URL = 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
const TILE_ATTRIBUTION = '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>';

const EVENT_COLORS: Readonly<Record<string, string>> = {
    'behavior.extremeBraking': '#EF4444',
    'behavior.harshBraking': '#F59E0B',
    'behavior.harshCornering': '#A78BFA',
    'behavior.harshAcceleration': '#34D399',
};

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

@customElement('trip-replay-modal')
export class TripReplayModal extends LitElement {
    static styles = [
        sharedStyles,
        unsafeCSS(leafletCss),
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
                width: min(90vw, 820px);
                max-height: 90vh;
                display: flex;
                flex-direction: column;
                overflow: hidden;
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                color: var(--on-surface);
                position: relative;
            }
            .replay-header {
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px 24px;
                border-bottom: 1px solid var(--outline-variant);
                flex-shrink: 0;
            }
            .replay-title { font: var(--type-headline-md); }
            .replay-subtitle {
                display: block;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-top: 4px;
            }
            .close {
                background: none;
                border: none;
                color: var(--on-surface-variant);
                padding: 4px;
                cursor: pointer;
            }
            .close:hover { color: var(--primary); }
            .map-wrapper { position: relative; flex-shrink: 0; }
            #map { width: 100%; height: 340px; background: var(--surface-container-lowest); }
            .sparse-msg {
                position: absolute;
                bottom: 10px;
                left: 50%;
                transform: translateX(-50%);
                background: rgba(0, 0, 0, 0.8);
                border: 1px solid var(--secondary);
                border-radius: var(--radius-sm);
                padding: 6px 12px;
                font: var(--type-label-caps);
                color: var(--secondary);
                pointer-events: none;
                white-space: nowrap;
            }
            .map-state {
                height: 340px;
                display: flex;
                align-items: center;
                justify-content: center;
                font: var(--type-body-md);
                color: var(--on-surface-variant);
            }
            .map-state.error { color: var(--error); }
            .stats-bar {
                display: flex;
                border-top: 1px solid var(--outline-variant);
                border-bottom: 1px solid var(--outline-variant);
                flex-shrink: 0;
            }
            .stat {
                flex: 1;
                padding: 12px 20px;
                border-right: 1px solid var(--outline-variant);
            }
            .stat:last-child { border-right: none; }
            .stat-label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .stat-value { font: var(--type-headline-md); font-size: 18px; }
            .stat-value .unit { font-size: 12px; font-weight: 400; color: var(--on-surface-variant); margin-left: 2px; }
            .controls {
                display: flex;
                align-items: center;
                gap: 12px;
                padding: 12px 20px;
                flex-shrink: 0;
            }
            .progress-bar {
                flex: 1;
                position: relative;
                height: 4px;
                overflow: visible;
            }
            .progress-track {
                height: 100%;
                background: var(--outline-variant);
                border-radius: var(--radius-full);
                overflow: hidden;
            }
            .progress-fill {
                height: 100%;
                background: var(--primary);
                border-radius: var(--radius-full);
            }
            .event-tick {
                position: absolute;
                top: -4px;
                width: 2px;
                height: 12px;
                background: var(--tick-color);
                border-radius: 1px;
                transform: translateX(-50%);
                cursor: default;
            }
            .event-tick-tooltip {
                display: none;
                position: absolute;
                bottom: 16px;
                left: 50%;
                transform: translateX(-50%);
                background: var(--surface-container-lowest);
                border: 1px solid var(--tick-color);
                border-radius: var(--radius-sm);
                padding: 3px 7px;
                font: var(--type-label-caps);
                font-size: 9px;
                color: var(--tick-color);
                white-space: nowrap;
                pointer-events: none;
            }
            .event-tick:hover .event-tick-tooltip { display: block; }
            .time-display {
                font: var(--type-label-caps);
                color: var(--on-surface-variant);
                white-space: nowrap;
            }
            .ctrl-btn {
                background: var(--surface-container-high);
                border: none;
                border-radius: var(--radius-sm);
                width: 32px;
                height: 32px;
                cursor: pointer;
                display: flex;
                align-items: center;
                justify-content: center;
                color: var(--on-surface);
                font-size: 14px;
                flex-shrink: 0;
            }
            .ctrl-btn.primary { background: var(--primary); color: var(--on-primary); }
            .ctrl-btn:hover { opacity: 0.8; }
            .speed-select {
                background: var(--surface-container-high);
                border: none;
                color: var(--on-surface-variant);
                font: var(--type-label-caps);
                padding: 4px 8px;
                border-radius: var(--radius-sm);
                cursor: pointer;
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
    private positionMarker?: L.CircleMarker;
    private drawnPolyline?: L.Polyline;
    private animationInterval?: number;
    private mapInitTimer?: number;

    private readonly onKeydown = (e: KeyboardEvent) => { if (e.key === 'Escape') this.dispatchClose(); };

    override connectedCallback() {
        super.connectedCallback();
        document.addEventListener('keydown', this.onKeydown);
        void this.fetchRoute();
    }

    override disconnectedCallback() {
        super.disconnectedCallback();
        document.removeEventListener('keydown', this.onKeydown);
        if (this.mapInitTimer !== undefined) { clearTimeout(this.mapInitTimer); this.mapInitTimer = undefined; }
        this.stopAnim();
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
            const resp = await TelemetryService.getInstance().tripRoute(this.tokenId, this.trip.startTime, this.trip.endTime!);

            if (resp.permissionsRequired) {
                this.fetchError = 'Grant DIMO permissions on this vehicle to see trip replay.';
                this.loading = false;
                return;
            }

            const raw = [...resp.waypoints].sort((a, b) => a.timestamp.localeCompare(b.timestamp));
            this.waypoints = downsample(raw);

            if (this.waypoints.length < 2) {
                this.isSparse = true;
                this.loading = false;
                await this.updateComplete;
                this.initFallbackMap();
                return;
            }

            const startMs = new Date(this.trip.startTime).getTime();
            const endMs = new Date(this.trip.endTime!).getTime();
            const range = endMs - startMs;
            this.eventFlags = range <= 0 ? [] : resp.events
                .filter((e) => e.name in EVENT_COLORS)
                .map((e) => ({
                    name: e.name,
                    pct: Math.min(100, Math.max(0, (new Date(e.timestamp).getTime() - startMs) / range * 100)),
                }));

            this.loading = false;
            await this.updateComplete;
            this.initMap();
        } catch (e) {
            this.fetchError = e instanceof Error ? e.message : 'Failed to load GPS data';
            this.loading = false;
        }
    }

    private initFallbackMap() {
        const el = this.shadowRoot?.getElementById('map');
        if (!el || !this.trip.startLocation || !this.trip.endLocation) return;
        const { lat: sLat, lon: sLng } = this.trip.startLocation;
        const { lat: eLat, lon: eLng } = this.trip.endLocation;

        this.map = L.map(el as HTMLElement);
        L.tileLayer(TILE_URL, { attribution: TILE_ATTRIBUTION, subdomains: 'abcd', maxZoom: 19 }).addTo(this.map);

        L.circleMarker([sLat, sLng], { color: '#39FF14', fillColor: '#39FF14', fillOpacity: 0.9, radius: 8 })
            .bindPopup('Start').addTo(this.map);
        L.circleMarker([eLat, eLng], { color: '#FF0055', fillColor: '#FF0055', fillOpacity: 0.9, radius: 8 })
            .bindPopup('End').addTo(this.map);
        L.polyline([[sLat, sLng], [eLat, eLng]], { color: '#3388ff', dashArray: '6,6', opacity: 0.5, weight: 2 }).addTo(this.map);

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
        L.tileLayer(TILE_URL, { attribution: TILE_ATTRIBUTION, subdomains: 'abcd', maxZoom: 19 }).addTo(this.map);

        L.polyline(bounds, { color: '#3388ff', opacity: 0.2, weight: 2, dashArray: '4,3' }).addTo(this.map);

        L.circleMarker(bounds[0], { color: '#39FF14', fillColor: '#39FF14', fillOpacity: 0.9, radius: 8 })
            .bindPopup('Start').addTo(this.map);
        L.circleMarker(bounds[bounds.length - 1], { color: '#FF0055', fillColor: '#FF0055', fillOpacity: 0.9, radius: 8 })
            .bindPopup('End').addTo(this.map);

        this.drawnPolyline = L.polyline([], { color: '#3388ff', weight: 2.5, opacity: 0.9 }).addTo(this.map);
        this.positionMarker = L.circleMarker(bounds[0], { color: '#3388ff', fillColor: '#3388ff', fillOpacity: 1, radius: 7 }).addTo(this.map);

        try { this.map.fitBounds(bounds as L.LatLngBoundsLiteral, { padding: [40, 40] }); } catch { /* ignore */ }
        this.mapInitTimer = window.setTimeout(() => {
            this.mapInitTimer = undefined;
            this.map?.invalidateSize();
            this.startAnim();
        }, 150);
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
    private get currentTs(): string {
        return this.waypoints.length ? dayjs(this.waypoints[this.currentStep].timestamp).format('HH:mm') : '';
    }
    private get endTs(): string {
        return this.trip.endTime ? dayjs(this.trip.endTime).format('HH:mm') : '';
    }

    override render() {
        const distFmt = formatDistance(this.trip.distanceKm ?? undefined, 1);
        const avgFmt = formatSpeed(this.trip.avgSpeedKph ?? undefined);
        const maxFmt = formatSpeed(this.trip.maxSpeedKph ?? undefined);
        const hasControls = !this.isSparse && !this.loading && !this.fetchError;

        return html`
            <div class="card" @click=${(e: Event) => e.stopPropagation()}>
                <div class="replay-header">
                    <div>
                        <span class="replay-title">Trip Replay</span>
                        <span class="replay-subtitle">
                            ${dayjs(this.trip.startTime).format('MMM D · HH:mm')} → ${this.endTs} · ${fmtDuration(this.trip.duration)}
                        </span>
                    </div>
                    <button class="close" @click=${this.dispatchClose}>
                        <span class="material-symbols-outlined">close</span>
                    </button>
                </div>

                <div class="map-wrapper">
                    ${this.loading
                        ? html`<div class="map-state">Loading GPS data…</div>`
                        : this.fetchError
                            ? html`<div class="map-state error">${this.fetchError}</div>`
                            : html`<div id="map"></div>`}
                    ${this.isSparse ? html`<div class="sparse-msg">GPS data sparse — showing start and end only</div>` : nothing}
                </div>

                <div class="stats-bar">
                    <div class="stat">
                        <div class="stat-label">Distance</div>
                        <div class="stat-value">${distFmt.value}<span class="unit">${distFmt.unit}</span></div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">Duration</div>
                        <div class="stat-value">${fmtDuration(this.trip.duration)}</div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">Avg speed</div>
                        <div class="stat-value">${avgFmt.value}<span class="unit">${avgFmt.unit}</span></div>
                    </div>
                    <div class="stat">
                        <div class="stat-label">Max speed</div>
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
                                <div class="event-tick" style="left:${flag.pct}%;--tick-color:${EVENT_COLORS[flag.name] ?? '#64748B'}">
                                    <div class="event-tick-tooltip">
                                        ${flag.name.replace(/^[^.]+\./, '').replace(/([A-Z])/g, ' $1').trim().toUpperCase()}
                                    </div>
                                </div>
                            `)}
                        </div>
                        <span class="time-display">${this.currentTs} / ${this.endTs}</span>
                        <button class="ctrl-btn primary" @click=${this.togglePlay} title=${this.isPlaying ? 'Pause' : 'Play'}>
                            <span class="material-symbols-outlined">${this.isPlaying ? 'pause' : 'play_arrow'}</span>
                        </button>
                        <button class="ctrl-btn" @click=${this.doReset} title="Reset">
                            <span class="material-symbols-outlined">replay</span>
                        </button>
                        <select class="speed-select" @change=${this.onSpeedChange}>
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
