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
// fetch-api and merges them into the local DB. It is the "pull" half of the
// sync: groups any producer has attested for a vehicle are added to the local
// membership set.
//
// Producers are NOT filtered by dimo_client_id: a tenant/org may run several
// apps (this one, other DIMO apps, third-party apps) under the same developer
// credentials, so the producer wallet does not reliably distinguish "ours" from
// a sibling app. Instead the sync is additive and de-duplicated.
//
// Merge semantics: additive union — take the latest group attestation per
// distinct producer (Source), union their groups, and add any membership not
// already present. Never removes (no single producer is authoritative over
// removals when credentials are shared). De-dup is guaranteed by the
// (tenant_id, token_id, fleet_group_id) primary key. Unknown groups are
// auto-created.
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
	return "pull vehicle group attestations from fetch-api and merge them into the DB (additive, de-duplicated)"
}
func (*importGroupAttestationsCmd) Usage() string {
	return `import-group-attestations [-tenant-id ID] [-token-id N] [-dry-run]:
	Pulls dimo.document.vehicle.groups attestations per vehicle (latest per
	producer) and adds any group membership not already present in
	vehicle_fleet_groups. Additive only — never removes; de-duplicated by primary
	key. Unknown groups are auto-created. -dry-run logs changes without writing.
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
			desired := desiredGroups(entries, p.logger, v.TokenID)
			if len(desired) == 0 {
				continue
			}

			added, rerr := p.merge(ctx, dt.ID, v.TokenID, desired)
			if rerr != nil {
				p.logger.Err(rerr).Int64("token_id", v.TokenID).Msg("merge, skipping")
				continue
			}
			if added > 0 {
				changed++
				p.logger.Info().Str("tenant_id", dt.ID).Int64("token_id", v.TokenID).
					Int("added", added).Int("groups", len(desired)).
					Bool("dry_run", p.dryRun).Msg("merged group attestations")
			}
		}
	}

	p.logger.Info().Int("checked", checked).Int("changed", changed).Bool("dry_run", p.dryRun).
		Msg("import-group-attestations complete")
	return subcommands.ExitSuccess
}

// desiredGroups returns the union of group memberships a vehicle should have,
// taken from the latest dimo.document.vehicle.groups attestation per distinct
// producer. Producers are NOT filtered by dimo_client_id: a tenant/org may run
// several apps under the same developer credentials (same CE source), so the
// producer wallet — not the source — is what distinguishes one app's view from
// a sibling's. Each producer's most-recent attestation contributes its groups,
// which respects a producer's own removals while still merging in siblings.
func desiredGroups(entries []gateway.AttestationEntry, logger zerolog.Logger, tokenID int64) []models.GroupRef {
	// Latest group attestation per producer (falling back to source when a CE
	// carries no producer).
	producerKey := func(e *gateway.AttestationEntry) string {
		if e.Producer != "" {
			return e.Producer
		}
		return e.Source
	}
	latest := map[string]*gateway.AttestationEntry{}
	latestTime := map[string]time.Time{}
	for i := range entries {
		e := &entries[i]
		if e.Type != service.VehicleGroupsCloudEventType {
			continue
		}
		k := producerKey(e)
		t, _ := time.Parse(time.RFC3339, e.Time)
		if _, ok := latest[k]; !ok || t.After(latestTime[k]) {
			latest[k] = e
			latestTime[k] = t
		}
	}

	// Union their groups, de-duplicated by group id.
	byID := map[string]models.GroupRef{}
	for _, e := range latest {
		var doc struct {
			Groups []models.GroupRef `json:"groups"`
		}
		if err := json.Unmarshal(e.Data, &doc); err != nil {
			logger.Err(err).Int64("token_id", tokenID).Str("producer", e.Source).
				Msg("parse groups document, skipping producer")
			continue
		}
		for _, g := range doc.Groups {
			if g.ID == "" {
				continue
			}
			if _, ok := byID[g.ID]; !ok {
				byID[g.ID] = g
			}
		}
	}

	out := make([]models.GroupRef, 0, len(byID))
	for _, g := range byID {
		out = append(out, g)
	}
	return out
}

// merge adds any desired group membership not already present for the vehicle.
// Additive only — never removes (no producer is authoritative over removals when
// developer credentials are shared). De-dup is guaranteed by the
// (tenant_id, token_id, fleet_group_id) primary key. Returns the number of
// memberships added (or that would be added, in dry-run).
func (p *importGroupAttestationsCmd) merge(ctx context.Context, tenantID string, tokenID int64, desired []models.GroupRef) (int, error) {
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
		return 0, err
	}
	currentIDs := make(map[string]bool, len(current))
	for _, m := range current {
		currentIDs[m.FleetGroupID] = true
	}

	var toAdd []string
	for id := range desiredIDs {
		if !currentIDs[id] {
			toAdd = append(toAdd, id)
		}
	}
	if len(toAdd) == 0 {
		return 0, nil
	}

	if p.dryRun {
		p.logger.Info().Str("tenant_id", tenantID).Int64("token_id", tokenID).
			Strs("add", toAdd).Msg("would add memberships")
		return len(toAdd), nil
	}

	added := 0
	for _, id := range toAdd {
		m := &dbmodels.VehicleFleetGroup{TenantID: tenantID, TokenID: tokenID, FleetGroupID: id}
		if err := m.Insert(ctx, p.pdb.DBS().Writer, boil.Infer()); err != nil {
			p.logger.Err(err).Int64("token_id", tokenID).Str("group_id", id).Msg("add membership")
			continue
		}
		added++
	}
	return added, nil
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
