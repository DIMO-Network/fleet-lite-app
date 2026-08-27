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

## Read contract — how documents come back out

The mobile app (`dimo-app-backend`) is the reference implementation. It writes
the documents users upload from their phone, and those land on the same vehicle
DID this app reads, so anything the two disagree about shows up as documents
that exist but are invisible here. Three rules, each of which we got wrong once:

**1. Always filter types server-side.** `cloudEvents` orders by timestamp DESC
and truncates to `limit` across *every* CE type on the subject — a vehicle
streaming `dimo.status` telemetry fills the window in minutes and its documents
never appear. `CloudEventFilter` matches types exactly (`type`, or `types` for
IN (...)); there is no prefix wildcard, so every concrete type is enumerated in
`gateway/document_types.go`. Adding a document category means adding it there,
and keeping it in step with `GLOVEBOX_CE_TYPES` in
`dimo-app-backend/src/vehicles-v2/vehicles.service.ts`.

**2. Never ignore the GraphQL `errors` array.** fetch-api types its list fields
non-null (`[CloudEvent!]!`) and its resolver bails on the first failed blob read,
so one bad S3 object nulls the entire result. Read `data` without checking
`errors` and that arrives as an empty list — reported to the user as "no
documents", with nothing logged. `decodeFetchResponse` is the only decode path
for exactly this reason.

**3. Pair raw and parsed CEs by `raweventid`, not by file hash.** fetch-api's
`CloudEventHeader` exposes no `filehash` field. We still *write* one, but it
cannot be read back, so it can never be a join key. The parsed CE carries the
raw CE's id in the `raweventid` extension — the same extension dimo-app-backend
stamps — and that is what `/documents/download` takes. Documents attested before
this landed have no `rawId` and cannot be downloaded.

Keeping raw blob CEs out of the list query follows from all three: the parsed CE
already names its blob, so listing never needs the attachments, and leaving them
out means a list request never touches S3.

## Document sharing — who may do what

A tenant's vehicle set is **whatever its dev license is SACD-privileged on**
(`FetchPrivilegedVehicles`), so it routinely holds vehicles the tenant does not
own. "In my fleet" is therefore not "mine", and the two must not be conflated.

The platform contract, which `dimo-app-backend` enforces and this app now
matches:

| Caller | Read | Add | Delete |
|---|---|---|---|
| Vehicle owner | yes | yes | yes, for documents we attested |
| Holds it under a share | yes | yes | **no** |

A share is READ + APPEND. `authorizeSubject` in dimo-app-backend returns a
`grantee` mode that "MUST NOT be treated as delete authorization"; here the
same line is `requireVehicleOwner`, called from `DeleteDocument` only. Both apps
write to the same CloudEvents, so a fleet sharee who could tombstone would be
deleting documents the mobile app would have refused to let them touch.

Two fields carry this to the UI. `isReadOnly` says the caller cannot modify a
document; `isThirdParty` says a different dev license attested it. They are not
the same thing and neither is a permission — the authorization lives in the
handlers.

`producer` is stamped on both CEs at upload with the caller's canonical account
DID, the same form `canonicalAccountDid` produces in dimo-app-backend. It comes
back as `uploadedBy`. This is provenance, not authority: it is what lets an
owner see which of their sharees contributed a document.

### What works today

Verified by reading the write and read paths on both sides:

- **Owner, either app.** A vehicle's owner sees every document on it in both
  apps regardless of which one added it. `resolveDocAccess` in dimo-app-backend
  short-circuits on `vehicle.owner`, and our reads go through the tenant's dev
  license, so neither side needs a per-user grant.
- **Fleet members, documents added from mobile.** These are what the original
  bug hid; the enumerated type filter fixes it.
- **Fleet members, vehicles held under a share.** Read and add work; delete is
  refused, which is the contract.
- **Narrow shares.** The asset JWT now asks for exactly the four privileges a
  CloudEvent read needs (see `assetJWTPrivileges`). It previously also demanded
  `GetLocationHistory`, and token-exchange fails the whole request on any
  missing privilege — so a share granting raw data but not all-time location
  404'd its documents here while reading fine in mobile.

### One gap, and it is a platform policy decision

**A document can only be deleted by the app that attested it.** This is not an
oversight to route around. DIS sets a tombstone's `source` from the
authenticated dev license and validates only the payload shape — it does *not*
check that the voided attestation belongs to that source
(`dis/internal/processors/cloudeventconvert/attesation_msg.go`). The ownership
check lives at read time, in fetch-api's `voidsSuppressionMod`, matching
`(source, voids_id)`. That match **is** the boundary stopping one dev license
from deleting another's attestations — across every attestation type on the
platform, not just documents. Dropping it to make cross-app delete work would
remove that boundary for everyone.

So we do not. Documents we did not attest come back `isReadOnly`, the delete
control is not offered, and `DELETE` returns `deletedEverywhere: false` when a
tombstone will only take effect locally.

The real fix, if the platform wants one, is for vehicle documents to be
attested under a single designated license so `source` matches by construction.
That is an architecture decision for the DIS/fetch-api owners.

### Sharing a vehicle from here does not yet share its documents

Confirmed, not suspected. `BuildSetPermissionsCall` in fleet-tenancy-api packs
`TryPackSetPermissions0(..., "")` — that last argument is SACD's `source`, and
it is empty. dimo-app-backend gates a **grantee** (not an owner) on
`hasDocumentAgreement`, which reads the agreements payload *from that source*:

```
resolveDocAccess → fetchSacdAgreements(sacd.source) → hasDocumentAgreement(payload, vehicleDID)
```

An empty source yields a null payload yields a refusal. Permission bits do not
substitute: a doc-only share is valid at `permissions == 0`, and conversely our
full default mask grants no document access.

Scope: this affects only **a third-party wallet we share a vehicle with, then
opening the mobile app**. Owners are unaffected (owner short-circuits), and our
own reads are unaffected (they go through the dev license).

Closing it is a change in fleet-tenancy-api, and the contract is exact:

1. Build `{"permissions": "<mask>", "data": {"agreements": [...]}}`, one
   agreement per document namespace:
   `{"type": "cloudevent", "eventType": "dimo.document.vehicle.*", "asset": "<vehicle DID>"}`
   — also `dimo.raw.vehicle.*` for attachments. `asset` is compared
   lower-cased against the DID; `eventType` is prefix-matched on the trailing
   `*` (`DOC_EVENT_TYPE_PATTERNS` in dimo-app-backend).
2. Pin it and pass `ipfs://<cid>` as the `source` argument.
3. The CID **must** be a dag-pb/UnixFS CIDv0 — `importBytes` with
   `{rawLeaves:false, leafType:'file', cidVersion:0}`, as token-metadata-worker's
   `computeCid` produces. `SacdSourceService` recomputes the content address
   from the bytes and rejects a mismatch, fail-closed. A raw-codec CID is
   rejected.
4. Keep the payload under 262144 bytes (single UnixFS block); larger payloads
   cannot be reconstructed by the verifier and are rejected.
5. It must be served by the gateway at `IPFS_GATEWAY_BASE_URL`, default
   `https://assets.dimo.org/ipfs/<cid>`.

Nothing in this repo blocks that: our reads never depend on the shared wallet's
grant. The change is entirely tenancy-side.

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
| `GET /documents/list?tokenId=N` | JWT | Exchanges dev JWT → asset JWT for the vehicle's DID, queries fetch-api `cloudEvents(did, limit, filter: {types})` over the enumerated document types, returns parsed docs each carrying its paired `rawId`. |
| `GET /documents/download?tokenId=N&rawId=Y` | JWT | Point-queries the raw CE by id (`latestCloudEvent(did, filter:{id})`), reads `dataBase64` or follows `dataUrl`, streams bytes with `Content-Disposition` for the browser. |
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
