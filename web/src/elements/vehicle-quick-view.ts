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
    @state() private vinCopied = false;
    private vinCopiedTimer: number | null = null;
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
            // Switching (or closing) the vehicle resets real-time, the VIN-copied
            // flash, and any selected trip route — the toggle is opt-in per vehicle.
            this.stopRealtime();
            this.vinCopied = false;
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

    /** Copy the VIN to the clipboard, flashing the icon as feedback. */
    private async copyVin(vin: string) {
        try {
            await navigator.clipboard.writeText(vin);
        } catch {
            return; // clipboard unavailable (permissions/insecure context) — no feedback
        }
        this.vinCopied = true;
        if (this.vinCopiedTimer) window.clearTimeout(this.vinCopiedTimer);
        this.vinCopiedTimer = window.setTimeout(() => { this.vinCopied = false; }, 1500);
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
            /* Floats next to the overview's map-control column (24px + 40px + 16px),
               and stops above its zoom/refresh pill (24px + 44px + 16px). The
               bounds are the overview's, not the viewport's: the page sits in an
               inset sheet, so 100vh overshoots and the panel used to cover the pill. */
            :host {
                position: absolute;
                top: calc(var(--top-bar-height) + 16px);
                bottom: 84px;
                left: 80px;
                z-index: 600; /* above leaflet panes (max ~400) and map controls */
                display: block;
                pointer-events: none; /* panel re-enables; host box shouldn't eat map clicks */
            }
            .panel {
                pointer-events: auto;
                width: 348px;
                max-height: 100%;
                overflow-y: auto;
                background: var(--glass-bg);
                backdrop-filter: blur(24px) saturate(1.4);
                -webkit-backdrop-filter: blur(24px) saturate(1.4);
                border-radius: var(--radius-xl);
                box-shadow: var(--shadow-float);
                display: flex;
                flex-direction: column;
                scrollbar-width: thin;
                scrollbar-color: var(--outline-variant) transparent;
            }
            @media (max-width: 768px) {
                :host {
                    top: auto;
                    left: 0;
                    height: auto;
                    right: 0;
                    bottom: 0;
                }
                .panel {
                    width: 100%;
                    max-height: 60vh;
                    border-radius: var(--radius-xl) var(--radius-xl) 0 0;
                }
            }

            header {
                display: flex;
                align-items: flex-start;
                justify-content: space-between;
                gap: 12px;
                padding: 18px 12px 10px 20px;
            }
            header .identity { min-width: 0; flex: 1; }
            header h3 {
                display: flex;
                align-items: center;
                gap: 8px;
                font: 600 17px/24px var(--font-headline);
                letter-spacing: -0.01em;
                color: var(--primary);
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            /* The span (not the flex h3) truncates, so a long title ellipsizes
               instead of running under its neighbors. */
            header h3 .title-text {
                min-width: 0;
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            header h3 .favorite-star {
                color: var(--favorite);
                font-size: 17px;
                font-variation-settings: 'FILL' 1;
                flex-shrink: 0;
                margin-left: -2px;
            }
            header h3 .status-dot {
                width: 9px;
                height: 9px;
                border-radius: var(--radius-full);
                flex-shrink: 0;
            }
            .status-green { background: var(--accent); box-shadow: 0 0 8px var(--accent-soft-strong); }
            .status-amber { background: var(--warning); }
            .status-red   { background: var(--error); }
            header .sub {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            /* Last seen on the left, the real-time toggle on the right — its own
               row so the pill can never sit on top of the title. */
            header .sub-row {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 8px;
                margin-top: 6px;
            }
            header .vin-row {
                display: flex;
                align-items: center;
                gap: 4px;
                margin-top: 2px;
                font: 400 12px/16px var(--font-body);
                letter-spacing: 0.02em;
            }
            header .vin-row .vin-text {
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            .copy-btn {
                color: var(--on-surface-variant);
                padding: 2px;
                border-radius: var(--radius-sm);
                display: inline-flex;
                flex-shrink: 0;
                transition: color 0.15s ease;
            }
            .copy-btn:hover { color: var(--primary); }
            .copy-btn .material-symbols-outlined { font-size: 13px; }
            .identifiers {
                display: flex;
                flex-wrap: wrap;
                align-items: center;
                gap: 6px;
                padding: 0 20px 10px;
            }
            .plate-pill {
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
            .plate-pill .material-symbols-outlined { font-size: 13px; color: var(--on-surface-variant); }
            .close-btn {
                color: var(--on-surface-variant);
                padding: 6px;
                border-radius: var(--radius-full);
                display: inline-flex;
                flex-shrink: 0;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .close-btn .material-symbols-outlined { font-size: 20px; }
            .close-btn:hover { background: var(--surface-container-high); color: var(--primary); }

            /* Real-time toggle: neutral pill; on = accent tint (it's live). */
            .rt-btn {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                padding: 4px 10px;
                border-radius: var(--radius-full);
                background: var(--surface-container-high);
                color: var(--on-surface-variant);
                font: 500 12px/16px var(--font-body);
                white-space: nowrap;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .rt-btn:hover { background: var(--surface-container-highest); color: var(--on-surface); }
            .rt-btn .material-symbols-outlined { font-size: 14px; }
            .rt-btn.active,
            .rt-btn.active:hover {
                background: var(--accent-soft-strong);
                color: var(--accent-ink);
            }
            .rt-dot {
                width: 7px;
                height: 7px;
                border-radius: 50%;
                background: var(--accent);
                animation: rt-pulse 1.6s ease-out infinite;
            }
            @keyframes rt-pulse {
                0% { box-shadow: 0 0 0 0 var(--accent-soft-strong); }
                100% { box-shadow: 0 0 0 6px transparent; }
            }

            .groups {
                display: flex;
                flex-wrap: wrap;
                gap: 6px;
                padding: 0 20px 14px;
            }
            /* Group chip: 12px/500 on a 14% tint of the group color, 6px dot. */
            .group-chip {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                padding: 3px 10px 3px 8px;
                border-radius: var(--radius-full);
                background: color-mix(in srgb, var(--gc, var(--outline)) 14%, transparent);
                font: var(--type-label);
                color: var(--on-surface);
            }
            .group-chip .swatch {
                width: 6px;
                height: 6px;
                border-radius: var(--radius-full);
            }

            /* Live signals: tonal tiles instead of a ruled grid. */
            .signals {
                display: grid;
                grid-template-columns: 1fr 1fr;
                gap: 6px;
                padding: 0 12px 4px;
            }
            .signal {
                background: color-mix(in srgb, var(--surface-container-high) 70%, transparent);
                border-radius: var(--radius-md);
                padding: 10px 12px;
            }
            .signal .label {
                display: flex;
                align-items: center;
                gap: 5px;
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin-bottom: 2px;
            }
            .signal .label .material-symbols-outlined { font-size: 14px; }
            .signal .value {
                font: 600 17px/24px var(--font-headline);
                letter-spacing: -0.01em;
                color: var(--primary);
            }
            .signal .value .unit {
                font: var(--type-label);
                color: var(--on-surface-variant);
                margin-left: 3px;
                letter-spacing: 0;
            }

            .state-row {
                padding: 8px 20px 12px;
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .state-row.perms {
                margin: 0 12px 4px;
                padding: 10px 12px;
                border-radius: var(--radius-md);
                background: color-mix(in srgb, var(--warning) 12%, transparent);
                color: var(--warning);
                display: flex;
                gap: 8px;
                align-items: flex-start;
            }
            .state-row.perms .material-symbols-outlined { font-size: 16px; margin-top: 2px; }

            .trips-head {
                display: flex;
                align-items: baseline;
                justify-content: space-between;
                padding: 16px 20px 6px;
            }
            .trips-head .title {
                font: 600 15px/22px var(--font-headline);
                color: var(--primary);
            }
            .trips-head .window { font: var(--type-label); color: var(--on-surface-variant); }
            .trips-list {
                max-height: 232px;
                overflow-y: auto;
                padding: 0 8px;
                display: flex;
                flex-direction: column;
                gap: 2px;
            }
            .trip-row {
                width: 100%;
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                padding: 8px 12px;
                border-radius: var(--radius-md);
                text-align: left;
                transition: background 0.15s ease;
            }
            .trip-row:hover { background: var(--surface-container-high); }
            .trip-row.selected,
            .trip-row.selected:hover {
                background: var(--accent-soft);
                box-shadow: inset 3px 0 0 var(--accent);
            }
            .trip-row .when { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
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
                font: var(--type-label);
                color: var(--accent-ink);
            }
            .trip-row .stats {
                flex-shrink: 0;
                text-align: right;
                font: var(--type-label);
                color: var(--on-surface-variant);
                white-space: nowrap;
            }
            .trip-row .stats .dist { font: 600 14px/20px var(--font-body); color: var(--primary); }
            .trip-row .route-spin {
                font-size: 16px;
                color: var(--accent-ink);
                animation: spin 0.8s linear infinite;
            }
            @keyframes spin { to { transform: rotate(360deg); } }

            footer {
                display: flex;
                gap: 10px;
                padding: 12px 16px 16px;
            }
            footer .btn {
                flex: 1;
                display: inline-flex;
                align-items: center;
                justify-content: center;
                gap: 6px;
                min-height: 40px;
                padding: 0 18px;
                border-radius: var(--radius-full);
                font: 500 14px/20px var(--font-body);
                text-decoration: none;
                cursor: pointer;
                background: var(--surface-container-high);
                color: var(--on-surface);
                transition: filter 0.15s ease, box-shadow 0.15s ease, background 0.15s ease;
            }
            footer .btn.primary {
                background: var(--brand-gradient);
                color: var(--on-accent);
                font-weight: 600;
            }
            footer .btn.primary:hover { filter: brightness(1.06); box-shadow: var(--accent-glow); }
            footer .btn[disabled] { opacity: 0.55; cursor: default; }
            footer .btn .soon { font-size: 11px; opacity: 0.8; }
        `,
    ];

    private renderTrips() {
        return html`
            <div class="trips-head">
                <span class="title">${msg('Trips')}</span>
                <span class="window">${msg('Last 2 weeks')}</span>
            </div>
            ${this.tripsLoading
                ? html`<div class="state-row">${msg('Loading trips…')}</div>`
                : this.trips.length === 0
                    ? html`<div class="state-row">${msg('No trips in the last 2 weeks.')}</div>`
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
                            <span class="title-text" title=${v.title}>${v.title}</span>
                        </h3>
                        <div class="sub-row">
                            <div class="sub">${v.seenAt}</div>
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
                        </div>
                        ${v.vin ? html`
                            <div class="sub vin-row">
                                <span class="vin-text">${v.vin}</span>
                                <button class="copy-btn" title="${msg('Copy VIN')}" @click=${() => this.copyVin(v.vin!)}>
                                    <span class="material-symbols-outlined">${this.vinCopied ? 'check' : 'content_copy'}</span>
                                </button>
                            </div>
                        ` : nothing}
                    </div>
                    <button class="close-btn" title="${msg('Close')}" @click=${this.close}>
                        <span class="material-symbols-outlined">close</span>
                    </button>
                </header>

                ${v.licensePlate ? html`
                    <div class="identifiers">
                        <span class="plate-pill">
                            <span class="material-symbols-outlined">directions_car</span>${v.licensePlate}
                        </span>
                    </div>
                ` : nothing}

                ${(v.groups?.length ?? 0) > 0 ? html`
                    <div class="groups">
                        ${v.groups!.map((g) => html`
                            <span class="group-chip" style="--gc:${g.color}">
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
