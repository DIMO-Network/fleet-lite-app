package main

import (
	"context"
	"flag"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// syncVehiclesCmd refreshes each tenant's local vehicles table from the set of
// vehicles their DIMO developer-license client ID is privileged on (SACD-shared),
// via the Identity API.
//
// This is the additive counterpart to prune-unshared-vehicles: VehicleService.
// SyncVehicles upserts every privileged vehicle and never deletes, so vehicles
// minted or shared since the last sync appear locally. Without it the vehicles
// table is only ever populated on tenant creation (the one best-effort initial
// sync in TenantsController.CreateTenant) or a manual POST /tenants/:id/sync-
// vehicles, so it silently drifts stale — and because the group-attestation
// import iterates the local vehicle list, stale vehicles mean stale groups.
// Run on a schedule (CronJob) to keep both current.
type syncVehiclesCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
}

func (*syncVehiclesCmd) Name() string { return "sync-vehicles" }
func (*syncVehiclesCmd) Synopsis() string {
	return "upsert each tenant's Identity-privileged (SACD-shared) vehicles into the local vehicles table"
}
func (*syncVehiclesCmd) Usage() string {
	return `sync-vehicles [-tenant-id ID]:
	For each tenant, fetch the vehicles its DIMO client ID is privileged on from the
	Identity API and upsert them into the local vehicles table. Additive only — never
	deletes (use prune-unshared-vehicles to remove no-longer-shared vehicles).
	-tenant-id limits the run to one tenant.
  `
}

func (p *syncVehiclesCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
}

func (p *syncVehiclesCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	vehicleSvc := service.NewVehicleService(&p.logger, &p.pdb, identityService)

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

	var synced, skippedTenants int
	for _, dt := range dbTenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, dt.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", dt.ID).Msg("load tenant, skipping")
			skippedTenants++
			continue
		}
		// SyncVehicles errors on an empty/invalid client id or an Identity failure;
		// skip that tenant so one bad tenant can't abort the whole run.
		n, serr := vehicleSvc.SyncVehicles(ctx, tenant)
		if serr != nil {
			p.logger.Err(serr).Str("tenant_id", dt.ID).Msg("sync vehicles, skipping tenant")
			skippedTenants++
			continue
		}
		synced += n
		p.logger.Info().Str("tenant_id", dt.ID).Int("synced", n).Msg("synced tenant vehicles")
	}

	p.logger.Info().Int("synced", synced).Int("skipped_tenants", skippedTenants).
		Int("tenants", len(dbTenants)).Msg("sync-vehicles complete")
	return subcommands.ExitSuccess
}
