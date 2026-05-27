export interface ExtractResult {
    vin: string;
    category: string;
    fields: Record<string, unknown>;
    fileHash: string;
    rawResponse: Record<string, unknown>;
}

export interface VinLookupResult {
    found: boolean;
    vin: string;
    vehicleTokenId?: number;
}

export interface DocumentEntry {
    id: string;
    type: string;
    source: string;
    time: string;
    fileHash: string;
    data: Record<string, unknown> | null;
    rawId?: string;
}

export interface ListDocumentsResponse {
    documents: DocumentEntry[];
    tokenDid: string;
    /** When true, dev license lacks SACD permissions on this vehicle — list cannot be fetched. */
    permissionsRequired?: boolean;
    /** Dev license address the user needs to grant SACDs to. */
    devLicense?: string;
}

export interface AttestSubmission {
    id: string;
    type: string;
    source: string;
}

export interface AttestResult {
    rawSubmission?: AttestSubmission;
    parsedSubmission: AttestSubmission;
}

export interface AttestRequest {
    tokenId: number;
    category: string;
    fileBase64: string;
    mimeType: string;
    fileName: string;
    parsedData: Record<string, unknown>;
}
