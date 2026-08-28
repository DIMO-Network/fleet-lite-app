import { ApiService } from './api-service.ts';

export interface PublicSettings {
    clientId: string;
    loginUrl: string;
    chainId: number;
    /** CARTO basemap key for the Leaflet tile layers. Empty means the maps
     *  render CARTO's "API KEY REQUIRED" watermark — see fleet-map.ts. */
    cartoBasemapKey: string;
}

export class SettingsService {
    private static instance: SettingsService;
    private cached: PublicSettings | null = null;
    private inflight: Promise<PublicSettings> | null = null;

    public static getInstance(): SettingsService {
        if (!SettingsService.instance) {
            SettingsService.instance = new SettingsService();
        }
        return SettingsService.instance;
    }

    public async fetchPublicSettings(): Promise<PublicSettings> {
        if (this.cached) return this.cached;
        if (this.inflight) return this.inflight;
        this.inflight = ApiService.getInstance()
            .get<PublicSettings>('/public/settings', false)
            .then((s) => {
                this.cached = s;
                this.inflight = null;
                return s;
            })
            .catch((e) => {
                this.inflight = null;
                throw e;
            });
        return this.inflight;
    }
}
