import { LitElement, html, css } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { ApiService } from '../services/api-service.ts';
import { Vehicle, VehiclesResponse } from '../types/vehicle.ts';

interface VehicleCard {
    tokenId: string;
    title: string;
    location: string;
    seenAt: string;
    online: boolean;
    notification?: number;
    errorMessage?: string;
}

@customElement('fleet-overview-view')
export class FleetOverviewView extends LitElement {
    @state() private vehicles: VehicleCard[] = [];
    @state() private loading = true;
    @state() private errorMessage: string | null = null;

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
            title: this.formatTitle(v),
            location: integration,
            seenAt: `Token #${v.tokenId}`,
            online: integrated,
            errorMessage: integrated ? undefined : 'No DIMO integration — pair a device to stream telemetry',
        };
    }

    async connectedCallback() {
        super.connectedCallback();
        try {
            const res = await ApiService.getInstance().get<VehiclesResponse>('/vehicles');
            this.vehicles = (res.vehicles || []).map((v) => this.toCard(v));
            this.loading = false;
        } catch (e) {
            // ApiService already redirected to /login.html on 401/400.
            this.loading = false;
            console.error('Failed to load vehicles', e);
            this.errorMessage = e instanceof Error ? e.message : 'Failed to load vehicles';
        }
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                position: relative;
                width: 100%;
                height: 100%;
                overflow: hidden;
            }

            header.top-bar {
                position: absolute;
                top: 0;
                left: 0;
                right: 0;
                height: 80px;
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--gutter);
                border-bottom: 1px solid var(--outline-variant);
                z-index: 40;
                background: rgba(28, 27, 27, 0.85);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
            }
            @media (max-width: 768px) {
                header.top-bar { display: none; }
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
            header.top-bar .live-tracking {
                background: var(--primary);
                color: var(--on-primary);
                padding: 8px 16px;
                border-radius: var(--radius-md);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
            }
            header.top-bar .icon-btn {
                color: var(--on-surface-variant);
                background: none;
                border: none;
                padding: 4px;
            }

            .map {
                position: absolute;
                inset: 0;
                z-index: 0;
                background-image: url('https://lh3.googleusercontent.com/aida-public/AB6AXuCafAyaZaFkGbs0F-VRUMpBhT4Qnwu1r8nQf4Aj8I_HOoT7H7MyPxONAqC8FiCiJdvky_xYReTYz08ZPsqIZg2dTljMrTa1UYio31UMUCcTwS4zmJ2zxWihc0W12bN-u0qvD216PoKq0AuQUMwpWDSJ_w2H-5Yq7HDaT1LO_C9FazKL-fnjDD_IhaqtcXkQsQbFMx0FRGgzZBNV2qbNQddzi5Zcus630eiiSj1CamQZiZnOl0msenE-fKKe4YbBsKcpuoRwRqvHwkc');
                background-size: cover;
                background-position: center;
            }
            .map::after {
                content: '';
                position: absolute;
                inset: 0;
                background: linear-gradient(180deg, rgba(19, 19, 19, 0.4) 0%, rgba(19, 19, 19, 0.8) 100%);
            }

            .map-controls {
                position: absolute;
                top: 96px;
                right: 24px;
                display: flex;
                flex-direction: column;
                gap: 12px;
                z-index: 10;
            }
            .map-controls button {
                width: 48px;
                height: 48px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full);
                color: var(--on-surface);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease;
            }
            .map-controls button:hover { background: var(--surface-container-high); }

            .vehicles-panel {
                position: absolute;
                bottom: 0;
                left: 0;
                right: 0;
                z-index: 20;
                background: rgba(28, 27, 27, 0.85);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                border: 1px solid var(--outline-variant);
                border-radius: 24px 24px 0 0;
                display: flex;
                flex-direction: column;
                overflow: hidden;
                box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
            }
            @media (min-width: 768px) {
                .vehicles-panel {
                    top: 96px;
                    bottom: 24px;
                    right: 24px;
                    left: auto;
                    width: 384px;
                    border-radius: 24px;
                }
            }

            .drag-handle {
                width: 100%;
                display: flex;
                justify-content: center;
                padding: 12px 0;
            }
            .drag-handle div {
                width: 48px;
                height: 4px;
                background: var(--outline-variant);
                border-radius: var(--radius-full);
            }
            @media (min-width: 768px) {
                .drag-handle { display: none; }
            }

            .panel-header {
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px 24px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .panel-header h3 { font: var(--type-headline-md); color: var(--primary); }
            .panel-header button {
                color: var(--primary);
                background: none;
                border: none;
                padding: 8px;
                border-radius: var(--radius-full);
                transition: background 0.15s ease;
            }
            .panel-header button:hover { background: var(--surface-container-high); }

            .vehicle-list {
                flex: 1;
                overflow-y: auto;
                padding: 16px;
                display: flex;
                flex-direction: column;
                gap: 16px;
            }
            .empty-state {
                color: var(--on-surface-variant);
                font: var(--type-body-sm);
                padding: 24px;
                text-align: center;
            }
            .empty-state.error { color: var(--error); }

            .vehicle-list::-webkit-scrollbar { width: 6px; }
            .vehicle-list::-webkit-scrollbar-thumb {
                background-color: var(--outline-variant);
                border-radius: 10px;
            }

            .vehicle-card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 16px;
                cursor: pointer;
                transition: border-color 0.15s ease;
                text-decoration: none;
                color: inherit;
                display: block;
            }
            .vehicle-card:hover { border-color: rgba(255, 255, 255, 0.5); }
            .vehicle-card.offline { border-color: rgba(255, 180, 171, 0.2); }
            .vehicle-card.offline:hover { border-color: rgba(255, 180, 171, 0.5); }

            .vehicle-row { display: flex; align-items: flex-start; gap: 16px; }
            .vehicle-icon {
                width: 64px;
                height: 64px;
                border-radius: var(--radius-full);
                background: var(--surface-container-highest);
                border: 1px solid var(--outline-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                flex-shrink: 0;
            }
            .vehicle-icon .material-symbols-outlined { color: var(--primary); font-size: 32px; }
            .vehicle-card.offline .vehicle-icon { opacity: 0.5; }
            .vehicle-card.offline .vehicle-icon .material-symbols-outlined { color: var(--on-surface-variant); }

            .vehicle-meta { flex: 1; min-width: 0; }
            .vehicle-meta h4 {
                font: var(--type-body-lg);
                font-weight: 600;
                color: var(--primary);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            .vehicle-meta .location {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 4px;
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            .vehicle-meta .seen {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-top: 8px;
            }
            .vehicle-meta .row-flex {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-top: 8px;
            }
            .notif-badge {
                background: var(--surface-container-highest);
                color: var(--on-surface);
                padding: 2px 8px;
                border-radius: var(--radius-sm);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
            }
            .vehicle-meta .error-msg {
                margin-top: 4px;
                display: flex;
                align-items: center;
                gap: 4px;
                color: var(--error);
                font: var(--type-body-sm);
            }
            .vehicle-meta .error-msg .material-symbols-outlined { font-size: 16px; }
        `,
    ];

    private renderCard(v: VehicleCard) {
        const cls = v.online ? 'vehicle-card' : 'vehicle-card offline';
        return html`
            <a class=${cls} href="#/vehicles/${v.tokenId}">
                <div class="vehicle-row">
                    <div class="vehicle-icon">
                        <span class="material-symbols-outlined">directions_car</span>
                    </div>
                    <div class="vehicle-meta">
                        <h4>${v.title}</h4>
                        ${v.online ? html`
                            <p class="location">${v.location}</p>
                            ${v.notification
                                ? html`<div class="row-flex">
                                        <p class="seen">${v.seenAt}</p>
                                        <span class="notif-badge">${v.notification}</span>
                                    </div>`
                                : html`<p class="seen">${v.seenAt}</p>`
                            }
                        ` : html`
                            <div class="error-msg">
                                <span class="material-symbols-outlined">warning</span>
                                <span>${v.errorMessage}</span>
                            </div>
                        `}
                    </div>
                </div>
            </a>
        `;
    }

    render() {
        return html`
            <header class="top-bar">
                <div class="left">
                    <h2>Fleet Overview</h2>
                    <nav>
                        <a href="#/" class="active">Map View</a>
                        <a href="#/stats">List View</a>
                    </nav>
                </div>
                <div class="right">
                    <button class="live-tracking">Live Tracking</button>
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <button class="icon-btn"><span class="material-symbols-outlined">account_circle</span></button>
                </div>
            </header>

            <div class="map"></div>

            <div class="map-controls">
                <button><span class="material-symbols-outlined">my_location</span></button>
                <button><span class="material-symbols-outlined">layers</span></button>
            </div>

            <div class="vehicles-panel">
                <div class="drag-handle"><div></div></div>
                <div class="panel-header">
                    <h3>Your cars</h3>
                    <button><span class="material-symbols-outlined">add</span></button>
                </div>
                <div class="vehicle-list custom-scrollbar">
                    ${this.loading
                        ? html`<p class="empty-state">Loading vehicles…</p>`
                        : this.errorMessage
                            ? html`<p class="empty-state error">${this.errorMessage}</p>`
                            : this.vehicles.length === 0
                                ? html`<p class="empty-state">No vehicles found on this account.</p>`
                                : this.vehicles.map(v => this.renderCard(v))
                    }
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'fleet-overview-view': FleetOverviewView;
    }
}
