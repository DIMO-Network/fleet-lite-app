import { LitElement, html, css, PropertyValues } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import * as L from 'leaflet';
import { VehicleLocation } from '../types/vehicle.ts';

/**
 * Leaflet map of the fleet's vehicle locations, modeled on rental-fleets-app's
 * <fleet-map>. Dark Carto basemap to match the obsidian design; one circle
 * marker per vehicle, colored by how recent its last GPS fix is. Popups link
 * into the vehicle-details view.
 *
 * Leaflet quirks in shadow DOM: its CSS is loaded via a <link> in render() (so
 * it scopes to this shadow root), the #map container is given an explicit size,
 * and invalidateSize() is called after layout settles. We use circleMarker (not
 * L.marker) to avoid Leaflet's default marker-image asset path, which breaks
 * under bundlers/shadow DOM.
 */
@customElement('fleet-map')
export class FleetMap extends LitElement {
    @property({ type: Array }) vehicles: VehicleLocation[] = [];
    @property({ type: String }) tenantId = '';

    private map?: L.Map;
    private markers = new Map<number, L.CircleMarker>();

    // Continental-US fallback when no vehicle reports a location.
    private static readonly DEFAULT_CENTER: L.LatLngExpression = [39.5, -98.35];
    private static readonly DEFAULT_ZOOM = 4;

    static styles = css`
        :host { display: block; width: 100%; height: 100%; }
        #map {
            width: 100%;
            height: 100%;
            background: #e8e8e8;
        }
        /* Dark, unobtrusive Leaflet controls + popups to match the theme. */
        .leaflet-control-zoom a {
            background: var(--surface-container-high, #2a2a2a);
            color: var(--on-surface, #e5e2e1);
            border-color: var(--outline-variant, #444748);
        }
        .leaflet-control-zoom a:hover { background: var(--surface-container-highest, #353534); }
        .leaflet-popup-content-wrapper, .leaflet-popup-tip {
            background: var(--surface-container-high, #2a2a2a);
            color: var(--on-surface, #e5e2e1);
        }
        .leaflet-popup-content { margin: 12px 14px; font-family: var(--font-body, sans-serif); }
        .leaflet-container { font-family: var(--font-body, sans-serif); }
        .pin-popup .title { font-weight: 600; color: var(--primary, #fff); }
        .pin-popup .seen { font-size: 12px; color: var(--on-surface-variant, #c4c7c8); margin-top: 2px; }
        .pin-popup a {
            display: inline-block;
            margin-top: 8px;
            color: var(--secondary, #ffb691);
            text-decoration: none;
            font-size: 13px;
        }
        .pin-popup a:hover { text-decoration: underline; }
    `;

    render() {
        return html`
            <link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css" />
            <div id="map"></div>
        `;
    }

    firstUpdated() {
        const el = this.shadowRoot?.getElementById('map');
        if (!el) return;

        this.map = L.map(el, { zoomControl: false, attributionControl: true })
            .setView(FleetMap.DEFAULT_CENTER, FleetMap.DEFAULT_ZOOM);
        L.control.zoom({ position: 'bottomleft' }).addTo(this.map);

        // Standard OpenStreetMap basemap (default Leaflet look, key-less).
        L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
            maxZoom: 19,
            attribution: '&copy; OpenStreetMap contributors',
        }).addTo(this.map);

        this.syncMarkers();

        // Leaflet measures the container on init; in shadow DOM the size isn't
        // final yet, so nudge it after layout.
        setTimeout(() => this.map?.invalidateSize(), 100);
        setTimeout(() => this.map?.invalidateSize(), 500);
    }

    updated(changed: PropertyValues) {
        if (changed.has('vehicles') && this.map) {
            this.syncMarkers();
        }
    }

    disconnectedCallback() {
        this.map?.remove();
        this.map = undefined;
        this.markers.clear();
        super.disconnectedCallback();
    }

    /** Recenter/zoom to fit all current markers (used by the overview's locate button). */
    public recenter() {
        if (!this.map) return;
        if (this.markers.size === 0) {
            this.map.setView(FleetMap.DEFAULT_CENTER, FleetMap.DEFAULT_ZOOM);
            return;
        }
        const pts: L.LatLngExpression[] = [];
        this.markers.forEach(m => pts.push(m.getLatLng()));
        this.map.fitBounds(L.latLngBounds(pts), { padding: [60, 60], maxZoom: 15 });
    }

    private syncMarkers() {
        if (!this.map) return;
        this.markers.forEach(m => m.remove());
        this.markers.clear();

        for (const v of this.vehicles) {
            if (!Number.isFinite(v.latitude) || !Number.isFinite(v.longitude)) continue;
            const fresh = isFresh(v.timestamp);
            const color = fresh ? '#ea6b18' : '#8e9192';
            const marker = L.circleMarker([v.latitude, v.longitude], {
                color: '#131313',
                weight: 2,
                fillColor: color,
                fillOpacity: fresh ? 0.95 : 0.6,
                radius: 8,
            }).addTo(this.map);
            marker.bindPopup(this.popupHtml(v));
            this.markers.set(v.tokenId, marker);
        }

        this.recenter();
    }

    private popupHtml(v: VehicleLocation): string {
        const href = `#/${this.tenantId}/vehicles/${v.tokenId}`;
        return `
            <div class="pin-popup">
                <div class="title">${escapeHtml(v.title)}</div>
                <div class="seen">Updated ${escapeHtml(relativeTime(v.timestamp))}</div>
                <a href="${href}">View details &rarr;</a>
            </div>`;
    }
}

/** A fix newer than 24h is "fresh". */
function isFresh(ts: string): boolean {
    const t = Date.parse(ts);
    if (Number.isNaN(t)) return false;
    return Date.now() - t < 24 * 60 * 60 * 1000;
}

function relativeTime(ts: string): string {
    const t = Date.parse(ts);
    if (Number.isNaN(t)) return 'unknown';
    const mins = Math.round((Date.now() - t) / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.round(mins / 60);
    if (hrs < 24) return `${hrs}h ago`;
    const days = Math.round(hrs / 24);
    return `${days}d ago`;
}

function escapeHtml(s: string): string {
    return s.replace(/[&<>"']/g, c =>
        ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string));
}

declare global {
    interface HTMLElementTagNameMap {
        'fleet-map': FleetMap;
    }
}
