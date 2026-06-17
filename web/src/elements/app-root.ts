import { LitElement, html, css } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { Routes } from '@lit-labs/router';
import { sharedStyles } from '../global-styles.ts';
import { TenantService } from '../services/tenant-service.ts';
import { PrefsService } from '../services/prefs-service.ts';
import { setLocale } from '../localization.ts';
import './side-nav.ts';
import './tenant-switcher.ts';
import '../views/fleet-overview.ts';
import '../views/vehicle-details.ts';
import '../views/glovebox.ts';
import '../views/account-settings.ts';
import '../views/onboard-tenant.ts';
import '../views/groups-management.ts';
import '../views/fleet-list-view.ts';

type NavKey = 'vehicles' | 'stats' | 'groups' | 'glovebox' | 'settings';

@customElement('app-root')
export class AppRoot extends LitElement {
    // We use Routes, not Router, on purpose: this app does hash-based routing
    // (links are `#/...` and we drive the router from the `hashchange` event
    // below). Router installs a global click interceptor that preventDefaults
    // every <a>, pushState()s its href, and routes on `anchor.pathname` — for a
    // `#/...` link that pathname is always `/`, so every click would just
    // re-render the `/` route. Routes has no click handling, so native hash
    // navigation fires `hashchange` and onHashChange() does the routing.
    private router: Routes;
    private boundOnHashChange = () => this.onHashChange();
    private tenantService = TenantService.getInstance();

    @state() private activeNav: NavKey = 'vehicles';
    // The current tenant id (first hash segment). Drives nav links + the
    // Tenant-Id header (via ApiService). Empty while onboarding.
    @state() private tenantId = '';
    // Tenant we've already recorded a login for this session (login is touched once).
    private loginRecordedFor = '';

    static styles = [
        sharedStyles,
        css`
            :host {
                display: flex;
                width: 100vw;
                height: 100vh;
                overflow: hidden;
                background: var(--background);
            }
            main {
                flex: 1;
                position: relative;
                display: flex;
                flex-direction: column;
                height: 100vh;
                overflow: hidden;
            }
        `,
    ];

    constructor() {
        super();
        this.router = new Routes(this, [
            { path: '/onboard',                       render: () => html`<onboard-tenant-view></onboard-tenant-view>` },
            { path: '/:tenantId/',                    render: () => html`<fleet-overview-view .tenantId=${this.tenantId}></fleet-overview-view>` },
            { path: '/:tenantId/vehicles/:tokenId',   render: ({ tokenId }) => html`<vehicle-details-view .tenantId=${this.tenantId} .tokenId=${tokenId}></vehicle-details-view>` },
            { path: '/:tenantId/groups',              render: () => html`<groups-management-view .tenantId=${this.tenantId}></groups-management-view>` },
            { path: '/:tenantId/glovebox',            render: () => html`<glovebox-view .tenantId=${this.tenantId}></glovebox-view>` },
            { path: '/:tenantId/settings',            render: () => html`<account-settings-view .tenantId=${this.tenantId}></account-settings-view>` },
            { path: '/:tenantId/stats',               render: () => html`<fleet-list-view .tenantId=${this.tenantId}></fleet-list-view>` },
        ]);
    }

    async connectedCallback() {
        super.connectedCallback();
        // Activate the saved/browser locale before the first route renders so the
        // initial paint is already localized (lit-localize runtime mode).
        const locale = PrefsService.getInstance().getLocale();
        if (locale === 'es') await setLocale(locale);
        window.addEventListener('hashchange', this.boundOnHashChange);
        this.onHashChange();
    }

    disconnectedCallback() {
        window.removeEventListener('hashchange', this.boundOnHashChange);
        super.disconnectedCallback();
    }

    private async onHashChange() {
        const path = location.hash.slice(1) || '/';

        // Onboarding has no tenant.
        if (path === '/onboard') {
            this.tenantId = '';
            this.router.goto('/onboard');
            this.requestUpdate();
            return;
        }

        const seg = path.split('/').filter(Boolean); // ['<tid>', 'vehicles', '3681']
        const tenantId = seg[0] || '';

        // No tenant in the route → pick a default (or send to onboarding).
        if (!tenantId) {
            await this.resolveDefaultTenant();
            return;
        }

        this.tenantId = tenantId;
        // Remember the active tenant so the next tenant-less load returns here.
        localStorage.setItem('lastTenantId', tenantId);
        // Record this session's login once per tenant (best-effort): bumps the
        // member's last_login_at and stores their email for the Members list.
        if (this.loginRecordedFor !== tenantId) {
            this.loginRecordedFor = tenantId;
            void this.tenantService.recordLogin(tenantId).catch(() => {});
        }
        this.activeNav = this.deriveActive('/' + seg.slice(1).join('/'));
        this.router.goto(path);
        this.requestUpdate();
    }

    // Decide where a tenant-less URL should land: 0 tenants → onboarding;
    // 1 tenant → it; >1 → the last-used tenant if still valid, else the first.
    private async resolveDefaultTenant() {
        try {
            const tenants = await this.tenantService.fetchTenants();
            if (tenants.length === 0) {
                location.hash = '/onboard';
                return;
            }
            const last = localStorage.getItem('lastTenantId');
            const pick = tenants.find(t => t.id === last) ?? tenants[0];
            location.hash = `/${pick.id}/`;
        } catch {
            location.hash = '/onboard';
        }
    }

    private deriveActive(path: string): NavKey {
        if (path === '/' || path.startsWith('/vehicles')) return 'vehicles';
        if (path.startsWith('/stats')) return 'stats';
        if (path.startsWith('/groups')) return 'groups';
        if (path.startsWith('/glovebox')) return 'glovebox';
        if (path.startsWith('/settings')) return 'settings';
        return 'vehicles';
    }

    render() {
        // Onboarding (and the brief resolving state) render full-bleed, no nav.
        const chrome = this.tenantId !== '';
        if (!chrome) {
            return html`<main>${this.router.outlet()}</main>`;
        }
        return html`
            <side-nav .active=${this.activeNav} .tenantId=${this.tenantId}></side-nav>
            <main>${this.router.outlet()}</main>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'app-root': AppRoot;
    }
}
