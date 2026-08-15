package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
)

// ErrTenancyWriteFailed wraps a membership write that reached the local table
// but not the shared model (or vice versa for revocations). Controllers map it
// to 502: the caller's request was fine, the upstream write was not, and a
// retry converges because both sides are idempotent.
var ErrTenancyWriteFailed = errors.New("membership write did not reach the tenancy service")

// The membership write-through.
//
// Since the authz cutover, "may this wallet act in this tenant" is answered
// ONLY by fleet-tenancy-api — so a membership written only to the local
// tenant_users table reports success and confers nothing. That exact bug shipped
// in kaufmann and was fixed there on 2026-08-12; this is fleet-lite's half,
// with the same ordering rules:
//
//   - GRANTS write locally first, then remotely. A half-failure leaves the
//     person with less access than intended (the local row exists, authz still
//     says no), never more; the failed request tells the granter, and a retry
//     converges.
//   - REVOCATIONS write remotely first, then locally. Same principle from the
//     other side: if the local delete then fails, authz already refuses.
//
// Remote writes are read-modify-write, NOT blind replacement. The shared
// membership may carry capabilities this app has no vocabulary for —
// onboard_vehicles granted through the console on the shared Kaufmann tenant,
// say — and the tenancy PUT replaces the record wholesale. Blindly sending
// this app's two-capability view would strip those on every edit, which is
// kaufmann's MergeMemberUpdate lesson relearned.

// RolePermissions maps fleet-lite's two roles onto the shared capability set —
// the Q5 mapping: every owner gate here is manage_members or manage_settings,
// and a plain member holds neither.
func RolePermissions(role string) []string {
	if role == RoleOwner {
		return []string{"manage_members", "manage_settings"}
	}
	return []string{}
}

// roleRank orders role labels so a merge can keep the higher one. Role is a
// display label, never an authorization input — the rank exists only so an
// edit from this app cannot demote a label the console granted.
func roleRank(role string) int {
	switch role {
	case RoleOwner:
		return 3
	case "admin":
		return 2
	case RoleMember:
		return 1
	}
	return 0
}

// GrantMember writes a membership everywhere it needs to exist: the local
// tenant_users row, then the shared model. actorWallet is the granter, for the
// shared record's audit trail.
func (s *TenantService) GrantMember(ctx context.Context, tenant *models.Tenant, wallet, role string, allowedGroupIDs []string, actorWallet string) error {
	if err := s.AddMember(ctx, tenant.ID, wallet, role, allowedGroupIDs); err != nil {
		return err
	}
	// Owners are always unrestricted; AddMember already forced the local
	// column NULL, and the remote write must say the same thing.
	if role == RoleOwner {
		allowedGroupIDs = nil
	}
	return s.putMembershipThrough(ctx, tenant, wallet, role, allowedGroupIDs, actorWallet)
}

// ChangeMemberScope updates a member's group scope everywhere. The scope is
// the field this operation sets; role and capabilities are preserved from the
// shared record.
func (s *TenantService) ChangeMemberScope(ctx context.Context, tenant *models.Tenant, wallet string, allowedGroupIDs []string) error {
	if err := s.UpdateMemberAccess(ctx, tenant.ID, wallet, allowedGroupIDs); err != nil {
		// The local row is authoritative for the owner-cannot-be-limited rule;
		// a managed tenant has no local row and skips straight to the shared
		// write.
		if !isMissingMembershipErr(err) {
			return err
		}
	}
	localRole, _ := s.GetMembershipRole(ctx, tenant.ID, wallet)
	return s.putMembershipThrough(ctx, tenant, wallet, localRole, allowedGroupIDs, "")
}

// RemoveMemberEverywhere revokes a membership: the shared model first, then
// the local row.
func (s *TenantService) RemoveMemberEverywhere(ctx context.Context, tenant *models.Tenant, wallet string) error {
	if s.tenancyReady() {
		if err := s.tenancy.DeleteMember(ctx, *tenant, wallet); err != nil {
			return fmt.Errorf("%w: %v", ErrTenancyWriteFailed, err)
		}
	}
	return s.RemoveMember(ctx, tenant.ID, wallet)
}

// putMembershipThrough merges this app's intent with the shared record and
// PUTs the whole membership. role may be empty (scope-only edits on a tenant
// with no local row); the remote record's label then survives unchanged.
func (s *TenantService) putMembershipThrough(ctx context.Context, tenant *models.Tenant, wallet, role string, allowedGroupIDs []string, actorWallet string) error {
	if !s.tenancyReady() {
		// Local dev without the service. Everywhere real, the tenancy client
		// is configured and this branch never runs.
		return nil
	}

	write := gateway.RemoteMemberWrite{
		Role:            role,
		Permissions:     RolePermissions(role),
		ScopeGroupIDs:   allowedGroupIDs,
		GrantedByWallet: actorWallet,
	}
	if remote, found, err := s.remoteMember(ctx, tenant, wallet); err != nil {
		return fmt.Errorf("%w: %v", ErrTenancyWriteFailed, err)
	} else if found {
		// Union, and keep the higher label: an edit from this app must never
		// strip a capability or demote a label another surface granted. The
		// scope is deliberately NOT merged — it is the thing being set.
		write.Permissions = unionStrings(remote.Permissions, write.Permissions)
		if roleRank(remote.Role) > roleRank(write.Role) {
			write.Role = remote.Role
		}
	}
	if write.Role == "" {
		write.Role = RoleMember
	}

	if err := s.tenancy.PutMember(ctx, *tenant, wallet, write); err != nil {
		return fmt.Errorf("%w: %v", ErrTenancyWriteFailed, err)
	}
	return nil
}

// remoteMember finds the wallet's shared membership, if any.
func (s *TenantService) remoteMember(ctx context.Context, tenant *models.Tenant, wallet string) (*models.RemoteMember, bool, error) {
	members, err := s.tenancy.Members(ctx, *tenant)
	if err != nil {
		return nil, false, err
	}
	for i := range members {
		if strings.EqualFold(members[i].Wallet, wallet) {
			return &members[i], true, nil
		}
	}
	return nil, false, nil
}

func (s *TenantService) tenancyReady() bool { return s.tenancy != nil && s.tenancy.Configured() }

// isMissingMembershipErr matches UpdateMemberAccess's not-found wrap without
// widening it into a general error swallow.
func isMissingMembershipErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "membership not found")
}

// unionStrings merges two sets preserving first-seen order.
func unionStrings(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := map[string]bool{}
	for _, list := range [][]string{a, b} {
		for _, v := range list {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}
