package main

import (
	"context"
	"encoding/json"
	"flag"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// importGroupAttestationsCmd pulls vehicle group-membership attestations from
// fetch-api and reconciles the local DB to match them (full mirror). It is the
// "pull" half of the sync: foreign producers (other fleet apps writing under
// their own developer license) become the source of truth for groups we did not
// author. Our own attestations are skipped — for those the DB is authoritative.
//
// Reconcile semantics (locked): full mirror (add + remove), foreign-only (skip
// our own dimo_client_id), auto-create unknown groups.
type importGroupAttestationsCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	tokenID  int64
	dryRun   bool
}

func (*importGroupAttestationsCmd) Name() string { return "import-group-attestations" }
func (*importGroupAttestationsCmd) Synopsis() string {
	return "pull vehicle group attestations from fetch-api and reconcile the DB (full mirror, foreign-only)"
}
func (*importGroupAttestationsCmd) Usage() string {
	return `import-group-attestations [-tenant-id ID] [-token-id N] [-dry-run]:
	Pulls the latest dimo.document.vehicle.groups attestation per vehicle and
	makes vehicle_fleet_groups match it. Skips attestations we produced. Unknown
	groups are auto-created. -dry-run logs changes without writing.
  `
}

func (p *importGroupAttestationsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
	f.Int64Var(&p.tokenID, "token-id", 0, "only this vehicle token id")
	f.BoolVar(&p.dryRun, "dry-run", false, "log what would change without writing")
}

// fetchLimit bounds how many recent CEs we pull per vehicle when looking for the
// latest group document.
const fetchLimit = 50

func (p *importGroupAttestationsCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	fetchAPI := gateway.NewFetchAPI(p.logger, &p.settings, authProvider)

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

	var checked, changed int
	for _, dt := range dbTenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, dt.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", dt.ID).Msg("load tenant, skipping")
			continue
		}
		if tenant.ClientID == "" || !common.IsHexAddress(tenant.ClientID) {
			p.logger.Warn().Str("tenant_id", dt.ID).Msg("tenant has no DIMO client id, skipping")
			continue
		}
		ourClient := common.HexToAddress(tenant.ClientID)

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
			did := authProvider.BuildVehicleDID(uint64(v.TokenID))

			entries, ferr := fetchAPI.ListByDID(*tenant, did, fetchLimit)
			if ferr != nil {
				p.logger.Debug().Err(ferr).Int64("token_id", v.TokenID).Msg("fetch attestations, skipping vehicle")
				continue
			}
			latest := latestGroupsEntry(entries)
			if latest == nil {
				continue
			}
			// Foreign-only: skip attestations we produced — for those the DB is
			// authoritative and the write-path keeps them current.
			if common.IsHexAddress(latest.Source) && common.HexToAddress(latest.Source) == ourClient {
				continue
			}

			var doc struct {
				Groups []models.GroupRef `json:"groups"`
			}
			if uerr := json.Unmarshal(latest.Data, &doc); uerr != nil {
				p.logger.Err(uerr).Int64("token_id", v.TokenID).Msg("parse groups document, skipping")
				continue
			}

			didChange, rerr := p.reconcile(ctx, dt.ID, v.TokenID, doc.Groups)
			if rerr != nil {
				p.logger.Err(rerr).Int64("token_id", v.TokenID).Msg("reconcile, skipping")
				continue
			}
			if didChange {
				changed++
				p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Str("producer", latest.Source).Int("groups", len(doc.Groups)).
					Bool("dry_run", p.dryRun).Msg("imported foreign group attestation")
			}
		}
	}

	p.logger.Info().Int("checked", checked).Int("changed", changed).Bool("dry_run", p.dryRun).
		Msg("import-group-attestations complete")
	return subcommands.ExitSuccess
}

// latestGroupsEntry returns the most recent dimo.document.vehicle.groups entry by
// CE time, or nil if there is none.
func latestGroupsEntry(entries []gateway.AttestationEntry) *gateway.AttestationEntry {
	var latest *gateway.AttestationEntry
	var latestTime time.Time
	for i := range entries {
		e := &entries[i]
		if e.Type != service.VehicleGroupsCloudEventType {
			continue
		}
		t, _ := time.Parse(time.RFC3339, e.Time)
		if latest == nil || t.After(latestTime) {
			latest = e
			latestTime = t
		}
	}
	return latest
}

// reconcile makes the vehicle's memberships match the desired set (full mirror).
// Returns whether any change was made (or would be, in dry-run).
func (p *importGroupAttestationsCmd) reconcile(ctx context.Context, tenantID string, tokenID int64, desired []models.GroupRef) (bool, error) {
	desiredIDs := make(map[string]bool, len(desired))
	for _, g := range desired {
		if g.ID == "" {
			continue
		}
		if err := p.ensureGroup(ctx, tenantID, g); err != nil {
			// Most likely a (tenant_id, name) collision with a different id — drop
			// this membership rather than fail the whole vehicle.
			p.logger.Warn().Err(err).Str("tenant_id", tenantID).Str("group_id", g.ID).
				Msg("ensure group, skipping membership")
			continue
		}
		desiredIDs[g.ID] = true
	}

	current, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
	).All(ctx, p.pdb.DBS().Reader)
	if err != nil {
		return false, err
	}
	currentIDs := make(map[string]bool, len(current))
	for _, m := range current {
		currentIDs[m.FleetGroupID] = true
	}

	var toAdd, toRemove []string
	for id := range desiredIDs {
		if !currentIDs[id] {
			toAdd = append(toAdd, id)
		}
	}
	for id := range currentIDs {
		if !desiredIDs[id] {
			toRemove = append(toRemove, id)
		}
	}
	if len(toAdd) == 0 && len(toRemove) == 0 {
		return false, nil
	}

	if p.dryRun {
		p.logger.Info().Str("tenant_id", tenantID).Int64("token_id", tokenID).
			Strs("add", toAdd).Strs("remove", toRemove).Msg("would reconcile memberships")
		return true, nil
	}

	for _, id := range toAdd {
		m := &dbmodels.VehicleFleetGroup{TenantID: tenantID, TokenID: tokenID, FleetGroupID: id}
		if err := m.Insert(ctx, p.pdb.DBS().Writer, boil.Infer()); err != nil {
			p.logger.Err(err).Int64("token_id", tokenID).Str("group_id", id).Msg("add membership")
		}
	}
	for _, id := range toRemove {
		if _, err := dbmodels.VehicleFleetGroups(
			dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
			dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
			dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(id),
		).DeleteAll(ctx, p.pdb.DBS().Writer); err != nil {
			p.logger.Err(err).Int64("token_id", tokenID).Str("group_id", id).Msg("remove membership")
		}
	}
	return true, nil
}

// ensureGroup creates the fleet group if it does not already exist for the
// tenant. Name defaults to the id, color to a neutral gray. A (tenant_id, name)
// collision with a different id surfaces as an error so the caller can skip the
// membership.
func (p *importGroupAttestationsCmd) ensureGroup(ctx context.Context, tenantID string, g models.GroupRef) error {
	exists, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.ID.EQ(g.ID),
		dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
	).Exists(ctx, p.pdb.DBS().Reader)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if p.dryRun {
		p.logger.Info().Str("tenant_id", tenantID).Str("group_id", g.ID).Str("name", g.Name).
			Msg("would create group")
		return nil
	}

	name := strings.TrimSpace(g.Name)
	if name == "" {
		name = g.ID
	}
	color := g.Color
	if color == "" {
		color = "#808080"
	}
	group := &dbmodels.FleetGroup{ID: g.ID, Name: name, Color: color, TenantID: tenantID}
	return group.Insert(ctx, p.pdb.DBS().Writer, boil.Infer())
}
