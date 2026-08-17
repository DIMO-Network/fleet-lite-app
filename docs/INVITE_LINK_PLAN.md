# Shareable Invitation Links — Investigation + Plan (fleet-lite-app)

> **SUPERSEDED 2026-08-17 — implemented here, then moved out.** The invitation
> lifecycle now lives entirely in **fleet-tenancy-api**: it mints the token,
> sends the email, receives Postmark's delivery webhooks, and writes the
> membership at accept. P4 of that repo's
> `docs/plans/04-invitations-into-tenancy.md` deleted this app's local
> implementation, its Postmark integration and its webhook route. This
> document is kept for the reasoning behind the design, not as a description
> of code in this repo.

Let an owner get the accept link for an invitation **in the UI** and share it
themselves — Slack, WhatsApp, in person — instead of relying on the invitation
email reaching the invitee.

Motivating cases:
- Email delivery is broken or the invite **bounced** (now visible as a red badge
  on the pending row — see `docs/POSTMARK_WEBHOOK_PLAN.md`), and the owner needs
  a way through *right now*.
- The owner is sitting next to the person and just wants to grant access without
  an email round-trip at all.

Status: **investigated, not built** (2026-08-03). Builds on
`docs/MEMBER_INVITATIONS_PLAN.md` and `docs/GROUP_ACCESS_PLAN.md`.

---

## Investigation findings

**The raw token is unrecoverable after creation.** `generateInviteToken()`
(`api/internal/service/invitation.go`) returns the token and its SHA-256 hash;
only `token_hash` is persisted. The plaintext exists in memory during
`Create`/`Resend` and then only inside the emailed URL.

This is deliberate — the database never holds a usable credential — and it is
the single constraint that shapes everything below:

> **There is no way to display the link for an invitation created earlier.
> Surfacing a link for an existing invite necessarily mints a new token, which
> invalidates the emailed one.**

**Resend already does exactly that.** `Resend` mints a fresh token, overwrites
`token_hash`, renews `expires_at`, and kills the previous link. So
"regenerate the link" is not a new hazard in this codebase — it is the existing
semantics of the button next to it. The UI just has to say so.

**The accept page already guards the dangerous part.** `web/src/accept-invite.ts`
shows *which account* the invite will bind to and requires an explicit confirm
before consuming the token — added after the 2026-07-08 incident where a shared
wallet silently consumed someone else's invite. That confirm step is precisely
the guard an out-of-band link needs, and it is already shipped.

**But the invite's email is never checked at accept time.** `Accept` compares
nothing about the caller against `invitations.email` — and it can't practically:
the DIMO JWT carries no email (the roster's email comes from the OAuth redirect
via the client, which is untrusted for authorization). The confirm screen
*displays* identity; it does not *verify* it. Any signed-in DIMO account holding
the link can accept.

---

## Options considered

### A. Return the link in the create/resend response

`Create` already has the raw token in scope; add `acceptUrl` to the response and
show it in an "Invitation created" modal with a copy button.

- Cheapest — one response field plus a modal.
- **Only works in that one moment.** Navigate away and the link is gone for good.
  Does nothing for "the invite I sent Tuesday bounced".
- Puts a live credential in a response body that also feeds the create path's
  normal logging/proxying.

### B. A "Get link" action that mints a fresh token — **recommended**

New endpoint `POST /tenants/:id/invitations/:invID/link`: mechanically `Resend`
without the Postmark call — regenerate the token, update `token_hash` and
`expires_at`, return `acceptUrl`. Owner-only, pending invites only.

- Works retroactively on any pending invite, which is the actual need.
- Cost — invalidating the emailed link — is real but already precedented by
  Resend, and can be stated plainly in the modal.
- One endpoint also covers option C.

### C. Invite-without-email

A "create a link instead of sending an email" mode in `invite-member-modal`,
skipping Postmark entirely, for the "just share access" case where email was
never wanted. This is B's endpoint plus a create-mode branch — worth folding in
later, not needed for the first cut.

**Recommendation: B, with C as a follow-up.**

---

## Design (option B)

### API

`POST /tenants/:id/invitations/:invID/link` → `{ "acceptUrl": "https://…", "expiresAt": "…" }`

- Owner-gated via the existing `requireOwner`.
- Pending invitations only; anything else → 404 `ErrInviteInvalid`, matching
  `Resend`.
- **POST, not GET** — keeps the token out of access logs, browser history and
  referrer headers.

### Service

Extract the token-refresh half of `Resend` into a shared helper — both paths
mint a token, overwrite `token_hash`, renew `expires_at`, and clear the email
tracking columns; only the Postmark call differs:

```go
// refreshToken mints a new single-use token for a pending invitation and
// returns the plaintext. The previous link dies here.
func (s *InvitationService) refreshToken(ctx context.Context, inv *dbmodels.Invitation) (string, error)

// GenerateLink refreshes the token and returns the accept URL without sending
// an email — for owners sharing access out-of-band.
func (s *InvitationService) GenerateLink(ctx context.Context, tenantID, invID string) (string, time.Time, error)
```

Clearing the email-tracking columns matters here for the same reason it does on
resend: the emailed message's `Delivered` badge must not linger over an invite
whose live link is now a copy-pasted one. A generated-link invite correctly
reads **Not sent**.

Log the action (`invite flow: accept link generated out-of-band`) with tenant,
invitation and requesting wallet — **never the token or the URL**.

### UI

- "Get link" button on pending invite rows in `tenant-members.ts`, alongside
  Resend / Revoke.
- New `invite-link-modal.ts`: the URL in a read-only field, a copy button, and
  copy that is explicit about both consequences:
  - *"Anyone with this link can join as **member** with access to **2 groups**."*
    — surface the actual role and group scope from the invitation.
  - *"This replaces the link already emailed to someone@example.com."*
- Show the expiry alongside it.
- New strings need the `localize:extract` → translate → `localize:build`
  round-trip (see `docs/LOCALIZATION.md`).

---

## Security considerations

The link is a **bearer credential**: whoever holds it joins the tenant at the
invited role and group scope. Email at least implies delivery to a claimed
address; a pasted link has no binding to the intended invitee whatsoever.

What limits the blast radius today:

- **The accept-page confirm step** (already shipped) — the holder sees which
  account they are about to bind and must confirm. This is the main mitigation
  and the reason this feature is viable at all.
- **Single-use** — the first account to accept consumes it.
- **7-day expiry** (`INVITE_EXPIRY_HOURS`) and **revocable** at any time.
- **Group scope is baked into the invitation**, so a leaked link grants a
  bounded slice, not the whole tenant — unless the invite was for `owner`.

What does *not* limit it:

- **No email binding at accept.** See findings above. A link shared into the
  wrong channel can be accepted by anyone signed into DIMO.

Guardrails to build with it:

- POST-only; never log the token or the accept URL; HTTPS only (already true).
- Modal copy that names the role and group scope being handed out, so the owner
  sees the size of what they are pasting.
- Consider blocking link generation for `owner`-role invitations, or at minimum
  a distinct warning — an owner invite grants everything, permanently.
- Consider stamping `link_generated_at` / `link_generated_by` on `invitations`
  so there is a trace that a credential left the system out-of-band. The table
  has no per-action audit today.

---

## Rollout order

1. Service: extract `refreshToken`, add `GenerateLink`.
2. Controller + route + owner gate; unit-test the pending-only guard.
3. `tenant-service.ts` method, `invite-link-modal.ts`, wire the row button.
4. Localization round-trip.

Estimated at roughly half a day including translation.

## Open questions

- **Block link generation for owner invites?** Leaning yes — the downside of a
  leaked owner link is unbounded.
- **Should generating a link revoke the emailed one loudly?** It already does so
  silently (token overwrite). Options: leave it, or mark `email_status` in a way
  that renders as "link shared manually" rather than "Not sent".
- **Is per-link audit worth a migration?** Only if fleets start sharing links
  routinely; revisit after usage.
- **Fold in option C (invite-without-email)?** Natural follow-up once the
  endpoint exists — the invite modal grows a "create link" mode and skips
  Postmark, which also makes local dev (where sending is disabled) usable
  end-to-end without a mail server.
