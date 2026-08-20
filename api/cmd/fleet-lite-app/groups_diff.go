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

// groupsDiffCmd compares this app's fleet_groups / vehicle_fleet_groups against
// what fleet-tenancy-api serves — the P3 gate of the groups move, exactly as
// tenancy-diff was the gate for the membership cutover.
//
// The comparison is deliberately asymmetric, for the same reason tenancy-diff's
// is. Tenancy holds the union of BOTH source systems, so a group or membership
// it has and we lack (remote-extra) is expected for the tenant that exists in
// both — kaufmann asserted it. The failures are the other direction: a group or
// membership we hold that tenancy lacks (missing-remote), or metadata that
// disagrees (differ). Those mean the backfill has drifted behind a local write
// — re-run backfill-groups — or was never faithful, and the flag must not be
// trusted until this is clean.
type groupsDiffCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	verbose  bool
}

func (*groupsDiffCmd) Name() string { return "groups-diff" }
func (*groupsDiffCmd) Synopsis() string {
	return "compare local fleet groups against fleet-tenancy-api"
}
func (*groupsDiffCmd) Usage() string {
	return `groups-diff [-tenant-id <uuid>] [-verbose]:
	Walks every tenant holding a DIMO client id and compares its local fleet
	groups and vehicle memberships against GET /v1/tenants/{id}/vehicle-groups.

	Verdicts per group:
	  agree           same name, colour and member set
	  remote-extra    tenancy holds a group or members we lack — expected for
	                  the tenant that exists in both source systems (kaufmann
	                  asserted them); informational
	  differ          name or colour disagree — FAILURE
	  missing-remote  a local group or membership tenancy lacks — FAILURE,
	                  usually a local write since the last backfill-groups run

	Exits non-zero on any differ or missing-remote. Run it after any group
	write while GROUPS_FROM_TENANCY is rolling out, and before trusting the
	flag anywhere.
  `
}

func (p *groupsDiffCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant")
	f.BoolVar(&p.verbose, "verbose", false, "log agreeing groups too")
}

// localGroupState is one local group with its member set, ready to compare.
type localGroupState struct {
	Name     string
	Color    string
	TokenIDs []int64
}

type groupVerdict string

const (
	groupAgree         groupVerdict = "agree"
	groupDiffer        groupVerdict = "differ"
	groupRemoteExtra   groupVerdict = "remote-extra"
	groupMissingRemote groupVerdict = "missing-remote"
)

// groupFinding is one group's comparison outcome.
type groupFinding struct {
	GroupID string
	Verdict groupVerdict
	Detail  string
}

// compareGroupSets compares one tenant's local groups against the remote view.
// Pure so the verdict logic is testable without a database or a service.
func compareGroupSets(local map[string]localGroupState, remote []models.RemoteFleetGroup) []groupFinding {
	remoteByID := make(map[string]models.RemoteFleetGroup, len(remote))
	for _, g := range remote {
		remoteByID[g.ID] = g
	}

	ids := make([]string, 0, len(local))
	for id := range local {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []groupFinding
	for _, id := range ids {
		l := local[id]
		r, ok := remoteByID[id]
		if !ok {
			out = append(out, groupFinding{id, groupMissingRemote, "group exists locally, not in tenancy"})
			continue
		}

		if r.Name != l.Name || r.Color != l.Color {
			out = append(out, groupFinding{id, groupDiffer,
				fmt.Sprintf("local %q %s, remote %q %s", l.Name, l.Color, r.Name, r.Color)})
			continue
		}

		missing := tokenSetDifference(l.TokenIDs, r.TokenIDs)
		extra := tokenSetDifference(r.TokenIDs, l.TokenIDs)
		switch {
		case len(missing) > 0:
			out = append(out, groupFinding{id, groupMissingRemote,
				fmt.Sprintf("members missing in tenancy: %v", missing)})
		case len(extra) > 0:
			out = append(out, groupFinding{id, groupRemoteExtra,
				fmt.Sprintf("members only in tenancy: %v", extra)})
		default:
			out = append(out, groupFinding{id, groupAgree, ""})
		}
	}

	remoteIDs := make([]string, 0, len(remote))
	for _, g := range remote {
		if _, ok := local[g.ID]; !ok {
			remoteIDs = append(remoteIDs, g.ID)
		}
	}
	sort.Strings(remoteIDs)
	for _, id := range remoteIDs {
		out = append(out, groupFinding{id, groupRemoteExtra, "group only in tenancy"})
	}
	return out
}

// tokenSetDifference returns the members of a not present in b, sorted.
func tokenSetDifference(a, b []int64) []int64 {
	inB := make(map[int64]bool, len(b))
	for _, v := range b {
		inB[v] = true
	}
	var out []int64
	for _, v := range a {
		if !inB[v] {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (p *groupsDiffCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
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

	// A tenant holding groups but no client id cannot be asked about — its
	// groups are unverifiable through /v1 and must be said out loud rather
	// than silently skipped.
	if p.tenantID == "" {
		if n, uerr := p.warnUnverifiableTenants(ctx); uerr != nil {
			p.logger.Err(uerr).Msg("check unverifiable tenants")
			return subcommands.ExitFailure
		} else if n > 0 {
			p.logger.Warn().Int("tenants", n).
				Msg("tenants with groups but no usable client id — their groups are NOT covered by this diff")
		}
	}

	counts := map[groupVerdict]int{}
	checkedGroups, checkedTenants := 0, 0
	// Tenants whose remote read failed. Collected rather than fatal, so one
	// tenant cannot blind the diff for the others — see the loop below.
	var unreachable []string

	for _, t := range tenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, t.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", t.ID).Msg("load tenant credentials")
			return subcommands.ExitFailure
		}

		local, lerr := readLocalGroupState(ctx, p.pdb.DBS().Reader, t.ID)
		if lerr != nil {
			p.logger.Err(lerr).Str("tenant_id", t.ID).Msg("read local groups")
			return subcommands.ExitFailure
		}
		// A tenant we cannot reach is recorded and skipped, NOT fatal.
		//
		// It used to return here, and that made the diff worth less than it
		// looks: the walk is ordered by tenant name, so whichever tenant
		// happened to fail first took every tenant after it down with it, and
		// a green run and a run that checked one tenant were indistinguishable
		// from the exit code alone. On 2026-08-20 two consecutive failures
		// named two different tenants and verified nothing beyond them.
		//
		// The run still fails at the end — an unverified tenant is not a pass —
		// but it fails having checked everything it could.
		remote, rerr := tenancyAPI.VehicleGroups(ctx, *tenant)
		if rerr != nil {
			p.logger.Err(rerr).Str("tenant_id", t.ID).Str("tenant", tenant.Name).
				Msg("vehicle-groups call failed; this tenant is NOT verified by this run")
			unreachable = append(unreachable, t.ID)
			continue
		}

		checkedTenants++
		for _, f := range compareGroupSets(local, remote) {
			counts[f.Verdict]++
			checkedGroups++

			if f.Verdict == groupAgree && !p.verbose {
				continue
			}
			ev := p.logger.Info()
			if f.Verdict == groupDiffer || f.Verdict == groupMissingRemote {
				ev = p.logger.Error()
			}
			ev.Str("tenant_id", t.ID).
				Str("tenant", tenant.Name).
				Str("group_id", f.GroupID).
				Str("verdict", string(f.Verdict)).
				Str("detail", f.Detail).
				Msg("groups diff")
		}
	}

	ev := p.logger.Info()
	if len(unreachable) > 0 {
		ev = p.logger.Error()
	}
	ev.Int("tenants", checkedTenants).
		Int("groups", checkedGroups).
		Int("agree", counts[groupAgree]).
		Int("remote_extra", counts[groupRemoteExtra]).
		Int("differ", counts[groupDiffer]).
		Int("missing_remote", counts[groupMissingRemote]).
		Int("unreachable", len(unreachable)).
		Strs("unreachable_tenants", unreachable).
		Msg("groups diff complete")

	// Unreachable counts as failure. The question this command answers is "is
	// the mirror trustworthy", and "we could not tell for one tenant" is not a
	// yes — but it is now visible as its own count instead of hiding behind an
	// exit code that could mean either.
	if counts[groupDiffer] > 0 || counts[groupMissingRemote] > 0 || len(unreachable) > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// readLocalGroupState loads one tenant's groups and member sets from the local
// tables — always local, whatever GROUPS_FROM_TENANCY says, because for both
// groups-diff and mirror-groups the local side IS one side of the comparison.
func readLocalGroupState(ctx context.Context, exec boil.ContextExecutor, tenantID string) (map[string]localGroupState, error) {
	groups, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
	).All(ctx, exec)
	if err != nil {
		return nil, fmt.Errorf("list local groups: %w", err)
	}

	out := make(map[string]localGroupState, len(groups))
	for _, g := range groups {
		out[g.ID] = localGroupState{Name: g.Name, Color: g.Color}
	}

	var rows []struct {
		FleetGroupID string `boil:"fleet_group_id"`
		TokenID      int64  `boil:"token_id"`
	}
	if err := queries.Raw(`
		SELECT fleet_group_id, token_id FROM vehicle_fleet_groups
		WHERE tenant_id = $1`, tenantID).Bind(ctx, exec, &rows); err != nil {
		return nil, fmt.Errorf("list local memberships: %w", err)
	}
	for _, r := range rows {
		s, ok := out[r.FleetGroupID]
		if !ok {
			// FK-impossible, but a convergence tool must report rather than drop.
			return nil, fmt.Errorf("membership references unknown local group %s", r.FleetGroupID)
		}
		s.TokenIDs = append(s.TokenIDs, r.TokenID)
		out[r.FleetGroupID] = s
	}
	return out, nil
}

// warnUnverifiableTenants counts tenants that hold groups but no client id.
func (p *groupsDiffCmd) warnUnverifiableTenants(ctx context.Context) (int, error) {
	var rows []struct {
		N int `boil:"n"`
	}
	err := queries.Raw(`
		SELECT COUNT(DISTINCT fg.tenant_id) AS n
		FROM fleet_groups fg
		JOIN tenants t ON t.id = fg.tenant_id
		WHERE t.dimo_client_id IS NULL OR t.dimo_client_id = ''`).
		Bind(ctx, p.pdb.DBS().Reader, &rows)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return rows[0].N, nil
}
