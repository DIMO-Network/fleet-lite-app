# Glovebox — Implementation Plan

> Goal: let a signed-in fleet-lite-app user upload, attest, list, view, download,
> and delete documents (insurance, registration, service invoices, …) tied to
> their DIMO vehicles. Persisted on DIMO infrastructure via the attestation +
> fetch APIs, not in our own DB.

Created: 2026-05-27

---

## References reviewed

| Source | Role |
|---|---|
| `dimo-driver/src/layouts/Glovebox/` + `hooks/queries/documentQueries.ts` | Mobile app reference. Uses dimo-app-backend's `/v2/documents` (single-step upload, fan-out list, tombstone delete). |
| `dimo-app-backend/src/documents-v2/` | Backend the mobile app talks to. NestJS, one shared dev license, owns the extract→attest pipeline. |
| `rental-fleets-app/api/internal/controllers/documents.go` + `service/extract_api.go`, `service/attest_service.go`, `gateway/fetch_api.go` | Multi-tenant Go reference. Two-step UX (extract → VIN-confirm → attest), per-tenant dev license. We borrow its Go shape and its confirm-before-attest UX. |

## Architecture chosen

**Hybrid** of the two patterns:

- **Per-user (single shared dev license)** like dimo-app-backend — fleet-lite-app
  is not multi-tenant. The dev license private key lives in `api/settings.yaml`
  under `DIMO_AUTH_PRIVATE_KEY` (we're reusing the same one as rental-fleets-app,
  client_id `0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB`).
- **Two-step UX** like rental-fleets — user uploads file, sees the extracted
  VIN + auto-matched vehicle suggestion, can override the target vehicle, then
  confirms to attest. Avoids the "wrong vehicle, oops, now there's a CE I have
  to tombstone" foot-gun.
- **CE types as source of truth** (rental-fleets pattern). UI maps DIMO
  canonical types to friendly category labels.
- **Tombstone CE on delete** (mobile pattern). No local-hide kludge.

```
┌─────────────────┐   ┌──────────────────────┐    ┌────────────────────────────┐
│  Frontend (Lit) │   │ fleet-lite-app/api   │    │ DIMO infrastructure        │
├─────────────────┤   ├──────────────────────┤    ├────────────────────────────┤
│ glovebox view   │──►│ POST /documents/     │───►│ extract.dimo.zone          │
│   pick file     │   │      extract         │    │   (Developer JWT)          │
│                 │◄──│ {vin, category,      │    │                            │
│                 │   │  fields, fileHash}   │    │                            │
│                 │   │                      │    │                            │
│                 │──►│ GET  /documents/     │    │  identity-api.dimo.zone    │
│ confirm modal   │   │      vin-lookup?vin= │    │  (no auth — public)        │
│                 │◄──│ {found, tokenId,     │    │  reuses /vehicles cache    │
│                 │   │  vehicle:{make,…}}   │    │                            │
│                 │   │                      │    │                            │
│                 │──►│ POST /documents/     │───►│ attest.dimo.zone           │
│                 │   │      attest          │    │   raw CE + parsed CE pair  │
│                 │◄──│ {rawSubmission,      │    │   signed locally with      │
│                 │   │  parsedSubmission}   │    │   DIMO_AUTH_PRIVATE_KEY    │
│                 │   │                      │    │                            │
│ doc list (right │──►│ GET  /documents/     │───►│ fetch-api.dimo.zone        │
│  panel)         │   │      list?tokenId=N  │    │   /v1/fetch/objects        │
│                 │◄──│ [{id, type, time,    │    │   (Asset JWT by DID)       │
│                 │   │  filehash, fields}]  │    │                            │
│                 │   │                      │    │                            │
│ doc detail      │──►│ GET  /documents/     │    │  same fetch-api, then      │
│   download      │   │      download?...    │    │  base64-decode + stream    │
│                 │   │                      │    │                            │
│ delete          │──►│ DELETE /documents/:id│───►│ attest.dimo.zone           │
│                 │   │      ?vehicleTokenId │    │   dimo.tombstone CE        │
│                 │   │                      │    │   for parsed + raw ids     │
└─────────────────┘   └──────────────────────┘    └────────────────────────────┘
```

---

## Backend (Go)

### New settings (`api/settings.yaml` + `settings.sample.yaml`)

```yaml
DIMO_AUTH_PRIVATE_KEY: '0x...'        # secret. Same value as rental-fleets-app uses.
EXTRACT_API_URL: 'https://extract.dimo.zone/extract'
FETCH_API_URL:   'https://fetch-api.dimo.zone/v1/fetch/objects'
ATTEST_API_URL:  'https://attest.dimo.zone'
```

`config.Settings` gets four new `url.URL` / `string` fields. `helm/values.yaml`
gets the non-secret URLs; `templates/secret.yaml` already pulls
`DIMO_AUTH_PRIVATE_KEY` from the existing ExternalSecret entry.

### New gateway / service files

| File | Purpose |
|---|---|
| `internal/gateway/dimo_auth_provider.go` | Wrapper around `github.com/DIMO-Network/shared/pkg/dimoauth`. Caches the dev JWT in-memory; exchanges for vehicle/asset JWTs on demand. Single-license (no per-tenant table). |
| `internal/service/extract_api.go` | HTTP client for `extract.dimo.zone`. Sends multipart file, parses VIN + fields + category out of nested response. |
| `internal/service/attest_service.go` | Builds + signs paired raw/parsed CloudEvents with secp256k1 (Ethereum personal-sign), POSTs to `attest.dimo.zone`. Includes tombstone helper. |
| `internal/service/fetch_api.go` | Asset-JWT-authenticated GraphQL client for `fetch-api.dimo.zone/v1/fetch/objects`. List + by-filehash lookup. |

These are direct ports from rental-fleets-app, trimmed of tenant references. The
auth provider drops the `models.Tenant` argument throughout — credentials come
from `config.Settings` instead.

### New controller (`internal/controllers/documents.go`)

| Route | Auth | Purpose |
|---|---|---|
| `POST /documents/extract` | JWT | Multipart file upload. Returns `{vin, category, fields, fileHash, rawResponse}`. |
| `GET /documents/vin-lookup?vin=X` | JWT | Matches VIN against the caller's vehicles by listing identity-api vehicles for the JWT wallet and scanning `aftermarketDevice.serial` + extracting a `vin` field from the definition (identity-api doesn't expose VIN on Vehicle directly — needs a separate query against the device-definitions-api or a `vinByVehicleTokenId` query; see **Open question 1**). |
| `POST /documents/attest` | JWT | Body: `{tokenId, category, fileBase64, mimeType, parsedData, fileName}`. Builds raw + parsed CEs, signs, POSTs to attest-api. Returns `{rawSubmission:{id,type,source}, parsedSubmission:{id,type,source}}`. |
| `GET /documents/list?tokenId=N` | JWT | Exchanges dev JWT → asset JWT for the vehicle's DID, queries fetch-api `cloudEvents(did, limit)`, returns merged list of parsed docs with their paired raw filehashes. |
| `GET /documents/download?tokenDid=X&filehash=Y` | JWT | Looks up the raw CE by filehash via fetch-api, base64-decodes `data_base64`, streams bytes with `Content-Disposition` for the browser. |
| `DELETE /documents/:id?tokenId=N` | JWT | Builds + signs a `dimo.tombstone` CE for the parsed id and its paired raw, POSTs to attest-api. |

Each handler reads the wallet from the JWT (we already have
`GetWalletAddressFromJWT`). For `vin-lookup` and `attest` we additionally
verify the user owns the targeted `tokenId` by intersecting against
`identity.FetchVehiclesByWalletAddress(wallet)` — prevents writing
attestations to vehicles you don't own.

### Wiring in `internal/app/app.go`

The same JWT-protected `authApp` group already exists; we just register the
new controller's routes there. No DB migrations needed yet — documents live on
DIS, not in our postgres.

---

## Frontend (Lit)

### Category mapping (`web/src/utils/document-categories.ts`)

```ts
export const CE_TYPE_TO_LABEL: Record<string, string> = {
  'dimo.document.vehicle.service.invoice': 'Service & parts',
  'dimo.document.vehicle.insurance':        'Insurance',
  'dimo.document.vehicle.registration':     'Registration',
  'dimo.document.vehicle.inspection':       'Inspection',
  'dimo.document.vehicle.title':            'Title',
  'dimo.document.vehicle.finance':          'Finance',
  'dimo.document.vehicle.regulatory.other': 'Regulatory',
  'dimo.document.vehicle.maintenance':      'Service & parts',
  'dimo.document.vehicle.note':             'Note',
  'dimo.document.vehicle.expense':          'Other',
  'dimo.document.vehicle.condition':        'Other',
};
```

The "Missing" rail in the Stitch design (Insurance / Registration / Inspection)
gets driven by checking which CE types are absent for the selected vehicle.

### New service (`web/src/services/document-service.ts`)

Typed wrappers around the six new backend endpoints. Returns strongly-typed
`ExtractResult`, `VinLookupResult`, `Document`, `AttestResult`.

### New element (`web/src/elements/upload-document-modal.ts`)

Three-state modal:

```
┌──────────────────────────────────────────┐
│  Step 1: Pick a file (PDF, JPG, PNG)     │
├──────────────────────────────────────────┤
│  Step 2: Confirm                         │
│    ◯ Detected VIN: 1HGCM82633A123456     │
│    ◯ Matched: 2021 Subaru Ascent  ✓      │
│    ◯ Category: Insurance                 │
│    [Change vehicle ▾]  [Confirm & Save]  │
├──────────────────────────────────────────┤
│  Step 3: Done. Show "uploaded" + link.   │
└──────────────────────────────────────────┘
```

When the extracted VIN doesn't match any of the user's vehicles, step 2
prompts a manual vehicle picker (dropdown of `/vehicles`).

### Glovebox updates (`web/src/views/glovebox.ts`)

- Left panel keeps its current vehicle list (already wired to `/vehicles`).
- Right panel:
  - Replace the static "Missing" list with one driven by which CE types the
    selected vehicle is missing.
  - Replace the "No records yet" empty state with the actual document list:
    grouped by category, each row showing date + filename + chevron.
  - Each row → opens a detail modal with parsed fields + download button.
  - Top-right "+" button → opens the upload modal.

### New element (`web/src/elements/document-detail-modal.ts`)

Reads attestation metadata, renders parsed fields as a definition list, plus a
"Download" button that hits `/documents/download` and a "Delete" button that
hits `DELETE /documents/:id`.

---

## Open questions

1. **VIN field on identity-api Vehicle.** The identity-api Vehicle type we
   query in `gateway/identity_api_queries.go` doesn't include the actual VIN
   string. Options:
   - Add `vin` to the GraphQL query (DIMO's `Vehicle` may expose it under a
     different field — needs verification against identity-api schema)
   - Use the `dimo.attestation.vin` CE on fetch-api as the authoritative VIN
     source per vehicle (mobile app does this)
   - Fall back to **manual vehicle pick** when we can't auto-match, which is
     always graceful
   
   Decision: ship vin-lookup that does the obvious match (compare extracted
   VIN to identity-api VINs **if available**, else fall back to manual pick).
   Iterate later if we discover an authoritative VIN source.

2. **First DB migration?** Still none required — all glovebox state lives on
   DIS. If we add SACD-grant tracking or per-doc local metadata later, that's
   the trigger for the first migration.

3. **File size limits.** rental-fleets caps at 25 MB. dimo-driver does
   client-side image resize. We'll mirror rental-fleets' 25 MB body limit
   (already set on the fiber app) and skip resize for now.

---

## Out of scope for this round

- SACD grants (sharing documents with non-owner wallets)
- AI document chat (the "AiOverlay" / staging-documents path in dimo-driver)
- Auto-creating maintenance log entries from a service invoice (rental-fleets
  pattern — needs the maintenance feature we don't have)
- Auto-creating ledger entries (same)
- Bulk multi-file upload — single file per modal session

---

## Execution order

A1. Backend settings + auth provider + extract service
A2. Attest service (sign + emit CE pair)
A3. Fetch-api gateway (list + by-filehash)
A4. Documents controller + app.go wiring
A5. `go build` + `curl` smoke test (extract a JPG, verify VIN comes back)

B1. Frontend `document-service.ts`
B2. `upload-document-modal.ts`
B3. Glovebox view rewrite (real doc list, missing-rail, modal triggers)
B4. `document-detail-modal.ts` (parsed fields + download + delete)
B5. Boot api + web, drive Chrome through upload → list → detail → delete.

C. Commit + push.
