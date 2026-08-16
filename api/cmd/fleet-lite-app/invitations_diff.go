package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/subcommands"
	"github.com/rs/zerolog"
)

// invitationsDiffCmd compares this app's invitations against what
// fleet-tenancy-api serves — the P2 gate of the invitations move, shaped like
// groups-diff and tenancy-diff before it.
//
// Asymmetric for the same reason as those: tenancy can legitimately hold
// invitations we do not (the operator console's, once P3 lands, marked by
// createdByTenantId). The failures are the other direction — an invitation we
// hold that tenancy lacks (missing-remote), or fields that disagree (differ).
// Either means the backfill has drifted behind a local write, so re-run
// backfill-invitations, or was never faithful.
//
// WHAT THIS CANNOT CHECK, and it is the most important field: the TOKEN HASH.
// The service never serves it and must not — the hash is the credential half
// that recognises an emailed link. So a clean diff proves the records match,
// NOT that an outstanding link still works. Only the plan's end-to-end gate
// proves that: send an invitation before the flag flip and accept it after.
// Run both; neither substitutes for the other.
type invitationsDiffCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	verbose  bool
}

func (*invitationsDiffCmd) Name() string { return "invitations-diff" }
func (*invitationsDiffCmd) Synopsis() string {
	return "compare local invitations against fleet-tenancy-api"
}
func (*invitationsDiffCmd) Usage() string {
	return `invitations-diff [-tenant-id <uuid>] [-verbose]:
	Walks every tenant holding a DIMO client id and compares its local
	invitations against GET /v1/tenants/{id}/invitations.

	Verdicts per invitation:
	  agree           same status, email, role, scope, expiry and invitee
	  remote-extra    tenancy holds one we lack — expected once the console
	                  sends invitations; informational
	  differ          a compared field disagrees — FAILURE
	  missing-remote  a local invitation tenancy lacks — FAILURE, usually a
	                  local write since the last backfill-invitations run

	Exits non-zero on any differ or missing-remote.

	NOTE the token hash is deliberately NOT compared — the service never serves
	it, because it is the credential that recognises an emailed link. A clean
	diff proves the records match, not that an outstanding link still works.
	Prove that separately: invite before the flag flip, accept after it.
  `
}

func (p *invitationsDiffCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant")
	f.BoolVar(&p.verbose, "verbose", false, "log agreeing invitations too")
}

type inviteVerdict string

const (
	inviteAgree         inviteVerdict = "agree"
	inviteDiffer        inviteVerdict = "differ"
	inviteRemoteExtra   inviteVerdict = "remote-extra"
	inviteMissingRemote inviteVerdict = "missing-remote"
)

type inviteFinding struct {
	InvitationID string
	Verdict      inviteVerdict
	Detail       string
}

// compareInvitationSets compares one tenant's local invitations against the
// remote view. Pure so the verdict logic is testable without a database.
func compareInvitationSets(local dbmodels.InvitationSlice, remote []models.RemoteInvitation) []inviteFinding {
	remoteByID := make(map[string]models.RemoteInvitation, len(remote))
	for _, r := range remote {
		remoteByID[r.ID] = r
	}

	sorted := append(dbmodels.InvitationSlice{}, local...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var out []inviteFinding
	for _, l := range sorted {
		r, ok := remoteByID[l.ID]
		if !ok {
			out = append(out, inviteFinding{l.ID, inviteMissingRemote,
				"invitation exists locally, not in tenancy"})
			continue
		}
		if d := invitationDifference(l, r); d != "" {
			out = append(out, inviteFinding{l.ID, inviteDiffer, d})
			continue
		}
		out = append(out, inviteFinding{l.ID, inviteAgree, ""})
	}

	var extra []string
	for _, r := range remote {
		found := false
		for _, l := range local {
			if l.ID == r.ID {
				found = true
				break
			}
		}
		if !found {
			extra = append(extra, r.ID)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		detail := "invitation only in tenancy"
		if r := remoteByID[id]; r.CreatedByTenantID != nil {
			detail = "invitation only in tenancy, sent by operator tenant " + *r.CreatedByTenantID
		}
		out = append(out, inviteFinding{id, inviteRemoteExtra, detail})
	}
	return out
}

// invitationDifference names the first field that disagrees, or "" when the two
// records match. Every comparison here is deliberate about representation:
//
//   - wallets compare case-insensitively — this app lowercases them and the
//     backfill checksums them to EIP-55, so one person would otherwise look
//     like a disagreement in every single row;
//   - email compares case-insensitively for the same reason (the service
//     lowercases on create);
//   - expiry compares to the second, because the wire format is RFC3339 and
//     the column holds microseconds;
//   - scope compares as the tri-state it is, nil distinct from empty.
func invitationDifference(l *dbmodels.Invitation, r models.RemoteInvitation) string {
	if l.Status != r.Status {
		return fmt.Sprintf("status: local %q, remote %q", l.Status, r.Status)
	}
	if !strings.EqualFold(l.Email, r.Email) {
		return fmt.Sprintf("email: local %q, remote %q", l.Email, r.Email)
	}
	if l.Role != r.Role {
		return fmt.Sprintf("role: local %q, remote %q", l.Role, r.Role)
	}
	if d := scopeDifference([]string(l.AllowedGroupIds), r.ScopeGroupIDs); d != "" {
		return d
	}
	remoteExpiry, err := time.Parse(time.RFC3339, r.ExpiresAt)
	if err != nil {
		return fmt.Sprintf("expiresAt: remote %q is not RFC3339", r.ExpiresAt)
	}
	if !l.ExpiresAt.Truncate(time.Second).Equal(remoteExpiry.Truncate(time.Second)) {
		return fmt.Sprintf("expiresAt: local %s, remote %s",
			l.ExpiresAt.UTC().Format(time.RFC3339), remoteExpiry.UTC().Format(time.RFC3339))
	}
	localInvitee := ""
	if l.InviteeWallet.Valid {
		localInvitee = l.InviteeWallet.String
	}
	remoteInvitee := ""
	if r.InviteeWallet != nil {
		remoteInvitee = *r.InviteeWallet
	}
	if !strings.EqualFold(localInvitee, remoteInvitee) {
		return fmt.Sprintf("inviteeWallet: local %q, remote %q", localInvitee, remoteInvitee)
	}
	return ""
}

// scopeDifference compares the three-valued group scope. nil means unrestricted
// and empty means restricted to nothing; conflating them is the inversion that
// has bitten this programme repeatedly, so they are reported as a real
// disagreement rather than smoothed over.
func scopeDifference(local, remote []string) string {
	if (local == nil) != (remote == nil) {
		return fmt.Sprintf("scope: local %s, remote %s — nil is unrestricted, [] is no access",
			describeScope(local), describeScope(remote))
	}
	if local == nil {
		return ""
	}
	l := append([]string{}, local...)
	rr := append([]string{}, remote...)
	sort.Strings(l)
	sort.Strings(rr)
	if strings.Join(l, ",") != strings.Join(rr, ",") {
		return fmt.Sprintf("scope: local %v, remote %v", l, rr)
	}
	return ""
}

func describeScope(s []string) string {
	if s == nil {
		return "nil (unrestricted)"
	}
	if len(s) == 0 {
		return "[] (no access)"
	}
	return fmt.Sprintf("%v", s)
}

func (p *invitationsDiffCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
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

	counts := map[inviteVerdict]int{}
	checkedInvites, checkedTenants := 0, 0

	for _, t := range tenants {
		tenant, terr := tenantSvc.GetTenantByID(ctx, t.ID)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", t.ID).Msg("load tenant credentials")
			return subcommands.ExitFailure
		}

		// Always the local table, whatever INVITES_FROM_TENANCY says — the
		// local side IS one side of the comparison.
		local, lerr := dbmodels.Invitations(
			dbmodels.InvitationWhere.TenantID.EQ(t.ID),
		).All(ctx, p.pdb.DBS().Reader)
		if lerr != nil {
			p.logger.Err(lerr).Str("tenant_id", t.ID).Msg("read local invitations")
			return subcommands.ExitFailure
		}
		remote, rerr := tenancyAPI.Invitations(ctx, *tenant)
		if rerr != nil {
			p.logger.Err(rerr).Str("tenant_id", t.ID).Msg("invitations call failed")
			return subcommands.ExitFailure
		}

		checkedTenants++
		for _, f := range compareInvitationSets(local, remote) {
			counts[f.Verdict]++
			checkedInvites++

			if f.Verdict == inviteAgree && !p.verbose {
				continue
			}
			ev := p.logger.Info()
			if f.Verdict == inviteDiffer || f.Verdict == inviteMissingRemote {
				ev = p.logger.Error()
			}
			ev.Str("tenant_id", t.ID).
				Str("tenant", tenant.Name).
				Str("invitation", f.InvitationID).
				Str("verdict", string(f.Verdict)).
				Str("detail", f.Detail).
				Msg("invitations diff")
		}
	}

	// A tenant holding invitations but no usable client id cannot be asked
	// about, so it is not covered — say so rather than let a clean run imply
	// coverage it does not have. Same caveat groups-diff names.
	if p.tenantID == "" {
		if n, uerr := p.countUnverifiableTenants(ctx); uerr != nil {
			p.logger.Warn().Err(uerr).Msg("could not check for unverifiable tenants")
		} else if n > 0 {
			p.logger.Warn().Int64("tenants", n).
				Msg("tenants with invitations but no usable client id — NOT covered by this diff")
		}
	}

	p.logger.Info().
		Int("tenants", checkedTenants).
		Int("invitations", checkedInvites).
		Int("agree", counts[inviteAgree]).
		Int("remote_extra", counts[inviteRemoteExtra]).
		Int("differ", counts[inviteDiffer]).
		Int("missing_remote", counts[inviteMissingRemote]).
		Msg("invitations diff complete — note the token hash is not compared; " +
			"prove outstanding links end to end")

	if counts[inviteDiffer] > 0 || counts[inviteMissingRemote] > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// countUnverifiableTenants counts tenants holding invitations but no client id.
func (p *invitationsDiffCmd) countUnverifiableTenants(ctx context.Context) (int64, error) {
	return dbmodels.Invitations(
		qm.InnerJoin("tenants t on t.id = invitations.tenant_id"),
		qm.Where("t.dimo_client_id IS NULL OR t.dimo_client_id = ''"),
		qm.Select("count(distinct invitations.tenant_id)"),
	).Count(ctx, p.pdb.DBS().Reader)
}
