package main

import (
	"context"
	"flag"
	"fmt"
	"sort"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/subcommands"
	"github.com/rs/zerolog"
)

// mirrorGroupsCmd reconverges the local fleet-group mirror from
// fleet-tenancy-api. With tenancy the single writer since P4, the local
// fleet_groups / vehicle_fleet_groups tables are only a mirror that the
// scope-filtering SQL joins against until P5 drops them; any drift (a
// half-failed write-through, a write made by another app against the same
// tenant) is corrected by pulling from the authority. This replaces the
// deleted CloudEvent import: same convergence job, but from the single owner
// instead of reconciling a peer's attestation stream.
type mirrorGroupsCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	dryRun   bool
}

func (*mirrorGroupsCmd) Name() string { return "mirror-groups" }
func (*mirrorGroupsCmd) Synopsis() string {
	return "reconverge the local fleet-group mirror from fleet-tenancy-api"
}
func (*mirrorGroupsCmd) Usage() string {
	return `mirror-groups [-tenant-id <uuid>] [-dry-run]:
	Walks every tenant holding a DIMO client id, reads its groups from
	GET /v1/tenants/{id}/vehicle-groups, and converges the local
	fleet_groups / vehicle_fleet_groups mirror onto that answer in one
	transaction per tenant: groups are inserted, renamed/recoloured or
	deleted to match, memberships likewise.

	Memberships whose vehicle is not in this app's vehicles table are
	skipped and counted (memberships_skipped_no_vehicle) — tenancy can hold
	vehicles this app has not synced, which is expected, not an error.

	-dry-run reports the same per-tenant counts and writes nothing.
	Exits non-zero on any error; converging with changes is still success.
  `
}

func (p *mirrorGroupsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant")
	f.BoolVar(&p.dryRun, "dry-run", false, "report what would change without writing")
}

// groupMeta is the group metadata the mirror stores locally.
type groupMeta struct {
	ID    string
	Name  string
	Color string
}

// membershipRef is one (group, vehicle) membership row.
type membershipRef struct {
	GroupID string
	TokenID int64
}

// mirrorPlan is the exact set of local writes that converge one tenant's
// mirror onto the remote authority. Pure data, so the diff logic is testable
// without a database or a service — the same split groups-diff makes with
// compareGroupSets.
type mirrorPlan struct {
	GroupsToInsert      []groupMeta
	GroupsToUpdate      []groupMeta
	GroupIDsToDelete    []string
	MembershipsToInsert []membershipRef
	// MembershipsToDelete includes the rows of deleted groups too, so the
	// reported counts describe every row the run removes rather than
	// depending on what the FK cascade did silently.
	MembershipsToDelete []membershipRef
	SkippedNoVehicle    int
}

func (p *mirrorPlan) empty() bool {
	return len(p.GroupsToInsert) == 0 && len(p.GroupsToUpdate) == 0 &&
		len(p.GroupIDsToDelete) == 0 && len(p.MembershipsToInsert) == 0 &&
		len(p.MembershipsToDelete) == 0
}

// planMirror computes the writes that make local match remote. haveVehicle is
// this app's synced vehicle set for the tenant: vehicle_fleet_groups carries a
// composite FK to vehicles(tenant_id, token_id), so a membership for a vehicle
// we have not synced cannot be inserted and is counted as skipped instead.
// All output slices are sorted so two runs over the same state plan — and
// log — identically.
func planMirror(local map[string]localGroupState, remote []models.RemoteFleetGroup, haveVehicle map[int64]bool) mirrorPlan {
	var plan mirrorPlan

	remoteByID := make(map[string]models.RemoteFleetGroup, len(remote))
	for _, g := range remote {
		remoteByID[g.ID] = g
	}

	remoteIDs := make([]string, 0, len(remote))
	for id := range remoteByID {
		remoteIDs = append(remoteIDs, id)
	}
	sort.Strings(remoteIDs)

	for _, id := range remoteIDs {
		r := remoteByID[id]
		l, exists := local[id]
		switch {
		case !exists:
			plan.GroupsToInsert = append(plan.GroupsToInsert, groupMeta{ID: r.ID, Name: r.Name, Color: r.Color})
		case l.Name != r.Name || l.Color != r.Color:
			plan.GroupsToUpdate = append(plan.GroupsToUpdate, groupMeta{ID: r.ID, Name: r.Name, Color: r.Color})
		}

		localMembers := make(map[int64]bool, len(l.TokenIDs))
		for _, t := range l.TokenIDs {
			localMembers[t] = true
		}
		remoteMembers := make(map[int64]bool, len(r.TokenIDs))
		for _, t := range r.TokenIDs {
			if remoteMembers[t] {
				continue
			}
			remoteMembers[t] = true
			if localMembers[t] {
				continue
			}
			if !haveVehicle[t] {
				plan.SkippedNoVehicle++
				continue
			}
			plan.MembershipsToInsert = append(plan.MembershipsToInsert, membershipRef{GroupID: id, TokenID: t})
		}
		for _, t := range l.TokenIDs {
			if !remoteMembers[t] {
				plan.MembershipsToDelete = append(plan.MembershipsToDelete, membershipRef{GroupID: id, TokenID: t})
			}
		}
	}

	localIDs := make([]string, 0, len(local))
	for id := range local {
		localIDs = append(localIDs, id)
	}
	sort.Strings(localIDs)
	for _, id := range localIDs {
		if _, ok := remoteByID[id]; ok {
			continue
		}
		plan.GroupIDsToDelete = append(plan.GroupIDsToDelete, id)
		for _, t := range local[id].TokenIDs {
			plan.MembershipsToDelete = append(plan.MembershipsToDelete, membershipRef{GroupID: id, TokenID: t})
		}
	}

	sortMemberships(plan.MembershipsToInsert)
	sortMemberships(plan.MembershipsToDelete)
	return plan
}

func sortMemberships(ms []membershipRef) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].GroupID != ms[j].GroupID {
			return ms[i].GroupID < ms[j].GroupID
		}
		return ms[i].TokenID < ms[j].TokenID
	})
}

func (p *mirrorGroupsCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	tenancyAPI := gateway.NewTenancyAPI(p.logger, &p.settings, authProvider)

	mods := []qm.QueryMod{
		dbmodels.TenantWhere.DimoClientID.IsNotNull(),
		dbmodels.TenantWhere.DimoClientID.NEQ(null.StringFrom("")),
		qm.OrderBy(dbmodels.TenantColumns.Name),
	}
	if p.tenantID != "" {
		mods = append(mods, dbmodels.TenantWhere.ID.EQ(p.tenantID))
	}
	tenants, err := dbmodels.Tenants(mods...).All(ctx, p.pdb.DBS().Reader)
	if err != nil {
		p.logger.Err(err).Msg("list tenants")
		return subcommands.ExitFailure
	}

	for _, t := range tenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, t.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", t.ID).Msg("load tenant credentials")
			return subcommands.ExitFailure
		}
		if err := p.mirrorTenant(ctx, tenancyAPI, *tenant); err != nil {
			p.logger.Err(err).Str("tenant_id", t.ID).Msg("mirror tenant groups")
			return subcommands.ExitFailure
		}
	}

	p.logger.Info().Int("tenants", len(tenants)).Bool("dry_run", p.dryRun).
		Msg("mirror-groups complete")
	return subcommands.ExitSuccess
}

// mirrorTenant plans and applies one tenant's convergence.
func (p *mirrorGroupsCmd) mirrorTenant(ctx context.Context, tenancyAPI *gateway.TenancyAPI, tenant models.Tenant) error {
	local, err := readLocalGroupState(ctx, p.pdb.DBS().Reader, tenant.ID)
	if err != nil {
		return err
	}
	haveVehicle, err := p.readVehicleTokenIDs(ctx, tenant.ID)
	if err != nil {
		return err
	}
	remote, err := tenancyAPI.VehicleGroups(ctx, tenant)
	if err != nil {
		return fmt.Errorf("vehicle-groups call: %w", err)
	}

	plan := planMirror(local, remote, haveVehicle)

	if plan.SkippedNoVehicle > 0 {
		// Expected: tenancy can hold vehicles this app has not synced. Their
		// memberships stay in tenancy and mirror in once the vehicle does.
		p.logger.Info().Str("tenant_id", tenant.ID).Int("memberships", plan.SkippedNoVehicle).
			Msg("skipping memberships for vehicles this app has not synced")
	}

	if !p.dryRun && !plan.empty() {
		if err := p.applyPlan(ctx, tenant.ID, plan); err != nil {
			return err
		}
	}

	p.logger.Info().Str("tenant_id", tenant.ID).Str("tenant", tenant.Name).
		Int("groups_added", len(plan.GroupsToInsert)).
		Int("groups_updated", len(plan.GroupsToUpdate)).
		Int("groups_removed", len(plan.GroupIDsToDelete)).
		Int("memberships_added", len(plan.MembershipsToInsert)).
		Int("memberships_removed", len(plan.MembershipsToDelete)).
		Int("memberships_skipped_no_vehicle", plan.SkippedNoVehicle).
		Bool("dry_run", p.dryRun).Msg("mirror converged")
	return nil
}

// applyPlan writes one tenant's convergence in a single transaction, so a
// failure part-way leaves the mirror as it was rather than half-moved.
//
// Order matters twice: membership deletes precede group deletes only so the
// removal counts describe rows this run actually deleted (the FK cascade
// would otherwise remove them silently), and group upserts precede membership
// inserts because the membership FK needs the group row to exist. Deletes
// precede inserts because fleet_groups is UNIQUE (tenant_id, name) — a
// re-slugged group keeps its name, so inserting the new id before deleting
// the old row would collide.
func (p *mirrorGroupsCmd) applyPlan(ctx context.Context, tenantID string, plan mirrorPlan) error {
	tx, err := p.pdb.DBS().Writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	for _, m := range plan.MembershipsToDelete {
		if _, err := dbmodels.VehicleFleetGroups(
			dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
			dbmodels.VehicleFleetGroupWhere.TokenID.EQ(m.TokenID),
			dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(m.GroupID),
		).DeleteAll(ctx, tx); err != nil {
			return fmt.Errorf("delete membership %s/%d: %w", m.GroupID, m.TokenID, err)
		}
	}
	if len(plan.GroupIDsToDelete) > 0 {
		if _, err := dbmodels.FleetGroups(
			dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
			dbmodels.FleetGroupWhere.ID.IN(plan.GroupIDsToDelete),
		).DeleteAll(ctx, tx); err != nil {
			return fmt.Errorf("delete groups: %w", err)
		}
	}
	for _, g := range append(plan.GroupsToInsert, plan.GroupsToUpdate...) {
		row := &dbmodels.FleetGroup{ID: g.ID, Name: g.Name, Color: g.Color, TenantID: tenantID}
		if err := row.Upsert(ctx, tx, true,
			[]string{"id"}, boil.Whitelist("name", "color", "updated_at"), boil.Infer()); err != nil {
			return fmt.Errorf("upsert group %s: %w", g.ID, err)
		}
	}
	for _, m := range plan.MembershipsToInsert {
		row := &dbmodels.VehicleFleetGroup{TenantID: tenantID, TokenID: m.TokenID, FleetGroupID: m.GroupID}
		// ON CONFLICT DO NOTHING: a request-path write can land the same row
		// between the plan read and this transaction; that is convergence, not
		// a failure.
		if err := row.Upsert(ctx, tx, false, nil, boil.None(), boil.Infer()); err != nil {
			return fmt.Errorf("insert membership %s/%d: %w", m.GroupID, m.TokenID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// readVehicleTokenIDs loads the tenant's synced vehicle token ids — the set a
// membership row's composite FK can point at.
func (p *mirrorGroupsCmd) readVehicleTokenIDs(ctx context.Context, tenantID string) (map[int64]bool, error) {
	var rows []struct {
		TokenID int64 `boil:"token_id"`
	}
	if err := queries.Raw(`
		SELECT token_id FROM vehicles WHERE tenant_id = $1`, tenantID).
		Bind(ctx, p.pdb.DBS().Reader, &rows); err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.TokenID] = true
	}
	return out, nil
}
