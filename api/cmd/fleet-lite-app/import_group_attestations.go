package main

import (
	"context"
	"flag"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// importGroupAttestationsCmd pulls vehicle group-membership attestations from
// fetch-api and merges them into the local DB. It is the "pull" half of the
// sync: groups any producer has attested for a vehicle are added to the local
// membership set. The merge itself lives in service.GroupSyncService so the
// lazy per-vehicle endpoint shares the exact same semantics (additive,
// de-duplicated, never removes — see docs/GROUP_SYNC.md).
//
// Activity tiering: -warm-only restricts the run to "warm" tenants (a member
// logged in within -warm-days days). The Helm chart runs a daily warm pass and
// a weekly full pass so dormant fleets still converge within a week.
type importGroupAttestationsCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	tokenID  int64
	dryRun   bool
	warmOnly bool
	warmDays int
}

func (*importGroupAttestationsCmd) Name() string { return "import-group-attestations" }
func (*importGroupAttestationsCmd) Synopsis() string {
	return "pull vehicle group attestations from fetch-api and merge them into the DB (additive, de-duplicated)"
}
func (*importGroupAttestationsCmd) Usage() string {
	return `import-group-attestations [-tenant-id ID] [-token-id N] [-warm-only] [-warm-days N] [-dry-run]:
	Pulls dimo.document.vehicle.groups attestations per vehicle (latest per
	producer) and adds any group membership not already present in
	vehicle_fleet_groups. Additive only — never removes; de-duplicated by primary
	key. Unknown groups are auto-created. -warm-only limits the run to tenants with
	a member login within -warm-days days (default 7). -dry-run logs changes
	without writing.
  `
}

func (p *importGroupAttestationsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
	f.Int64Var(&p.tokenID, "token-id", 0, "only this vehicle token id")
	f.BoolVar(&p.dryRun, "dry-run", false, "log what would change without writing")
	f.BoolVar(&p.warmOnly, "warm-only", false, "only tenants with a recent member login (see -warm-days)")
	f.IntVar(&p.warmDays, "warm-days", 7, "login-recency window (days) that makes a tenant warm")
}

func (p *importGroupAttestationsCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	fetchAPI := gateway.NewFetchAPI(p.logger, &p.settings, authProvider)
	groupSync := service.NewGroupSyncService(&p.logger, &p.pdb, fetchAPI, authProvider)
	plateSync := service.NewLicensePlateSyncService(&p.logger, &p.pdb, fetchAPI, authProvider)

	// Resolve tenants to process.
	var dbTenants dbmodels.TenantSlice
	var err error
	if p.tenantID != "" {
		dbTenants, err = dbmodels.Tenants(dbmodels.TenantWhere.ID.EQ(p.tenantID)).All(ctx, p.pdb.DBS().Reader)
	} else {
		dbTenants, err = dbmodels.Tenants().All(ctx, p.pdb.DBS().Reader)
	}
	if err != nil {
		p.logger.Fatal().Err(err).Msg("failed to list tenants")
	}

	warmWindow := time.Duration(p.warmDays) * 24 * time.Hour

	var checked, changed, platesChanged, skippedTenants int
	for _, dt := range dbTenants {
		// Activity tiering: skip cold tenants on the warm pass.
		if p.warmOnly {
			warm, werr := tenantSvc.HasRecentLogin(ctx, dt.ID, warmWindow)
			if werr != nil {
				p.logger.Err(werr).Str("tenant_id", dt.ID).Msg("warm check, skipping tenant")
				continue
			}
			if !warm {
				skippedTenants++
				continue
			}
		}

		tenant, terr := tenantSvc.GetTenantByID(ctx, dt.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", dt.ID).Msg("load tenant, skipping")
			continue
		}
		if tenant.ClientID == "" || !common.IsHexAddress(tenant.ClientID) {
			p.logger.Warn().Str("tenant_id", dt.ID).Msg("tenant has no DIMO client id, skipping")
			continue
		}

		// Vehicles for this tenant (optionally one token id).
		var vehicles dbmodels.VehicleSlice
		if p.tokenID != 0 {
			vehicles, err = dbmodels.Vehicles(
				dbmodels.VehicleWhere.TenantID.EQ(dt.ID),
				dbmodels.VehicleWhere.TokenID.EQ(p.tokenID),
			).All(ctx, p.pdb.DBS().Reader)
		} else {
			vehicles, err = dbmodels.Vehicles(dbmodels.VehicleWhere.TenantID.EQ(dt.ID)).All(ctx, p.pdb.DBS().Reader)
		}
		if err != nil {
			p.logger.Err(err).Str("tenant_id", dt.ID).Msg("list vehicles, skipping tenant")
			continue
		}

		for _, v := range vehicles {
			checked++
			res, serr := groupSync.SyncVehicle(ctx, *tenant, v.TokenID, service.SyncOpts{DryRun: p.dryRun})
			if serr != nil {
				p.logger.Debug().Err(serr).Int64("token_id", v.TokenID).Msg("sync vehicle, skipping")
				continue
			}
			if res.Added > 0 || res.Removed > 0 {
				changed++
				p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Int("added", res.Added).Int("removed", res.Removed).
					Bool("dry_run", p.dryRun).Msg("reconciled group attestations")
			}

			// License plate: cache the latest registration-document plate for this
			// vehicle on the same per-vehicle pull. Best-effort — a plate failure
			// must never derail the group sync.
			pres, perr := plateSync.SyncVehicle(ctx, *tenant, v.TokenID, service.SyncOpts{DryRun: p.dryRun})
			if perr != nil {
				p.logger.Debug().Err(perr).Int64("token_id", v.TokenID).Msg("sync license plate, skipping")
			} else if pres.Changed {
				platesChanged++
				p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Str("license_plate", pres.Plate).Bool("dry_run", p.dryRun).Msg("cached license plate")
			}
		}
	}

	p.logger.Info().Int("checked", checked).Int("changed", changed).
		Int("plates_changed", platesChanged).
		Int("skipped_tenants", skippedTenants).Bool("warm_only", p.warmOnly).
		Bool("dry_run", p.dryRun).Msg("import-group-attestations complete")
	return subcommands.ExitSuccess
}
