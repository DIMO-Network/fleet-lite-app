import { LitElement, html, css } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { Routes } from '@lit-labs/router';
import { sharedStyles } from '../global-styles.ts';
import './side-nav.ts';
import '../views/fleet-overview.ts';
import '../views/vehicle-details.ts';
import '../views/glovebox.ts';
import '../views/account-settings.ts';

type NavKey = 'vehicles' | 'stats' | 'glovebox' | 'settings';

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

    @state() private activeNav: NavKey = 'vehicles';

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
            { path: '/',                    render: () => html`<fleet-overview-view></fleet-overview-view>` },
            { path: '/vehicles/:tokenId',   render: ({ tokenId }) => html`<vehicle-details-view .tokenId=${tokenId}></vehicle-details-view>` },
            { path: '/glovebox',            render: () => html`<glovebox-view></glovebox-view>` },
            { path: '/settings',            render: () => html`<account-settings-view></account-settings-view>` },
            { path: '/stats',               render: () => html`<fleet-overview-view></fleet-overview-view>` },
            { path: '/support',             render: () => html`<account-settings-view></account-settings-view>` },
            { path: '/logout',              render: () => html`<account-settings-view></account-settings-view>` },
        ]);
    }

    connectedCallback() {
        super.connectedCallback();
        window.addEventListener('hashchange', this.boundOnHashChange);
        this.onHashChange();
    }

    disconnectedCallback() {
        window.removeEventListener('hashchange', this.boundOnHashChange);
        super.disconnectedCallback();
    }

    private onHashChange() {
        const path = location.hash.slice(1) || '/';
        this.router.goto(path);
        this.activeNav = this.deriveActive(path);
        this.requestUpdate();
    }

    private deriveActive(path: string): NavKey {
        if (path === '/' || path.startsWith('/vehicles')) return 'vehicles';
        if (path.startsWith('/stats')) return 'stats';
        if (path.startsWith('/glovebox')) return 'glovebox';
        if (path.startsWith('/settings')) return 'settings';
        return 'vehicles';
    }

    render() {
        return html`
            <side-nav .active=${this.activeNav}></side-nav>
            <main>${this.router.outlet()}</main>
        `;
    }
}

declare global {
    interface HTMLElementTagNameMap {
        'app-root': AppRoot;
    }
}
