import { ApiService } from './api-service.ts';
import { TenantService } from './tenant-service.ts';
import { ChargingFleetSummary, ChargingSettings } from '../types/charging.ts';

export class ChargingService {
    private static instance: ChargingService;
    public static getInstance(): ChargingService {
        if (!ChargingService.instance) {
            ChargingService.instance = new ChargingService();
        }
        return ChargingService.instance;
    }

    /** GET /charging/settings. */
    getSettings(): Promise<ChargingSettings> {
        return ApiService.getInstance().get<ChargingSettings>('/charging/settings');
    }

    /** PUT /charging/settings. */
    putSettings(settings: ChargingSettings): Promise<ChargingSettings> {
        return ApiService.getInstance().put<ChargingSettings>('/charging/settings', settings);
    }

    /** GET /charging/summary?from&to. Fleet-wide rollup + map points. */
    getSummary(from: Date, to: Date): Promise<ChargingFleetSummary> {
        const q = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
        return ApiService.getInstance().get<ChargingFleetSummary>(`/charging/summary?${q.toString()}`);
    }

    /** GET /charging/:tokenId/sessions?from&to. */
    getVehicleSessions(tokenId: number, from: Date, to: Date): Promise<{ sessions: ChargingFleetSummary['sessions'] }> {
        const q = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
        return ApiService.getInstance().get(`/charging/${tokenId}/sessions?${q.toString()}`);
    }

    /** Trigger a browser download of the CSV export. Omit tokenId for the fleet-wide export. */
    async exportCsv(from: Date, to: Date, tokenId?: number): Promise<void> {
        const base = ApiService.getInstance().getApiBaseUrl();
        const token = localStorage.getItem('token');
        const q = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
        if (tokenId) q.set('tokenId', String(tokenId));
        const res = await fetch(`${base}/charging/export.csv?${q.toString()}`, {
            headers: {
                ...(token ? { Authorization: `Bearer ${token}` } : {}),
                ...TenantService.getInstance().tenantIdHeader(),
            },
        });
        if (!res.ok) {
            throw new Error(`export failed: ${res.status} ${await res.text()}`);
        }
        const blob = await res.blob();
        const disposition = res.headers.get('Content-Disposition') || '';
        const match = /filename="?([^";]+)"?/i.exec(disposition);
        const filename = match?.[1] || 'charging-export.csv';
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    }
}
