# Member Invitations — Implementation Plan (fleet-lite-app)

> **SUPERSEDED 2026-08-17 — implemented here, then moved out.** The invitation
> lifecycle now lives entirely in **fleet-tenancy-api**: it mints the token,
> sends the email, receives Postmark's delivery webhooks, and writes the
> membership at accept. P4 of that repo's
> `docs/plans/04-invitations-into-tenancy.md` deleted this app's local
> implementation, its Postmark integration and its webhook route. This
> document is kept for the reasoning behind the design, not as a description
> of code in this repo.

Add **email-based member invitations** to the Members view, using a **token accept-link**
flow and **Postmark** as the transactional email provider. Keep the existing
"paste a wallet address" add path intact.

Reference patterns:
- **b2b-fleet-mgr-app** — email-driven onboarding UI (`web/src/views/create-user.ts`),
  but it is a UI+proxy only; the real work happens upstream.
- **kaufmann-oracle** — backend that turns an email into an account and sends a
  transactional email via Customer.io (`internal/gateway/customer_io.go`,
  `internal/controllers/account.go`). We mirror its *structure* (gateway + service +
  controller) but swap Customer.io → Postmark and use a **token/accept** flow instead of
  direct account creation (fleet-lite has no DIMO Accounts API integration).

---

## Why a token accept-link (not kaufmann's direct-create)

kaufmann calls the external **DIMO Accounts API** to mint a wallet for an email, then adds
that wallet to the tenant immediately. **fleet-lite has no Accounts API integration** — it
derives the member's wallet from their own DIMO JWT at login time (`ethereum_address`
claim) and records their email on first login via `TouchLogin` (`POST /tenants/:id/login`).

So the natural fit is a classic invite:

```
Owner enters email + role in Members view
  → POST /tenants/:id/invitations {email, role}           (owner-only)
  → invitations row {token_hash, email, role, expires_at, status=pending}
  → Postmark (withTemplate) sends link: {APP_BASE_URL}/accept-invite.html?token=XYZ
  → invitee opens link → logs in with their DIMO passkey
  → frontend calls POST /invitations/accept {token}        (JWT-auth, NOT membership-gated)
  → backend: validate token (pending + not expired)
             → tenantSvc.AddMember(invitee JWT wallet, invitation.role)
             → record invitee email, mark invitation accepted, store invitee_wallet
```

The token is the bearer credential; the wallet is discovered from the invitee's JWT on accept.

---

## What already exists in fleet-lite (reuse — do not rebuild)

| Capability | Location | Notes |
|---|---|---|
| Web framework (Fiber v2) | `api/internal/app/app.go` | `authApp` = JWT only; `tenantApp` = JWT + tenant-membership middleware |
| Members table | `tenant_users (tenant_id, wallet, role, created_at, updated_at, last_login_at, email)` | invite adds rows here on accept |
| Add/upsert member | `service/tenant.go` `AddMember(ctx, tenantID, wallet, role)` | reuse verbatim on accept |
| Membership role lookup | `service/tenant.go` `GetMembershipRole`; controller `requireMember` (`controllers/tenants.go`) | owner-gate create/list/revoke |
| JWT wallet extraction | `controllers/common.go` `GetWalletAddressFromJWT(c)` | used by accept |
| Login/email capture | `service/tenant.go` `TouchLogin`; `POST /tenants/:id/login` | email comes from OAuth redirect (client-supplied) |
| Outbound HTTP gateway pattern | `gateway/fetch_api.go` | mirror for `postmark_api.go` |
| Config loader | `internal/config/settings.go` (YAML tags, `settings.sample.yaml`) | add Postmark fields |
| DB tooling | goose v3 migrations (`api/internal/db/migrations`), sqlboiler v4 (`internal/db/models`) | `make addmigration`, `make migrate`, `make sqlboiler` |
| CLI framework | `cmd/fleet-lite-app/main.go` (google/subcommands) | home for the template-push command |
| Frontend router | `web/src/elements/app-root.ts` (`@lit-labs/router`, hash routes) | pre-login pages are standalone HTML (`login.html`) |
| HTTP client | `web/src/services/api-service.ts` | auto-attaches `Authorization` + `Tenant-Id`; `get(ep, false)` for unauth |
| Members UI | `web/src/elements/tenant-members.ts` (Lit), service `web/src/services/tenant-service.ts` | add invite form + pending list here |
| Login flow | `web/src/elements/login-element.ts`, `web/login.html` | DIMO OAuth → redirect with `?token=&email=`; JWT in `localStorage.token` |
| Localization | `@lit/localize`, `msg()` / `str` | wrap all new copy |

---

## Decisions (locked — 2026-06-17)

1. **Invite mechanism:** token accept-link (above). No DIMO Accounts API integration.
2. **Keep wallet-paste:** the existing `POST /tenants/:id/members {wallet, role}` add path stays;
   email invite is added *alongside* it.
3. **Role at invite time:** owner picks `member | owner` in the invite form; stored on the
   invitation row, applied verbatim by `AddMember` on accept. (Only owners can invite, so only an
   owner can grant owner — same as today.)
4. **Postmark templates = repo source of truth, pushed via API.** Template bodies live in
   `api/templates/postmark/` and are pushed to Postmark by alias via a make target. Sending uses
   `POST /email/withTemplate` with `TemplateAlias` + `TemplateModel` (no HTML in Go).
5. **Accept is JWT-auth, NOT membership-gated.** It lives on `authApp` and is keyed by the **token
   only** (no tenant in the path) — the invitee is not yet a member, and the token resolves the
   tenant. *(Correction to the obvious-but-wrong version that hangs accept off `tenantApp`/`:id`.)*
6. **Token storage:** generate ~32 bytes of entropy; store only its **hash** (`token_hash`),
   unique-indexed. Single-use + expiry-enforced.
7. **Bare (non-schema-qualified) table names** in the migration — match existing migrations; the
   schema is resolved at runtime via `search_path` (DB_NAME). `make sqlboiler` strips the prefix.

### Decision (added 2026-06-28) — locale-aware invitation emails

8. **The invite email is sent in the inviter's active UI language** (English or Spanish — the two
   locales the app ships, see [LOCALIZATION.md](LOCALIZATION.md)). One Postmark template per locale:
   - **English** → alias `fleet-invitation` (the base alias in `POSTMARK_INVITATION_TEMPLATE_ALIAS`).
   - **Spanish** → alias `fleet-invitation-es` (base alias **+ `-es`**, derived in code — no extra
     config value).
   Bodies live in the repo as `invitation.html`/`.txt` (en) and `invitation.es.html`/`.es.txt` (es)
   and are pushed by `make push-postmark-templates` (manifest lists both).
   - **Locale is passed per request, not persisted.** The frontend sends the active locale
     (`getLocale()` from `localization.ts`) on **create** and on **resend**; both are always
     UI-initiated, so there's no code path that sends without a caller-supplied locale and **no DB
     column is needed**. Resend therefore uses the owner's *current* language (intended).
   - Unknown/empty locale falls back to English.

---

## Data model

New migration `…_invitations.sql` (via `make addmigration`):

```sql
CREATE TABLE IF NOT EXISTS invitations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    email            TEXT NOT NULL,
    role             TEXT NOT NULL DEFAULT 'member',
    token_hash       TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',   -- pending | accepted | revoked | expired
    invited_by_wallet VARCHAR(43) NOT NULL,
    invitee_wallet   VARCHAR(43),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ NOT NULL,
    accepted_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_token_hash ON invitations (token_hash);
CREATE INDEX IF NOT EXISTS idx_invitations_tenant_id ON invitations (tenant_id);
CREATE INDEX IF NOT EXISTS idx_invitations_status ON invitations (status);
```

Then `make sqlboiler` → `internal/db/models/invitations.go`.

---

## Backend changes (`api/`)

| # | Area | File | Work |
|---|---|---|---|
| 1 | Migration | `internal/db/migrations/…_invitations.sql` | table above (no schema prefix) |
| 2 | Model | `internal/db/models/invitations.go` | `make sqlboiler` (generated) |
| 3 | Postmark gateway | `internal/gateway/postmark_api.go` (new) | mirror `fetch_api.go`; `SendInvitation(toEmail, templateAlias, model)` → `POST /email/withTemplate` w/ `X-Postmark-Server-Token` (alias is a **param**, chosen by the caller's locale); `UpsertTemplate(...)` for the push command |
| 4 | Invitation service | `internal/service/invitation.go` (new) | `Create`/`Resend` take a `locale`; resolve alias (`fleet-invitation` / `…-es`) and send; `Accept` (validate, `AddMember`, mark accepted), `List`, `Revoke`; injects `*TenantService` + Postmark gateway |
| 5 | Controller | `internal/controllers/invitations.go` (new) | Create/List/Revoke use `requireMember` + owner check (mirror `AddMember`); Create/Resend read `locale` from the request body; `Accept` uses only `GetWalletAddressFromJWT` |
| 6 | Routes | `internal/app/app.go` | `tenantApp`: `POST/GET /tenants/:id/invitations`, `DELETE /tenants/:id/invitations/:invID`. `authApp`: `POST /invitations/accept` |
| 7 | Config | `internal/config/settings.go`, `settings.sample.yaml` | `POSTMARK_SERVER_TOKEN` (secret), `INVITATION_FROM_EMAIL`, `POSTMARK_INVITATION_TEMPLATE_ALIAS` (default `fleet-invitation`), `APP_BASE_URL`, `INVITE_EXPIRY_HOURS` (default 168) |
| 8 | Wiring | `cmd/fleet-lite-app/main.go` / `app.go` | construct Postmark gateway + invitation service/controller |

### Endpoints

| Method | Path | Group | Auth | Body |
|---|---|---|---|---|
| POST | `/tenants/:id/invitations` | tenantApp | owner | `{email, role}` |
| GET | `/tenants/:id/invitations` | tenantApp | member | — (lists pending/recent) |
| DELETE | `/tenants/:id/invitations/:invID` | tenantApp | owner | — (revoke) |
| POST | `/invitations/accept` | **authApp** | any logged-in JWT | `{token}` |

---

## Postmark templates (repo as source of truth)

```
api/templates/postmark/
  manifest.json          # [{ alias, subject, htmlFile, textFile }] — one entry per locale
  invitation.html        # en — uses {{tenant_name}}, {{accept_url}}, {{inviter}}, {{expires_in}}
  invitation.txt
  invitation.es.html     # es — same {{mustache}} variables, Spanish copy
  invitation.es.txt
```

The two locales map to two aliases: `fleet-invitation` (en) and `fleet-invitation-es` (es). Both
use the **same `TemplateModel` variables**, so only the copy differs.

- **Push:** `make push-postmark-templates` → small Go subcommand in `cmd/fleet-lite-app`
  that upserts each template by **alias** via Postmark's Templates API (`POST/PUT /templates`).
  Rerunnable per environment (uses the same `POSTMARK_SERVER_TOKEN`). Pushes **both** aliases.
- **Send:** the service resolves the alias from the request locale (en → base alias, es → base
  `+ -es`) and passes it to the gateway, which calls `POST /email/withTemplate` with that
  `TemplateAlias` + `TemplateModel { tenant_name, accept_url, inviter, expires_in }`. No HTML in Go.

---

## Frontend changes (`web/`)

| # | File | Work |
|---|---|---|
| 1 | `src/elements/tenant-members.ts` | add "Invite by email" form (email + role select) beside the wallet input; add a **Pending invitations** list with resend/revoke (owner-only) |
| 2 | `src/services/tenant-service.ts` | `createInvitation(tenantId, email, role, locale)`, `listInvitations(tenantId)`, `revokeInvitation(tenantId, invId)`, `resendInvitation(tenantId, invId, locale)`, `acceptInvitation(token)`. `create`/`resend` send the active `getLocale()` in the body |
| 3 | `accept-invite.html` (new, pre-login) | standalone page like `login.html`: if no JWT, bounce to DIMO login carrying the token through the redirect; after login, `POST /invitations/accept {token}`, then redirect into the tenant. Wire route if a hash route is needed. |
| 4 | Localization | wrap new copy in `msg()` / `str`; regenerate locale bundles |

---

## Security / edge notes

- **Token = bearer credential.** Whoever holds the link can accept. Store only the hash; ~32B entropy.
- **Email match is best-effort.** DIMO's JWT carries no email; the email comes from the OAuth
  redirect (client-supplied), so we cannot hard-enforce "logged-in email == invited email." The
  token is the real gate; the invited email is recorded for display only.
- **Single-use + expiry.** Accept flips `pending → accepted`; expired/revoked tokens reject.
  Re-inviting an existing member no-ops gracefully.
- **Owner-only issue/revoke**, mirroring the existing `AddMember`/`RemoveMember` authz.

---

## Build order

1. Migration → `make sqlboiler` model.
2. Config fields + sample YAML.
3. Postmark gateway (`withTemplate` send + `UpsertTemplate`).
4. Invitation service → controller → routes/wiring. Verify with curl.
5. Template files + `push-postmark-templates` command/make target.
6. Frontend: service methods → invite form + pending list → `accept-invite.html`.
7. Localization pass.
