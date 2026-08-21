package main

import (
	"context"
	"flag"
	"sort"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/subcommands"
	"github.com/rs/zerolog"
)

// vehiclesDiffCmd compares fleet-tenancy-api's active entitled set for each
// explicit-mode tenant against this app's local vehicles rows — the same shape
// as groups-diff and tenancy-diff, for the set that the 2026-08-19 empty-fleet
// incident showed nobody was watching.
//
// Unlike the group diffs this repo used to carry the comparison is symmetric, and both directions are
// failures. syncEntitledVehicles both upserts the entitled set and prunes
// everything outside it, so after a successful sync the two sides are equal by
// construction. Any inequality means the sync did not run, did not finish, or
// was skipped:
//
//	missing_local  entitled, no local row — the incident's own signature. Can
//	               also mean the entitlement stands while the SACD share does
//	               not (sync warns "entitled vehicles missing from the
//	               operator's privileged set" and writes no row). Both need a
//	               human, so both fail.
//	extra_local    a local row no longer entitled — a revocation that was never
//	               pruned. This is what makes a customer's list show a vehicle
//	               the operator took away.
//
// Tenants holding their own DIMO client id are implicit-mode: their fleet comes
// from the developer license, not from an entitlement set, and there is nothing
// here to compare. They are counted and named rather than silently passed over.
type vehiclesDiffCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	verbose  bool
}

func (*vehiclesDiffCmd) Name() string { return "vehicles-diff" }
func (*vehiclesDiffCmd) Synopsis() string {
	return "compare local vehicles against fleet-tenancy-api's entitled set"
}
func (*vehiclesDiffCmd) Usage() string {
	return `vehicles-diff [-tenant-id <uuid>] [-verbose]:
	Walks every credential-less (operator-managed) tenant, confirms it is
	explicit-mode, and compares GET /v1/tenants/{id}/vehicles — the active
	entitled set — against the local vehicles rows.

	Verdicts per token id:
	  agree          entitled and present locally
	  missing_local  entitled, no local row — FAILURE. Either the sync was
	                 skipped, or the entitlement stands while the SACD share
	                 does not.
	  extra_local    local row outside the entitled set — FAILURE, a revoked
	                 entitlement that was never pruned.

	Exits non-zero on any missing_local or extra_local. Run it after
	sync-vehicles, and any time a fleet looks wrong.
  `
}

func (p *vehiclesDiffCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant")
	f.BoolVar(&p.verbose, "verbose", false, "log agreeing token ids too")
}

// vehicleDiff is one tenant's comparison outcome, bucketed by token id.
type vehicleDiff struct {
	Agree        []int64
	MissingLocal []int64
	ExtraLocal   []int64
}

// compareVehicleSets buckets one tenant's token ids. Pure so the verdict logic
// is testable without a database or a service.
func compareVehicleSets(entitled, local []int64) vehicleDiff {
	inLocal := make(map[int64]bool, len(local))
	for _, id := range local {
		inLocal[id] = true
	}
	inEntitled := make(map[int64]bool, len(entitled))
	for _, id := range entitled {
		inEntitled[id] = true
	}

	var d vehicleDiff
	for id := range inEntitled {
		if inLocal[id] {
			d.Agree = append(d.Agree, id)
		} else {
			d.MissingLocal = append(d.MissingLocal, id)
		}
	}
	for id := range inLocal {
		if !inEntitled[id] {
			d.ExtraLocal = append(d.ExtraLocal, id)
		}
	}

	sortInt64s(d.Agree)
	sortInt64s(d.MissingLocal)
	sortInt64s(d.ExtraLocal)
	return d
}

func sortInt64s(s []int64) { sort.Slice(s, func(i, j int) bool { return s[i] < s[j] }) }

func (p *vehiclesDiffCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	tenancyAPI := gateway.NewTenancyAPI(p.logger, &p.settings, authProvider)
	if !tenancyAPI.Configured() {
		p.logger.Error().Msg("no tenancy client configured — there is nothing to compare against")
		return subcommands.ExitFailure
	}

	// Credential-less tenants only: the operator-managed population, whose
	// fleet is an entitlement set rather than a developer license.
	mods := []qm.QueryMod{
		qm.Expr(
			dbmodels.TenantWhere.DimoClientID.IsNull(),
			qm.Or2(dbmodels.TenantWhere.DimoClientID.EQ(null.StringFrom(""))),
		),
		qm.OrderBy(dbmodels.TenantColumns.Name),
	}
	if p.tenantID != "" {
		// An explicit -tenant-id names one tenant on purpose; do not filter it
		// out on credentials, report what it actually is.
		mods = []qm.QueryMod{dbmodels.TenantWhere.ID.EQ(p.tenantID)}
	}
	tenants, err := dbmodels.Tenants(mods...).All(ctx, p.pdb.DBS().Reader)
	if err != nil {
		p.logger.Err(err).Msg("list tenants")
		return subcommands.ExitFailure
	}

	var totalAgree, totalMissing, totalExtra, checkedTenants int
	var notExplicit []string

	for _, t := range tenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, t.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", t.ID).Msg("load tenant")
			return subcommands.ExitFailure
		}

		// The mode read is what disambiguates the entitlement endpoint's
		// 200 [] — the same trap syncEntitledVehicles guards. Treating an
		// implicit tenant's empty list as an empty entitled set would report
		// every one of its vehicles as extra_local.
		detail, derr := tenancyAPI.TenantDetail(ctx, t.ID)
		if derr != nil {
			p.logger.Err(derr).Str("tenant_id", t.ID).Msg("resolve tenant from tenancy")
			return subcommands.ExitFailure
		}
		if detail.EntitlementMode != "explicit" {
			notExplicit = append(notExplicit, t.ID)
			p.logger.Info().Str("tenant_id", t.ID).Str("tenant", tenant.Name).
				Str("entitlement_mode", detail.EntitlementMode).
				Msg("not explicit-mode — no entitled set to compare, skipping")
			continue
		}

		ents, eerr := tenancyAPI.Entitlements(ctx, *tenant)
		if eerr != nil {
			p.logger.Err(eerr).Str("tenant_id", t.ID).Msg("entitlements call failed")
			return subcommands.ExitFailure
		}
		entitled := make([]int64, 0, len(ents))
		for _, e := range ents {
			entitled = append(entitled, e.VehicleTokenID)
		}

		local, lerr := readLocalVehicleTokenIDs(ctx, p.pdb.DBS().Reader, t.ID)
		if lerr != nil {
			p.logger.Err(lerr).Str("tenant_id", t.ID).Msg("read local vehicles")
			return subcommands.ExitFailure
		}

		d := compareVehicleSets(entitled, local)
		checkedTenants++
		totalAgree += len(d.Agree)
		totalMissing += len(d.MissingLocal)
		totalExtra += len(d.ExtraLocal)

		ev := p.logger.Info()
		if len(d.MissingLocal) > 0 || len(d.ExtraLocal) > 0 {
			ev = p.logger.Error()
		}
		ev = ev.Str("tenant_id", t.ID).Str("tenant", tenant.Name).
			Int("entitled", len(entitled)).Int("local", len(local)).
			Int("agree", len(d.Agree)).
			Int("missing_local", len(d.MissingLocal)).
			Int("extra_local", len(d.ExtraLocal))
		if len(d.MissingLocal) > 0 {
			ev = ev.Ints64("missing_local_token_ids", d.MissingLocal)
		}
		if len(d.ExtraLocal) > 0 {
			ev = ev.Ints64("extra_local_token_ids", d.ExtraLocal)
		}
		if p.verbose {
			ev = ev.Ints64("agree_token_ids", d.Agree)
		}
		ev.Msg("vehicles diff")
	}

	summary := p.logger.Info()
	if totalMissing > 0 || totalExtra > 0 {
		summary = p.logger.Error()
	}
	if len(notExplicit) > 0 {
		summary = summary.Strs("not_explicit_mode", notExplicit)
	}
	summary.Int("tenants", checkedTenants).
		Int("agree", totalAgree).
		Int("missing_local", totalMissing).
		Int("extra_local", totalExtra).
		Msg("vehicles diff complete")

	if totalMissing > 0 || totalExtra > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// readLocalVehicleTokenIDs loads one tenant's local vehicle token ids — always
// from the local table, whatever any read-path flag says, because the local
// side IS one side of this comparison.
func readLocalVehicleTokenIDs(ctx context.Context, exec boil.ContextExecutor, tenantID string) ([]int64, error) {
	rows, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		qm.Select(dbmodels.VehicleColumns.TokenID),
	).All(ctx, exec)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.TokenID)
	}
	return out, nil
}
