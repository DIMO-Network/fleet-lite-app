# Postmark Delivery/Open Tracking — Investigation + Plan (fleet-lite-app)

Show owners what happened to an invitation email after "Send invite": **sent →
delivered → opened** (and **bounced**, the failure case that matters most). Today the
UI only knows whether the Postmark API call succeeded (`emailSent`); everything after
that is invisible, so "they never got it" is undiagnosable without the Postmark
dashboard.

Builds on `docs/MEMBER_INVITATIONS_PLAN.md` (invitations table, Postmark gateway) and
the invite-flow info logs added 2026-07-08.

---

## Investigation findings (Postmark docs, verified 2026-07-08)

**Webhooks.** Postmark POSTs JSON per event to a URL configured per **server → message
stream** (dashboard: Server → Message Stream → Webhooks tab, or via API). Event types:
Delivery, Bounce, SpamComplaint, Open, Click, SubscriptionChange, Inbound. Multiple
webhooks per stream are allowed.

**Correlation.** Two mechanisms, both echoed back in every webhook payload:
- `MessageID` — returned by `POST /email/withTemplate` (we currently discard it).
- `Metadata` — arbitrary key/values we attach at send time (e.g.
  `{"invitation_id": "<uuid>"}`), echoed verbatim in webhook payloads.

Metadata is the robust choice: it survives without any lookup table and directly names
our row. We'll store `MessageID` too (dedup/debug).

**Delivery payload** (`RecordType: "Delivery"`): `MessageID`, `Recipient`,
`DeliveredAt` (ISO 8601), `Details` (receiving server's response line), `Metadata`,
`MessageStream`.

**Open payload** (`RecordType: "Open"`): `MessageID`, `Recipient`, `ReceivedAt`,
`FirstOpen` (bool), `Metadata`, plus client/OS/geo. Requirements:
- Send with `"TrackOpens": true` (per-message; no account-wide change needed).
- Open tracking = invisible tracking pixel → only fires if the client loads images.
  Apple MPP auto-opens inflate this; corporate clients that block images suppress it.
  **Treat "opened" as best-effort signal, "delivered" as the reliable one.**
- Configure `PostFirstOpenOnly = true` so we get one Open event, not one per re-read.

**Bounce payload** (`RecordType: "Bounce"`): includes `Type` (HardBounce, etc.),
`Description`, `Email`, `MessageID`, `Metadata`. This is the one that turns "they
never got it" into a visible red badge.

**Webhook auth options:** IP allowlist, HTTP basic auth in the URL
(`https://user:pass@host/…`), or HMAC-SHA256 signature of the raw body. Basic auth is
the pragmatic fit (single secret in helm values + Postmark config); HMAC is available
if we want tamper-proofing later.

**Retries:** Delivery/Open retry at 1/5/15 min on non-200; a 403 stops retries.
Handler must be idempotent (same event can arrive twice) and should 200 even for
events it can't match, to avoid useless retries.

---

## Design

### Data: new columns on `invitations`

```sql
-- 202607XXXXXXXX_invitation_email_tracking.sql  (schema comes from search_path — do NOT hardcode)
ALTER TABLE invitations
    ADD COLUMN postmark_message_id text,
    ADD COLUMN email_status        text,        -- sent | delivered | opened | bounced
    ADD COLUMN email_status_at     timestamptz,  -- when that status was reached
    ADD COLUMN email_status_detail text;         -- bounce description, etc.
CREATE INDEX invitations_postmark_message_id_idx ON invitations (postmark_message_id);
```

Single status column, monotonic ranking `sent < delivered < opened` (bounced wins over
anything): webhooks can arrive out of order; only upgrade, never downgrade. Resend
resets to `sent` with the new `MessageID`.

### Send side (`gateway/postmark_api.go`, `service/invitation.go`)

- `SendInvitation` payload += `"TrackOpens": true`, `"Metadata": {"invitation_id": id}`;
  return the response `MessageID`.
- Create/Resend: persist `postmark_message_id`, `email_status='sent'`,
  `email_status_at=now()`. (Pass `inv.ID` into `sendEmail`.)

### Receive side: `POST /webhooks/postmark`

- **Public route** (Postmark can't do DIMO JWTs) next to the existing `/public/*` and
  `/identity/*` unauthenticated routes in `app.go` — no new ingress needed, the API is
  already reachable at the app's public host.
- Auth: reject unless basic-auth credentials match new setting
  `POSTMARK_WEBHOOK_SECRET` (username fixed, e.g. `postmark`). Return **403** on bad
  credentials (stops Postmark retries), **200** otherwise.
- Handler: parse `RecordType`; resolve invitation by `Metadata.invitation_id`
  (fallback: `MessageID`); apply status upgrade; info-log each event
  (`invite flow: email delivered/opened/bounced`). Unknown/unmatched → log + 200.
- Ignore `Click`/`SpamComplaint`/etc. for now (200, no-op).

### UI (`tenant-members.ts`)

- Pending invite row meta line gains the email status:
  `Invited as member · expires 7/15 · Delivered` (or `Opened`, or red `Bounced —
  address not found`). Tooltip shows `email_status_at`.
- No polling needed initially — status refreshes on the existing `load()` whenever the
  members view is opened. (Live updates would need SSE/poll; not worth it yet.)

### Postmark configuration (one-time, manual)

- Server → `outbound` stream → Webhooks → add
  `https://postmark:<secret>@<app-host>/webhooks/postmark`, check **Delivery**,
  **Bounce**, **Open** (with *first open only*).
- Dev/prod use different Postmark servers → configure each against its own host.

## Rollout order

1. ✅ Migration + send-side capture (`MessageID`, metadata, `TrackOpens`) — PR #77.
2. ✅ Webhook endpoint (`POST /webhooks/postmark`, basic-auth via
   `POSTMARK_WEBHOOK_SECRET`) + chart secret + `make configure-postmark-webhook`
   (idempotent create/update via the Postmark Webhooks API — no dashboard step).
   After deploy: create the AWS secret, then run the configure command per env.
3. UI status on pending invites.

## Open questions

- Bounce handling beyond display: auto-flag the invite for resend? (Postmark
  suppresses hard-bounced addresses on its side; a resend to the same address may
  need the suppression cleared in Postmark.)
- Do we care about `Click` (accept-link clicked) as a stronger signal than `Open`?
  Free to add later — same handler, one more `RecordType` case. Note Postmark link
  tracking rewrites URLs through their redirect — for an auth-bearing accept link
  that's a (mild) token-through-third-party concern; skip unless needed.
