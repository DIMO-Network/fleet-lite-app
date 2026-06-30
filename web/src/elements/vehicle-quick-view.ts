import { LitElement, html, css, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { msg } from '@lit/localize';
import { sharedStyles } from '../global-styles.ts';
import { TelemetryService } from '../services/telemetry-service.ts';
import { PrefsService } from '../services/prefs-service.ts';
import { formatSpeed, formatDistance, formatTemperature, formatVoltage, formatPercent, FormattedValue } from '../utils/units.ts';
import { tripSignal, tripDistanceKm, tripTimeShort } from '../utils/trips.ts';
import { VehicleCard } from '../types/vehicle.ts';
import { SignalLatest, Trip } from '../types/telemetry.ts';

/**
 * Floating quick-view panel for one vehicle, docked over the map (bottom
 * sheet on mobile). Opened by clicking a marker or a list card on the fleet
 * overview; shows identity + live signals from /telemetry/:id/latest and
 * links out to the full details page. Closes on Esc, ✕, or when `vehicle`
 * is set to null. Emits a `close` CustomEvent — the parent owns the state.
 */
@customElement('vehicle-quick-view')
export class VehicleQuickView extends LitElement {
    @property({ type: String }) tenantId = '';
    @property({ attribute: false }) vehicle: VehicleCard | null = null;

    @state() private signals: Record<string, SignalLatest> = {};
    @state() private loading = false;
    @state() private permissionsRequired = false;

    @state() private trips: Trip[] = [];
    @state() private tripsLoading = false;
    @state() private selectedTrip: Trip | null = null;
    @state() private routeLoading = false;

    /** Real-time: when on, re-poll this vehicle's signals + location on an
     * interval. Off by default, reset whenever the selected vehicle changes. */
    @state() private realtimeOn = false;
    private rtTimer: number | null = null;
    private static readonly REALTIME_INTERVAL_MS = 20 * 1000;

    /** How far back the trips list looks. */
    private static readonly TRIPS_WINDOW_DAYS = 14;

    private unsubscribePrefs: (() => void) | null = null;
    private boundOnKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape' && this.vehicle) this.close();
    };

    connectedCallback() {
        super.connectedCallback();
        window.addEventListener('keydown', this.boundOnKeyDown);
        // Units toggle should re-render the signal grid live.
        this.unsubscribePrefs = PrefsService.getInstance().subscribe(() => this.requestUpdate());
    }

    disconnectedCallback() {
        window.removeEventListener('keydown', this.boundOnKeyDown);
        this.unsubscribePrefs?.();
        this.stopRealtime();
        super.disconnectedCallback();
    }

    willUpdate(changed: Map<string, unknown>) {
        if (changed.has('vehicle')) {
            // Switching (or closing) the vehicle resets real-time and any
            // selected trip route — the toggle is opt-in per vehicle.
            this.stopRealtime();
            if (this.vehicle) {
                this.clearTripSelection();
                void this.loadSignals();
                void this.loadTrips();
            }
        }
    }

    private close() {
        this.clearTripSelection();
        this.dispatchEvent(new CustomEvent('close', { bubbles: true, composed: true }));
    }

    // quiet = a real-time refresh: update the grid in place without flashing the
    // loading state or clearing the existing values first.
    private async loadSignals(quiet = false) {
        const tokenId = this.vehicle?.tokenId;
        if (!tokenId) return;
        if (!quiet) {
            this.loading = true;
            this.signals = {};
        }
        this.permissionsRequired = false;
        try {
            const res = await TelemetryService.getInstance().latest(Number(tokenId));
            // The user may have clicked another vehicle while this was in flight.
            if (this.vehicle?.tokenId !== tokenId) return;
            this.signals = res.signals || {};
            this.permissionsRequired = !!res.permissionsRequired;
        } catch {
            // Leave the grid as-is — identity info is still useful.
        } finally {
            if (!quiet && this.vehicle?.tokenId === tokenId) this.loading = false;
        }
    }

    /** Toggle real-time updates for this vehicle (off by default). */
    private toggleRealtime() {
        if (this.realtimeOn) {
            this.stopRealtime();
            return;
        }
        this.realtimeOn = true;
        this.realtimeTick(); // refresh immediately, then on the interval
        this.rtTimer = window.setInterval(() => this.realtimeTick(), VehicleQuickView.REALTIME_INTERVAL_MS);
    }

    private stopRealtime() {
        if (this.rtTimer !== null) {
            clearInterval(this.rtTimer);
            this.rtTimer = null;
        }
        this.realtimeOn = false;
    }

    // One real-time tick: quietly reload this vehicle's signal grid, and ask the
    // parent (which owns the map) to re-pull + reposition its marker.
    private realtimeTick() {
        const tokenId = this.vehicle?.tokenId;
        if (!tokenId) return;
        void this.loadSignals(true);
        this.dispatchEvent(new CustomEvent('rt-tick', {
            detail: { tokenId }, bubbles: true, composed: true,
        }));
    }

    private async loadTrips() {
        const tokenId = this.vehicle?.tokenId;
        if (!tokenId) return;
        this.tripsLoading = true;
        this.trips = [];
        try {
            const to = new Date();
            const from = new Date(to.getTime() - VehicleQuickView.TRIPS_WINDOW_DAYS * 24 * 60 * 60 * 1000);
            const res = await TelemetryService.getInstance().segments(Number(tokenId), from.toISOString(), to.toISOString());
            if (this.vehicle?.tokenId !== tokenId) return; // superseded
            this.trips = res.segments || [];
        } catch {
            // Trips section degrades to its empty state; signals stay useful.
        } finally {
            if (this.vehicle?.tokenId === tokenId) this.tripsLoading = false;
        }
    }

    /**
     * Select a trip and fetch its route for the parent map to draw; clicking
     * the selected trip again deselects and clears the route. The parent owns
     * the map — this element only emits `trip-route` events.
     */
    private async selectTrip(trip: Trip) {
        if (this.selectedTrip === trip) {
            this.clearTripSelection();
            return;
        }
        const tokenId = this.vehicle?.tokenId;
        if (!tokenId) return;
        this.selectedTrip = trip;
        this.routeLoading = true;
        try {
            const res = await TelemetryService.getInstance().tripRoute(
                Number(tokenId), trip.start.timestamp, trip.end.timestamp);
            // The user may have switched trips/vehicles while this was in flight.
            if (this.selectedTrip !== trip || this.vehicle?.tokenId !== tokenId) return;
            const points = (res.points || []).map((p) => [p.lat, p.lon] as [number, number]);
            // Fall back to the trip's own endpoints when sampling found nothing,
            // so the map always shows at least the start->end line.
            if (points.length === 0) {
                points.push(
                    [trip.start.value.latitude, trip.start.value.longitude],
                    [trip.end.value.latitude, trip.end.value.longitude],
                );
            }
            this.dispatchEvent(new CustomEvent('trip-route', {
                detail: { points, trip },
                bubbles: true,
                composed: true,
            }));
        } catch {
            this.clearTripSelection();
        } finally {
            if (this.selectedTrip === trip) this.routeLoading = false;
        }
    }

    private clearTripSelection() {
        if (!this.selectedTrip) return;
        this.selectedTrip = null;
        this.routeLoading = false;
        this.dispatchEvent(new CustomEvent('trip-route', {
            detail: { points: null, trip: null },
            bubbles: true,
            composed: true,
        }));
    }

    private signalValue(name: string): number | undefined {
        const v = this.signals[name]?.value;
        if (typeof v === 'number') return v;
        if (typeof v === 'string') {
            const n = Number(v);
            return Number.isFinite(n) ? n : undefined;
        }
        return undefined;
    }

    /** Signal rows that have data, formatted per the user's unit preference. */
    private signalRows(): Array<{ icon: string; label: string; fv: FormattedValue }> {
        const rows: Array<{ icon: string; label: string; fv: FormattedValue }> = [];
        const push = (icon: string, label: string, raw: number | undefined, fmt: (n: number | undefined) => FormattedValue) => {
            if (raw == null) return;
            rows.push({ icon, label, fv: fmt(raw) });
        };
        push('speed',            msg('Speed'),       this.signalValue('speed'), (n) => formatSpeed(n));
        push('local_gas_station', msg('Fuel'),       this.signalValue('powertrainFuelSystemRelativeLevel'), (n) => formatPercent(n));
        push('battery_charging_full', msg('Charge'), this.signalValue('powertrainTractionBatteryStateOfChargeCurrent'), (n) => formatPercent(n));
        push('swap_driving_apps_wheel', msg('Odometer'), this.signalValue('powertrainTransmissionTravelledDistance'), (n) => formatDistance(n));
        push('bolt',             msg('Battery'),     this.signalValue('lowVoltageBatteryCurrentVoltage'), (n) => formatVoltage(n));
        push('thermostat',       msg('Coolant'),     this.signalValue('powertrainCombustionEngineECT'), (n) => formatTemperature(n));
        return rows;
    }

    static styles = [
        sharedStyles,
        css`
            :host {
                position: absolute;
                top: 96px;
                left: 24px;
                z-index: 600; /* above leaflet panes (max ~400) and map controls */
                display: block;
                pointer-events: none; /* panel re-enables; host box shouldn't eat map clicks */
            }
            .panel {
                pointer-events: auto;
                width: 340px;
                max-height: calc(100vh - 200px);
                overflow-y: auto;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                box-shadow: 0 12px 40px rgba(0, 0, 0, 0.45);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                display: flex;
                flex-direction: column;
            }
            @media (max-width: 768px) {
                :host {
                    top: auto;
                    left: 0;
                    right: 0;
                    bottom: 0;
                }
                .panel {
                    width: 100%;
                    max-height: 60vh;
                    border-radius: var(--radius-lg) var(--radius-lg) 0 0;
                    border-bottom: none;
                }
            }

            header {
                display: flex;
                align-items: flex-start;
                justify-content: space-between;
                gap: 12px;
                padding: 16px 16px 12px;
            }
            header .identity { min-width: 0; }
            header h3 {
                display: flex;
                align-items: center;
                gap: 8px;
                font: var(--type-headline-sm, var(--type-body-lg));
                font-weight: 600;
                color: var(--primary);
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            header h3 .favorite-star { color: #f5c84b; font-size: 18px; }
            header h3 .status-dot {
                width: 10px;
                height: 10px;
                border-radius: var(--radius-full);
                flex-shrink: 0;
            }
            .status-green { background: #69dbad; }
            .status-amber { background: #f5c84b; }
            .status-red   { background: var(--error, #e57373); }
            header .sub {
                margin-top: 4px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .close-btn {
                background: none;
                border: none;
                color: var(--on-surface-variant);
                padding: 6px;
                border-radius: var(--radius-full);
                cursor: pointer;
                display: inline-flex;
                flex-shrink: 0;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close-btn:hover { background: var(--surface-container-high); color: var(--primary); }

            .header-actions {
                display: flex;
                align-items: center;
                gap: 6px;
                flex-shrink: 0;
            }
            .rt-btn {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                padding: 5px 10px;
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-full);
                background: none;
                color: var(--on-surface-variant);
                font-size: 12px;
                font-weight: 500;
                cursor: pointer;
                white-space: nowrap;
                transition: background 0.15s ease, color 0.15s ease, border-color 0.15s ease;
            }
            .rt-btn:hover { background: var(--surface-container-high); color: var(--primary); }
            .rt-btn .material-symbols-outlined { font-size: 16px; }
            .rt-btn.active {
                color: var(--primary);
                border-color: var(--primary);
                background: color-mix(in srgb, var(--primary) 10%, transparent);
            }
            .rt-dot {
                width: 8px;
                height: 8px;
                border-radius: 50%;
                background: var(--primary);
                box-shadow: 0 0 0 0 color-mix(in srgb, var(--primary) 60%, transparent);
                animation: rt-pulse 1.6s ease-out infinite;
            }
            @keyframes rt-pulse {
                0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--primary) 50%, transparent); }
                100% { box-shadow: 0 0 0 7px transparent; }
            }

            .groups {
                display: flex;
                flex-wrap: wrap;
                gap: 6px;
                padding: 0 16px 12px;
            }
            .group-chip {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                padding: 3px 10px;
                border-radius: var(--radius-full);
                border: 1px solid var(--outline-variant);
                background: var(--surface-container-high);
                font: var(--type-body-sm);
                color: var(--on-surface);
            }
            .group-chip .swatch {
                width: 8px;
                height: 8px;
                border-radius: var(--radius-full);
            }

            .signals {
                display: grid;
                grid-template-columns: 1fr 1fr;
                gap: 1px;
                background: var(--outline-variant);
                border-top: 1px solid var(--outline-variant);
            }
            .signal {
                background: var(--surface-container-low);
                padding: 12px 16px;
            }
            .signal .label {
                display: flex;
                align-items: center;
                gap: 6px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .signal .label .material-symbols-outlined { font-size: 14px; }
            .signal .value { font: var(--type-body-lg); font-weight: 600; color: var(--primary); }
            .signal .value .unit {
                font: var(--type-body-sm);
                font-weight: 400;
                color: var(--on-surface-variant);
                margin-left: 4px;
            }

            .state-row {
                padding: 16px;
                border-top: 1px solid var(--outline-variant);
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .state-row.perms { color: #f5c84b; display: flex; gap: 8px; align-items: flex-start; }
            .state-row.perms .material-symbols-outlined { font-size: 16px; margin-top: 1px; }

            .trips-head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 12px 16px 8px;
                border-top: 1px solid var(--outline-variant);
            }
            .trips-head .title {
                display: flex;
                align-items: center;
                gap: 6px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .trips-head .title .material-symbols-outlined { font-size: 15px; }
            .trips-head .window { font: var(--type-body-sm); color: var(--on-surface-variant); opacity: 0.7; }
            .trips-list { max-height: 220px; overflow-y: auto; }
            .trip-row {
                width: 100%;
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                padding: 10px 16px;
                background: none;
                border: none;
                border-top: 1px solid var(--outline-variant);
                cursor: pointer;
                text-align: left;
                transition: background 0.15s ease;
            }
            .trip-row:hover { background: var(--surface-container-high); }
            .trip-row.selected {
                background: var(--surface-container-high);
                box-shadow: inset 3px 0 0 #f5c84b;
            }
            .trip-row .when { min-width: 0; }
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
            .trip-row .route-spin { font-size: 14px; color: var(--on-surface-variant); }

            footer {
                display: flex;
                gap: 10px;
                padding: 12px 16px 16px;
                border-top: 1px solid var(--outline-variant);
            }
            footer .btn {
                flex: 1;
                display: inline-flex;
                align-items: center;
                justify-content: center;
                gap: 6px;
                padding: 10px 0;
                border-radius: var(--radius-full);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                text-decoration: none;
                cursor: pointer;
                border: 1px solid var(--outline-variant);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
            }
            footer .btn.primary {
                background: var(--primary);
                border-color: var(--primary);
                color: var(--on-primary);
            }
            footer .btn[disabled] { opacity: 0.55; cursor: default; }
            footer .btn .soon {
                font-size: 9px;
                letter-spacing: 0.04em;
                opacity: 0.8;
            }
        `,
    ];

    private renderTrips() {
        return html`
            <div class="trips-head">
                <span class="title"><span class="material-symbols-outlined">route</span>${msg('Trips')}</span>
                <span class="window">${msg('Last 2 weeks')}</span>
            </div>
            ${this.tripsLoading
                ? html`<div class="state-row" style="border-top:none;">${msg('Loading trips…')}</div>`
                : this.trips.length === 0
                    ? html`<div class="state-row" style="border-top:none;">${msg('No trips in the last 2 weeks.')}</div>`
                    : html`
                        <div class="trips-list custom-scrollbar">
                            ${this.trips.map((trip) => this.renderTripRow(trip))}
                        </div>`
            }
        `;
    }

    private renderTripRow(trip: Trip) {
        const selected = this.selectedTrip === trip;
        const dist = tripDistanceKm(trip);
        const avg = tripSignal(trip, 'speed', 'AVG');
        const max = tripSignal(trip, 'speed', 'MAX');
        const distFv = dist != null ? formatDistance(dist, 1) : null;
        const avgFv = avg != null ? formatSpeed(avg) : null;
        const maxFv = max != null ? formatSpeed(max) : null;
        return html`
            <button class="trip-row ${selected ? 'selected' : ''}" @click=${() => this.selectTrip(trip)}>
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
                        ? html`<span class="material-symbols-outlined route-spin">progress_activity</span>`
                        : html`
                            ${distFv ? html`<span class="dist">${distFv.value} ${distFv.unit}</span>` : ''}
                            ${avgFv && maxFv ? html`<br />${avgFv.value}/${maxFv.value} ${maxFv.unit}` : ''}
                        `}
                </span>
            </button>
        `;
    }

    render() {
        const v = this.vehicle;
        if (!v) return nothing;
        const statusClass = !v.online ? 'status-red' : v.noPermissions ? 'status-amber' : 'status-green';
        const rows = this.signalRows();
        return html`
            <div class="panel" role="dialog" aria-label=${v.title}>
                <header>
                    <div class="identity">
                        <h3>
                            <span class="status-dot ${statusClass}"></span>
                            ${v.isFavorite ? html`<span class="material-symbols-outlined favorite-star" title="${msg('Favorite')}">star</span>` : ''}
                            <span>${v.title}</span>
                        </h3>
                        <div class="sub">${v.location ? html`${v.location} · ` : ''}${v.seenAt}</div>
                    </div>
                    <div class="header-actions">
                        <button
                            class="rt-btn ${this.realtimeOn ? 'active' : ''}"
                            title=${this.realtimeOn ? msg('Stop real-time updates') : msg('Start real-time updates')}
                            aria-pressed=${this.realtimeOn}
                            @click=${this.toggleRealtime}>
                            ${this.realtimeOn
                                ? html`<span class="rt-dot"></span>`
                                : html`<span class="material-symbols-outlined">sync</span>`}
                            <span class="rt-label">${msg('Real-time')} · 20s</span>
                        </button>
                        <button class="close-btn" title="${msg('Close')}" @click=${this.close}>
                            <span class="material-symbols-outlined">close</span>
                        </button>
                    </div>
                </header>

                ${(v.groups?.length ?? 0) > 0 ? html`
                    <div class="groups">
                        ${v.groups!.map((g) => html`
                            <span class="group-chip">
                                <span class="swatch" style="background:${g.color}"></span>${g.name}
                            </span>
                        `)}
                    </div>
                ` : nothing}

                ${this.loading
                    ? html`<div class="state-row">${msg('Loading telemetry…')}</div>`
                    : this.permissionsRequired
                        ? html`
                            <div class="state-row perms">
                                <span class="material-symbols-outlined">lock</span>
                                <span>${msg('Grant DIMO permissions to see live telemetry on this vehicle.')}</span>
                            </div>`
                        : rows.length > 0
                            ? html`
                                <div class="signals">
                                    ${rows.map((r) => html`
                                        <div class="signal">
                                            <div class="label"><span class="material-symbols-outlined">${r.icon}</span>${r.label}</div>
                                            <div class="value">${r.fv.value}<span class="unit">${r.fv.unit}</span></div>
                                        </div>
                                    `)}
                                </div>`
                            : html`<div class="state-row">${msg('No telemetry data reported yet.')}</div>`
                }

                ${this.renderTrips()}

                <footer>
                    <a class="btn primary" href="#/${this.tenantId}/vehicles/${v.tokenId}">
                        ${msg('Full details')}
                    </a>
                </footer>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'vehicle-quick-view': VehicleQuickView;
    }
}
