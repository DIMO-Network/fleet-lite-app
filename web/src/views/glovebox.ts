import { LitElement, html, css } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';

interface GloveboxVehicle {
    tokenId: string;
    title: string;
    recordCount: number;
    active?: boolean;
}

const VEHICLES: GloveboxVehicle[] = [
    { tokenId: '1', title: '2022 Ram 1500', recordCount: 0 },
    { tokenId: '2', title: '2022 Ram 1500', recordCount: 0 },
    { tokenId: '3', title: '2023 Chrysler 300', recordCount: 0 },
    { tokenId: '4', title: '2023 Chrysler 300', recordCount: 0 },
    { tokenId: '5', title: '2026 Mercedes-Benz A 200', recordCount: 0, active: true },
];

@customElement('glovebox-view')
export class GloveboxView extends LitElement {
    @state() private selected: GloveboxVehicle = VEHICLES.find(v => v.active) ?? VEHICLES[0];

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                flex-direction: row;
                width: 100%;
                height: 100%;
                overflow: hidden;
            }

            .list-panel {
                width: 400px;
                border-right: 1px solid var(--outline-variant);
                display: flex;
                flex-direction: column;
                height: 100%;
                flex-shrink: 0;
            }
            @media (max-width: 1024px) {
                .list-panel { width: 40%; }
            }
            @media (max-width: 768px) {
                .list-panel { display: none; }
            }

            .list-header {
                padding: var(--stack-lg) var(--margin-desktop);
                border-bottom: 1px solid var(--outline-variant);
                background: rgba(19, 19, 19, 0.8);
                backdrop-filter: blur(12px);
                -webkit-backdrop-filter: blur(12px);
                position: sticky;
                top: 0;
                z-index: 10;
                display: flex;
                justify-content: space-between;
                align-items: center;
            }
            .list-header h1 { font: var(--type-headline-lg); color: var(--primary); letter-spacing: -0.01em; }
            .list-header button {
                width: 40px;
                height: 40px;
                border-radius: var(--radius-full);
                background: none;
                border: none;
                color: var(--primary);
                display: flex;
                align-items: center;
                justify-content: center;
                transition: background 0.15s ease;
            }
            .list-header button:hover { background: var(--surface-container); }

            .vehicle-list {
                flex: 1;
                overflow-y: auto;
                padding: var(--stack-md) var(--margin-mobile);
                display: flex;
                flex-direction: column;
                gap: 12px;
            }

            .vehicle-card {
                background: var(--surface-container-low);
                border: 1px solid var(--outline-variant);
                border-radius: var(--radius-lg);
                padding: 12px;
                display: flex;
                align-items: center;
                gap: 16px;
                cursor: pointer;
                transition: background 0.15s ease;
                color: inherit;
                text-decoration: none;
            }
            .vehicle-card:hover { background: var(--surface-container); }
            .vehicle-card.active { background: var(--surface-container-highest); }

            .vehicle-icon {
                width: 48px;
                height: 48px;
                border-radius: var(--radius-full);
                background: var(--surface-container-highest);
                border: 1px solid var(--outline-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                flex-shrink: 0;
            }
            .vehicle-icon .material-symbols-outlined { color: var(--on-surface-variant); }
            .vehicle-card.active .vehicle-icon .material-symbols-outlined { color: var(--primary); }

            .vehicle-meta { flex: 1; min-width: 0; }
            .vehicle-meta h3 {
                font: var(--type-body-md);
                font-weight: 700;
                color: var(--primary);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }
            .vehicle-meta p {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                white-space: nowrap;
                overflow: hidden;
                text-overflow: ellipsis;
            }

            .vehicle-card .right-group { display: flex; align-items: center; gap: 8px; }
            .vehicle-card .start-pill {
                padding: 4px 12px;
                background: var(--surface-container-highest);
                border-radius: var(--radius-full);
                color: var(--primary);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                transition: background 0.15s ease;
            }
            .vehicle-card:hover .start-pill { background: var(--outline-variant); }
            .vehicle-card .right-group .material-symbols-outlined { color: var(--on-surface-variant); }

            /* Right panel */
            .detail-panel {
                flex: 1;
                display: flex;
                flex-direction: column;
                height: 100%;
                overflow-y: auto;
                background: var(--background);
            }

            .detail-header {
                padding: 32px var(--gutter) 24px var(--gutter);
                display: flex;
                align-items: center;
                gap: 24px;
                border-bottom: 1px solid rgba(68, 71, 72, 0.3);
            }
            .detail-icon {
                width: 80px;
                height: 80px;
                border-radius: var(--radius-full);
                background: var(--surface-container);
                border: 1px solid var(--outline-variant);
                display: flex;
                align-items: center;
                justify-content: center;
                box-shadow: 0 0 15px rgba(255, 255, 255, 0.05);
                flex-shrink: 0;
            }
            .detail-icon .material-symbols-outlined {
                font-size: 40px;
                color: var(--on-surface-variant);
                font-variation-settings: 'wght' 200;
            }
            .detail-header h2 { font: var(--type-headline-lg); color: var(--primary); letter-spacing: -0.01em; }
            .detail-header p {
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                margin-top: 4px;
            }

            .detail-body {
                padding: var(--stack-lg) var(--gutter);
                max-width: var(--container-max-width);
                margin: 0 auto;
                width: 100%;
                flex: 1;
                display: flex;
                flex-direction: column;
            }

            .filter-row { margin-bottom: var(--stack-lg); }
            .filter-pill {
                padding: 8px 16px;
                background: var(--primary);
                color: var(--on-primary);
                border-radius: var(--radius-full);
                font: var(--type-label-caps);
                letter-spacing: 0.05em;
                text-transform: uppercase;
                font-weight: 700;
                display: inline-flex;
                align-items: center;
                gap: 4px;
                border: none;
            }
            .filter-pill .sep { opacity: 0.6; }

            .missing-section { margin-bottom: 48px; }
            .missing-head {
                display: flex;
                align-items: center;
                gap: 8px;
                margin-bottom: 16px;
            }
            .missing-head .dot {
                width: 4px;
                height: 4px;
                background: var(--secondary);
                border-radius: var(--radius-full);
            }
            .missing-head h3 {
                font: var(--type-label-caps);
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
            }
            .missing-list {
                border-left: 2px solid var(--surface-container-high);
                padding-left: 24px;
                display: flex;
                flex-direction: column;
                gap: 24px;
            }
            .missing-item { cursor: pointer; }
            .missing-item h4 {
                font: var(--type-body-lg);
                color: var(--primary);
                transition: color 0.15s ease;
            }
            .missing-item:hover h4 { color: var(--secondary); }
            .missing-item p {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 4px;
            }

            .empty-state {
                flex: 1;
                display: flex;
                flex-direction: column;
                align-items: center;
                justify-content: center;
                text-align: center;
                padding: 48px 0;
                margin-top: auto;
            }
            .empty-state h3 {
                font: var(--type-headline-md);
                color: var(--primary);
                margin-bottom: 8px;
            }
            .empty-state p {
                font: var(--type-label-caps);
                letter-spacing: 0.1em;
                text-transform: uppercase;
                color: var(--on-surface-variant);
                max-width: 280px;
                margin-bottom: 32px;
                line-height: 1.6;
            }
            .empty-state .add-doc {
                padding: 12px 24px;
                border-radius: var(--radius-full);
                border: 1px solid var(--primary);
                color: var(--primary);
                background: none;
                font: var(--type-body-md);
                font-weight: 500;
                transition: background 0.3s ease, color 0.3s ease;
            }
            .empty-state .add-doc:hover {
                background: var(--primary);
                color: var(--on-primary);
            }
        `,
    ];

    private renderListCard(v: GloveboxVehicle) {
        const cls = v.tokenId === this.selected.tokenId ? 'vehicle-card active' : 'vehicle-card';
        const onClick = () => { this.selected = v; };
        return html`
            <div class=${cls} @click=${onClick}>
                <div class="vehicle-icon">
                    <span class="material-symbols-outlined">directions_car</span>
                </div>
                <div class="vehicle-meta">
                    <h3>${v.title}</h3>
                    <p>${v.recordCount === 0 ? 'Add documents' : `${v.recordCount} Records`}</p>
                </div>
                <div class="right-group">
                    <span class="start-pill">Start</span>
                    <span class="material-symbols-outlined">chevron_right</span>
                </div>
            </div>
        `;
    }

    render() {
        return html`
            <section class="list-panel">
                <header class="list-header">
                    <h1>Glovebox</h1>
                    <button><span class="material-symbols-outlined">add</span></button>
                </header>
                <div class="vehicle-list custom-scrollbar">
                    ${VEHICLES.map(v => this.renderListCard(v))}
                </div>
            </section>

            <section class="detail-panel">
                <div class="detail-header">
                    <div class="detail-icon">
                        <span class="material-symbols-outlined">directions_car</span>
                    </div>
                    <div>
                        <h2>${this.selected.title}</h2>
                        <p>${this.selected.recordCount} Records</p>
                    </div>
                </div>

                <div class="detail-body">
                    <div class="filter-row">
                        <button class="filter-pill">
                            All <span class="sep">•</span> ${this.selected.recordCount}
                        </button>
                    </div>

                    <div class="missing-section">
                        <div class="missing-head">
                            <span class="dot"></span>
                            <h3>Missing</h3>
                        </div>
                        <div class="missing-list">
                            <div class="missing-item">
                                <h4>Add insurance</h4>
                                <p>Track renewals</p>
                            </div>
                            <div class="missing-item">
                                <h4>Add registration</h4>
                                <p>Track expiration</p>
                            </div>
                            <div class="missing-item">
                                <h4>Add inspection</h4>
                                <p>Track next inspection</p>
                            </div>
                        </div>
                    </div>

                    <div class="empty-state">
                        <h3>No records yet.</h3>
                        <p>- Upload anything - receipt, insurance pdf, reg card</p>
                        <button class="add-doc">Add document</button>
                    </div>
                </div>
            </section>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'glovebox-view': GloveboxView;
    }
}
