package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// tenancyDiffCmd compares every local membership against what fleet-tenancy-api
// answers for the same (tenant, wallet), and reports where they disagree.
//
// This is the evidence that has to exist before cutover. The alternative —
// calling /v1/authz alongside the local check in the request path and logging
// disagreements — only produces evidence where traffic happens to go, and none
// at all while the product has no users. This walks the whole table instead, so
// coverage is complete and immediate.
//
// It is read-only and touches no request path.
type tenancyDiffCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	verbose  bool
}

func (*tenancyDiffCmd) Name() string { return "tenancy-diff" }
func (*tenancyDiffCmd) Synopsis() string {
	return "compare every local membership against fleet-tenancy-api's answer"
}
func (*tenancyDiffCmd) Usage() string {
	return `tenancy-diff [-tenant-id ID] [-verbose]:
	For every row in tenant_users, asks /v1/authz the same question the local
	membership check answers, and compares role, capabilities and group scope.

	-tenant-id ID  limit to one tenant (default: every tenant with a client id)
	-verbose       log agreements too, not just differences

	Outcomes:

	  agree           local and remote say the same thing
	  role-differs    capabilities and scope agree, the role label does not.
	                  Reported, not failed: permissions are authoritative and
	                  role is a display label
	  remote-extra    remote grants strictly more. Expected where a membership
	                  exists in both fleet-lite and kaufmann — the service holds
	                  the merge of both, so this side alone looks smaller
	  differ          a real disagreement: remote grants less than local, or
	                  scope differs in a way a merge cannot explain
	  missing-remote  local has the membership, remote reports no access. At
	                  cutover this person loses access

	Exits non-zero on any differ or missing-remote — the two that change
	someone's access for the worse.
  `
}

func (p *tenancyDiffCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "limit to one tenant")
	f.BoolVar(&p.verbose, "verbose", false, "log agreements as well as differences")
}

// localAccess is what this app's own tables grant, expressed in the shared
// model's vocabulary so it can be compared field for field.
type localAccess struct {
	Role         string
	Permissions  []string
	Unrestricted bool
	ScopeGroups  []string
}

// fleetLiteLocalAccess maps a tenant_users row onto the shared model.
//
// It reproduces the backfill's own mapping deliberately: fleet-lite's only
// owner-gated operations are member management and tenant settings, so an owner
// is exactly those two capabilities and a member is none. A NULL
// allowed_group_ids is unrestricted. If this mapping and the backfill's ever
// drift apart, this command reports the difference — which is the point.
// allowedGroups must preserve its nil-ness: a NULL column is unrestricted,
// while an empty array is restricted to nothing.
func fleetLiteLocalAccess(role string, allowedGroups []string) localAccess {
	var perms []string
	if role == service.RoleOwner {
		perms = []string{"manage_members", "manage_settings"}
	}
	return localAccess{
		Role:         role,
		Permissions:  perms,
		Unrestricted: allowedGroups == nil,
		ScopeGroups:  allowedGroups,
	}
}

// diffVerdict is the outcome of one comparison.
type diffVerdict string

const (
	verdictAgree         diffVerdict = "agree"
	verdictRoleDiffers   diffVerdict = "role-differs"
	verdictRemoteExtra   diffVerdict = "remote-extra"
	verdictDiffer        diffVerdict = "differ"
	verdictMissingRemote diffVerdict = "missing-remote"
)

// compareAccess decides how a local grant and a remote answer relate.
//
// The asymmetry is deliberate. Remote granting *more* than local is expected
// wherever a membership exists in both source systems, because the service
// holds the merge of both and either side alone looks smaller. Remote granting
// *less* is never expected — that is somebody losing access at cutover.
func compareAccess(local localAccess, remote *gateway.AuthzResult) (diffVerdict, string) {
	if remote == nil || remote.Via == "none" {
		return verdictMissingRemote, "remote reports no access"
	}

	localPerms := normalizeSet(local.Permissions)
	remotePerms := normalizeSet(remote.Permissions)

	missing := setDifference(localPerms, remotePerms)
	extra := setDifference(remotePerms, localPerms)

	// Scope. nil (unrestricted) is the most permissive value there is, so
	// remote-unrestricted against local-restricted is a widening, not a
	// conflict; the reverse is a real narrowing.
	scopeVerdict, scopeDetail := compareScope(local, remote)

	switch {
	case scopeVerdict == verdictDiffer:
		return verdictDiffer, joinDetails(permsDetail(missing, extra), scopeDetail)
	case len(missing) > 0:
		return verdictDiffer, joinDetails(permsDetail(missing, extra), scopeDetail)
	case len(extra) > 0 || scopeVerdict == verdictRemoteExtra:
		return verdictRemoteExtra, joinDetails(permsDetail(missing, extra), scopeDetail)
	case !strings.EqualFold(local.Role, remote.Role):
		return verdictRoleDiffers, fmt.Sprintf("local role %q, remote role %q", local.Role, remote.Role)
	}
	return verdictAgree, ""
}

// compareScope compares group scope, honouring nil-vs-empty.
//
// nil means unrestricted and an empty slice means restricted to nothing — the
// inversion that silently granted 131 memberships the whole fleet during the
// backfill. Testing len() would collapse the two and make this command blind to
// exactly the class of bug it exists to catch.
func compareScope(local localAccess, remote *gateway.AuthzResult) (diffVerdict, string) {
	remoteUnrestricted := remote.Unrestricted()

	switch {
	case local.Unrestricted && remoteUnrestricted:
		return verdictAgree, ""
	case local.Unrestricted && !remoteUnrestricted:
		// Local sees everything, remote does not: a narrowing.
		return verdictDiffer, fmt.Sprintf("local unrestricted, remote restricted to %d group(s)", len(remote.ScopeGroupIDs))
	case !local.Unrestricted && remoteUnrestricted:
		return verdictRemoteExtra, "local restricted, remote unrestricted"
	}

	localSet := normalizeSet(local.ScopeGroups)
	remoteSet := normalizeSet(remote.ScopeGroupIDs)
	missing := setDifference(localSet, remoteSet)
	extra := setDifference(remoteSet, localSet)

	switch {
	case len(missing) > 0:
		return verdictDiffer, fmt.Sprintf("groups missing remotely: %s", strings.Join(missing, ","))
	case len(extra) > 0:
		return verdictRemoteExtra, fmt.Sprintf("extra groups remotely: %s", strings.Join(extra, ","))
	}
	return verdictAgree, ""
}

func permsDetail(missing, extra []string) string {
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "capabilities missing remotely: "+strings.Join(missing, ","))
	}
	if len(extra) > 0 {
		parts = append(parts, "extra capabilities remotely: "+strings.Join(extra, ","))
	}
	return strings.Join(parts, "; ")
}

func joinDetails(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// normalizeSet lowercases, de-duplicates and sorts, so comparisons do not
// depend on ordering or casing from either side.
func normalizeSet(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// setDifference returns members of a that are absent from b. Both must be
// normalized.
func setDifference(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	out := []string{}
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}

func (p *tenancyDiffCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
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

	counts := map[diffVerdict]int{}
	checked := 0

	for _, t := range tenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, t.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", t.ID).Msg("load tenant credentials")
			return subcommands.ExitFailure
		}

		members, merr := dbmodels.TenantUsers(
			dbmodels.TenantUserWhere.TenantID.EQ(t.ID),
			qm.OrderBy(dbmodels.TenantUserColumns.Wallet),
		).All(ctx, p.pdb.DBS().Reader)
		if merr != nil {
			p.logger.Err(merr).Str("tenant_id", t.ID).Msg("list memberships")
			return subcommands.ExitFailure
		}

		for _, m := range members {
			// Wallets are stored inconsistently cased across the two systems;
			// checksum before asking so one person is one question.
			wallet := common.HexToAddress(m.Wallet).Hex()

			remote, aerr := tenancyAPI.AuthzFresh(ctx, *tenant, wallet)
			if aerr != nil {
				p.logger.Err(aerr).Str("tenant_id", t.ID).Str("wallet", wallet).Msg("authz call failed")
				return subcommands.ExitFailure
			}

			local := fleetLiteLocalAccess(m.Role, []string(m.AllowedGroupIds))
			verdict, detail := compareAccess(local, remote)
			counts[verdict]++
			checked++

			if verdict == verdictAgree && !p.verbose {
				continue
			}

			ev := p.logger.Info()
			if verdict == verdictDiffer || verdict == verdictMissingRemote {
				ev = p.logger.Error()
			}
			ev.Str("tenant_id", t.ID).
				Str("tenant", tenant.Name).
				Str("wallet", wallet).
				Str("verdict", string(verdict)).
				Str("local_role", local.Role).
				Str("remote_role", remote.Role).
				Bool("local_unrestricted", local.Unrestricted).
				Bool("remote_unrestricted", remote.Unrestricted()).
				Strs("local_permissions", local.Permissions).
				Strs("remote_permissions", remote.Permissions).
				Str("detail", detail).
				Msg("tenancy diff")
		}
	}

	p.logger.Info().
		Int("checked", checked).
		Int("agree", counts[verdictAgree]).
		Int("role_differs", counts[verdictRoleDiffers]).
		Int("remote_extra", counts[verdictRemoteExtra]).
		Int("differ", counts[verdictDiffer]).
		Int("missing_remote", counts[verdictMissingRemote]).
		Msg("tenancy diff complete")

	if counts[verdictDiffer] > 0 || counts[verdictMissingRemote] > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}
