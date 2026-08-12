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
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/rs/zerolog"
)

// DefaultFetchLimit bounds how many recent CEs we pull per vehicle when looking
// for the latest group document.
const DefaultFetchLimit = 50

// GroupSyncService is the shared "pull" half of the attestation-backed group
// sync: it reads a vehicle's group-membership attestations from Fetch API and
// reconciles the local vehicle_fleet_groups cache to them. Both the
// import-group-attestations cron and the lazy per-vehicle endpoint drive it, so
// the reconcile semantics live here once (see docs/GROUP_SYNC.md).
//
// Reconcile semantics: the authoritative set is the union of the latest
// dimo.document.vehicle.groups attestation per distinct producer. Adds always
// apply; removals (local groups no longer in any producer's latest CE) apply
// only when the freshness gate is open — our own producer-stamped CE has caught
// up to groups_updated_at — so a sync inside the 5-10s publish lag never reverts
// an optimistic local write. De-dup on add is guaranteed by the
// (tenant_id, token_id, fleet_group_id) primary key. Unknown groups are
// auto-created.
type GroupSyncService struct {
	logger       *zerolog.Logger
	pdb          *db.Store
	fetchAPI     *gateway.FetchAPI
	authProvider *gateway.DimoAuthProvider

	// dropForeignTenantGroups enforces tenant-matching on incoming group
	// CloudEvents. MUST stay false until every tenant uuid is unified across
	// fleet-lite and the oracle — see normaliseGroupID.
	dropForeignTenantGroups bool
}

func NewGroupSyncService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider, dropForeignTenantGroups bool) *GroupSyncService {
	return &GroupSyncService{
		logger: logger, pdb: pdb, fetchAPI: fetchAPI, authProvider: authProvider,
		dropForeignTenantGroups: dropForeignTenantGroups,
	}
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
	Removed int  // memberships removed (Phase 2 reconcile; 0 when the removal gate is closed)
	Skipped bool // true when the cooldown short-circuited the pull (no fetch performed)
}

// SyncVehicle pulls one vehicle's group attestations and reconciles the local
// cache to the authoritative set (union of the latest CE per producer). Adds
// always apply; removals apply only when the freshness gate is open — i.e. our
// own latest producer-stamped CE has caught up to groups_updated_at, so a sync
// running inside the 5-10s publish lag can never revert an optimistic local
// write (see docs/GROUP_SYNC.md Phase 2). Stamps last_group_sync_at on success.
// Returns ErrVehicleNotFound for an unknown vehicle.
func (s *GroupSyncService) SyncVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, opts SyncOpts) (SyncResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultFetchLimit
	}

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

	// Cooldown: skip the (asset-JWT) Fetch call when we synced recently.
	if opts.Cooldown > 0 && v.LastGroupSyncAt.Valid && time.Since(v.LastGroupSyncAt.Time) < opts.Cooldown {
		return SyncResult{Skipped: true}, nil
	}

	did := s.authProvider.BuildVehicleDID(uint64(tokenID))
	entries, err := s.fetchAPI.ListByDIDAndType(tenant, did, VehicleGroupsCloudEventType, limit)
	if err != nil {
		return SyncResult{}, fmt.Errorf("fetch group attestations: %w", err)
	}

	desired := desiredGroups(entries, *s.logger, tokenID, tenant.ID, s.dropForeignTenantGroups)
	// Removals are honored only when the gate is open AND the fetch actually
	// returned CEs — a successful-but-empty read must never wipe local groups.
	allowRemove := len(entries) > 0 && removalAllowed(entries, v.GroupsUpdatedAt)

	added, removed, err := s.reconcile(ctx, tenant.ID, tokenID, desired, allowRemove, opts.DryRun)
	if err != nil {
		return SyncResult{}, err
	}
	if !opts.DryRun {
		s.touchLastGroupSyncAt(ctx, tenant.ID, tokenID)
	}
	return SyncResult{Added: added, Removed: removed}, nil
}

// removalAllowed reports whether it is safe to honor removals for a vehicle —
// i.e. our local state is not ahead of what we've published on-chain, so the
// authoritative union can be trusted to drop groups. Open when there is no
// pending local change (groups_updated_at is NULL), or our latest
// producer-stamped CE is at least as recent as groups_updated_at. Closed when we
// have a local change but no producer CE confirming it has reached the chain yet
// (don't revert the optimistic write across the publish lag).
func removalAllowed(entries []gateway.AttestationEntry, groupsUpdatedAt null.Time) bool {
	if !groupsUpdatedAt.Valid {
		return true
	}
	var ourLatest time.Time
	for i := range entries {
		e := &entries[i]
		if e.Producer != GroupAttestationProducer {
			continue
		}
		if t, err := time.Parse(time.RFC3339, e.Time); err == nil && t.After(ourLatest) {
			ourLatest = t
		}
	}
	if ourLatest.IsZero() {
		return false
	}
	return !ourLatest.Before(groupsUpdatedAt.Time)
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
// normaliseGroupID maps a CloudEvent group id into the tenant being reconciled,
// reporting whether the group should be accepted at all.
//
// The rule is tenant-matching, NOT producer-matching: a group belongs to the
// tenant whose uuid prefixes its id, whoever published the CloudEvent. So an
// operator tenant viewed in fleet-lite sees the groups b2b created for it, while
// a customer tenant never sees the operator's. Nothing keys off which app wrote
// the CE — under a shared operator developer license they all share a `source`.
//
// Legacy ids are bare slugs written before the tenant-prefix migration. They are
// safe to adopt: reconcile always runs for one known vehicle in one known tenant,
// so a bare slug is unambiguous in that context.
//
// dropForeign gates the only behaviour change users can see. It MUST stay false
// until the two systems agree on tenant uuids — today kaufmann's "Kaufmann"
// tenant and fleet-lite's are different uuids for the same company, so enforcing
// the match would drop every group kaufmann asserts, and reconcile would then
// remove the memberships that depend on them. Turning it on before fleet-lite has
// republished its own groups is data loss, not a policy change.
// See docs/operator-tenancy/07-r1-group-id-migration.md.
func normaliseGroupID(tenantID, id string, dropForeign bool) (string, bool) {
	i := strings.Index(id, GroupIDSeparator)
	if i <= 0 {
		return tenantID + GroupIDSeparator + id, true // legacy bare slug
	}
	if id[:i] == tenantID {
		return id, true
	}
	if dropForeign {
		return "", false
	}
	// Transitional: adopt a foreign-tenant group into our own namespace, which
	// is exactly what happened implicitly before ids carried a tenant. Removed
	// with the flag once every tenant uuid is unified.
	return tenantID + GroupIDSeparator + id[i+1:], true
}

func desiredGroups(entries []gateway.AttestationEntry, logger zerolog.Logger, tokenID int64, tenantID string, dropForeign bool) []models.GroupRef {
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
			normID, ok := normaliseGroupID(tenantID, g.ID, dropForeign)
			if !ok {
				logger.Debug().Int64("token_id", tokenID).Str("group_id", g.ID).
					Str("tenant_id", tenantID).Msg("dropping group from another tenant")
				continue
			}
			g.ID = normID
			// Stamp the CE's own time so ensureGroup can tell a current name
			// from one attested before a rename.
			g.AttestedAt = latestTime[producerKey(e)]
			if prev, seen := byID[g.ID]; !seen || g.AttestedAt.After(prev.AttestedAt) {
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

// reconcile brings the vehicle's local memberships in line with the desired set
// (the authoritative union of latest CE per producer). Adds always apply; the
// removals of locally-present groups absent from desired apply only when
// allowRemove is true (the freshness gate). De-dup on add is guaranteed by the
// (tenant_id, token_id, fleet_group_id) primary key. Returns counts of
// memberships added and removed (or that would change, in dry-run).
func (s *GroupSyncService) reconcile(ctx context.Context, tenantID string, tokenID int64, desired []models.GroupRef, allowRemove, dryRun bool) (int, int, error) {
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
		return 0, 0, err
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
	var toRemove []string
	if allowRemove {
		for id := range currentIDs {
			if !desiredIDs[id] {
				toRemove = append(toRemove, id)
			}
		}
	}
	if len(toAdd) == 0 && len(toRemove) == 0 {
		return 0, 0, nil
	}

	if dryRun {
		s.logger.Info().Str("tenant_id", tenantID).Int64("token_id", tokenID).
			Strs("add", toAdd).Strs("remove", toRemove).Bool("allow_remove", allowRemove).
			Msg("would reconcile memberships")
		return len(toAdd), len(toRemove), nil
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
	removed := 0
	for _, id := range toRemove {
		n, err := dbmodels.VehicleFleetGroups(
			dbmodels.VehicleFleetGroupWhere.TenantID.EQ(tenantID),
			dbmodels.VehicleFleetGroupWhere.TokenID.EQ(tokenID),
			dbmodels.VehicleFleetGroupWhere.FleetGroupID.EQ(id),
		).DeleteAll(ctx, s.pdb.DBS().Writer)
		if err != nil {
			s.logger.Err(err).Int64("token_id", tokenID).Str("group_id", id).Msg("remove membership")
			continue
		}
		removed += int(n)
	}
	return added, removed, nil
}

// ensureGroup creates the fleet group for the tenant, or brings an existing
// one's name and colour into line with the attestation. Name defaults to the
// id, colour to a neutral gray. A (tenant_id, name) collision with a different
// id surfaces as an error so the caller can skip the membership.
//
// THE ATTESTATION WINS on metadata. This used to be create-only, which meant a
// group renamed at its source kept its original name here forever: the row was
// written once from whatever attestation happened to be read first, and no
// later import could correct it. A production group sat misnamed for a day
// that way, and re-running the import could never have fixed it — the reconcile
// reported "changed=0" because memberships were already correct and names were
// never compared at all.
//
// The tradeoff, which is deliberate: a group renamed *here* is reverted on the
// next import. fleet-lite's own PATCH republishes the group document, so in the
// steady state the two converge on whichever side published last — but a local
// rename made while the producer is silent will not stick. Group identity
// already belongs to the producer (the id carries its tenant prefix, and
// membership is reconciled from its attestations); letting metadata follow the
// same owner keeps one source of truth rather than two that silently disagree.
func (s *GroupSyncService) ensureGroup(ctx context.Context, tenantID string, g models.GroupRef, dryRun bool) error {
	name, color := resolveGroupMetadata(g)

	existing, err := dbmodels.FleetGroups(
		dbmodels.FleetGroupWhere.ID.EQ(g.ID),
		dbmodels.FleetGroupWhere.TenantID.EQ(tenantID),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if existing != nil {
		if existing.Name == name && existing.Color == color {
			return nil
		}
		// Only a *newer* attestation may rewrite metadata.
		//
		// Group metadata is carried redundantly on every member vehicle's
		// attestation, so members attested before a rename and after it
		// disagree. Adopting whichever was read last makes the stored name a
		// function of iteration order: a single import would rewrite the row
		// once per member, and the surviving value was whichever vehicle
		// happened to come last. Comparing against the row's own updated_at
		// makes it monotonic — a rename at the source is newer and wins, a
		// stale sibling is older and is ignored.
		//
		// This also protects a rename made here: the local write bumps
		// updated_at, so only a genuinely later attestation supersedes it.
		if !g.AttestedAt.After(existing.UpdatedAt) {
			s.logger.Debug().Str("tenant_id", tenantID).Str("group_id", g.ID).
				Time("attested_at", g.AttestedAt).Time("group_updated_at", existing.UpdatedAt).
				Str("stale_name", name).Msg("ignoring group metadata older than the stored row")
			return nil
		}
		if dryRun {
			s.logger.Info().Str("tenant_id", tenantID).Str("group_id", g.ID).
				Str("from_name", existing.Name).Str("to_name", name).
				Str("from_color", existing.Color).Str("to_color", color).
				Msg("would update group metadata")
			return nil
		}
		s.logger.Info().Str("tenant_id", tenantID).Str("group_id", g.ID).
			Str("from_name", existing.Name).Str("to_name", name).
			Time("attested_at", g.AttestedAt).
			Msg("updating group metadata from a newer attestation")
		existing.Name = name
		existing.Color = color
		_, uerr := existing.Update(ctx, s.pdb.DBS().Writer, boil.Whitelist("name", "color", "updated_at"))
		return uerr
	}

	if dryRun {
		s.logger.Info().Str("tenant_id", tenantID).Str("group_id", g.ID).Str("name", name).
			Msg("would create group")
		return nil
	}

	group := &dbmodels.FleetGroup{ID: g.ID, Name: name, Color: color, TenantID: tenantID}
	return group.Insert(ctx, s.pdb.DBS().Writer, boil.Infer())
}

// resolveGroupMetadata is what an attestation's group metadata becomes once
// stored: the name falls back to the id and the colour to a neutral gray.
//
// Pure and separate from ensureGroup so the fallbacks can be asserted without a
// database — and so the create and update paths cannot drift apart, which is how
// a group could otherwise be created with one name and updated to another from
// the same input.
func resolveGroupMetadata(g models.GroupRef) (name, color string) {
	name = strings.TrimSpace(g.Name)
	if name == "" {
		name = g.ID
	}
	color = g.Color
	if color == "" {
		color = "#808080"
	}
	return name, color
}
