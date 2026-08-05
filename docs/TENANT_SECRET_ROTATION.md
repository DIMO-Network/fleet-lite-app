# Tenant credential encryption — key rotation runbook

## What went wrong

`TENANT_SECRET_ENC_KEY` was **never set in production**. Not in
`fleet-lite-app-secret`, not in `fleet-lite-app-config`, and not templated in
`charts/` at all. The config field had no default and no validation.

An empty key is not "no encryption". `TenantService.encryptSecret` derives
`sha256(TENANT_SECRET_ENC_KEY)`, and `sha256("")` is a perfectly valid AES-256
key — a constant anyone can compute. `aes.NewCipher` accepts it, GCM seals
successfully, the ciphertext looks normal, nothing errors and nothing logs.

Every tenant's DIMO developer-license private key was encrypted at rest under a
publicly known key. That license is what grants access to all of that tenant's
vehicle data.

Measured 2026-08-05: **5 of 5 production tenants** had a populated
`dimo_api_key_enc`.

kaufmann-oracle was unaffected — its key is set and real.

## What changed

| Change | Where |
|---|---|
| `Settings.Validate()` refuses to boot on an empty key outside local | `internal/config/settings.go` |
| Called at startup, before subcommands | `cmd/fleet-lite-app/main.go` |
| Dual-key decrypt: current key, then the legacy empty key when `ALLOW_LEGACY_EMPTY_ENC_KEY` is set | `internal/service/tenant.go` |
| `reencrypt-tenant-secrets` CLI | `cmd/fleet-lite-app/reencrypt_tenant_secrets.go` |
| `TENANT_SECRET_ENC_KEY` in the ExternalSecret | `charts/.../secret.yaml` |
| `ALLOW_LEGACY_EMPTY_ENC_KEY` in values | `charts/.../values*.yaml` |

`Validate` is deliberately fatal. Silent weak crypto is worse than a failed
deploy — but it means **the order below is not optional**.

## Rollout

> ⚠️ Two ordering hazards, both of which cause an outage if ignored.
>
> 1. **The AWS Secrets Manager entry must exist before the chart is applied.**
>    A missing `remoteRef` fails the whole ExternalSecret, not just that key —
>    the pod loses `DB_PASSWORD` too.
> 2. **The key must be set before this code deploys.** `Validate` is fatal, so
>    deploying first means the app will not start.

### 1. Create the key in AWS Secrets Manager

```sh
# 32 bytes of randomness, base64. Any high-entropy string works — it is a
# passphrase, not a raw AES key (sha256 derives the key from it).
openssl rand -base64 32
```

Store at `prod/fleet-lite-app/tenant_secret_enc_key`. **Keep a copy** — losing it
means losing every tenant's stored credential.

> fleet-lite-app deploys to the **`prod` namespace only** — there is no dev
> deployment. The ExternalSecret key is namespace-scoped
> (`{{ .Release.Namespace }}/fleet-lite-app/...`), so `prod/...` is the only
> entry that needs to exist. ✅ created 2026-08-05.

Verify the ExternalSecret picks it up before going further:

```sh
kubectl get externalsecret fleet-lite-app-secret -n prod
kubectl get secret fleet-lite-app-secret -n prod \
  -o jsonpath='{.data.TENANT_SECRET_ENC_KEY}' | wc -c   # non-zero
```

### 2. Deploy with the legacy fallback on

Set in `charts/fleet-lite-app/values-prod.yaml`:

```yaml
ALLOW_LEGACY_EMPTY_ENC_KEY: "true"
```

Now reads try the real key first and fall back to `sha256("")` for the existing
rows; writes use the real key. Nothing breaks, no downtime.

Confirm tenants still resolve — the warning below appears once per legacy row:

```sh
kubectl logs -n prod deploy/fleet-lite-app -c fleet-lite-app | grep "legacy empty key"
```

### 3. Re-encrypt

Dry run first. It verifies every row can be decrypted and round-trips the
re-encryption, without writing:

```sh
kubectl exec -n prod deploy/fleet-lite-app -c fleet-lite-app -- \
  /fleet-lite-app reencrypt-tenant-secrets -from-empty-key -dry-run
```

Expect `to_reencrypt=5, already_current=0, no_key_stored=0`. If any row cannot
be decrypted the command aborts having written nothing — investigate rather than
forcing it.

Then for real:

```sh
kubectl exec -n prod deploy/fleet-lite-app -c fleet-lite-app -- \
  /fleet-lite-app reencrypt-tenant-secrets -from-empty-key
```

It writes in a single transaction: all rows or none. Re-running is safe — rows
already readable under the current key are skipped.

### 4. Turn the fallback off

```yaml
ALLOW_LEGACY_EMPTY_ENC_KEY: "false"
```

Redeploy (`values-prod.yaml`). The weak key stops being a valid way to read anything. The "legacy
empty key" warnings should be gone; if any appear, a row was missed — turn the
fallback back on and re-run step 3.

### 5. Clean up

Once prod is through step 4, delete the shim: the
`AllowLegacyEmptyEncKey` field, the fallback branch in `decryptSecret`, and its
tests. Grep for `AllowLegacyEmptyEncKey` — it exists only for this migration.

## Ordinary rotation later

The same machinery rotates any key without downtime, using `-from-key` instead
of `-from-empty-key`:

1. Add the new key, keep the old one available.
2. Deploy with the new key as `TENANT_SECRET_ENC_KEY`.
3. `reencrypt-tenant-secrets -from-key <old>`.
4. Retire the old key.

Step 2 needs a fallback path for the old key. Today the fallback is hardcoded to
the empty key; a general rotation would want a
`TENANT_SECRET_ENC_KEY_PREVIOUS` setting. Not built — add it when a real
rotation is scheduled rather than carrying unused generality.

## Should the credentials be treated as compromised?

They were readable by anyone with the ciphertext for as long as the key was
unset. Whether that warrants rotating the five DIMO developer licenses
themselves is a judgement call about who could have had database or backup
access. Re-encrypting protects them going forward; it does not undo prior
exposure.
