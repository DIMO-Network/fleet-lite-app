import { html, nothing, type ReactiveController, type ReactiveControllerHost, type TemplateResult } from 'lit';
import { msg } from '@lit/localize';
import { SettingsService } from '../services/settings-service.ts';
import { LicenseService } from '../services/license-service.ts';
import {
    buildShareVehiclesUrl,
    grantFallbackRedirectUri,
    pickGrantRedirectUri,
    sameClientId,
} from './dimo-permissions.ts';

/**
 * Where a grant to `license` may return (see pickGrantRedirectUri), or null
 * when nothing on this origin will do. A failed lookup falls back to the app
 * root, which is safe for every license.
 */
export async function resolveGrantRedirect(license: string, appClientId: string): Promise<string | null> {
    try {
        const uris = await LicenseService.getInstance().redirectUris(license);
        return pickGrantRedirectUri(uris, location.origin, { loginPageSafe: !sameClientId(license, appClientId) });
    } catch {
        return grantFallbackRedirectUri();
    }
}

/**
 * The "Grant permissions" action on a missing-permissions banner, shared by
 * every view that shows one: the link to DIMO's sharing screen for the fleet's
 * license, or the setup hint when that license has no redirect URI this app
 * can use.
 *
 * The link is withheld until the redirect is known: never a guessed return
 * address. Styling stays with the host (`.grant`, `.grant-setup`).
 */
export class GrantLinkController implements ReactiveController {
    private loginUrl = '';
    private appClientId = '';
    private license = '';
    /** undefined = not known yet; null = no usable redirect on this origin. */
    private redirect: string | null | undefined = undefined;
    /** Bumped per lookup, so a slow answer for an old license is dropped. */
    private generation = 0;

    constructor(private readonly host: ReactiveControllerHost) {
        host.addController(this);
    }

    hostConnected() {
        void SettingsService.getInstance()
            .fetchPublicSettings()
            .then((s) => {
                this.loginUrl = s.loginUrl;
                this.appClientId = s.clientId;
                this.resolve();
            })
            .catch(() => { /* no settings: no link, the banner text stands alone */ });
    }

    /**
     * The license the missing permissions must be granted to ('' when the API
     * named none). Call whenever fresh data arrives. The same license keeps
     * its link; a license that had no usable redirect is looked up again, so
     * one fixed in the console shows its link on the next load.
     */
    setLicense(license: string) {
        if (license === this.license && this.redirect !== null) return;
        this.license = license;
        this.resolve();
    }

    /** The DIMO sharing URL for `vehicles`, or '' while it can't be built. */
    url(vehicles: Array<number | string>): string {
        if (!this.redirect || !this.loginUrl || !this.license) return '';
        return buildShareVehiclesUrl({
            loginUrl: this.loginUrl,
            clientId: this.license,
            redirectUri: this.redirect,
            vehicles,
        });
    }

    /** The banner's action: the link, the setup hint, or nothing while unknown. */
    render(vehicles: Array<number | string>, iconSize: number): TemplateResult | typeof nothing {
        const url = this.url(vehicles);
        if (url) {
            return html`
                <a class="grant" href=${url} target="_blank" rel="noopener">
                    ${msg('Grant permissions')}
                    <span class="material-symbols-outlined" style="font-size:${iconSize}px;">open_in_new</span>
                </a>
            `;
        }
        if (this.redirect === null) {
            return html`<p class="grant-setup">${msg(html`To grant from here, add <code>${location.origin}/</code> to this license’s redirect URIs in the DIMO developer console.`)}</p>`;
        }
        return nothing;
    }

    private resolve() {
        const generation = ++this.generation;
        this.redirect = undefined;
        this.host.requestUpdate();
        const license = this.license;
        if (!license || !this.appClientId) return;
        void resolveGrantRedirect(license, this.appClientId).then((redirect) => {
            if (generation !== this.generation) return;
            this.redirect = redirect;
            this.host.requestUpdate();
        });
    }
}
