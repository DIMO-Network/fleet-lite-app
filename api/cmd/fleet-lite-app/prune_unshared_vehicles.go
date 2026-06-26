package main

import (
	"context"
	"flag"

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

// pruneUnsharedVehiclesCmd removes vehicles from the local vehicles table that
// are no longer shared (via SACD) with the owning tenant's DIMO client ID.
//
// For each tenant it asks the Identity API for the full set of vehicles the
// tenant's developer-license client ID is privileged on (FetchPrivilegedVehicles
// — the SACD-shared set, the same source the per-tenant vehicle sync uses) and
// deletes any locally-stored vehicle whose token id is not in that set.
//
// Destructive, so it runs in dry-run mode by default; pass -dry-run=false to
// actually delete. If the Identity API call for a tenant fails the whole tenant
// is skipped so a transient error can never wipe a fleet.
type pruneUnsharedVehiclesCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	dryRun   bool
}

func (*pruneUnsharedVehiclesCmd) Name() string { return "prune-unshared-vehicles" }
func (*pruneUnsharedVehiclesCmd) Synopsis() string {
	return "delete vehicles whose SACDs no longer share them with their tenant's DIMO client ID"
}
func (*pruneUnsharedVehiclesCmd) Usage() string {
	return `prune-unshared-vehicles [-tenant-id ID] [-dry-run=false]:
	Iterates over every tenant's vehicles. For each tenant it resolves the DIMO
	client ID, fetches the set of vehicles that client ID is privileged on
	(SACD-shared) from the Identity API, and deletes any local vehicle whose token
	id is not in that set. Defaults to dry-run (logs what it would delete); pass
	-dry-run=false to actually delete. -tenant-id limits the run to one tenant.
  `
}

func (p *pruneUnsharedVehiclesCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
	f.BoolVar(&p.dryRun, "dry-run", true, "log what would be deleted without deleting (default true; pass -dry-run=false to delete)")
}

func (p *pruneUnsharedVehiclesCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)

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

	var checked, deleted, skippedTenants int
	for _, dt := range dbTenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, dt.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", dt.ID).Msg("load tenant, skipping")
			skippedTenants++
			continue
		}
		if tenant.ClientID == "" || !common.IsHexAddress(tenant.ClientID) {
			p.logger.Warn().Str("tenant_id", dt.ID).Msg("tenant has no DIMO client id, skipping")
			skippedTenants++
			continue
		}

		// Local vehicles for this tenant.
		vehicles, verr := dbmodels.Vehicles(dbmodels.VehicleWhere.TenantID.EQ(dt.ID)).All(ctx, p.pdb.DBS().Reader)
		if verr != nil {
			p.logger.Err(verr).Str("tenant_id", dt.ID).Msg("list vehicles, skipping tenant")
			skippedTenants++
			continue
		}

		// Vehicles the tenant's client ID is privileged on (SACD-shared). On any
		// error skip the whole tenant — never delete based on an incomplete set.
		shared, serr := identityService.FetchPrivilegedVehicles(tenant.ClientID)
		if serr != nil {
			p.logger.Err(serr).Str("tenant_id", dt.ID).Str("client_id", tenant.ClientID).
				Msg("fetch privileged vehicles, skipping tenant")
			skippedTenants++
			continue
		}
		sharedSet := make(map[int64]struct{}, len(shared))
		for _, sv := range shared {
			sharedSet[sv.TokenID] = struct{}{}
		}

		localTokenIDs := make([]int64, 0, len(vehicles))
		for _, v := range vehicles {
			localTokenIDs = append(localTokenIDs, v.TokenID)
		}
		p.logger.Info().Str("tenant_id", dt.ID).Str("client_id", tenant.ClientID).
			Int("local_vehicles", len(vehicles)).Int("shared_vehicles", len(shared)).
			Ints64("local_token_ids", localTokenIDs).Msg("processing tenant")

		for _, v := range vehicles {
			checked++
			if _, ok := sharedSet[v.TokenID]; ok {
				continue // still shared with this tenant; keep it
			}

			if p.dryRun {
				p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Bool("dry_run", true).Msg("would delete vehicle: not shared with tenant's client id")
				deleted++
				continue
			}
			if _, derr := v.Delete(ctx, p.pdb.DBS().Writer); derr != nil {
				p.logger.Err(derr).Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Msg("delete vehicle, skipping")
				continue
			}
			p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
				Msg("deleted vehicle: not shared with tenant's client id")
			deleted++
		}
	}

	p.logger.Info().Int("checked", checked).Int("deleted", deleted).
		Int("skipped_tenants", skippedTenants).Bool("dry_run", p.dryRun).
		Msg("prune-unshared-vehicles complete")
	return subcommands.ExitSuccess
}
