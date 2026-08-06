# The on-chain surface, and where web2 takes over

## The position

**On-chain does two things. Everything else is web2.**

1. The vehicle NFT + synthetic device exist, **owned by the operator's user
   account**.
2. One SACD grant at mint, to the **operator's developer license**.

Sub-tenants get **no SACD grants at all**. Their access is decided entirely by
the shared tenancy service. Which customer sees a vehicle, who their users are,
what groups it's in, and when access is revoked — all web2.

That is the intended design, not a compromise. Access control that changes
weekly shouldn't require a passkey prompt and a transaction.

## What's on chain today (already)

Not a change — it's what the mint path already does.

`kaufmann-oracle/internal/onboarding/onboard.go:286`:

```go
Owner:           args.Owner,           // wallet addr of frontend user
VehicleOwnerSig: vehicleMintSignature, // signed payload from frontend user
```

The frontend user is the operator's staff member signing with their passkey in
`b2b-fleet-mgr-app`. So **on-chain ownership already sits with the operator's
account**, not the customer's.

`MintVehicleAndSDWithDDAndSACD` attaches the SACD grant built in
`web/src/elements/add-vin-element.ts`.

## Why the operator's SACD grant still has to exist

It would be tempting to drop SACD entirely. It can't go, for one mechanical
reason:

fleet-lite enumerates a fleet with `vehicles(filterBy: {privileged: clientID})`,
and identity-api's `privileged` filter is driven by **SACD grants, not
ownership**. Without a grant to the operator's developer license, the operator
can't list its own fleet from identity-api even though it owns every vehicle.

So the grant's job is **enumeration and data access for the operator**. One
grantee, set once at mint, never touched again. It is not, and should never
become, a per-customer authorization mechanism — that's what the tenancy service
is for.

## What that means for the onboarding UI

`add-vin-element.ts` currently asks whoever is onboarding a vehicle to pick
grantees: a checkbox list seeded from the tenant's `dimo_client_id`, plus a
free-text "use below" override.

Under the operator model that decision always has the same answer.

**Target behaviour:**

- **Default, always:** grant to the current operator tenant's DIMO client id,
  resolved automatically from the operator tenant's credentials. No user input.
- **Advanced:** keep the grantee picker behind a collapsed expander for the
  cases that still need it — legacy self-serve tenants, a customer who has their
  own developer license, a one-off external grantee.

A vehicle minted with the wrong grantee is invisible to the operator and needs
an on-chain fix to recover, so removing the everyday opportunity for that
mistake is worth doing on its own merits, independent of the tenancy work.

## Customer offboarding

With operator-held ownership this is much smaller than it first looked:

| Situation | Operation |
|---|---|
| Customer stops using the product; operator keeps the vehicles | Revoke entitlements. Pure web2 — one table update, effective within the authz cache window |
| Customer leaves and takes vehicles with them | Existing transfer flow: `/vehicle/transfer` and `/vehicle/transfer/shared` in b2b, which already handle on-chain ownership change and shared-account signing |
| Customer wants direct DIMO API access while staying | Provision them their own developer license and add a second SACD grantee. The tenant already supports its own `tenant_credentials` row |

None of these require a per-vehicle re-SACD exercise.

The remaining loose end is Q3: accounts created on a customer's behalf register
the **operator's** signer as `providedSignerAddress`, so the operator can sign
for those users indefinitely. That needs a revocation story, but it's an
accounts-api question rather than a tenancy one.

## Considered and dropped: entitlement attestations

An earlier draft proposed publishing a `dimo.document.vehicle.tenancy`
CloudEvent alongside each entitlement row, recording which tenant a vehicle is
shared with — signed, durable, no gas, reconstructable the way
`import-group-attestations` rebuilds groups.

**Dropped.** It doesn't earn its complexity:

- It's a **record, not a gate**. Enforcement stays in `AssertEntitled` either
  way, so it buys no isolation.
- The database is already the source of truth and is backed up. "Reconstructable
  from attestations" solves a problem we don't have.
- It adds a CE type, a publish path, a reconcile path and a schema decision to
  every entitlement change — for an audit trail that an ordinary audit table
  gives us more cheaply and with better query ergonomics.

If an external, signed, third-party-verifiable record of customer access is ever
a real requirement (a compliance ask, a customer dispute), revisit it then. It's
additive and nothing in the design blocks it.

Recorded here so it doesn't get re-proposed from first principles.

## Direction of travel

The on-chain surface is two things, both set once at mint by a flow that already
exists and already works.

- **If DIMO's SACD story changes**, or fleet enumeration gets a different
  mechanism, nothing in the tenancy design moves. Entitlements are already web2.
- **If a customer ever needs chain-enforced access** — a large account, a
  compliance requirement — the architecture supports their own developer license
  and a second SACD grantee without restructuring.
- **If we want to reduce chain usage further**, what's left is ownership (needed
  for the asset to exist) and one grant (needed for enumeration). There isn't
  much to remove.

The shape: **the chain records what a vehicle is and who owns it; web2 records
who may look at it.**
