# Vehicle sharing — fleet-lite surface

Status: **planned, not started**. Written 2026-08-18.

A **Share** button on the vehicle **list view** (not the map): enter a 0x
wallet address, pick a duration, and that wallet receives an on-chain SACD
permission grant on the vehicle. The owner keeps the NFT. No wallet prompt —
the transaction is signed server-side by the operator's signer.

**The authoritative plan is
[`fleet-tenancy-api/docs/plans/05-vehicle-sharing.md`](../../fleet-tenancy-api/docs/plans/05-vehicle-sharing.md).**
It records the decisions (SACD grant not transfer; server-signed v1 with
browser-passkey phase 2; tx machinery in fleet-tenancy-api; River job model;
list-but-not-revoke v1 scope; fixed default permissions), the authorization
chain, the API shapes, and the traps. This doc covers only what lands in this
repo — steps 3 and 4 of that plan.

**v1 adds no web3 dependencies to this repo, frontend or backend.** The
passkey stack (Turnkey/ZeroDev, for owners whose accounts weren't created by
kaufmann-oracle) is phase 2, consumed as an npm package published from
`b2b-fleet-mgr-app` — nothing here should anticipate it.

## api/ (step 3)

New authenticated endpoints, thin over fleet-tenancy-api:

```
POST /vehicles/:tokenID/share          { grantee, durationDays }  → 202 { jobId }
GET  /vehicles/:tokenID/share/status?jobId=…                      → { state, isSuccessful, errors }
```

- **Gate on the `manage_vehicles` capability** via the existing `/v1/authz`
  path — the same capability kaufmann's shared-account routes were missing.
  Not `role`; role is never an authorization input.
- Forward to the tenancy endpoints
  (`POST /v1/tenants/{id}/vehicles/{tokenId}/share`, status alongside) through
  the existing `internal/gateway/tenancy_api.go` client, passing the session
  wallet for the tenancy-side re-check.
- The `tokenID` must come from the tenant's own vehicle set (the usual
  entitlement-filtered read) before forwarding — never pass through a raw
  caller-supplied token id.

`GET /vehicles` grows a per-vehicle **`canShare`** boolean: join each
vehicle's `owner_address` against tenancy's
`GET /v1/tenants/{id}/shareable-owners` (owners whose kernel accounts
registered the operator signer — resolved live against accounts-api on the
tenancy side, decided 2026-08-18). The join here is still a string
comparison — checksum-normalise both sides before comparing.

Listing existing shares needs **no new backend**: the modal reads chain state
through the existing identity-api proxy.

## web/ (step 4)

Two pieces, both plain Lit following this repo's conventions (scoped CSS, no
Tailwind, `sharedStyles` for Material Symbols, `msg()` for every string):

**`fleet-list-view.ts`** — a Share action on each vehicle card, rendered only
when `vehicle.canShare` and the session holds `manage_vehicles`. List view
only; the map quick-view is untouched.

**`share-vehicle-modal.ts`** (new element, pattern-match the existing modals
e.g. `manage-group-vehicles-modal.ts`):

- Wallet input with hex-address validation (`0x` + 40 hex chars) before the
  button enables; reject the vehicle's own owner address client-side too.
- Duration picker: fixed options (e.g. 30 days / 1 year / indefinite —
  final set is an open question in the tenancy doc). No permission
  checkboxes in v1; the default permission set is fixed server-side.
- Existing-shares list: query the identity-api proxy for
  `vehicle(tokenId) { sacds { nodes { grantee permissions expiresAt createdAt } } }`
  — same read the b2b sharing panel does. Chain state is the source of truth;
  nothing is read back from our own database.
- Submit → `POST /vehicles/:tokenID/share` → poll the status endpoint until
  `isSuccessful` or a bounded attempt cap (kaufmann polls ~30 × 4s; match
  that). Success is the **`isSuccessful` boolean**, not a `"Success"` string.
- On success: refresh the existing-shares list from identity-api (it may lag
  the receipt by a few seconds — keep a manual refresh affordance rather than
  polling identity-api).

## Traps local to this repo

- `canShare` and the submit-time check are the same live accounts-api
  lookup on the tenancy side, so they can only diverge within the short
  cache TTL (e.g. a signer rotated minutes ago). A 403 from submit despite a
  visible button is that window — surface the error message, don't retry.
- Don't conflate this with b2b's "Share Vehicle Tracking" modal — that
  feature is time-boxed tracking links, not SACD, and its naming has already
  trapped people once.
