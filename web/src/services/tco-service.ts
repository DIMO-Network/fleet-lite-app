import { ApiService } from './api-service.ts';
import { TenantService } from './tenant-service.ts';
import { FleetTCOSummary, TCOSettings, VehicleTCOSummary } from '../types/tco.ts';

export class TCOService {
    private static instance: TCOService;
    public static getInstance(): TCOService {
        if (!TCOService.instance) {
            TCOService.instance = new TCOService();
        }
        return TCOService.instance;
    }

    /** GET /tco/settings?tokenId=N. */
    getSettings(tokenId: number): Promise<TCOSettings> {
        return ApiService.getInstance().get<TCOSettings>(`/tco/settings?tokenId=${tokenId}`);
    }

    /** PUT /tco/settings. */
    putSettings(settings: TCOSettings): Promise<TCOSettings> {
        return ApiService.getInstance().put<TCOSettings>('/tco/settings', settings);
    }

    /** GET /tco/summary. Fleet-wide rollup. */
    getSummary(): Promise<FleetTCOSummary> {
        return ApiService.getInstance().get<FleetTCOSummary>('/tco/summary');
    }

    /** GET /tco/vehicle/:tokenId. */
    getVehicleDetail(tokenId: number): Promise<VehicleTCOSummary> {
        return ApiService.getInstance().get<VehicleTCOSummary>(`/tco/vehicle/${tokenId}`);
    }

    /** PUT /tco/vehicle/:tokenId/backfill/:documentId. Attaches a dollar
     * amount to a document that was uploaded without one, via a
     * cost-amendment CE — the original document is untouched. */
    backfillAmount(tokenId: number, documentId: string, amount: number, currency = 'USD'): Promise<{ id: string }> {
        return ApiService.getInstance().put<{ id: string }>(
            `/tco/vehicle/${tokenId}/backfill/${encodeURIComponent(documentId)}`,
            { amount, currency },
        );
    }

    /** Trigger a browser download of the CSV export. Omit tokenId for the fleet-wide export. */
    async exportCsv(tokenId?: number): Promise<void> {
        const base = ApiService.getInstance().getApiBaseUrl();
        const token = localStorage.getItem('token');
        const qs = tokenId ? `?tokenId=${tokenId}` : '';
        const res = await fetch(`${base}/tco/export.csv${qs}`, {
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
        const filename = match?.[1] || 'tco-export.csv';
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
