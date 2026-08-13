package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/lib/pq"
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
	// synchronously-maintained mirror until P5 drops them. Reads additionally
	// come from it when readFromTenancy is set (GROUPS_FROM_TENANCY). The
	// scope-filtering SQL joins (vehicle.go, geofence.go) keep reading the
	// mirror, which the mirror-groups command reconverges from the authority.
	tenancy remoteGroupSource
	// readFromTenancy flips the *View read methods to the remote source.
	readFromTenancy bool
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
	return &FleetGroupService{logger: logger, pdb: pdb}
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
// given fleet groups. Used to authorize limited members' per-vehicle requests.
func (s *FleetGroupService) VehicleInGroups(ctx context.Context, tenantID string, tokenID int64, groupIDs []string) (bool, error) {
	if len(groupIDs) == 0 {
		return false, nil
	}
	n, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
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

// GetGroup returns one group for the tenant, or ErrGroupNotFound.
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

	g, err := s.GetGroup(ctx, tenant.ID, groupID)
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

// DeleteGroup deletes a group (cascading its memberships). Returns
// ErrGroupNotFound if it does not exist for the tenant.
func (s *FleetGroupService) DeleteGroup(ctx context.Context, tenant models.Tenant, groupID string) error {
	if err := s.requireTenancy(); err != nil {
		return err
	}
	if err := s.tenancy.DeleteGroup(ctx, tenant, groupID); err != nil {
		return remoteWriteErr(err, "delete fleet group")
	}
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

	if err := s.tenancy.AddGroupVehicles(ctx, tenant, groupID, []int64{tokenID}); err != nil {
		return false, remoteWriteErr(err, "add vehicle to group")
	}

	exists, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(groupID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	if exists {
		return false, nil
	}
	m := &dbmodels.VehicleFleetGroup{TenantID: tenant.ID, TokenID: tokenID, FleetGroupID: groupID}
	if err := m.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		return false, fmt.Errorf("mirror membership locally: %w", err)
	}
	return true, nil
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

// LoadVehicleGroups returns the groups a vehicle currently belongs to (tenant-
// scoped), ordered by name. An empty result is valid — the vehicle is in no
// groups. Takes an explicit executor so it can run inside a transaction.
func (s *FleetGroupService) LoadVehicleGroups(ctx context.Context, exec boil.ContextExecutor, tenantID string, tokenID int64) ([]GroupRef, error) {
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

// VehicleGroups loads a single vehicle's current groups using the service's
// own reader.
func (s *FleetGroupService) VehicleGroups(ctx context.Context, tenantID string, tokenID int64) ([]GroupRef, error) {
	return s.LoadVehicleGroups(ctx, s.pdb.DBS().Reader, tenantID, tokenID)
}

// VehicleGroupsMap returns, for the whole tenant, a map of token id -> the groups
// that vehicle belongs to. Used to attach `groups` to the /vehicles list in one
// query rather than per-vehicle.
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
// (tenant-scoped). Used for group-level attestation fan-out (recolor/delete).
func (s *FleetGroupService) GroupMemberTokenIDs(ctx context.Context, tenantID, groupID string) ([]int64, error) {
	members, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
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
// LoadVehicleGroups stays on the local mirror on purpose: it serves the same
// per-vehicle view the scope-filtering SQL joins against, and both read the
// mirror until P5 drops the local tables.

// ListGroupsView is ListGroups for the read-only management surface.
func (s *FleetGroupService) ListGroupsView(ctx context.Context, tenant models.Tenant) ([]GroupWithCount, error) {
	if !s.readFromTenancy || s.tenancy == nil {
		return s.ListGroups(ctx, tenant.ID)
	}
	groups, err := s.tenancy.VehicleGroups(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list fleet groups from tenancy: %w", err)
	}
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
	if !s.readFromTenancy || s.tenancy == nil {
		g, err := s.GetGroup(ctx, tenant.ID, groupID)
		if err != nil {
			return nil, nil, err
		}
		members, err := s.GroupMemberTokenIDs(ctx, tenant.ID, groupID)
		if err != nil {
			// Preserves the old controller behaviour: the group renders, the
			// count is best-effort.
			s.logger.Err(err).Str("group", groupID).Msg("count members")
			members = nil
		}
		return g, members, nil
	}
	groups, err := s.tenancy.VehicleGroups(ctx, tenant)
	if err != nil {
		return nil, nil, fmt.Errorf("get fleet group from tenancy: %w", err)
	}
	for _, g := range groups {
		if g.ID == groupID {
			return &dbmodels.FleetGroup{ID: g.ID, Name: g.Name, Color: g.Color,
				TenantID: tenant.ID, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}, g.TokenIDs, nil
		}
	}
	return nil, nil, ErrGroupNotFound
}

// VehicleGroupsMapView is VehicleGroupsMap for the /vehicles list.
func (s *FleetGroupService) VehicleGroupsMapView(ctx context.Context, tenant models.Tenant) (map[int64][]GroupRef, error) {
	if !s.readFromTenancy || s.tenancy == nil {
		return s.VehicleGroupsMap(ctx, tenant.ID)
	}
	groups, err := s.tenancy.VehicleGroups(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("load tenant vehicle groups from tenancy: %w", err)
	}
	// The endpoint orders groups by name, so appending in order preserves the
	// per-vehicle name ordering the local query produced.
	out := make(map[int64][]GroupRef)
	for _, g := range groups {
		ref := GroupRef{ID: g.ID, Name: g.Name, Color: g.Color}
		for _, tokenID := range g.TokenIDs {
			out[tokenID] = append(out[tokenID], ref)
		}
	}
	return out, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}
