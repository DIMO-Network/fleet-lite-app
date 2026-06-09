package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/rs/zerolog"
)

// DefaultFetchLimit bounds how many recent CEs we pull per vehicle when looking
// for the latest group document.
const DefaultFetchLimit = 50

// GroupSyncService is the shared "pull" half of the attestation-backed group
// sync: it reads a vehicle's group-membership attestations from Fetch API and
// additively merges them into the local vehicle_fleet_groups cache. Both the
// import-group-attestations cron and the lazy per-vehicle endpoint drive it, so
// the merge semantics live here once (see docs/GROUP_SYNC.md).
//
// Merge semantics: additive union — take the latest dimo.document.vehicle.groups
// attestation per distinct producer, union their groups, and add any membership
// not already present. Never removes (no single producer is authoritative over
// removals when developer credentials are shared — that is Phase 2). De-dup is
// guaranteed by the (tenant_id, token_id, fleet_group_id) primary key. Unknown
// groups are auto-created.
type GroupSyncService struct {
	logger       *zerolog.Logger
	pdb          *db.Store
	fetchAPI     *gateway.FetchAPI
	authProvider *gateway.DimoAuthProvider
}

func NewGroupSyncService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider) *GroupSyncService {
	return &GroupSyncService{logger: logger, pdb: pdb, fetchAPI: fetchAPI, authProvider: authProvider}
}

// SyncOpts tunes a single vehicle sync.
type SyncOpts struct {
	// DryRun logs the changes that would be made without writing.
	DryRun bool
	// Cooldown, when > 0, skips the Fetch pull entirely if the vehicle was synced
	// within this window (last_group_sync_at). Used by the lazy endpoint to avoid
	// hammering Fetch API on rapid repeat views; the cron passes 0 (always pull).
	Cooldown time.Duration
	// Limit overrides how many recent CEs to pull (DefaultFetchLimit when 0).
	Limit int
}

// SyncResult reports what a SyncVehicle call did.
type SyncResult struct {
	Added   int  // memberships added to the local cache
	Skipped bool // true when the cooldown short-circuited the pull (no fetch performed)
}

// SyncVehicle pulls one vehicle's group attestations and additively merges them.
// It stamps last_group_sync_at on success (so the cooldown and cron freshness
// selection can see it). Returns ErrVehicleNotFound when a cooldown check is
// requested for an unknown vehicle.
func (s *GroupSyncService) SyncVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, opts SyncOpts) (SyncResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultFetchLimit
	}

	// Cooldown: skip the (asset-JWT) Fetch call when we synced recently.
	if opts.Cooldown > 0 {
		v, err := dbmodels.Vehicles(
			dbmodels.VehicleWhere.TenantID.EQ(tenant.ID),
			dbmodels.VehicleWhere.TokenID.EQ(tokenID),
		).One(ctx, s.pdb.DBS().Reader)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return SyncResult{}, ErrVehicleNotFound
			}
			return SyncResult{}, fmt.Errorf("load vehicle: %w", err)
		}
		if v.LastGroupSyncAt.Valid && time.Since(v.LastGroupSyncAt.Time) < opts.Cooldown {
			return SyncResult{Skipped: true}, nil
		}
	}

	did := s.authProvider.BuildVehicleDID(uint64(tokenID))
	entries, err := s.fetchAPI.ListByDIDAndType(tenant, did, VehicleGroupsCloudEventType, limit)
	if err != nil {
		return SyncResult{}, fmt.Errorf("fetch group attestations: %w", err)
	}

	desired := desiredGroups(entries, *s.logger, tokenID)
	added := 0
	if len(desired) > 0 {
		added, err = s.merge(ctx, tenant.ID, tokenID, desired, opts.DryRun)
		if err != nil {
			return SyncResult{}, err
		}
	}
	if !opts.DryRun {
		s.touchLastGroupSyncAt(ctx, tenant.ID, tokenID)
	}
	return SyncResult{Added: added}, nil
}

// touchLastGroupSyncAt stamps the vehicle's last_group_sync_at to now. Best
// effort — a failure here doesn't undo a successful merge, so it is logged and
// swallowed.
func (s *GroupSyncService) touchLastGroupSyncAt(ctx context.Context, tenantID string, tokenID int64) {
	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{"last_group_sync_at": time.Now()}); err != nil {
		s.logger.Warn().Err(err).Str("tenant_id", tenantID).Int64("token_id", tokenID).
			Msg("stamp last_group_sync_at")
	}
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
		if e.Type != VehicleGroupsCloudEventType {
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
// Additive only — never removes. De-dup is guaranteed by the
// (tenant_id, token_id, fleet_group_id) primary key. Returns the number of
// memberships added (or that would be added, in dry-run).
func (s *GroupSyncService) merge(ctx context.Context, tenantID string, tokenID int64, desired []models.GroupRef, dryRun bool) (int, error) {
	desiredIDs := make(map[string]bool, len(desired))
	for _, g := range desired {
		if g.ID == "" {
			continue
		}
		if err := s.ensureGroup(ctx, tenantID, g, dryRun); err != nil {
			// Most likely a (tenant_id, name) collision with a different id — drop
			// this membership rather than fail the whole vehicle.
			s.logger.Warn().Err(err).Str("tenant_id", tenantID).Str("group_id", g.ID).
				Msg("ensure group, skipping membership")
			continue
		}
		desiredIDs[g.ID] = true
	}

	current, err := dbmodels.VehicleFleetGroups(
		dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
		dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
	).All(ctx, s.pdb.DBS().Reader)
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

	if dryRun {
		s.logger.Info().Str("tenant_id", tenantID).Int64("token_id", tokenID).
			Strs("add", toAdd).Msg("would add memberships")
		return len(toAdd), nil
	}

	added := 0
	for _, id := range toAdd {
		m := &dbmodels.VehicleFleetGroup{TenantID: tenantID, TokenID: tokenID, FleetGroupID: id}
		if err := m.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
			s.logger.Err(err).Int64("token_id", tokenID).Str("group_id", id).Msg("add membership")
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
func (s *GroupSyncService) ensureGroup(ctx context.Context, tenantID string, g models.GroupRef, dryRun bool) error {
	exists, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.ID.EQ(g.ID),
		dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
	).Exists(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if dryRun {
		s.logger.Info().Str("tenant_id", tenantID).Str("group_id", g.ID).Str("name", g.Name).
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
	return group.Insert(ctx, s.pdb.DBS().Writer, boil.Infer())
}
