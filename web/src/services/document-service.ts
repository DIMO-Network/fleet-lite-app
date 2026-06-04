import { ApiService } from './api-service.ts';
import { TenantService } from './tenant-service.ts';
import {
    AttestRequest,
    AttestResult,
    ExtractResult,
    ListDocumentsResponse,
    VinLookupResult,
} from '../types/document.ts';

/**
 * Reads a File into a base64 string (no `data:...;base64,` prefix).
 */
export function fileToBase64(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => {
            const result = String(reader.result || '');
            const idx = result.indexOf('base64,');
            resolve(idx >= 0 ? result.slice(idx + 'base64,'.length) : result);
        };
        reader.onerror = () => reject(reader.error);
        reader.readAsDataURL(file);
    });
}

export class DocumentService {
    private static instance: DocumentService;
    public static getInstance(): DocumentService {
        if (!DocumentService.instance) {
            DocumentService.instance = new DocumentService();
        }
        return DocumentService.instance;
    }

    /** POST /documents/extract with multipart form. Returns parsed metadata + VIN. */
    async extract(file: File): Promise<ExtractResult> {
        const base = ApiService.getInstance().getApiBaseUrl();
        const form = new FormData();
        form.append('file', file, file.name);
        const token = localStorage.getItem('token');
        const res = await fetch(`${base}/documents/extract`, {
            method: 'POST',
            headers: {
                ...(token ? { Authorization: `Bearer ${token}` } : {}),
                ...TenantService.getInstance().tenantIdHeader(),
            },
            body: form,
        });
        if (!res.ok) {
            throw new Error(`extract failed: ${res.status} ${await res.text()}`);
        }
        return res.json();
    }

    /** GET /documents/vin-lookup?vin=X. Returns {found, vehicleTokenId?}. */
    lookupVIN(vin: string): Promise<VinLookupResult> {
        return ApiService.getInstance().get<VinLookupResult>(`/documents/vin-lookup?vin=${encodeURIComponent(vin)}`);
    }

    /** POST /documents/attest with JSON body. Builds + emits raw+parsed CE pair. */
    attest(req: AttestRequest): Promise<AttestResult> {
        return ApiService.getInstance().post<AttestResult>('/documents/attest', req);
    }

    /** GET /documents/list?tokenId=N. */
    list(tokenId: number): Promise<ListDocumentsResponse> {
        return ApiService.getInstance().get<ListDocumentsResponse>(`/documents/list?tokenId=${tokenId}`);
    }

    /** DELETE /documents/:id?tokenId=N. Emits a tombstone CE. */
    delete(id: string, tokenId: number): Promise<{ parsedSubmission: { id: string } }> {
        return fetch(`${ApiService.getInstance().getApiBaseUrl()}/documents/${encodeURIComponent(id)}?tokenId=${tokenId}`, {
            method: 'DELETE',
            headers: {
                'Content-Type': 'application/json',
                Authorization: `Bearer ${localStorage.getItem('token') ?? ''}`,
                ...TenantService.getInstance().tenantIdHeader(),
            },
        }).then(async (r) => {
            if (!r.ok) throw new Error(`delete failed: ${r.status} ${await r.text()}`);
            return r.json();
        });
    }

    /** Trigger a browser file download for the raw bytes by tokenId + filehash. */
    async download(tokenId: number, fileHash: string): Promise<void> {
        const base = ApiService.getInstance().getApiBaseUrl();
        const token = localStorage.getItem('token');
        const res = await fetch(
            `${base}/documents/download?tokenId=${tokenId}&filehash=${encodeURIComponent(fileHash)}`,
            {
                headers: {
                    ...(token ? { Authorization: `Bearer ${token}` } : {}),
                    ...TenantService.getInstance().tenantIdHeader(),
                },
            },
        );
        if (!res.ok) {
            throw new Error(`download failed: ${res.status}`);
        }
        const blob = await res.blob();
        const disposition = res.headers.get('Content-Disposition') || '';
        const match = /filename="?([^";]+)"?/i.exec(disposition);
        const filename = match?.[1] || `document-${fileHash.slice(0, 8)}`;
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
