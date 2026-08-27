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
    data: Record<string, unknown> | null;
    /**
     * Id of the raw blob CE this document was extracted from, from the parsed
     * CE's `raweventid`. Empty for documents attested before raweventid
     * pairing landed — those have no downloadable file.
     */
    rawId?: string;
    /**
     * Attested by a different dev license — e.g. a document this vehicle's
     * owner added from the DIMO mobile app. Provenance, not permission.
     */
    isThirdParty?: boolean;
    /**
     * The caller cannot modify this document: they do not own the vehicle, or
     * we did not attest it (our tombstone would not suppress it platform-wide,
     * so offering delete would be a lie).
     */
    isReadOnly?: boolean;
    /** Account DID of the wallet that uploaded it. Absent on older documents. */
    uploadedBy?: string;
}

export interface ListDocumentsResponse {
    documents: DocumentEntry[];
    tokenDid: string;
    /** Whether the caller owns this vehicle or merely holds it under a share. */
    isOwner?: boolean;
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
