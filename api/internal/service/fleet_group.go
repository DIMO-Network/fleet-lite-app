package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
)

// GroupRef is the slim group reference used by the attestation publisher, the
// import command, and the /vehicles filter payload. Aliased to the models type
// so the same shape flows through the whole feature.
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
}

func NewFleetGroupService(logger *zerolog.Logger, pdb *db.Store) *FleetGroupService {
	return &FleetGroupService{logger: logger, pdb: pdb}
}

var slugNonAlphanum = regexp.MustCompile(`[^a-z0-9]+`)

// slug derives a stable, url/attestation-safe id from a group name.
func slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugNonAlphanum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
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

// CreateGroup creates a tenant-scoped group with a slug id derived from the name.
// Returns ErrGroupNameExists on a (tenant_id, name) collision.
func (s *FleetGroupService) CreateGroup(ctx context.Context, tenantID, name, color string) (*dbmodels.FleetGroup, error) {
	g := &dbmodels.FleetGroup{
		ID:       slug(name),
		Name:     name,
		Color:    color,
		TenantID: tenantID,
	}
	if g.ID == "" {
		return nil, fmt.Errorf("group name must contain at least one alphanumeric character")
	}
	if err := g.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGroupNameExists
		}
		return nil, fmt.Errorf("insert fleet group: %w", err)
	}
	return g, nil
}

// UpdateGroup updates the color and/or name of a group. nil pointers are left
// unchanged. Returns ErrGroupNotFound / ErrGroupNameExists where applicable.
func (s *FleetGroupService) UpdateGroup(ctx context.Context, tenantID, groupID string, name, color *string) (*dbmodels.FleetGroup, error) {
	g, err := s.GetGroup(ctx, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	cols := []string{"updated_at"}
	if name != nil && *name != "" {
		g.Name = *name
		cols = append(cols, "name")
	}
	if color != nil && *color != "" {
		g.Color = *color
		cols = append(cols, "color")
	}
	if _, err := g.Update(ctx, s.pdb.DBS().Writer, boil.Whitelist(cols...)); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGroupNameExists
		}
		return nil, fmt.Errorf("update fleet group: %w", err)
	}
	return g, nil
}

// DeleteGroup deletes a group (cascading its memberships). Returns
// ErrGroupNotFound if it does not exist for the tenant.
func (s *FleetGroupService) DeleteGroup(ctx context.Context, tenantID, groupID string) error {
	g, err := s.GetGroup(ctx, tenantID, groupID)
	if err != nil {
		return err
	}
	if _, err := g.Delete(ctx, s.pdb.DBS().Writer); err != nil {
		return fmt.Errorf("delete fleet group: %w", err)
	}
	return nil
}

// AddVehicle adds a vehicle (by token id) to a group, idempotently. It verifies
// the group and vehicle both belong to the tenant first. Returns whether a new
// membership row was created.
func (s *FleetGroupService) AddVehicle(ctx context.Context, tenantID string, tokenID int64, groupID string) (bool, error) {
	if _, err := s.GetGroup(ctx, tenantID, groupID); err != nil {
		return false, err
	}
	ok, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("verify vehicle: %w", err)
	}
	if !ok {
		return false, ErrVehicleNotFound
	}

	exists, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(groupID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	if exists {
		return false, nil
	}

	m := &dbmodels.VehicleFleetGroup{TenantID: tenantID, TokenID: tokenID, FleetGroupID: groupID}
	if err := m.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		return false, fmt.Errorf("add vehicle to group: %w", err)
	}
	s.touchGroupsUpdatedAt(ctx, tenantID, tokenID)
	return true, nil
}

// RemoveVehicle removes a vehicle from a group. It verifies the group belongs to
// the tenant first. Returns whether a membership row was removed.
func (s *FleetGroupService) RemoveVehicle(ctx context.Context, tenantID string, tokenID int64, groupID string) (bool, error) {
	if _, err := s.GetGroup(ctx, tenantID, groupID); err != nil {
		return false, err
	}
	n, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
		dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(groupID),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	if err != nil {
		return false, fmt.Errorf("remove vehicle from group: %w", err)
	}
	if n > 0 {
		s.touchGroupsUpdatedAt(ctx, tenantID, tokenID)
	}
	return n > 0, nil
}

// touchGroupsUpdatedAt stamps the vehicle's groups_updated_at to now after a
// local membership change. Lets the cron prioritise recently-changed vehicles
// and is the Phase-2 write guard against the Fetch-API publish lag. Best
// effort — a failure here doesn't undo the membership change, so it's logged
// and swallowed.
func (s *FleetGroupService) touchGroupsUpdatedAt(ctx context.Context, tenantID string, tokenID int64) {
	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{"groups_updated_at": time.Now()}); err != nil {
		s.logger.Warn().Err(err).Str("tenant_id", tenantID).Int64("token_id", tokenID).
			Msg("stamp groups_updated_at")
	}
}

// LoadVehicleGroups returns the groups a vehicle currently belongs to (tenant-
// scoped), ordered by name. An empty result is valid — the vehicle is in no
// groups. Shared by the /vehicles payload, the attest publisher, and the import
// command; takes an explicit executor so it can run inside a transaction.
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

// VehicleGroups loads a single vehicle's current groups using the service's own
// reader. Convenience for the write-path attestation goroutine, which runs on a
// background context (the request context is gone by then).
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

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}
