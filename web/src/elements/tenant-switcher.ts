import { LitElement, html, css } from 'lit';
import { msg } from '@lit/localize';
import { customElement, property, state } from 'lit/decorators.js';
import { sharedStyles } from '../global-styles.ts';
import { TenantService, Tenant } from '../services/tenant-service.ts';

/**
 * Top-right dropdown for switching the active tenant. Lists the wallet's
 * tenants; selecting one navigates to that tenant's fleet root, which (via the
 * route) updates the current tenant everywhere. Also offers "Add fleet" which
 * routes to onboarding.
 */
@customElement('tenant-switcher')
export class TenantSwitcher extends LitElement {
    @property({ type: String }) currentTenantId = '';
    @state() private tenants: Tenant[] = [];
    @state() private open = false;

    private onDocClick = (e: Event) => {
        if (!e.composedPath().includes(this)) this.open = false;
    };

    static styles = [
        sharedStyles,
        css`
            :host { position: relative; display: inline-block; }
            button.trigger {
                display: flex;
                align-items: center;
                gap: 8px;
                height: 36px;
                padding: 0 10px 0 12px;
                background: var(--surface-container-high);
                color: var(--on-surface);
                border-radius: var(--radius-full);
                font: 500 13px/18px var(--font-body);
                max-width: 220px;
                transition: background 0.15s ease;
            }
            button.trigger:hover,
            button.trigger[aria-expanded='true'] { background: var(--surface-container-highest); }
            button.trigger .glyph { font-size: 18px; color: var(--on-surface-variant); }
            .name {
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            .chev { font-size: 18px; color: var(--on-surface-variant); }
            .menu {
                position: absolute;
                top: calc(100% + 8px);
                right: 0;
                min-width: 240px;
                max-width: 320px;
                background: var(--surface-overlay);
                border-radius: var(--radius-lg);
                padding: 6px;
                z-index: 100;
                box-shadow: var(--shadow-float);
                transform-origin: top right;
                animation: menu-in 0.14s ease-out;
            }
            @keyframes menu-in {
                from { opacity: 0; transform: translateY(-4px) scale(0.98); }
            }
            .item {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 10px;
                width: 100%;
                height: 36px;
                padding: 0 10px;
                border-radius: var(--radius-md);
                color: var(--on-surface);
                font: var(--type-body-sm);
                text-align: left;
                transition: background 0.15s ease, color 0.15s ease;
            }
            .item:hover { background: var(--surface-container-high); }
            .item.current { font-weight: 500; color: var(--primary); }
            .item .check { color: var(--primary); font-size: 18px; flex: none; }
            .sep { height: 1px; background: var(--outline-variant); margin: 6px 4px; }
            .item.add { justify-content: flex-start; color: var(--on-surface-variant); }
            .item.add:hover { color: var(--on-surface); }
            .item.add .material-symbols-outlined { font-size: 18px; }
        `,
    ];

    connectedCallback() {
        super.connectedCallback();
        document.addEventListener('click', this.onDocClick);
        void this.load();
    }

    disconnectedCallback() {
        document.removeEventListener('click', this.onDocClick);
        super.disconnectedCallback();
    }

    private async load() {
        try {
            this.tenants = await TenantService.getInstance().fetchTenants();
        } catch {
            this.tenants = [];
        }
    }

    private currentName(): string {
        const t = this.tenants.find(x => x.id === this.currentTenantId);
        return t?.name || msg('Fleet');
    }

    private select(id: string) {
        this.open = false;
        if (id !== this.currentTenantId) location.hash = `/${id}/`;
    }

    private addTenant() {
        this.open = false;
        location.hash = '/onboard';
    }

    render() {
        return html`
            <button class="trigger" aria-expanded=${this.open ? 'true' : 'false'} @click=${(e: Event) => { e.stopPropagation(); this.open = !this.open; }}>
                <span class="material-symbols-outlined glyph">garage</span>
                <span class="name">${this.currentName()}</span>
                <span class="material-symbols-outlined chev">${this.open ? 'expand_less' : 'expand_more'}</span>
            </button>
            ${this.open ? html`
                <div class="menu">
                    ${this.tenants.map(t => html`
                        <button class="item ${t.id === this.currentTenantId ? 'current' : ''}" @click=${() => this.select(t.id)}>
                            <span class="name">${t.name}</span>
                            ${t.id === this.currentTenantId
                                ? html`<span class="material-symbols-outlined check">check</span>`
                                : ''}
                        </button>
                    `)}
                    <div class="sep"></div>
                    <button class="item add" @click=${this.addTenant}>
                        <span class="material-symbols-outlined">add</span>
                        <span>${msg('Add fleet')}</span>
                    </button>
                </div>
            ` : ''}
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'tenant-switcher': TenantSwitcher;
    }
}
