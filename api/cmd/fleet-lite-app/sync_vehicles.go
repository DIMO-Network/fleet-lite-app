package main

import (
	"context"
	"errors"
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
//
// Exit status is part of the contract: any tenant this run could not sync
// fails the command. A tenant skipped silently while the job reports success
// is how an operator-managed customer sat on an empty fleet for three days —
// the CronJob was green the whole time. See docs/HANDOFF.md, 2026-08-19.
//
// The one exception is service.ErrTenantUnconfigured — a tenant with no parent
// and no credential that resolves, which no license backs and this job cannot
// make syncable. Those are logged at error and named in the summary, but do
// not fail the run: they are a provisioning gap in fleet-tenancy-api, and a
// job that stays red until someone else fixes it stops being read at all.
// First hit 2026-08-30, by the operator tenant "DIMO Build": a tenancy row
// with a credential record whose dimo_client_id was never filled in.
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

	Exits non-zero if any tenant was skipped, for any reason. Every skip is
	logged with a reason field.
  `
}

func (p *syncVehiclesCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
}

func (p *syncVehiclesCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	tenancyAPI := gateway.NewTenancyAPI(p.logger, &p.settings, authProvider)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	vehicleSvc := service.NewVehicleService(&p.logger, &p.pdb, identityService)

	// The same wires app.go:118 makes for the web server. Without them every
	// credential-less (operator-managed) tenant takes VehicleService's
	// "no tenancy client is configured" path and is skipped — the omission
	// that caused the 2026-08-19 empty-fleet incident. Only UseTenancy is
	// needed here: the sync path resolves the entitled set and its metadata
	// and consults neither the membership gate nor the group index, which are
	// read-time filters. Wiring them would be dead weight that reads as
	// coverage.
	if tenancyAPI.Configured() {
		tenantSvc.UseTenancy(tenancyAPI)
		vehicleSvc.UseTenancy(tenancyAPI)
	} else {
		p.logger.Warn().Msg("no tenancy client configured — operator-managed tenants cannot be synced and will fail this run")
	}

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

	var synced int
	// Skipped tenants are collected rather than only counted so the summary can
	// name them. A CronJob failure that does not say which customer is affected
	// costs the next reader the same investigation this one already paid for.
	var skipped []string
	// unconfigured is kept apart from skipped because only skipped fails the
	// run. Both are still named in the summary — an unbacked tenant is a real
	// thing to fix, just not by this job.
	var unconfigured []string
	for _, dt := range dbTenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, dt.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", dt.ID).Str("reason", "load tenant failed").
				Msg("skipping tenant")
			skipped = append(skipped, dt.ID)
			continue
		}
		// SyncVehicles errors on an empty/invalid client id or an Identity failure;
		// carry on to the remaining tenants so one bad tenant can't cost the rest
		// their sync — but the run as a whole still fails, below.
		n, serr := vehicleSvc.SyncVehicles(ctx, tenant)
		if serr != nil {
			if errors.Is(serr, service.ErrTenantUnconfigured) {
				p.logger.Error().Err(serr).Str("tenant_id", dt.ID).Str("tenant", tenant.Name).
					Str("reason", "no license backs this tenant").Msg("skipping tenant")
				unconfigured = append(unconfigured, dt.ID)
				continue
			}
			p.logger.Err(serr).Str("tenant_id", dt.ID).Str("tenant", tenant.Name).
				Str("reason", "sync failed").Msg("skipping tenant")
			skipped = append(skipped, dt.ID)
			continue
		}
		synced += n
		p.logger.Info().Str("tenant_id", dt.ID).Int("synced", n).Msg("synced tenant vehicles")
	}

	ev := p.logger.Info()
	if len(skipped) > 0 || len(unconfigured) > 0 {
		ev = p.logger.Error()
	}
	if len(skipped) > 0 {
		ev = ev.Strs("skipped_tenant_ids", skipped)
	}
	if len(unconfigured) > 0 {
		ev = ev.Strs("unconfigured_tenant_ids", unconfigured)
	}
	ev.Int("synced", synced).Int("skipped_tenants", len(skipped)).
		Int("unconfigured_tenants", len(unconfigured)).
		Int("tenants", len(dbTenants)).Msg("sync-vehicles complete")

	// A skipped tenant is a tenant whose fleet is now stale, and stale here is
	// invisible to the customer until they notice vehicles missing. Fail the
	// run so the CronJob alerts instead of reporting a green partial sync.
	// unconfigured tenants are deliberately not counted here — see the type
	// comment.
	if len(skipped) > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}
