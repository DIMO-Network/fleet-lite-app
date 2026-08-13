package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/lib/pq"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

// GroupRef is the slim group reference embedded in the /vehicles filter
// payload and the per-vehicle groups endpoint. Aliased to the models type so
// the same shape flows through the whole feature.
type GroupRef = models.GroupRef

// ErrGroupNameExists is returned by CreateGroup when a group with the same name
// already exists for the tenant (the (tenant_id, name) unique constraint).
var ErrGroupNameExists = errors.New("fleet group with this name already exists")

// ErrGroupNotFound is returned when a group id does not exist for the tenant.
var ErrGroupNotFound = errors.New("fleet group not found")

// ErrVehicleNotFound is returned when a vehicle token id is not a synced vehicle
// for the tenant.
var ErrVehicleNotFound = errors.New("vehicle not found for tenant")

// ErrGroupScopeUnavailable means the group structure that scopes a limited
// member's access could not be read from fleet-tenancy-api.
//
// It is deliberately not recoverable into an unfiltered read. Callers map it to
// 503, matching NewTenantMiddleware's treatment of an unavailable authz answer:
// this is our failure, not the caller's. The local tables are the *revert* path
// (GROUPS_FROM_TENANCY), not a runtime fallback — a fallback consulted only
// during an incident is a fallback nobody has verified.
var ErrGroupScopeUnavailable = errors.New("fleet group scope is unavailable")

// GroupWithCount is a fleet group plus its current member count, returned by
// ListGroups for the management UI.
type GroupWithCount struct {
	Group        *dbmodels.FleetGroup
	VehicleCount int
}

// FleetGroupService owns tenant-scoped CRUD over fleet groups and their vehicle
// memberships. It is pure data access — write-path attestation is triggered by
// the controller after a successful mutation (see Decision 1 in the plan).
type FleetGroupService struct {
	logger *zerolog.Logger
	pdb    *db.Store

	// tenancy is the fleet-tenancy-api client. Since P4 every WRITE goes
	// through it — the service owns the record and the local tables are a
	// synchronously-maintained mirror until P5 drops them. Since P5 every group
	// READ comes from it too when readFromTenancy is set (GROUPS_FROM_TENANCY),
	// including the scope filters in vehicle.go / geofence.go / invitation.go
	// that used to join the mirror.
	tenancy remoteGroupSource
	// readFromTenancy flips every group read to the remote source. It is the
	// revert path, not a runtime fallback: with it on, a remote failure fails
	// the request rather than falling back to the mirror.
	readFromTenancy bool

	// indexCache holds one GroupIndex per tenant id.
	//
	// gateway.TenancyAPI.VehicleGroups documents itself as "deliberately
	// uncached ... group reads are screen-shaped rather than per-request". That
	// premise expires here. Since P5 the vehicle scope filter resolves a limited
	// member's groups on EVERY request, so uncached the endpoint would take this
	// app's whole request rate — the same reason Authz is cached.
	//
	// The cache lives on this service rather than in the gateway because only
	// this service sees the writes: every group mutation funnels through
	// CreateGroup/UpdateGroup/DeleteGroup/AddVehicle/RemoveVehicle, each of
	// which busts the entry. The TTL therefore bounds staleness from *other*
	// writers (an operator in b2b-fleet-mgr-app), not from our own.
	indexCache *cache.Cache
}

// groupIndexTTL bounds how stale a cached index may be. It matches the tenancy
// client's authz window: the two answers gate the same request, and there is no
// sense in one being fresher than the other.
const groupIndexTTL = 60 * time.Second

// groupIndexSource is the slice of FleetGroupService that the group-scoped
// services (vehicles, geofences, invitations) need. An interface so each of
// them is testable without a live tenancy client.
type groupIndexSource interface {
	groupsIndexed() bool
	groupIndex(ctx context.Context, tenant models.Tenant) (*GroupIndex, error)
}

// remoteGroupSource is the slice of gateway.TenancyAPI this service needs.
// An interface so both directions are testable without a live service.
type remoteGroupSource interface {
	VehicleGroups(ctx context.Context, tenant models.Tenant) ([]models.RemoteFleetGroup, error)
	CreateGroup(ctx context.Context, tenant models.Tenant, name, color string) error
	UpdateGroup(ctx context.Context, tenant models.Tenant, groupID string, name, color *string) error
	DeleteGroup(ctx context.Context, tenant models.Tenant, groupID string) error
	AddGroupVehicles(ctx context.Context, tenant models.Tenant, groupID string, tokenIDs []int64) error
	RemoveGroupVehicle(ctx context.Context, tenant models.Tenant, groupID string, tokenID int64) error
}

func NewFleetGroupService(logger *zerolog.Logger, pdb *db.Store) *FleetGroupService {
	return &FleetGroupService{
		logger:     logger,
		pdb:        pdb,
		indexCache: cache.New(groupIndexTTL, 2*groupIndexTTL),
	}
}

// UseTenancy wires the tenancy client. Writes go through it unconditionally —
// there is deliberately no local-only write path left, because a write that
// reports success without reaching the record's owner is the
// reported-success-conferred-nothing bug the membership cutover already hit.
// readFromTenancy additionally serves the *View reads remotely.
func (s *FleetGroupService) UseTenancy(client remoteGroupSource, readFromTenancy bool) {
	s.tenancy = client
	s.readFromTenancy = readFromTenancy
}

// ----- The group index (P5 of the groups move) -----
//
// Every remaining reader of fleet_groups / vehicle_fleet_groups asks a set
// question — which tokens are in these groups, which groups is this token in,
// do these ids exist — and GET /v1/tenants/{id}/vehicle-groups answers all of
// them in one call. GroupIndex is that answer arranged for lookup, which is
// what lets the scope filters stop joining tables P5 is about to drop.

// GroupIndex is one tenant's whole group structure, indexed for the lookups the
// scope filters and the display reads need.
//
// Treat it as immutable: instances are shared between concurrent requests out
// of indexCache, so every method returns a fresh slice rather than the stored
// one. Nothing here mutates the receiver.
type GroupIndex struct {
	// ordered is every group, name-ordered — the shape ListGroupsView and
	// GetGroupView answer from, carrying the count and timestamps GroupRef does
	// not.
	ordered []models.RemoteFleetGroup
	// byID is the slim reference for each group id.
	byID map[string]GroupRef
	// members maps group id -> its member token ids, deduped and ascending.
	members map[string][]int64
	// byToken maps token id -> the groups it belongs to, name-ordered.
	byToken map[int64][]GroupRef
}

// NewGroupIndex builds the index from one VehicleGroups answer.
//
// Groups are sorted by name here rather than trusted from the wire: the
// per-vehicle lists below are name-ordered only because this loop appends in
// that order, and that ordering is the contract the local SQL (ORDER BY
// fg.name) established and the UI renders against.
func NewGroupIndex(groups []models.RemoteFleetGroup) *GroupIndex {
	ordered := slices.Clone(groups)
	slices.SortFunc(ordered, func(a, b models.RemoteFleetGroup) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		// Names collide only mid-rename; the id keeps the order total.
		return strings.Compare(a.ID, b.ID)
	})

	idx := &GroupIndex{
		ordered: ordered,
		byID:    make(map[string]GroupRef, len(ordered)),
		members: make(map[string][]int64, len(ordered)),
		byToken: make(map[int64][]GroupRef),
	}
	for _, g := range ordered {
		ref := GroupRef{ID: g.ID, Name: g.Name, Color: g.Color}
		idx.byID[g.ID] = ref
		ids := slices.Clone(g.TokenIDs)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		idx.members[g.ID] = ids
		for _, tokenID := range ids {
			idx.byToken[tokenID] = append(idx.byToken[tokenID], ref)
		}
	}
	return idx
}

// TokenIDsForGroups returns the union of the given groups' member token ids,
// deduped and ascending. Ids naming no group contribute nothing.
//
// The result is always non-nil, including when the union is empty. An empty
// token set is a real answer — "this member reaches no vehicle" — and every
// filter built from it must express exactly that. See allowedTokensFilter.
func (i *GroupIndex) TokenIDsForGroups(groupIDs []string) []int64 {
	out := []int64{}
	for _, gid := range groupIDs {
		out = append(out, i.members[gid]...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// CountForGroups is the size of that union — the vehicle count of a
// group-scoped geofence, where a vehicle in two of its groups counts once.
func (i *GroupIndex) CountForGroups(groupIDs []string) int {
	return len(i.TokenIDsForGroups(groupIDs))
}

// GroupsForVehicle returns the groups a vehicle belongs to, name-ordered.
func (i *GroupIndex) GroupsForVehicle(tokenID int64) []GroupRef {
	return slices.Clone(i.byToken[tokenID])
}

// VehicleGroupsMap returns token id -> its groups for the whole tenant, each
// list name-ordered. Vehicles in no group are absent, as in the SQL it replaces.
func (i *GroupIndex) VehicleGroupsMap() map[int64][]GroupRef {
	out := make(map[int64][]GroupRef, len(i.byToken))
	for tokenID, refs := range i.byToken {
		out[tokenID] = slices.Clone(refs)
	}
	return out
}

// MemberTokenIDs returns one group's member token ids, ascending. An unknown
// group has no members, which is also what the local query answered.
func (i *GroupIndex) MemberTokenIDs(groupID string) []int64 {
	return slices.Clone(i.members[groupID])
}

// VehicleInGroups reports whether the vehicle belongs to at least one of the
// given groups.
func (i *GroupIndex) VehicleInGroups(tokenID int64, groupIDs []string) bool {
	for _, ref := range i.byToken[tokenID] {
		if slices.Contains(groupIDs, ref.ID) {
			return true
		}
	}
	return false
}

// AllExist reports whether every id names a group in this tenant — the
// validation behind "one or more group ids do not exist".
func (i *GroupIndex) AllExist(groupIDs []string) bool {
	for _, gid := range groupIDs {
		if _, ok := i.byID[gid]; !ok {
			return false
		}
	}
	return true
}

// Get returns one group's slim reference.
func (i *GroupIndex) Get(groupID string) (GroupRef, bool) {
	ref, ok := i.byID[groupID]
	return ref, ok
}

// all returns every group name-ordered, with counts and timestamps.
func (i *GroupIndex) all() []models.RemoteFleetGroup { return i.ordered }

// group returns one group with its counts, timestamps and member set.
func (i *GroupIndex) group(groupID string) (models.RemoteFleetGroup, bool) {
	for _, g := range i.ordered {
		if g.ID == groupID {
			return g, true
		}
	}
	return models.RemoteFleetGroup{}, false
}

// groupsIndexed reports whether group reads are served from the index. False
// keeps every caller on the local mirror — the revert path.
func (s *FleetGroupService) groupsIndexed() bool {
	return s.readFromTenancy && s.tenancy != nil
}

// groupIndex returns the tenant's index, from cache when warm.
//
// Only successes are cached, exactly as gateway.TenancyAPI.Authz does it: a
// cached rejection would extend an outage past its cause — the failure would
// outlive the fix.
func (s *FleetGroupService) groupIndex(ctx context.Context, tenant models.Tenant) (*GroupIndex, error) {
	if s.tenancy == nil {
		return nil, fmt.Errorf("%w: tenancy client is not configured", ErrGroupScopeUnavailable)
	}
	if cached, found := s.indexCache.Get(tenant.ID); found {
		return cached.(*GroupIndex), nil
	}
	groups, err := s.tenancy.VehicleGroups(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("%w: load fleet groups from tenancy: %w", ErrGroupScopeUnavailable, err)
	}
	idx := NewGroupIndex(groups)
	s.indexCache.Set(tenant.ID, idx, cache.DefaultExpiration)
	return idx, nil
}

// invalidateGroupIndex drops a tenant's cached index. Called after every
// successful remote write, which is strictly better than waiting out the TTL
// and is the reason the cache sits on this service at all.
func (s *FleetGroupService) invalidateGroupIndex(tenantID string) {
	s.indexCache.Delete(tenantID)
}

var slugNonAlphanum = regexp.MustCompile(`[^a-z0-9]+`)

// slug derives a stable, url/attestation-safe id from a group name.
func slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugNonAlphanum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// GroupIDSeparator divides the tenant uuid from the slug in a fleet group id.
//
// '_' is unambiguous: slug() collapses every non-alphanumeric run to '-', so a
// slug never contains '_', and a uuid contains '-' but never '_'. A tenant-scoped
// id therefore holds exactly one '_' and a legacy (pre-migration) id holds none,
// which is what lets TenantOwnsGroupID tell them apart without a lookup.
const GroupIDSeparator = "_"

// GroupID builds a tenant-scoped fleet group id: <tenant-uuid>_<slug>.
//
// The tenant prefix does two jobs. It makes ids globally unique, fixing a live
// collision where the second tenant to create "Vans" was told the name was taken
// by a group in another tenant. And it makes the id self-attributing: under a
// shared operator developer license every producer's group CloudEvents carry the
// same `source`, so data.groups[].id is the only field that can tell two
// tenants' groups apart.
func GroupID(tenantID, name string) string {
	s := slug(name)
	if s == "" {
		return ""
	}
	return tenantID + GroupIDSeparator + s
}

// TenantOwnsGroupID reports whether a group id belongs to the given tenant.
// Legacy bare-slug ids (no separator) carry no tenant and return false.
func TenantOwnsGroupID(tenantID, groupID string) bool {
	i := strings.Index(groupID, GroupIDSeparator)
	return i > 0 && groupID[:i] == tenantID
}

// ListGroups returns the tenant's groups with member counts, ordered by name.
func (s *FleetGroupService) ListGroups(ctx context.Context, tenantID string) ([]GroupWithCount, error) {
	var rows []struct {
		ID           string `boil:"id"`
		Name         string `boil:"name"`
		Color        string `boil:"color"`
		VehicleCount int    `boil:"vehicle_count"`
	}
	err := queries.Raw(`
		SELECT fg.id, fg.name, fg.color, COUNT(vfg.token_id) AS vehicle_count
		FROM fleet_groups fg
		LEFT JOIN vehicle_fleet_groups vfg ON vfg.fleet_group_id = fg.id
		WHERE fg.tenant_id = $1
		GROUP BY fg.id
		ORDER BY fg.name`, tenantID).Bind(ctx, s.pdb.DBS().Reader, &rows)
	if err != nil {
		return nil, fmt.Errorf("list fleet groups: %w", err)
	}
	out := make([]GroupWithCount, len(rows))
	for i, r := range rows {
		out[i] = GroupWithCount{
			Group:        &dbmodels.FleetGroup{ID: r.ID, Name: r.Name, Color: r.Color, TenantID: tenantID},
			VehicleCount: r.VehicleCount,
		}
	}
	return out, nil
}

// VehicleInGroups reports whether the vehicle belongs to at least one of the
// given fleet groups. Used to authorize limited members' per-vehicle requests,
// so an empty group list is "no" — never "unrestricted".
func (s *FleetGroupService) VehicleInGroups(ctx context.Context, tenant models.Tenant, tokenID int64, groupIDs []string) (bool, error) {
	if len(groupIDs) == 0 {
		return false, nil
	}
	if s.groupsIndexed() {
		idx, err := s.groupIndex(ctx, tenant)
		if err != nil {
			return false, err
		}
		return idx.VehicleInGroups(tokenID, groupIDs), nil
	}
	n, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
		qm.AndIn("fleet_group_id in ?", toAnySlice(groupIDs)...),
	).Count(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("vehicle in groups: %w", err)
	}
	return n > 0, nil
}

func toAnySlice(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// GetGroup returns one group for the tenant from the local mirror, or
// ErrGroupNotFound. Local-path-only since P5 — the index-backed read is
// GetGroupView / groupForUpdate.
func (s *FleetGroupService) GetGroup(ctx context.Context, tenantID, groupID string) (*dbmodels.FleetGroup, error) {
	g, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.ID.EQ(groupID),
		dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("get fleet group: %w", err)
	}
	return g, nil
}

// ----- Writes (P4 of the groups move) -----
//
// Every mutation goes to fleet-tenancy-api FIRST — it owns the record — and is
// then mirrored into the local tables, which the scope-filtering SQL still
// reads until P5. The order is deliberate: a half-failure (remote succeeded,
// local did not) leaves the authority right and the mirror behind, which a
// retry or the mirror-groups command converges; the opposite order can leave a
// write that reported success to the user but never reached the owner — the
// membership cutover's reported-success-conferred-nothing bug.
//
// Every remote call is idempotent-or-classified, so retrying a failed request
// converges: re-adding is a no-op, re-deleting answers not-found (mapped
// below), re-creating answers name-taken.

// remoteWriteErr classifies a tenancy write failure into this service's
// sentinel errors. Anything unclassified is surfaced as-is — the request must
// fail, not pretend.
func remoteWriteErr(err error, op string) error {
	var httpErr interface{ HTTPStatus() int }
	if errors.As(err, &httpErr) {
		switch httpErr.HTTPStatus() {
		case 404:
			return ErrGroupNotFound
		case 409:
			return ErrGroupNameExists
		}
	}
	return fmt.Errorf("%s in tenancy: %w", op, err)
}

// requireTenancy is the write-path guard: without a client every mutation
// fails loudly. A local-only group write would diverge from the record's owner
// the moment it succeeded.
func (s *FleetGroupService) requireTenancy() error {
	if s.tenancy == nil {
		return fmt.Errorf("tenancy client is not configured; group writes require it since P4")
	}
	return nil
}

// CreateGroup creates a tenant-scoped group with a slug id derived from the name.
// Returns ErrGroupNameExists on a (tenant_id, name) collision.
func (s *FleetGroupService) CreateGroup(ctx context.Context, tenant models.Tenant, name, color string) (*dbmodels.FleetGroup, error) {
	if err := s.requireTenancy(); err != nil {
		return nil, err
	}
	g := &dbmodels.FleetGroup{
		ID:       GroupID(tenant.ID, name),
		Name:     name,
		Color:    color,
		TenantID: tenant.ID,
	}
	if g.ID == "" {
		return nil, fmt.Errorf("group name must contain at least one alphanumeric character")
	}
	if err := s.tenancy.CreateGroup(ctx, tenant, name, color); err != nil {
		return nil, remoteWriteErr(err, "create fleet group")
	}
	s.invalidateGroupIndex(tenant.ID)
	// Mirror. The upsert (rather than a bare insert) is what makes a retry
	// after a half-failure converge instead of tripping the unique index.
	if err := g.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{"id"}, boil.Whitelist("name", "color", "updated_at"), boil.Infer()); err != nil {
		return nil, fmt.Errorf("mirror fleet group locally: %w", err)
	}
	return g, nil
}

// UpdateGroup updates the color and/or name of a group. nil pointers are left
// unchanged. Returns ErrGroupNotFound / ErrGroupNameExists where applicable.
func (s *FleetGroupService) UpdateGroup(ctx context.Context, tenant models.Tenant, groupID string, name, color *string) (*dbmodels.FleetGroup, error) {
	if err := s.requireTenancy(); err != nil {
		return nil, err
	}
	if err := s.tenancy.UpdateGroup(ctx, tenant, groupID, name, color); err != nil {
		return nil, remoteWriteErr(err, "update fleet group")
	}
	// Bust before the read-back: the authority has moved, so a cached index
	// would answer with the values this call just replaced.
	s.invalidateGroupIndex(tenant.ID)

	g, err := s.groupForUpdate(ctx, tenant, groupID)
	if errors.Is(err, ErrGroupNotFound) {
		// The authority has the group but the mirror lost it (a prior
		// half-failure). Rebuild the row rather than failing an update the
		// owner has already accepted.
		g = &dbmodels.FleetGroup{ID: groupID, TenantID: tenant.ID}
	} else if err != nil {
		return nil, err
	}
	if name != nil && *name != "" {
		g.Name = *name
	}
	if color != nil && *color != "" {
		g.Color = *color
	}
	if err := g.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{"id"}, boil.Whitelist("name", "color", "updated_at"), boil.Infer()); err != nil {
		return nil, fmt.Errorf("mirror fleet group locally: %w", err)
	}
	return g, nil
}

// groupForUpdate re-reads a group after a successful remote update so the
// mirror row and the response carry the authority's current values.
func (s *FleetGroupService) groupForUpdate(ctx context.Context, tenant models.Tenant, groupID string) (*dbmodels.FleetGroup, error) {
	if !s.groupsIndexed() {
		return s.GetGroup(ctx, tenant.ID, groupID)
	}
	idx, err := s.groupIndex(ctx, tenant)
	if err != nil {
		return nil, err
	}
	g, ok := idx.group(groupID)
	if !ok {
		return nil, ErrGroupNotFound
	}
	return &dbmodels.FleetGroup{ID: g.ID, Name: g.Name, Color: g.Color,
		TenantID: tenant.ID, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}, nil
}

// DeleteGroup deletes a group (cascading its memberships). Returns
// ErrGroupNotFound if it does not exist for the tenant.
func (s *FleetGroupService) DeleteGroup(ctx context.Context, tenant models.Tenant, groupID string) error {
	if err := s.requireTenancy(); err != nil {
		return err
	}
	if err := s.tenancy.DeleteGroup(ctx, tenant, groupID); err != nil {
		return remoteWriteErr(err, "delete fleet group")
	}
	s.invalidateGroupIndex(tenant.ID)
	if _, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.ID.EQ(groupID),
		dbmodels.FleetGroupWhere.TenantID.EQ(tenant.ID),
	).DeleteAll(ctx, s.pdb.DBS().Writer); err != nil {
		return fmt.Errorf("mirror fleet group delete locally: %w", err)
	}
	return nil
}

// AddVehicle adds a vehicle (by token id) to a group, idempotently. The
// vehicle must be one of the tenant's synced vehicles — that check stays here,
// against this app's own vehicles table, because the tenancy service
// deliberately holds no fleet data to validate against.
func (s *FleetGroupService) AddVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, groupID string) (bool, error) {
	if err := s.requireTenancy(); err != nil {
		return false, err
	}
	ok, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("verify vehicle: %w", err)
	}
	if !ok {
		return false, ErrVehicleNotFound
	}

	// Read membership BEFORE the write. The return value answers "did this call
	// add it?", which is only answerable from the pre-write state; reading after
	// the write would make the cache-bust ordering load-bearing for a boolean.
	existed, err := s.membershipExists(ctx, tenant, tokenID, groupID)
	if err != nil {
		return false, err
	}

	if err := s.tenancy.AddGroupVehicles(ctx, tenant, groupID, []int64{tokenID}); err != nil {
		return false, remoteWriteErr(err, "add vehicle to group")
	}
	s.invalidateGroupIndex(tenant.ID)

	// Mirror. Upsert-do-nothing rather than check-then-insert: "was it new?"
	// now comes from the authority, and a membership the authority holds but the
	// mirror lost (a prior half-failure) must still be repaired here.
	m := &dbmodels.VehicleFleetGroup{TenantID: tenant.ID, TokenID: tokenID, FleetGroupID: groupID}
	if err := m.Upsert(ctx, s.pdb.DBS().Writer, false,
		[]string{"tenant_id", "token_id", "fleet_group_id"}, boil.None(), boil.Infer()); err != nil {
		return false, fmt.Errorf("mirror membership locally: %w", err)
	}
	return !existed, nil
}

// membershipExists reports whether the vehicle is already in the group.
func (s *FleetGroupService) membershipExists(ctx context.Context, tenant models.Tenant, tokenID int64, groupID string) (bool, error) {
	if s.groupsIndexed() {
		idx, err := s.groupIndex(ctx, tenant)
		if err != nil {
			return false, err
		}
		return idx.VehicleInGroups(tokenID, []string{groupID}), nil
	}
	exists, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(groupID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	return exists, nil
}

// RemoveVehicle removes a vehicle from a group. Returns whether a local
// membership row was removed.
func (s *FleetGroupService) RemoveVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, groupID string) (bool, error) {
	if err := s.requireTenancy(); err != nil {
		return false, err
	}
	if err := s.tenancy.RemoveGroupVehicle(ctx, tenant, groupID, tokenID); err != nil {
		return false, remoteWriteErr(err, "remove vehicle from group")
	}
	s.invalidateGroupIndex(tenant.ID)
	n, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(groupID),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	if err != nil {
		return false, fmt.Errorf("mirror membership removal locally: %w", err)
	}
	return n > 0, nil
}

// localVehicleGroups returns the groups a vehicle currently belongs to from the
// local mirror (tenant-scoped), ordered by name. An empty result is valid — the
// vehicle is in no groups. Takes an explicit executor so it can run inside a
// transaction.
//
// Unexported since P5: it reads a table that is about to be dropped, and the
// index-backed VehicleGroups is what new callers want.
func (s *FleetGroupService) localVehicleGroups(ctx context.Context, exec boil.ContextExecutor, tenantID string, tokenID int64) ([]GroupRef, error) {
	var rows []struct {
		ID    string `boil:"id"`
		Name  string `boil:"name"`
		Color string `boil:"color"`
	}
	err := queries.Raw(`
		SELECT fg.id, fg.name, fg.color
		FROM fleet_groups fg
		JOIN vehicle_fleet_groups vfg ON vfg.fleet_group_id = fg.id
		WHERE vfg.tenant_id = $1 AND vfg.token_id = $2
		ORDER BY fg.name`, tenantID, tokenID).Bind(ctx, exec, &rows)
	if err != nil {
		return nil, fmt.Errorf("load vehicle groups: %w", err)
	}
	groups := make([]GroupRef, len(rows))
	for i, r := range rows {
		groups[i] = GroupRef{ID: r.ID, Name: r.Name, Color: r.Color}
	}
	return groups, nil
}

// VehicleGroups loads a single vehicle's current groups, name-ordered.
func (s *FleetGroupService) VehicleGroups(ctx context.Context, tenant models.Tenant, tokenID int64) ([]GroupRef, error) {
	if s.groupsIndexed() {
		idx, err := s.groupIndex(ctx, tenant)
		if err != nil {
			return nil, err
		}
		return idx.GroupsForVehicle(tokenID), nil
	}
	return s.localVehicleGroups(ctx, s.pdb.DBS().Reader, tenant.ID, tokenID)
}

// VehicleGroupsMap returns, for the whole tenant, a map of token id -> the groups
// that vehicle belongs to, from the local mirror. Used to attach `groups` to the
// /vehicles list in one query rather than per-vehicle.
func (s *FleetGroupService) VehicleGroupsMap(ctx context.Context, tenantID string) (map[int64][]GroupRef, error) {
	var rows []struct {
		TokenID int64  `boil:"token_id"`
		ID      string `boil:"id"`
		Name    string `boil:"name"`
		Color   string `boil:"color"`
	}
	err := queries.Raw(`
		SELECT vfg.token_id, fg.id, fg.name, fg.color
		FROM vehicle_fleet_groups vfg
		JOIN fleet_groups fg ON fg.id = vfg.fleet_group_id
		WHERE vfg.tenant_id = $1
		ORDER BY fg.name`, tenantID).Bind(ctx, s.pdb.DBS().Reader, &rows)
	if err != nil {
		return nil, fmt.Errorf("load tenant vehicle groups: %w", err)
	}
	out := make(map[int64][]GroupRef)
	for _, r := range rows {
		out[r.TokenID] = append(out[r.TokenID], GroupRef{ID: r.ID, Name: r.Name, Color: r.Color})
	}
	return out, nil
}

// GroupMemberTokenIDs returns the token ids of vehicles currently in a group
// (tenant-scoped), ascending. Used for group-level attestation fan-out
// (recolor/delete) and the group's member count.
func (s *FleetGroupService) GroupMemberTokenIDs(ctx context.Context, tenant models.Tenant, groupID string) ([]int64, error) {
	if s.groupsIndexed() {
		idx, err := s.groupIndex(ctx, tenant)
		if err != nil {
			return nil, err
		}
		return idx.MemberTokenIDs(groupID), nil
	}
	members, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(groupID),
		qm.OrderBy("token_id"),
	).All(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("group member token ids: %w", err)
	}
	ids := make([]int64, len(members))
	for i, m := range members {
		ids[i] = m.TokenID
	}
	return ids, nil
}

// ----- The flagged read surface (P3 of the groups move) -----
//
// The *View methods are what the read-only controller paths call. Local they
// are exactly the old queries; with GROUPS_FROM_TENANCY they are served from
// fleet-tenancy-api, authenticated as the tenant being read. A remote failure
// is surfaced, not silently downgraded to the local tables — a fallback nobody
// watches is how a broken read path stays broken, and the flag itself is the
// way back.
//
// Since P5 they share the cached GroupIndex with the scope filters, so one
// request that both lists vehicles and decorates them with group chips asks the
// tenancy service once rather than twice.

// ListGroupsView is ListGroups for the read-only management surface.
func (s *FleetGroupService) ListGroupsView(ctx context.Context, tenant models.Tenant) ([]GroupWithCount, error) {
	if !s.groupsIndexed() {
		return s.ListGroups(ctx, tenant.ID)
	}
	idx, err := s.groupIndex(ctx, tenant)
	if err != nil {
		return nil, err
	}
	groups := idx.all()
	out := make([]GroupWithCount, len(groups))
	for i, g := range groups {
		out[i] = GroupWithCount{
			Group: &dbmodels.FleetGroup{ID: g.ID, Name: g.Name, Color: g.Color,
				TenantID: tenant.ID, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt},
			VehicleCount: g.VehicleCount,
		}
	}
	return out, nil
}

// GetGroupView returns one group and its member token ids.
func (s *FleetGroupService) GetGroupView(ctx context.Context, tenant models.Tenant, groupID string) (*dbmodels.FleetGroup, []int64, error) {
	if !s.groupsIndexed() {
		g, err := s.GetGroup(ctx, tenant.ID, groupID)
		if err != nil {
			return nil, nil, err
		}
		members, err := s.GroupMemberTokenIDs(ctx, tenant, groupID)
		if err != nil {
			// Preserves the old controller behaviour: the group renders, the
			// count is best-effort.
			s.logger.Err(err).Str("group", groupID).Msg("count members")
			members = nil
		}
		return g, members, nil
	}
	idx, err := s.groupIndex(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	g, ok := idx.group(groupID)
	if !ok {
		return nil, nil, ErrGroupNotFound
	}
	return &dbmodels.FleetGroup{ID: g.ID, Name: g.Name, Color: g.Color,
			TenantID: tenant.ID, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt},
		idx.MemberTokenIDs(groupID), nil
}

// VehicleGroupsMapView is VehicleGroupsMap for the /vehicles list.
//
// Its one caller treats a failure here as decoration missing rather than access
// denied (group chips are not access control), which is why this is the one
// group read P5 leaves best-effort. See controllers/vehicles.go.
func (s *FleetGroupService) VehicleGroupsMapView(ctx context.Context, tenant models.Tenant) (map[int64][]GroupRef, error) {
	if !s.groupsIndexed() {
		return s.VehicleGroupsMap(ctx, tenant.ID)
	}
	idx, err := s.groupIndex(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return idx.VehicleGroupsMap(), nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}
