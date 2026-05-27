import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';

@customElement('vehicle-details-view')
export class VehicleDetailsView extends LitElement {
    @property({ type: String }) tokenId: string = '';

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: column;
                width: 100%;
                height: 100%;
                overflow-y: auto;
                background: var(--background);
            }

            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 10;
                height: 80px;
                width: 100%;
                background: rgba(19, 19, 19, 0.8);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 0 var(--margin-desktop);
                border-bottom: 1px solid var(--outline-variant);
            }
            header.top-bar .left { display: flex; align-items: center; gap: 32px; }
            header.top-bar h2 { font: var(--type-headline-md); color: var(--primary); }
            header.top-bar nav { display: flex; gap: 24px; }
            header.top-bar nav a {
                text-decoration: none;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                padding-bottom: 4px;
            }
            header.top-bar nav a.active {
                color: var(--primary);
                border-bottom: 2px solid var(--primary);
            }
            header.top-bar .right { display: flex; align-items: center; gap: 16px; }
            .live-tracking {
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 8px 16px;
                border-radius: var(--radius-full);
                border: 1px solid var(--outline-variant);
                color: var(--primary);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                background: none;
            }
            .live-tracking:hover { background: var(--surface-container-high); }
            .status-dot {
                width: 8px;
                height: 8px;
                border-radius: var(--radius-full);
                background: var(--tertiary-fixed-dim);
                position: relative;
            }
            .status-dot::after {
                content: '';
                position: absolute;
                inset: -4px;
                background: inherit;
                border-radius: var(--radius-full);
                filter: blur(4px);
                opacity: 0.5;
            }
            .icon-btn {
                color: var(--on-surface-variant);
                background: none;
                border: none;
                padding: 4px;
            }

            .canvas {
                flex: 1;
                padding: var(--margin-desktop);
                max-width: var(--container-max-width);
                margin: 0 auto;
                width: 100%;
            }

            .hero-status {
                display: flex;
                align-items: center;
                gap: 16px;
                margin-bottom: 32px;
            }
            .hero-status .chip {
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                color: var(--on-surface-variant);
                padding: 4px 12px;
                border-radius: var(--radius-full);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                display: flex;
                align-items: center;
                gap: 8px;
            }
            .hero-status .chip .material-symbols-outlined { font-size: 16px; }
            .hero-status .meta {
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                gap: 8px;
            }
            .hero-status .meta .dot {
                width: 4px;
                height: 4px;
                border-radius: var(--radius-full);
                background: var(--outline-variant);
            }

            .grid {
                display: grid;
                grid-template-columns: repeat(12, 1fr);
                gap: var(--gutter);
                margin-bottom: 48px;
            }
            @media (max-width: 768px) {
                .grid { grid-template-columns: 1fr; }
            }

            .data-card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: var(--gutter);
                transition: background 0.2s ease;
            }
            .data-card:hover { background: var(--surface-container-high); }
            .data-card h4 {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .data-card-head {
                display: flex;
                align-items: center;
                justify-content: space-between;
                margin-bottom: 24px;
            }
            .data-card-head .material-symbols-outlined { color: var(--on-surface-variant); }

            .col-12 { grid-column: span 12; }
            .col-6  { grid-column: span 6; }
            .col-4  { grid-column: span 4; }
            .col-3  { grid-column: span 3; }
            @media (max-width: 768px) {
                .col-12, .col-6, .col-4, .col-3 { grid-column: span 1; }
            }

            .trips-card {
                position: relative;
                grid-column: span 12;
                min-height: 300px;
                border-radius: var(--radius-lg);
                overflow: hidden;
                border: 1px solid var(--outline-variant);
            }
            .trips-bg {
                position: absolute;
                inset: 0;
                background-image: url('https://lh3.googleusercontent.com/aida-public/AB6AXuCUEePP8X7lhZsYuKcY97xNfG4loiGb-LmVCARxTGWO0EQyT5v5ozen4q9d9KfdN_MhsqKS0yFqeFrR4zLYzT3iSl-4nksBYrRgd-AmsnlMKA5U_gZdRNrM2SMq9628-jevQ3MFdQU9cgKBV2CYDD_lPR0fB_w2H7VYjJVZeS-LoqYp4-saTHHer7ouvx0RezGPbPVUFTY0dr4jOcrTMFxanTNy6M4NMo2-wY0-hZ3RsjhKviJ625RW8FKjzcHSbFnHxX1xCCoOT5E');
                background-size: cover;
                background-position: center;
            }
            .trips-content {
                position: absolute;
                inset: 0;
                background: linear-gradient(180deg, rgba(19, 19, 19, 0.2) 0%, rgba(19, 19, 19, 1) 100%);
                padding: var(--gutter);
                display: flex;
                flex-direction: column;
                justify-content: space-between;
            }
            .trips-top {
                display: flex;
                justify-content: space-between;
                align-items: flex-start;
            }
            .trips-top h3 { font: var(--type-headline-md); color: var(--primary); }
            .trips-top .chip {
                background: rgba(53, 53, 52, 0.8);
                backdrop-filter: blur(8px);
                color: var(--primary);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                padding: 4px 12px;
                border-radius: var(--radius-full);
                border: 1px solid var(--outline-variant);
            }
            .trips-bottom .from {
                font: var(--type-body-lg);
                font-weight: 500;
                color: var(--primary);
                margin-bottom: 4px;
            }
            .trips-bottom .to {
                font: var(--type-body-md);
                color: var(--on-surface-variant);
                display: flex;
                align-items: center;
                gap: 8px;
                margin-bottom: 16px;
            }
            .trips-bottom .to .material-symbols-outlined { font-size: 16px; }
            .trips-bottom .stats {
                display: flex;
                gap: 24px;
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }

            .section-label {
                grid-column: span 12;
                margin-top: 16px;
                font: var(--type-body-lg);
                font-weight: 500;
                color: var(--primary);
            }
            .section-headline {
                grid-column: span 12;
                margin-top: 16px;
                font: var(--type-headline-md);
                color: var(--primary);
                font-weight: 500;
            }

            .stat-row {
                display: flex;
                gap: 32px;
                flex: 1;
                height: 100%;
            }
            .stat-col {
                display: flex;
                flex-direction: column;
                justify-content: space-between;
            }
            .stat-label {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-bottom: 4px;
            }
            .stat-value-lg {
                display: flex;
                align-items: baseline;
                gap: 4px;
            }
            .stat-value-lg .num {
                font: var(--type-data-display);
                letter-spacing: -0.03em;
                color: var(--primary);
            }
            .stat-value-lg .unit {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }
            .stat-value-md {
                display: flex;
                align-items: baseline;
                gap: 4px;
            }
            .stat-value-md .num {
                font: var(--type-headline-lg);
                letter-spacing: -0.01em;
                color: var(--primary);
            }
            .stat-value-md .unit {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
            }

            .chart {
                flex: 1;
                display: flex;
                align-items: flex-end;
                justify-content: space-between;
                gap: 8px;
                height: 100%;
                padding-bottom: 8px;
            }
            .bar { width: 100%; border-radius: 4px; }
            .bar.orange  { background: linear-gradient(to bottom, var(--secondary-container), transparent 80%); }
            .bar.green   { background: linear-gradient(to bottom, var(--tertiary-fixed-dim), transparent 80%); }
            .bar.blue    { background: linear-gradient(to bottom, #3b82f6, transparent 80%); }

            .card-tall  { height: 280px; display: flex; flex-direction: column; }
            .card-mid   { height: 200px; display: flex; flex-direction: column; justify-content: space-between; }
            .card-short { height: 180px; display: flex; flex-direction: column; justify-content: space-between; }

            .fuel-bar {
                width: 100%;
                height: 32px;
                border-radius: var(--radius-sm);
                background: var(--surface-container-highest);
                position: relative;
                overflow: hidden;
            }
            .fuel-bar::before {
                content: '';
                position: absolute;
                bottom: 0;
                left: 0;
                right: 0;
                height: 2px;
                background: var(--secondary-container);
            }
            .fuel-bar::after {
                content: '';
                position: absolute;
                inset: 0;
                background: linear-gradient(to top, rgba(234, 107, 24, 0.4), transparent);
            }

            .pill-normal {
                padding: 4px 8px;
                border-radius: var(--radius-sm);
                background: rgba(105, 219, 173, 0.1);
                border: 1px solid rgba(105, 219, 173, 0.2);
                color: var(--tertiary-fixed-dim);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
            }

            .distance-row {
                display: flex;
                align-items: flex-end;
                justify-content: space-between;
                height: 100%;
                margin-top: 16px;
            }
            .distance-row .chart { gap: 8px; }
            .distance-row .chart .bar { width: 16px; }

            .quick-actions {
                max-width: 480px;
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                overflow: hidden;
                margin-bottom: 96px;
            }
            .quick-actions button {
                width: 100%;
                display: flex;
                align-items: center;
                justify-content: space-between;
                padding: 16px;
                background: none;
                border: none;
                border-bottom: 1px solid var(--outline-variant);
                text-align: left;
                color: inherit;
                transition: background 0.15s ease;
            }
            .quick-actions button:last-child { border-bottom: none; }
            .quick-actions button:hover { background: var(--surface-container-high); }
            .quick-actions button .left-group { display: flex; align-items: center; gap: 16px; }
            .quick-actions button .label {
                font: var(--type-body-md);
                color: var(--primary);
            }
            .quick-actions button .material-symbols-outlined.muted { color: var(--on-surface-variant); }

            .err-engineering {
                position: absolute;
                bottom: 16px;
                right: 16px;
                font-size: 48px;
                color: var(--outline-variant);
                opacity: 0.3;
                pointer-events: none;
            }
            .relative { position: relative; overflow: hidden; }
        `,
    ];

    private bars(heights: number[], color: 'orange' | 'green' | 'blue') {
        return heights.map(h => html`<div class="bar ${color}" style="height: ${h}%;"></div>`);
    }

    render() {
        return html`
            <header class="top-bar">
                <div class="left">
                    <h2>2025 Maxus T60 4X2 GL MT</h2>
                    <nav>
                        <a href="#" class="active">Overview</a>
                        <a href="#">Diagnostics</a>
                        <a href="#">Trips</a>
                    </nav>
                </div>
                <div class="right">
                    <button class="live-tracking">
                        <span class="status-dot"></span>
                        Live Tracking
                    </button>
                    <button class="icon-btn"><span class="material-symbols-outlined">notifications</span></button>
                    <button class="icon-btn"><span class="material-symbols-outlined">account_circle</span></button>
                </div>
            </header>

            <div class="canvas">
                <div class="hero-status">
                    <div class="chip">
                        <span class="material-symbols-outlined">battery_charging_full</span>
                        <span>1</span>
                    </div>
                    <div class="meta">
                        <span class="dot"></span>
                        <span>5,309 miles away</span>
                        <span class="dot"></span>
                        <span>2 hours ago</span>
                    </div>
                </div>

                <div class="grid">
                    <!-- Trips -->
                    <div class="trips-card">
                        <div class="trips-bg"></div>
                        <div class="trips-content">
                            <div class="trips-top">
                                <h3>Trips</h3>
                                <span class="chip">3h ago</span>
                            </div>
                            <div class="trips-bottom">
                                <p class="from">Las Encinas 641, Cerrillos</p>
                                <p class="to">
                                    <span class="material-symbols-outlined">arrow_forward</span>
                                    <span>Avenida Gladys Marín 6096, Estación Central</span>
                                </p>
                                <div class="stats">
                                    <span>14.3 mi</span>
                                    <span>1h 5m</span>
                                    <span>11:31 AM</span>
                                </div>
                            </div>
                        </div>
                    </div>

                    <div class="section-label">Last 7 days</div>

                    <!-- Speed -->
                    <div class="data-card col-6 card-tall">
                        <div class="data-card-head">
                            <h4>Speed</h4>
                            <span class="material-symbols-outlined">chevron_right</span>
                        </div>
                        <div class="stat-row">
                            <div class="stat-col">
                                <div>
                                    <p class="stat-label">Top</p>
                                    <div class="stat-value-lg"><span class="num">78</span><span class="unit">mph</span></div>
                                </div>
                                <div>
                                    <p class="stat-label">Average</p>
                                    <div class="stat-value-md"><span class="num">22</span><span class="unit">mph</span></div>
                                </div>
                            </div>
                            <div class="chart">${this.bars([60, 40, 75, 50, 80, 90, 65, 85], 'orange')}</div>
                        </div>
                    </div>

                    <!-- Utilization -->
                    <div class="data-card col-6 card-tall">
                        <div class="data-card-head">
                            <h4>Utilization</h4>
                            <span class="material-symbols-outlined">chevron_right</span>
                        </div>
                        <div class="stat-row">
                            <div class="stat-col">
                                <div>
                                    <p class="stat-label">Total</p>
                                    <div class="stat-value-lg"><span class="num">30.1</span><span class="unit">h</span></div>
                                </div>
                                <div>
                                    <p class="stat-label">Daily avg</p>
                                    <div class="stat-value-md"><span class="num">4.3</span><span class="unit">h</span></div>
                                </div>
                            </div>
                            <div class="chart">${this.bars([45, 15, 20, 15, 25, 15, 85, 35], 'green')}</div>
                        </div>
                    </div>

                    <!-- Fuel -->
                    <div class="data-card col-4 card-mid">
                        <div class="data-card-head">
                            <h4>Fuel usage</h4>
                            <span class="material-symbols-outlined">chevron_right</span>
                        </div>
                        <div>
                            <div class="stat-value-lg" style="margin-bottom: 16px;">
                                <span class="num">0</span><span class="unit">gallons</span>
                            </div>
                            <div class="fuel-bar"></div>
                        </div>
                    </div>

                    <!-- Coolant -->
                    <div class="data-card col-4 card-mid">
                        <div class="data-card-head" style="border-bottom: 1px solid var(--outline-variant); padding-bottom: 16px; margin-bottom: 16px;">
                            <h4>Coolant temperature</h4>
                        </div>
                        <div style="display:flex; justify-content:space-between; align-items:flex-end;">
                            <div class="stat-value-lg"><span class="num">189</span><span class="unit">°F</span></div>
                            <span class="pill-normal">Normal</span>
                        </div>
                    </div>

                    <!-- Distance -->
                    <div class="data-card col-4 card-mid">
                        <div class="data-card-head">
                            <h4>Distance</h4>
                            <span class="material-symbols-outlined">chevron_right</span>
                        </div>
                        <div class="distance-row">
                            <div class="stat-value-md" style="padding-bottom: 8px;">
                                <span class="num">342</span><span class="unit">miles</span>
                            </div>
                            <div class="chart" style="flex: 1; margin-left: 16px;">
                                ${this.bars([60, 40, 75, 50, 80, 90, 65], 'blue')}
                            </div>
                        </div>
                    </div>

                    <div class="section-headline">Vehicle status</div>

                    <!-- Battery -->
                    <div class="data-card col-3 card-short">
                        <div class="data-card-head">
                            <h4>Battery voltage</h4>
                            <span class="material-symbols-outlined">chevron_right</span>
                        </div>
                        <div class="stat-value-md"><span class="num">12.881</span><span class="unit">V</span></div>
                    </div>

                    <!-- Error codes -->
                    <div class="data-card col-3 card-short relative">
                        <div class="data-card-head">
                            <h4>Error codes</h4>
                            <span class="material-symbols-outlined">chevron_right</span>
                        </div>
                        <div>
                            <div class="stat-value-md" style="margin-bottom: 8px;">
                                <span class="num">0</span><span class="unit">codes</span>
                            </div>
                            <p class="stat-label">2 hours ago</p>
                        </div>
                        <span class="material-symbols-outlined err-engineering">engineering</span>
                    </div>

                    <!-- Odometer -->
                    <div class="data-card col-3 card-short">
                        <div class="data-card-head"><h4>Odometer</h4></div>
                        <div class="stat-value-md"><span class="num">5,688</span><span class="unit">mi</span></div>
                    </div>

                    <!-- AdBlue -->
                    <div class="data-card col-3 card-short">
                        <div class="data-card-head"><h4>AdBlue</h4></div>
                        <div class="stat-value-md"><span class="num">0</span><span class="unit">%</span></div>
                    </div>
                </div>

                <div class="quick-actions">
                    <button>
                        <div class="left-group">
                            <span class="material-symbols-outlined muted">star</span>
                            <span class="label">Make favorite</span>
                        </div>
                    </button>
                    <button>
                        <div class="left-group">
                            <span class="material-symbols-outlined muted">wifi</span>
                            <span class="label">Data sources</span>
                        </div>
                        <span class="material-symbols-outlined muted">chevron_right</span>
                    </button>
                </div>
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'vehicle-details-view': VehicleDetailsView;
    }
}
