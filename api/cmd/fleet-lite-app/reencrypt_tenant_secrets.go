package main

import (
	"context"
	"flag"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// reencryptTenantSecretsCmd rewrites every tenant's encrypted DIMO API key from
// one encryption passphrase to another.
//
// Why this exists: TENANT_SECRET_ENC_KEY was unset in production, and an empty
// key is not "no encryption" — encryptSecret derives sha256("") which is a
// perfectly valid AES-256 key that anyone can compute. Every stored credential
// was protected by a public constant, with nothing errored and nothing logged.
//
// That migration is complete, and the runtime fallback that read legacy rows
// (ALLOW_LEGACY_EMPTY_ENC_KEY) has been removed — keeping it left the weak key
// a valid way to read every credential. This command remains as the recovery
// path and as the tool for ordinary key rotation.
//
// If a straggler row written under the empty key ever turns up, it now fails to
// decrypt at runtime rather than being silently readable. Recover it with
// `-from-empty-key`, which reads through DecryptSecretWith directly and does not
// depend on any app setting.
//
// For a real rotation (old key -> new key), the order matters: set
// TENANT_SECRET_ENC_KEY to the new key and deploy first, then run this with
// `-from-key <old>`. Rewriting before the app knows the new key breaks every
// tenant immediately.
type reencryptTenantSecretsCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	fromKey  string
	useEmpty bool
	dryRun   bool
	tenantID string
}

func (*reencryptTenantSecretsCmd) Name() string { return "reencrypt-tenant-secrets" }
func (*reencryptTenantSecretsCmd) Synopsis() string {
	return "re-encrypt tenant DIMO API keys from an old passphrase to TENANT_SECRET_ENC_KEY"
}
func (*reencryptTenantSecretsCmd) Usage() string {
	return `reencrypt-tenant-secrets [-from-key KEY | -from-empty-key] [-tenant-id ID] [-dry-run]:
	Decrypt each tenant's DIMO API key with the old passphrase and re-encrypt it
	with the current TENANT_SECRET_ENC_KEY.

	-from-empty-key   old passphrase is "" (the unset-key case this fixes)
	-from-key KEY     old passphrase, for ordinary key rotation
	-tenant-id ID     limit to one tenant
	-dry-run          verify every row can be decrypted and re-encrypted, write nothing

	Rows already readable under the current key are left alone, so re-running is
	safe. Nothing is written unless every row in scope succeeds.
  `
}

func (p *reencryptTenantSecretsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.fromKey, "from-key", "", "old encryption passphrase")
	f.BoolVar(&p.useEmpty, "from-empty-key", false, `old passphrase is "" (the unset-key case)`)
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all)")
	f.BoolVar(&p.dryRun, "dry-run", false, "verify only; write nothing")
}

func (p *reencryptTenantSecretsCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if p.fromKey == "" && !p.useEmpty {
		p.logger.Error().Msg("one of -from-key or -from-empty-key is required")
		return subcommands.ExitUsageError
	}
	if p.settings.TenantSecretEncKey == "" {
		p.logger.Error().Msg("TENANT_SECRET_ENC_KEY is empty — set the new key before re-encrypting")
		return subcommands.ExitFailure
	}
	if p.fromKey == p.settings.TenantSecretEncKey {
		p.logger.Error().Msg("old and new passphrases are identical; nothing to do")
		return subcommands.ExitUsageError
	}

	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	mods := []qm.QueryMod{}
	if p.tenantID != "" {
		mods = append(mods, dbmodels.TenantWhere.ID.EQ(p.tenantID))
	}
	tenants, err := dbmodels.Tenants(mods...).All(ctx, p.pdb.DBS().Reader)
	if err != nil {
		p.logger.Err(err).Msg("list tenants")
		return subcommands.ExitFailure
	}

	// Pass 1 — verify everything before writing anything. GCM authenticates, so a
	// successful Open proves the passphrase; a failure here means the row was not
	// written with the passphrase we were told to expect, and guessing would be
	// worse than stopping.
	type pending struct {
		tenant *dbmodels.Tenant
		newEnc string
	}
	var (
		todo    []pending
		already int
		empty   int
	)
	for _, t := range tenants {
		if !t.DimoAPIKeyEnc.Valid || t.DimoAPIKeyEnc.String == "" {
			empty++
			continue
		}
		if _, err := service.DecryptSecretWith(p.settings.TenantSecretEncKey, t.DimoAPIKeyEnc.String); err == nil {
			already++ // already under the current key — idempotent re-run
			continue
		}
		plain, err := service.DecryptSecretWith(p.fromKey, t.DimoAPIKeyEnc.String)
		if err != nil {
			p.logger.Error().Str("tenant_id", t.ID).Str("tenant", t.Name).
				Msg("cannot decrypt with either the old or the current key — aborting, nothing written")
			return subcommands.ExitFailure
		}
		newEnc, err := service.EncryptSecretWith(p.settings.TenantSecretEncKey, plain)
		if err != nil {
			p.logger.Err(err).Str("tenant_id", t.ID).Msg("re-encrypt failed — aborting")
			return subcommands.ExitFailure
		}
		// Round-trip before trusting it.
		if back, err := service.DecryptSecretWith(p.settings.TenantSecretEncKey, newEnc); err != nil || back != plain {
			p.logger.Error().Str("tenant_id", t.ID).Msg("round-trip verification failed — aborting")
			return subcommands.ExitFailure
		}
		todo = append(todo, pending{tenant: t, newEnc: newEnc})
	}

	p.logger.Info().
		Int("to_reencrypt", len(todo)).
		Int("already_current", already).
		Int("no_key_stored", empty).
		Int("tenants_total", len(tenants)).
		Bool("dry_run", p.dryRun).
		Msg("verification complete")

	if p.dryRun || len(todo) == 0 {
		return subcommands.ExitSuccess
	}

	// Pass 2 — write, all or nothing.
	tx, err := p.pdb.DBS().Writer.BeginTx(ctx, nil)
	if err != nil {
		p.logger.Err(err).Msg("begin transaction")
		return subcommands.ExitFailure
	}
	for _, it := range todo {
		it.tenant.DimoAPIKeyEnc = null.StringFrom(it.newEnc)
		if _, err := it.tenant.Update(ctx, tx, boil.Whitelist("dimo_api_key_enc", "updated_at")); err != nil {
			_ = tx.Rollback()
			p.logger.Err(err).Str("tenant_id", it.tenant.ID).Msg("update failed — rolled back, nothing written")
			return subcommands.ExitFailure
		}
	}
	if err := tx.Commit(); err != nil {
		p.logger.Err(err).Msg("commit failed")
		return subcommands.ExitFailure
	}

	p.logger.Info().Int("reencrypted", len(todo)).Msg("done — every row now reads under TENANT_SECRET_ENC_KEY")
	return subcommands.ExitSuccess
}
