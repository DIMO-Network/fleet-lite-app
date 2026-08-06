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
	"github.com/aarondl/sqlboiler/v4/queries/qm"
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
	vinOnly  bool
}

func (*importGroupAttestationsCmd) Name() string { return "import-group-attestations" }
func (*importGroupAttestationsCmd) Synopsis() string {
	return "pull vehicle group attestations from fetch-api and merge them into the DB (additive, de-duplicated)"
}
func (*importGroupAttestationsCmd) Usage() string {
	return `import-group-attestations [-tenant-id ID] [-token-id N] [-warm-only] [-warm-days N] [-vin-only] [-dry-run]:
	Pulls dimo.document.vehicle.groups attestations per vehicle (latest per
	producer) and adds any group membership not already present in
	vehicle_fleet_groups. Additive only — never removes; de-duplicated by primary
	key. Unknown groups are auto-created. -warm-only limits the run to tenants with
	a member login within -warm-days days (default 7). -vin-only skips the group
	and registration-document sync and only backfills vehicles.vin from the DIMO
	VIN VC, restricted to vehicles with no VIN yet (see docs/VIN_SYNC_PLAN.md).
	-dry-run logs changes without writing.
  `
}

func (p *importGroupAttestationsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
	f.Int64Var(&p.tokenID, "token-id", 0, "only this vehicle token id")
	f.BoolVar(&p.dryRun, "dry-run", false, "log what would change without writing")
	f.BoolVar(&p.warmOnly, "warm-only", false, "only tenants with a recent member login (see -warm-days)")
	f.IntVar(&p.warmDays, "warm-days", 7, "login-recency window (days) that makes a tenant warm")
	f.BoolVar(&p.vinOnly, "vin-only", false, "only backfill vehicles.vin from the DIMO VIN VC (vehicles without a VIN)")
}

func (p *importGroupAttestationsCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	fetchAPI := gateway.NewFetchAPI(p.logger, &p.settings, authProvider)
	groupSync := service.NewGroupSyncService(&p.logger, &p.pdb, fetchAPI, authProvider, p.settings.DropForeignTenantGroups)
	telemetryAPI := service.NewTelemetryAPIService(p.logger, &p.settings, authProvider)
	plateSync := service.NewLicensePlateSyncService(&p.logger, &p.pdb, fetchAPI, authProvider, telemetryAPI)

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

	// syncAttempted/syncFailed exist to make a systemic failure visible. Per-
	// vehicle errors are logged at debug and skipped, which is right for a
	// best-effort convergence pass — but it also meant this command could fail
	// on every single vehicle and still exit 0. It did exactly that in
	// production for as long as the cronjob ran unmeshed: identity-api 403s an
	// unmeshed caller, so no developer JWT could be minted and every vehicle
	// failed, nightly, green. See the linkerd note in charts/.../cronjobs.yaml.
	var checked, changed, platesChanged, vinsChanged, skippedTenants int
	var syncAttempted, syncFailed int
	var firstSyncErr error
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

		// Vehicles for this tenant (optionally one token id). The vin-only
		// backfill restricts to vehicles with no VIN yet — pull-once: a filled
		// vehicle never costs another telemetry query.
		mods := []qm.QueryMod{dbmodels.VehicleWhere.TenantID.EQ(dt.ID)}
		if p.tokenID != 0 {
			mods = append(mods, dbmodels.VehicleWhere.TokenID.EQ(p.tokenID))
		}
		if p.vinOnly {
			mods = append(mods, qm.Where("(vin IS NULL OR vin = '')"))
		}
		vehicles, err := dbmodels.Vehicles(mods...).All(ctx, p.pdb.DBS().Reader)
		if err != nil {
			p.logger.Err(err).Str("tenant_id", dt.ID).Msg("list vehicles, skipping tenant")
			continue
		}

		for _, v := range vehicles {
			checked++

			// vin-only: just the VIN VC read, no group or registration-doc pull.
			if p.vinOnly {
				pres, perr := plateSync.SyncVINOnly(ctx, *tenant, v.TokenID, p.dryRun)
				if perr != nil {
					p.logger.Debug().Err(perr).Int64("token_id", v.TokenID).Msg("vin vc sync, skipping")
				} else if pres.VINChanged {
					vinsChanged++
					p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
						Str("vin", pres.VIN).Bool("dry_run", p.dryRun).Msg("cached vin from vc")
				}
				continue
			}

			syncAttempted++
			res, serr := groupSync.SyncVehicle(ctx, *tenant, v.TokenID, service.SyncOpts{DryRun: p.dryRun})
			if serr != nil {
				syncFailed++
				if firstSyncErr == nil {
					firstSyncErr = serr
				}
				p.logger.Debug().Err(serr).Int64("token_id", v.TokenID).Msg("sync vehicle, skipping")
				continue
			}
			if res.Added > 0 || res.Removed > 0 {
				changed++
				p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Int("added", res.Added).Int("removed", res.Removed).
					Bool("dry_run", p.dryRun).Msg("reconciled group attestations")
			}

			// Registration fields: cache the latest license_plate + vin for this
			// vehicle from the same per-vehicle pull. Best-effort — a failure here
			// must never derail the group sync.
			pres, perr := plateSync.SyncVehicle(ctx, *tenant, v.TokenID, service.SyncOpts{DryRun: p.dryRun})
			if perr != nil {
				p.logger.Debug().Err(perr).Int64("token_id", v.TokenID).Msg("sync registration fields, skipping")
			} else {
				if pres.Changed {
					platesChanged++
					p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
						Str("license_plate", pres.Plate).Bool("dry_run", p.dryRun).Msg("cached license plate")
				}
				if pres.VINChanged {
					vinsChanged++
					p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
						Str("vin", pres.VIN).Bool("dry_run", p.dryRun).Msg("cached vin")
				}
			}
		}
	}

	// Every attempted vehicle failing is not a bad night — it is a broken
	// deployment. Nothing else distinguishes "converged, no changes" from
	// "could not talk to anything", because both report changed=0.
	//
	// Deliberately only the group-sync path: -vin-only reads a VIN VC that many
	// vehicles legitimately do not have, so 100% "failure" is a normal outcome
	// there and would make the job cry wolf.
	totalFailure := syncAttempted > 0 && syncFailed == syncAttempted

	ev := p.logger.Info()
	if totalFailure {
		ev = p.logger.Error().Err(firstSyncErr)
	}
	ev.Int("checked", checked).Int("changed", changed).
		Int("plates_changed", platesChanged).
		Int("vins_changed", vinsChanged).
		Int("sync_attempted", syncAttempted).Int("sync_failed", syncFailed).
		Int("skipped_tenants", skippedTenants).Bool("warm_only", p.warmOnly).
		Bool("dry_run", p.dryRun).Msg("import-group-attestations complete")

	if totalFailure {
		p.logger.Error().Int("vehicles", syncAttempted).
			Msg("every vehicle failed to sync — treating as a failed run, not an empty one")
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}
