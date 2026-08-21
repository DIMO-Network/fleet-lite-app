package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/lib/pq"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

// AccessibleTokenIDs returns the set of vehicle token ids a limited member may
// touch (union of their allowed groups). Callers with unrestricted access
// should not call this — pass-through their full sets instead.
//
// An empty result means "reaches nothing". There is no value of
// allowedGroupIDs for which it means "reaches everything", and every caller
// intersects against it rather than skipping the intersection when it is empty.
func (s *VehicleService) AccessibleTokenIDs(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string) (map[int64]bool, error) {
	idx, err := s.groups.groupIndex(ctx, tenant)
	if err != nil {
		return nil, err
	}
	ids := idx.TokenIDsForGroups(allowedGroupIDs)
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	// Membership gate: with enforcement on, group scope only reaches the paid
	// subset. Geofence counts read this set, so skipping the intersection here
	// would leave those screens counting vehicles the fleet no longer shows.
	if err := s.intersectActiveMemberships(ctx, tenant, out); err != nil {
		return nil, err
	}
	return out, nil
}

// intersectActiveMemberships narrows a token-id set to the actively-membered
// subset when enforcement is on for the tenant. No-op when unenforced or when
// no membership gate is wired; an unavailable answer propagates rather than
// passing the set through unfiltered.
func (s *VehicleService) intersectActiveMemberships(ctx context.Context, tenant models.Tenant, set map[int64]bool) error {
	if s.memberships == nil {
		return nil
	}
	enforced, tokenIDs, err := s.memberships.ActiveTokens(ctx, tenant)
	if err != nil {
		return err
	}
	if !enforced {
		return nil
	}
	active := make(map[int64]bool, len(tokenIDs))
	for _, id := range tokenIDs {
		active[id] = true
	}
	for id := range set {
		if !active[id] {
			delete(set, id)
		}
	}
	return nil
}

// allowedTokensFilter is allowedGroupsFilter's index-fed form: group membership
// has already been resolved to a token-id set, so the filter is a plain
// membership test.
//
// It is `= ANY(?)` and NOT `if len(tokenIDs) > 0 { apply }`. AN EMPTY SET MUST
// MATCH ZERO VEHICLES. Postgres evaluates `token_id = ANY('{}')` to false for
// every row, which is the answer; skipping the filter on an empty set would
// hand a member scoped to empty (or vehicle-less) groups the entire fleet —
// the inversion that gave 131 memberships a 524-vehicle fleet during the
// backfill. TokenIDsForGroups never returns nil for the same reason.
func allowedTokensFilter(tokenIDs []int64) qm.QueryMod {
	return qm.Where("token_id = ANY(?)", pq.Array(tokenIDs))
}

// VehicleService syncs a tenant's privileged vehicles from identity-api into the
// DB and reads them back, scoped by tenant.
type VehicleService struct {
	logger      *zerolog.Logger
	pdb         *db.Store
	identityAPI gateway.IdentityAPI

	// groups resolves a limited member's fleet groups to token ids. nil (the
	// sync-only construction in cmd/) or an unflagged source keeps every scope
	// read on the local mirror.
	groups groupIndexSource

	// memberships answers "which vehicles are paid for". nil (the sync-only
	// construction in cmd/, or a deployment without a tenancy client) means no
	// membership filtering at all.
	memberships membershipGate

	// tenancy resolves an explicit-mode tenant's fleet: its entitled token ids
	// and the operator's minted credential. nil means credential-less tenants
	// cannot sync — a named error, not a silent skip.
	tenancy tenancySource

	// entitledCache holds one entitled token-id slice per tenant, for the READ
	// path only. Two reasons, and the second is the important one:
	//
	// Load — the set read now runs on every vehicle-list render, exactly as the
	// membership gate does. Uncached it would take this app's whole request
	// rate to tenancy.
	//
	// Freshness coherence — the membership gate and the group index are both
	// cached for 60s. A live, uncached set read against 60s-stale gates would
	// reintroduce the very mixing this path exists to remove, just with the
	// staleness on the other foot. All three legs of the intersection must age
	// together, so this shares their TTL.
	//
	// The sync path deliberately does NOT use it: a nightly job that prunes
	// rows must act on the current answer, not one up to a minute old.
	entitledCache *cache.Cache

	// metadata is the roster — what a vehicle IS, from fleet-tenancy-api.
	// nil means metadata comes from the local vehicles table, exactly as it
	// always has: the VEHICLE_METADATA_FROM_TENANCY revert path, which is a
	// config flip rather than a release. See UseVehicleMetadata.
	metadata      vehicleMetadataSource
	metadataCache *cache.Cache
}

// membershipGate is the slice of MembershipService the vehicle filters need.
type membershipGate interface {
	ActiveTokens(ctx context.Context, tenant models.Tenant) (enforced bool, tokenIDs []int64, err error)
}

// tenancySource is the slice of gateway.TenancyAPI this service uses: the
// entitled set and the mode/credential reads the sync needs. Narrow and
// interfaced for the same reason membershipGate is — so the set-resolution
// logic is testable without a live service.
type tenancySource interface {
	Configured() bool
	Entitlements(ctx context.Context, tenant models.Tenant) ([]models.RemoteEntitlement, error)
	TenantDetail(ctx context.Context, tenantID string) (*models.RemoteTenantDetail, error)
	DimoToken(ctx context.Context, tenantID string) (*models.RemoteMintedToken, error)
}

func NewVehicleService(logger *zerolog.Logger, pdb *db.Store, identityAPI gateway.IdentityAPI) *VehicleService {
	return &VehicleService{
		logger:        logger,
		pdb:           pdb,
		identityAPI:   identityAPI,
		entitledCache: cache.New(entitledTTL, 2*entitledTTL),
	}
}

// entitledTTL bounds how stale the read path's entitled set may be. It matches
// membershipTTL and the group-index window on purpose: all three gate the same
// request, and the point of resolving them together is that none is fresher
// than the others.
const entitledTTL = 60 * time.Second

// UseGroupIndex wires the group index that scopes limited members. Without it
// the scope filters read the local mirror.
func (s *VehicleService) UseGroupIndex(src groupIndexSource) { s.groups = src }

// UseMemberships wires the membership gate. Without it (sync-only builds, or
// no tenancy client) no membership filtering happens — which is also the
// correct behaviour for every tenant until an operator flips enforcement on.
func (s *VehicleService) UseMemberships(m membershipGate) { s.memberships = m }

// UseTenancy wires the tenancy client that resolves explicit-mode tenants'
// fleets. Without it, syncing a credential-less tenant errors by name.
//
// A nil *TenancyAPI leaves the field nil rather than storing a typed nil: an
// interface holding a nil pointer is not == nil, so every `s.tenancy == nil`
// guard in this file would pass and then call through it. Same trap app.go
// documents when constructing SharingService, and it is silent when it fires.
func (s *VehicleService) UseTenancy(t *gateway.TenancyAPI) {
	if t == nil {
		return
	}
	s.tenancy = t
}

// membershipFilter restricts a vehicles query to tokens with an active
// membership, when this tenant's operator has enforcement turned on.
//
// UNLIKE scopeFilter THIS APPLIES TO OWNERS TOO. scopeFilter runs only for
// limited members (allowedGroupIDs != nil); enforcement is per tenant and
// gates everyone, so this is appended unconditionally by its callers. Folding
// it into scopeFilter would silently exempt every unrestricted member — an
// owner-account test would pass and the feature would be half-off in
// production.
//
// AN EMPTY SET MUST MATCH ZERO VEHICLES. `token_id = ANY('{}')` is false for
// every row, which is the correct answer for a tenant with enforcement on and
// no active memberships — that customer has paid for nothing. Do not rewrite
// this as `if len(tokenIDs) > 0 { apply }`; see allowedTokensFilter above for
// what that inversion did during the backfill.
//
// A tenancy failure propagates as ErrMembershipScopeUnavailable and must never
// degrade into an unfiltered query, mirroring ErrGroupScopeUnavailable.
func (s *VehicleService) membershipFilter(ctx context.Context, tenant models.Tenant) (qm.QueryMod, error) {
	if s.memberships == nil {
		return nil, nil
	}
	enforced, tokenIDs, err := s.memberships.ActiveTokens(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if !enforced {
		return nil, nil
	}
	return qm.Where("token_id = ANY(?)", pq.Array(tokenIDs)), nil
}

// scopeFilter builds the limited-member vehicle filter for whichever read path
// is active.
//
// A tenancy failure propagates as ErrGroupScopeUnavailable; it must never
// degrade into an unfiltered query. Call this only when the caller is limited —
// nil allowedGroupIDs means unrestricted and no filter at all.
func (s *VehicleService) scopeFilter(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string) (qm.QueryMod, error) {
	idx, err := s.groups.groupIndex(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return allowedTokensFilter(idx.TokenIDsForGroups(allowedGroupIDs)), nil
}

// resolvesFromTenancy reports whether this tenant's vehicle SET comes from
// fleet-tenancy-api rather than from the local table.
//
// Only credential-less (operator-managed, explicit-mode) tenants. A self-serve
// tenant holding its own DIMO client id has no entitlement set to resolve —
// its fleet genuinely IS whatever its developer license is privileged on, which
// the local table caches — so for those the local-table-plus-filters path is
// correct, not merely legacy.
func (s *VehicleService) resolvesFromTenancy(tenant models.Tenant) bool {
	return tenant.ClientID == "" && s.tenancy != nil && s.tenancy.Configured()
}

// resolveTokenSet answers "which vehicles are this caller's", from one source
// at one freshness: entitled ∩ active memberships ∩ group scope, every leg read
// from fleet-tenancy-api.
//
// THIS IS THE FIX FOR THE 2026-08-19 EMPTY-FLEET INCIDENT, and the reason it is
// a set intersection rather than a query with filters. The old path took the
// SET from the nightly local cache and applied the membership and group gates
// live on 60-second TTLs. TRAST's cached set held one revoked token; the live
// gates correctly excluded it; cached-set ∩ live-gate = ∅ and the customer saw
// nothing. Had everything been stale they would have seen one wrong vehicle;
// had everything been live, nine. Zero was the artifact of mixing, and it was
// silent. Do not reintroduce a gate that reads from a different snapshot than
// the set.
//
// Returned ids are sorted so callers are deterministic.
func (s *VehicleService) resolveTokenSet(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string) ([]int64, error) {
	entitled, err := s.entitledTokenIDs(ctx, tenant)
	if err != nil {
		return nil, err
	}
	set := make(map[int64]bool, len(entitled))
	for _, id := range entitled {
		set[id] = true
	}

	// Membership gate. Applies to owners too — enforcement is per tenant, not
	// per member — which is why it is not folded into the scope block below.
	// An unavailable answer propagates; it must never degrade into an
	// unfiltered set.
	if err := s.intersectActiveMemberships(ctx, tenant, set); err != nil {
		return nil, err
	}

	// Group scope, for limited members only. nil means unrestricted; an empty
	// allowed-group list means "reaches nothing" and must survive as an empty
	// set rather than being skipped — the inversion that once handed a member
	// scoped to empty groups the entire fleet.
	if allowedGroupIDs != nil {
		idx, gerr := s.groups.groupIndex(ctx, tenant)
		if gerr != nil {
			return nil, gerr
		}
		inScope := make(map[int64]bool)
		for _, id := range idx.TokenIDsForGroups(allowedGroupIDs) {
			inScope[id] = true
		}
		for id := range set {
			if !inScope[id] {
				delete(set, id)
			}
		}
	}

	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// entitledTokenIDs is the cached read-path entitlement lookup. Only successes
// are cached, exactly as the authz, membership and group-index caches do it: a
// cached failure would extend an outage past its cause.
func (s *VehicleService) entitledTokenIDs(ctx context.Context, tenant models.Tenant) ([]int64, error) {
	if s.entitledCache != nil {
		if cached, found := s.entitledCache.Get(tenant.ID); found {
			return cached.([]int64), nil
		}
	}
	ents, err := s.tenancy.Entitlements(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("entitlements for tenant %s: %w", tenant.ID, err)
	}
	out := make([]int64, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.VehicleTokenID)
	}
	if s.entitledCache != nil {
		s.entitledCache.Set(tenant.ID, out, cache.DefaultExpiration)
	}
	return out, nil
}

// listResolvedVehicles builds the vehicle list for a tenant whose set comes
// from tenancy: resolve the set, then LEFT-JOIN the local table for metadata.
//
// THE JOIN DIRECTION IS THE WHOLE POINT. Every token in the resolved set
// appears, whether or not a metadata row exists for it; one with no row comes
// back carrying its token id and MetadataPending. An inner join here — or any
// "skip tokens we have no row for" — would move the bug rather than fix it: the
// set would be provably correct while the response stayed short, which is
// harder to see than the original. See TestListResolvedVehiclesMissingRow.
func (s *VehicleService) listResolvedVehicles(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string) ([]models.Vehicle, error) {
	tokenIDs, err := s.resolveTokenSet(ctx, tenant, allowedGroupIDs)
	if err != nil {
		return nil, err
	}

	rows, err := dbmodels.Vehicles(
		qm.Where("tenant_id = ?", tenant.ID),
		qm.Where("token_id = ANY(?)", pq.Array(tokenIDs)),
		qm.OrderBy("last_seen DESC NULLS LAST, token_id"),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	favorites, err := s.favoriteSet(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}

	// The cutover (plan 07 step 4). The set above is unchanged and still comes
	// from the same three gates; only where the METADATA comes from moves. The
	// local rows are still read either way — they hold the last GPS fix, which
	// is app-local and is not in the roster.
	if s.metadata != nil {
		meta, merr := s.rosterMetadata(ctx, tenant, tokenIDs)
		if merr != nil {
			return nil, merr
		}
		return mergeRosterVehicles(tokenIDs, meta, rows, favorites), nil
	}

	return mergeResolvedVehicles(tokenIDs, rows, favorites), nil
}

// mergeResolvedVehicles is the LEFT-JOIN itself: every token in the resolved
// set becomes a vehicle, using the local row when there is one and a thin
// MetadataPending placeholder when there is not.
//
// Pure, so the missing-row case is testable without a database — and it is the
// case worth testing. An inner join, or a "skip tokens we have no row for",
// yields a provably correct set with a short response, which is strictly harder
// to diagnose than the empty fleet this whole plan step exists to fix.
//
// Ordering: cached rows first in the caller's last-seen order, thin rows after
// in token order. A vehicle with no metadata has no last_seen to sort by, so it
// goes at the end rather than being interleaved by a value it does not have.
func mergeResolvedVehicles(tokenIDs []int64, rows []*dbmodels.Vehicle, favorites map[int64]bool) []models.Vehicle {
	byToken := make(map[int64]bool, len(rows))
	for _, r := range rows {
		byToken[r.TokenID] = true
	}

	out := make([]models.Vehicle, 0, len(tokenIDs))
	for _, r := range rows {
		v := rowToVehicle(r)
		v.IsFavorite = favorites[v.TokenID]
		v.LicensePlate = r.LicensePlate.String
		v.VIN = r.Vin.String
		applyLastLocation(&v, r)
		out = append(out, v)
	}
	for _, id := range tokenIDs {
		if byToken[id] {
			continue
		}
		out = append(out, models.Vehicle{
			TokenID:         id,
			IsFavorite:      favorites[id],
			MetadataPending: true,
		})
	}
	return out
}

// SyncVehicles refreshes the tenant's rows in the vehicles table and returns
// the count synced.
//
// Two paths, chosen by whether the tenant holds its own credentials:
//
//   - Self-serve (ClientID set): everything the tenant's developer license is
//     privileged on. Additive, exactly as it always was — removal is the
//     manual prune-unshared-vehicles command's job.
//   - Operator-managed (no ClientID): the entitled set carved out of the
//     operator's fleet, and rows that leave it are DELETED. Deletion is safe
//     precisely because the entitled set is authoritative and exclusive per
//     operator — and required, because revoking an entitlement is the
//     operator taking a vehicle away, which additive sync would never show.
func (s *VehicleService) SyncVehicles(ctx context.Context, tenant *models.Tenant) (int, error) {
	if tenant.ClientID == "" {
		return s.syncEntitledVehicles(ctx, tenant)
	}
	vehicles, err := s.identityAPI.FetchPrivilegedVehicles(tenant.ClientID)
	if err != nil {
		return 0, fmt.Errorf("fetch privileged vehicles: %w", err)
	}

	for _, v := range vehicles {
		if err := s.upsertIdentityVehicle(ctx, tenant.ID, v); err != nil {
			return 0, err
		}
	}
	return len(vehicles), nil
}

// syncEntitledVehicles materialises an explicit-mode tenant's fleet: the
// entitled token ids from fleet-tenancy-api, with metadata from the operator's
// privileged set under the operator's minted credential.
func (s *VehicleService) syncEntitledVehicles(ctx context.Context, tenant *models.Tenant) (int, error) {
	if s.tenancy == nil || !s.tenancy.Configured() {
		return 0, fmt.Errorf("tenant %s has no DIMO client ID and no tenancy client is configured", tenant.ID)
	}

	// The mode read is what disambiguates the entitlement endpoint's 200 []:
	// for an implicit-mode tenant an empty list means "ask the license", and
	// a credential-less implicit tenant is a configuration hole worth naming,
	// not a fleet worth emptying.
	detail, err := s.tenancy.TenantDetail(ctx, tenant.ID)
	if err != nil {
		return 0, fmt.Errorf("resolve tenant %s from tenancy: %w", tenant.ID, err)
	}
	if detail.EntitlementMode != "explicit" {
		return 0, fmt.Errorf("tenant %s is %s-mode but holds no credentials; nothing to sync from",
			tenant.ID, detail.EntitlementMode)
	}

	ents, err := s.tenancy.Entitlements(ctx, *tenant)
	if err != nil {
		return 0, fmt.Errorf("entitlements for tenant %s: %w", tenant.ID, err)
	}
	entitled := make([]int64, 0, len(ents))
	for _, e := range ents {
		entitled = append(entitled, e.VehicleTokenID)
	}

	synced := 0
	if len(entitled) > 0 {
		// The operator's client id comes from the minted effective credential —
		// the identity query enumerates the operator's whole privileged set,
		// and the entitled ids select this tenant's slice of it.
		minted, merr := s.tenancy.DimoToken(ctx, tenant.ID)
		if merr != nil {
			return 0, fmt.Errorf("effective credential for tenant %s: %w", tenant.ID, merr)
		}
		vehicles, ferr := s.identityAPI.FetchPrivilegedVehicles(minted.ClientID)
		if ferr != nil {
			return 0, fmt.Errorf("fetch operator privileged vehicles: %w", ferr)
		}

		want := make(map[int64]bool, len(entitled))
		for _, id := range entitled {
			want[id] = true
		}
		for _, v := range vehicles {
			if !want[v.TokenID] {
				continue
			}
			if err := s.upsertIdentityVehicle(ctx, tenant.ID, v); err != nil {
				return 0, err
			}
			delete(want, v.TokenID)
			synced++
		}
		// Entitled but absent from the operator's privileged set: usually a
		// revoked or never-granted SACD. The entitlement stands, the data does
		// not — a data condition to surface, not a sync failure.
		if len(want) > 0 {
			missing := make([]int64, 0, len(want))
			for id := range want {
				missing = append(missing, id)
			}
			s.logger.Warn().Str("tenant", tenant.ID).Ints64("token_ids", missing).
				Msg("entitled vehicles missing from the operator's privileged set")
		}
	}

	// Rows outside the entitled set were revoked (or the set is now empty):
	// the operator took them away, so they go. Favorites, group memberships
	// and geofence assignments cascade with the row.
	res, err := dbmodels.Vehicles(
		qm.Where("tenant_id = ?", tenant.ID),
		qm.Where("NOT (token_id = ANY(?))", pq.Array(entitled)),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	if err != nil {
		return 0, fmt.Errorf("prune revoked vehicles for tenant %s: %w", tenant.ID, err)
	}
	if res > 0 {
		s.logger.Info().Str("tenant", tenant.ID).Int64("removed", res).
			Msg("pruned vehicles no longer entitled")
	}
	return synced, nil
}

// upsertIdentityVehicle writes one identity-api vehicle node as this tenant's
// row — the shared half of both sync paths.
func (s *VehicleService) upsertIdentityVehicle(ctx context.Context, tenantID string, v models.Vehicle) error {
	raw, _ := json.Marshal(v)
	row := &dbmodels.Vehicle{
		TenantID:     tenantID,
		TokenID:      v.TokenID,
		OwnerAddress: null.StringFrom(v.Owner),
		Make:         null.StringFrom(v.Definition.Make),
		Model:        null.StringFrom(v.Definition.Model),
		Year:         null.IntFrom(v.Definition.Year),
		DefinitionID: null.StringFrom(v.Definition.ID),
		Raw:          null.JSONFrom(raw),
		SyncedAt:     time.Now(),
	}
	if v.AftermarketDevice != nil {
		row.DeviceType = null.StringFrom("aftermarket")
		row.Imei = null.StringFrom(v.AftermarketDevice.IMEI)
		row.Serial = null.StringFrom(v.AftermarketDevice.Serial)
	} else if v.SyntheticDevice.TokenID != 0 {
		row.DeviceType = null.StringFrom("synthetic")
	}
	if v.MintedAt != nil {
		row.MintedAt = null.TimeFrom(*v.MintedAt)
	}

	if err := row.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{"tenant_id", "token_id"},
		boil.Whitelist("owner_address", "make", "model", "year", "definition_id",
			"device_type", "imei", "serial", "minted_at", "raw", "synced_at", "updated_at"),
		boil.Infer()); err != nil {
		return fmt.Errorf("upsert vehicle %d: %w", v.TokenID, err)
	}
	return nil
}

// ListVehicles returns the tenant's synced vehicles in identity-api Vehicle
// shape, with IsFavorite populated from the tenant's favorites.
//
// allowedGroupIDs carries the nil-vs-empty distinction from GetAllowedGroups
// intact: nil is unrestricted and skips filtering entirely, non-nil is a
// limited member and is ALWAYS filtered — an empty slice is a member who sees
// nothing, not a member who sees everything.
func (s *VehicleService) ListVehicles(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string) ([]models.Vehicle, error) {
	// Operator-managed tenants resolve the set from tenancy and use the local
	// table only for metadata. Self-serve tenants keep the local-table path,
	// which for them is correct rather than legacy — see resolvesFromTenancy.
	if s.resolvesFromTenancy(tenant) {
		return s.listResolvedVehicles(ctx, tenant, allowedGroupIDs)
	}
	mods := []qm.QueryMod{
		qm.Where("tenant_id = ?", tenant.ID),
		// Most-recently-seen first (the composite idx_vehicles_tenant_last_seen
		// serves this filter+sort); never-seen vehicles sort last, token_id as a
		// stable tiebreaker. Favourites are pinned to the top client-side.
		qm.OrderBy("last_seen DESC NULLS LAST, token_id"),
	}
	// Membership gate first, unconditionally — it applies to owners too, which
	// is exactly what makes it not foldable into the limited-member block below.
	if mfilter, err := s.membershipFilter(ctx, tenant); err != nil {
		return nil, err
	} else if mfilter != nil {
		mods = append(mods, mfilter)
	}
	if allowedGroupIDs != nil {
		filter, err := s.scopeFilter(ctx, tenant, allowedGroupIDs)
		if err != nil {
			return nil, err
		}
		mods = append(mods, filter)
	}
	rows, err := dbmodels.Vehicles(mods...).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	favorites, err := s.favoriteSet(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	out := make([]models.Vehicle, 0, len(rows))
	for _, r := range rows {
		v := rowToVehicle(r)
		v.IsFavorite = favorites[v.TokenID]
		v.LicensePlate = r.LicensePlate.String
		v.VIN = r.Vin.String
		applyLastLocation(&v, r)
		out = append(out, v)
	}
	return out, nil
}

// GetVehicle returns one synced vehicle for the tenant, or an error if not
// found. A non-nil allowedGroupIDs additionally requires the vehicle to be in
// one of those groups — out-of-scope vehicles error exactly like nonexistent
// ones, so limited members can't probe what exists.
func (s *VehicleService) GetVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, allowedGroupIDs []string) (*models.Vehicle, error) {
	if s.resolvesFromTenancy(tenant) {
		return s.getResolvedVehicle(ctx, tenant, tokenID, allowedGroupIDs)
	}
	mods := []qm.QueryMod{
		qm.Where("tenant_id = ?", tenant.ID),
		qm.And("token_id = ?", tokenID),
	}
	// Membership gate, owners included. An unmembered vehicle misses this
	// filter and returns the same error a nonexistent one does — the existing
	// 404-not-403 convention, so nobody can probe what exists.
	if mfilter, err := s.membershipFilter(ctx, tenant); err != nil {
		return nil, err
	} else if mfilter != nil {
		mods = append(mods, mfilter)
	}
	if allowedGroupIDs != nil {
		filter, ferr := s.scopeFilter(ctx, tenant, allowedGroupIDs)
		if ferr != nil {
			return nil, ferr
		}
		mods = append(mods, filter)
	}
	r, err := dbmodels.Vehicles(mods...).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	v := rowToVehicle(r)
	v.IsFavorite, err = s.IsFavorite(ctx, tenant.ID, tokenID)
	if err != nil {
		return nil, err
	}
	v.LicensePlate = r.LicensePlate.String
	v.VIN = r.Vin.String
	applyLastLocation(&v, r)
	return &v, nil
}

// getResolvedVehicle is GetVehicle for a tenant whose set comes from tenancy.
//
// Membership in the resolved set is the authorization decision; the local row
// only supplies metadata. A token in the set with no row returns a thin vehicle
// rather than the not-found the old path gave — that 404 was the single-vehicle
// face of the same incident, and a customer deep-linking to a freshly-entitled
// vehicle hit it.
//
// A token outside the set returns sql.ErrNoRows, exactly as a nonexistent one
// does. Out-of-scope and nonexistent must stay indistinguishable so a limited
// member cannot probe what exists — the same 404-not-403 convention the old
// path relied on its filters to produce.
func (s *VehicleService) getResolvedVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, allowedGroupIDs []string) (*models.Vehicle, error) {
	tokenIDs, err := s.resolveTokenSet(ctx, tenant, allowedGroupIDs)
	if err != nil {
		return nil, err
	}
	inSet := false
	for _, id := range tokenIDs {
		if id == tokenID {
			inSet = true
			break
		}
	}
	if !inSet {
		return nil, sql.ErrNoRows
	}

	favorite, err := s.IsFavorite(ctx, tenant.ID, tokenID)
	if err != nil {
		return nil, err
	}

	r, err := dbmodels.Vehicles(
		qm.Where("tenant_id = ?", tenant.ID),
		qm.And("token_id = ?", tokenID),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var local []*dbmodels.Vehicle
	if r != nil {
		local = []*dbmodels.Vehicle{r}
	}

	// The detail view reads from the same place the list does, or the two
	// disagree about one vehicle on two screens — which is the whole failure
	// this plan is about, at the smallest possible scale.
	if s.metadata != nil {
		meta, merr := s.rosterMetadata(ctx, tenant, []int64{tokenID})
		if merr != nil {
			return nil, merr
		}
		merged := mergeRosterVehicles([]int64{tokenID}, meta, local,
			map[int64]bool{tokenID: favorite})
		return &merged[0], nil
	}

	if r == nil {
		return &models.Vehicle{TokenID: tokenID, IsFavorite: favorite, MetadataPending: true}, nil
	}
	v := rowToVehicle(r)
	v.IsFavorite = favorite
	v.LicensePlate = r.LicensePlate.String
	v.VIN = r.Vin.String
	applyLastLocation(&v, r)
	return &v, nil
}

// applyLastLocation copies the cached last-GPS-fix columns onto the assembled
// Vehicle. Kept off rowToVehicle because those columns aren't part of the
// identity `raw` shape — same reasoning as IsFavorite/LicensePlate/VIN.
func applyLastLocation(v *models.Vehicle, r *dbmodels.Vehicle) {
	if r.LastLat.Valid {
		lat := r.LastLat.Float64
		v.LastLat = &lat
	}
	if r.LastLon.Valid {
		lon := r.LastLon.Float64
		v.LastLon = &lon
	}
	if r.LastSeen.Valid {
		ts := r.LastSeen.Time
		v.LastSeen = &ts
	}
	if r.LocationPulledAt.Valid {
		ts := r.LocationPulledAt.Time
		v.LocationPulledAt = &ts
	}
}

// UpsertLastLocations writes through the latest GPS fix for each vehicle into
// its row (the last_lat/last_lon/last_seen display cache). Best-effort: a fix
// with no/unparseable timestamp is skipped so we never stamp last_seen with a
// zero time, and a per-row failure is logged but never fails the batch. Only
// the three location columns (plus updated_at) are touched; a vehicle missing
// from the table simply updates zero rows.
func (s *VehicleService) UpsertLastLocations(ctx context.Context, tenantID string, locs map[uint64]LocationCoords) {
	for id, c := range locs {
		ts, err := time.Parse(time.RFC3339, c.Timestamp)
		if err != nil {
			continue
		}
		row := &dbmodels.Vehicle{
			TenantID: tenantID,
			TokenID:  int64(id),
			LastLat:  null.Float64From(c.Lat),
			LastLon:  null.Float64From(c.Lon),
			LastSeen: null.TimeFrom(ts),
		}
		if _, err := row.Update(ctx, s.pdb.DBS().Writer,
			boil.Whitelist("last_lat", "last_lon", "last_seen", "updated_at")); err != nil {
			s.logger.Warn().Uint64("tokenId", id).Err(err).Msg("write-through last location")
		}
	}
}

// StampLocationPulled records that we just fetched these vehicles' locations
// from telemetry-api (location_pulled_at = now) so the freshness window can
// suppress re-pulls. Call with FleetLocationsResult.Fetched — the vehicles
// actually queried this call, regardless of outcome (coords / no-data /
// no-permission) — NOT cache hits, whose pulled_at must keep its earlier value.
// Best-effort: one batched UPDATE, logged-and-swallowed on failure.
func (s *VehicleService) StampLocationPulled(ctx context.Context, tenantID string, tokenIDs []uint64) {
	if len(tokenIDs) == 0 {
		return
	}
	ids := make([]int64, len(tokenIDs))
	for i, id := range tokenIDs {
		ids[i] = int64(id)
	}
	now := time.Now()
	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleWhere.TokenID.IN(ids),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{
		"location_pulled_at": now,
		"updated_at":         now,
	}); err != nil {
		s.logger.Warn().Err(err).Str("tenant_id", tenantID).Msg("stamp location_pulled_at")
	}
}

// favoriteSet returns the tenant's favorited token IDs as a lookup set.
func (s *VehicleService) favoriteSet(ctx context.Context, tenantID string) (map[int64]bool, error) {
	rows, err := dbmodels.VehicleFavorites(
		qm.Where("tenant_id = ?", tenantID),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(rows))
	for _, r := range rows {
		out[r.TokenID] = true
	}
	return out, nil
}

// IsFavorite reports whether the tenant has starred the given vehicle.
func (s *VehicleService) IsFavorite(ctx context.Context, tenantID string, tokenID int64) (bool, error) {
	return dbmodels.VehicleFavorites(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("token_id = ?", tokenID),
	).Exists(ctx, s.pdb.DBS().Reader)
}

// AddFavorite stars a vehicle for the tenant. Idempotent — re-starring an
// already-favorited vehicle is a no-op.
func (s *VehicleService) AddFavorite(ctx context.Context, tenantID string, tokenID int64) error {
	row := &dbmodels.VehicleFavorite{TenantID: tenantID, TokenID: tokenID}
	return row.Upsert(ctx, s.pdb.DBS().Writer, false, []string{"tenant_id", "token_id"}, boil.None(), boil.Infer())
}

// RemoveFavorite unstars a vehicle for the tenant. Idempotent — unstarring a
// vehicle that isn't favorited is a no-op.
func (s *VehicleService) RemoveFavorite(ctx context.Context, tenantID string, tokenID int64) error {
	_, err := dbmodels.VehicleFavorites(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("token_id = ?", tokenID),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	return err
}

// rowToVehicle reconstructs the identity Vehicle shape from a DB row, preferring
// the stored raw identity node and falling back to the flat columns.
func rowToVehicle(r *dbmodels.Vehicle) models.Vehicle {
	if r.Raw.Valid && len(r.Raw.JSON) > 0 {
		var v models.Vehicle
		if err := json.Unmarshal(r.Raw.JSON, &v); err == nil {
			return v
		}
	}
	return models.Vehicle{
		TokenID: r.TokenID,
		Owner:   r.OwnerAddress.String,
		Definition: models.Definition{
			ID:    r.DefinitionID.String,
			Make:  r.Make.String,
			Model: r.Model.String,
			Year:  r.Year.Int,
		},
	}
}
