import { LitElement, html, css, nothing } from 'lit';
import { msg, str } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { MembershipService } from '../services/membership-service.ts';
import { Membership, MembershipStatus } from '../types/membership.ts';

function termLabel(months: number): string {
    return months === 1 ? msg('1 month') : msg(str`${months} months`);
}

function vehicleTitle(m: Membership): string {
    const parts = [m.year ? String(m.year) : '', m.make, m.model].filter(Boolean);
    if (parts.length) return parts.join(' ');
    return m.vin || `#${m.vehicleTokenId}`;
}

function formatDate(iso: string): string {
    return new Date(iso).toLocaleDateString(undefined, {
        year: 'numeric', month: 'short', day: 'numeric',
    });
}

// This fleet's memberships — read only.
//
// Memberships are bought from and managed by the operator, never here, so this
// page deliberately offers no actions. Its job is to answer two questions a
// customer can otherwise only guess at: what have I paid for, and why can't I
// see a vehicle I know I own?
//
// The second one is the reason this page exists at all. When the operator has
// enforcement on, vehicles without an active membership are not returned to
// this app — they are absent from the fleet, not greyed out — and without
// somewhere saying so, a lapsed membership looks like a bug.
@customElement('memberships-view')
export class MembershipsView extends LitElement {
    @property({ type: String }) tenantId = '';

    @state() private loading = true;
    @state() private error = '';
    @state() private available = true;
    @state() private enforced = false;
    @state() private memberships: Membership[] = [];

    private service = MembershipService.getInstance();

    async connectedCallback() {
        super.connectedCallback();
        await this.load();
    }

    private async load() {
        this.loading = true;
        this.error = '';
        try {
            const res = await this.service.list();
            this.available = res.available;
            this.enforced = res.enforced;
            this.memberships = res.memberships;
        } catch (e) {
            this.error = e instanceof Error ? e.message : msg('Could not load your memberships.');
        } finally {
            this.loading = false;
        }
    }

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
            :host > * { flex-shrink: 0; }

            header.top-bar {
                position: sticky;
                top: 0;
                z-index: 40;
                height: var(--top-bar-height);
                background: var(--background);
                display: flex;
                align-items: center;
                gap: 8px;
                padding: 0 var(--gutter);
            }
            @media (max-width: 768px) {
                header.top-bar { padding: 0 var(--margin-mobile); height: 64px; }
            }
            header.top-bar h2 { font: var(--type-headline-md); letter-spacing: -0.01em; color: var(--primary); }
            .back {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                width: 36px;
                height: 36px;
                margin-left: -8px;
                color: var(--on-surface-variant);
                text-decoration: none;
                border-radius: var(--radius-full);
                transition: background 0.15s ease, color 0.15s ease;
            }
            .back .material-symbols-outlined { font-size: 22px; }
            .back:hover { background: var(--surface-container-high); color: var(--on-surface); }

            .canvas {
                flex: 1;
                width: 100%;
                max-width: 800px;
                margin: 0 auto;
                padding: var(--stack-md) var(--gutter) var(--stack-lg);
            }
            @media (max-width: 768px) {
                .canvas { padding: var(--stack-md) var(--margin-mobile) var(--stack-lg); }
            }

            /* Informational callout: tonal, no border, leading info glyph. */
            .note {
                display: flex;
                gap: 12px;
                padding: 14px 16px;
                margin-bottom: 12px;
                border-radius: var(--radius-lg);
                background: var(--surface-container-low);
                color: var(--on-surface);
                font: var(--type-body-sm);
            }
            .note::before {
                content: 'info';
                font-family: 'Material Symbols Outlined';
                font-size: 20px;
                line-height: 20px;
                font-feature-settings: 'liga';
                font-variation-settings: 'wght' 350;
                color: var(--on-surface-variant);
                flex-shrink: 0;
            }
            .note + .list, .note + .empty { margin-top: 24px; }

            .list {
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
                overflow: hidden;
            }
            .item {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 16px;
                min-height: 68px;
                padding: 12px 20px;
                border-bottom: 1px solid var(--outline-variant);
            }
            .item:last-child { border-bottom: none; }
            .item .title { font: 600 15px/22px var(--font-headline); color: var(--primary); }
            .item .sub {
                font: var(--type-body-sm);
                color: var(--on-surface-variant);
                margin-top: 2px;
            }

            /* Status chip: tint + dot. Mint only for a live (active) membership. */
            .pill {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                flex-shrink: 0;
                padding: 3px 10px;
                border-radius: var(--radius-full);
                font: var(--type-label);
                white-space: nowrap;
            }
            .pill::before {
                content: '';
                width: 6px;
                height: 6px;
                border-radius: var(--radius-full);
                background: currentColor;
            }
            .pill.active { color: var(--accent-ink); background: var(--accent-soft); }
            .pill.soon { color: var(--warning); background: color-mix(in srgb, var(--warning) 14%, transparent); }
            .pill.expired { color: var(--error); background: color-mix(in srgb, var(--error) 12%, transparent); }

            .empty {
                padding: 40px 24px;
                text-align: center;
                color: var(--on-surface-variant);
                font: var(--type-body-md);
                background: var(--surface-container-low);
                border-radius: var(--radius-lg);
            }
            .empty .lead {
                color: var(--primary);
                font: 600 17px/24px var(--font-headline);
                margin-bottom: 8px;
            }
        `,
    ];

    private statusPill(m: Membership) {
        const labels: Record<MembershipStatus, string> = {
            active: msg('Active'),
            expiring_soon: msg('Expiring soon'),
            expired: msg('Expired'),
            canceled: msg('Cancelled'),
        };
        const cls: Record<MembershipStatus, string> = {
            active: 'active',
            expiring_soon: 'soon',
            expired: 'expired',
            canceled: 'expired',
        };
        return html`<span class="pill ${cls[m.status]}">${labels[m.status]}</span>`;
    }

    private renderItem(m: Membership) {
        return html`
            <div class="item">
                <div>
                    <div class="title">${vehicleTitle(m)}</div>
                    <div class="sub">
                        ${termLabel(m.termMonths)} ·
                        ${m.status === 'expired'
                            ? msg(str`expired ${formatDate(m.expiresAt)}`)
                            : msg(str`renews ${formatDate(m.expiresAt)}`)}
                    </div>
                </div>
                ${this.statusPill(m)}
            </div>
        `;
    }

    private renderBody() {
        if (this.loading) {
            return html`<div class="empty">${msg('Loading your memberships…')}</div>`;
        }
        if (this.error) {
            return html`<div class="empty">${this.error}</div>`;
        }
        // The backend does not serve memberships on this deployment yet. Said
        // plainly, because "no memberships" and "this isn't switched on" are
        // different facts and only one of them is worth contacting anyone about.
        if (!this.available) {
            return html`
                <div class="empty">
                    <div class="lead">${msg('Memberships aren’t switched on yet')}</div>
                    ${msg('When they are, everything your fleet has paid for will be listed here.')}
                </div>
            `;
        }
        if (this.memberships.length === 0) {
            return html`
                <div class="empty">
                    <div class="lead">${msg('No memberships yet')}</div>
                    ${msg('Memberships are arranged through your fleet operator. Once they add one for a vehicle, it appears here.')}
                </div>
            `;
        }
        return html`<div class="list">${this.memberships.map((m) => this.renderItem(m))}</div>`;
    }

    render() {
        const expiring = this.memberships.filter((m) => m.status === 'expiring_soon').length;

        return html`
            <header class="top-bar">
                <a class="back" href="#/${this.tenantId}/settings" title=${msg('Back to account')}>
                    <span class="material-symbols-outlined">arrow_back</span>
                </a>
                <h2>${msg('Memberships')}</h2>
            </header>

            <div class="canvas">
                ${this.available && this.enforced && !this.loading
                    ? html`
                        <div class="note">
                            ${msg('Only vehicles with an active membership appear in your fleet. If a vehicle you expect is missing, its membership has run out — your fleet operator can renew it or move it to another vehicle.')}
                        </div>
                      `
                    : nothing}
                ${expiring > 0
                    ? html`
                        <div class="note">
                            ${expiring === 1
                                ? msg('One membership expires within 30 days. Your fleet operator renews it.')
                                : msg(str`${expiring} memberships expire within 30 days. Your fleet operator renews them.`)}
                        </div>
                      `
                    : nothing}
                ${this.renderBody()}
            </div>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'memberships-view': MembershipsView;
    }
}
