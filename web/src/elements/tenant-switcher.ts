import { LitElement, html, css } from 'lit';
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
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                color: var(--on-surface);
                border-radius: 999px;
                padding: 8px 14px;
                font-size: 13px;
                font-weight: 600;
                cursor: pointer;
                max-width: 220px;
            }
            button.trigger:hover { border-color: var(--outline); }
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
                background: var(--surface-container-high);
                border: 1px solid var(--outline-variant);
                border-radius: 12px;
                padding: 6px;
                z-index: 100;
                box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
            }
            .item {
                display: flex;
                align-items: center;
                justify-content: space-between;
                gap: 8px;
                width: 100%;
                box-sizing: border-box;
                padding: 10px 12px;
                border-radius: 8px;
                background: none;
                border: none;
                color: var(--on-surface);
                font-size: 13px;
                text-align: left;
                cursor: pointer;
            }
            .item:hover { background: var(--surface-container-highest); }
            .item .check { color: var(--tertiary-container); font-size: 18px; }
            .sep { height: 1px; background: var(--outline-variant); margin: 6px 4px; }
            .item.add { color: var(--on-surface-variant); }
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
        return t?.name || 'Fleet';
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
            <button class="trigger" @click=${(e: Event) => { e.stopPropagation(); this.open = !this.open; }}>
                <span class="material-symbols-outlined" style="font-size:18px;">garage</span>
                <span class="name">${this.currentName()}</span>
                <span class="material-symbols-outlined chev">${this.open ? 'expand_less' : 'expand_more'}</span>
            </button>
            ${this.open ? html`
                <div class="menu">
                    ${this.tenants.map(t => html`
                        <button class="item" @click=${() => this.select(t.id)}>
                            <span class="name">${t.name}</span>
                            ${t.id === this.currentTenantId
                                ? html`<span class="material-symbols-outlined check">check</span>`
                                : ''}
                        </button>
                    `)}
                    <div class="sep"></div>
                    <button class="item add" @click=${this.addTenant}>
                        <span class="material-symbols-outlined">add</span>
                        <span>Add fleet</span>
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
