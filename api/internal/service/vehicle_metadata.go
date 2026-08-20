package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/patrickmn/go-cache"
)

// vehicleMetadataSource is the slice of gateway.TenancyAPI this file needs:
// what a set of vehicles IS, from the roster fleet-tenancy-api reconciles
// against the chain nightly.
type vehicleMetadataSource interface {
	VehicleMetadata(ctx context.Context, tenant models.Tenant, tokenIDs []int64) ([]models.RemoteVehicleMetadata, error)
}

// metadataTTL bounds how stale roster metadata may be.
//
// It deliberately does NOT have to equal entitledTTL / membershipTTL / the
// group-index window, and that is worth stating because those three are
// asserted equal in a test and this one is not. They gate the SET — a stale
// gate can add or remove a vehicle, so they must age together. This is the
// join over a set already decided, and stale metadata can only make a make and
// model slightly old. It can never remove a vehicle from anybody's fleet.
const metadataTTL = 5 * time.Minute

// UseVehicleMetadata wires the roster as the metadata source, which is the
// VEHICLE_METADATA_FROM_TENANCY cutover (plan 07 step 4). Without it, metadata
// comes from the local vehicles table exactly as before — that is the revert
// path, and it is a config flip rather than a release.
func (s *VehicleService) UseVehicleMetadata(src vehicleMetadataSource) {
	s.metadata = src
	s.metadataCache = cache.New(metadataTTL, 2*metadataTTL)
}

// rosterMetadata returns roster rows for the tokens, keyed by token id.
//
// Cached per tenant as a MAP, filled by misses rather than all-or-nothing. A
// whole-response cache keyed by tenant would answer a newly-entitled vehicle
// with "no metadata" for up to the TTL — the set is resolved live-ish and the
// metadata would lag behind it, which is the same shape of mismatch this plan
// exists to remove, in miniature. Fetching only the misses keeps a steady-state
// render at zero calls and a changed fleet at one small one.
//
// Only successes are cached, like every other cache on this path: a cached
// failure would extend an outage past its cause.
func (s *VehicleService) rosterMetadata(ctx context.Context, tenant models.Tenant,
	tokenIDs []int64) (map[int64]models.RemoteVehicleMetadata, error) {
	out := make(map[int64]models.RemoteVehicleMetadata, len(tokenIDs))

	var missing []int64
	for _, id := range tokenIDs {
		if s.metadataCache != nil {
			if hit, found := s.metadataCache.Get(metadataCacheKey(tenant.ID, id)); found {
				out[id] = hit.(models.RemoteVehicleMetadata)
				continue
			}
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return out, nil
	}

	rows, err := s.metadata.VehicleMetadata(ctx, tenant, missing)
	if err != nil {
		return nil, fmt.Errorf("roster metadata for tenant %s: %w", tenant.ID, err)
	}
	for _, r := range rows {
		out[r.VehicleTokenID] = r
		if s.metadataCache != nil {
			s.metadataCache.Set(metadataCacheKey(tenant.ID, r.VehicleTokenID), r, cache.DefaultExpiration)
		}
	}
	// A token the roster does not hold is deliberately NOT cached as absent.
	// It is the freshly-entitled case, where the next reconcile fills it in;
	// caching the miss would hold the thin row for the TTL after the roster
	// could answer properly.
	return out, nil
}

func metadataCacheKey(tenantID string, tokenID int64) string {
	return fmt.Sprintf("%s:%d", tenantID, tokenID)
}

// mergeRosterVehicles is the join, and the shape of the cutover.
//
// THREE SOURCES, ONE ROW, AND THE PRECEDENCE MATTERS:
//
//   - the roster says what the vehicle IS — owner, definition, mint time,
//     device pairing. Authoritative, because it is the chain's answer
//     reconciled nightly, and the local copy is the one that drifted.
//   - the local row carries what only this app knows: last GPS fix and its
//     pull time. Those are app-local columns and stay app-local.
//   - favourites are per tenant and per vehicle, also ours.
//
// VIN and plate fall BACK to the local row when the roster has none. Both are
// fields the roster is a consumer for rather than a source of — identity-api
// serves neither — so today the roster's are usually empty while this app's
// registration-attestation sync has filled its own. Preferring the roster and
// falling back means the cutover cannot blank a plate a customer can already
// see, and it starts using roster values the moment kaufmann feeds them.
//
// A token with NO roster row still appears, thin, with MetadataPending — the
// same inversion step 2 established, one layer down. An inner join here would
// give a provably correct set and a short response.
//
// Pure, so every one of those cases is a test rather than an intention.
func mergeRosterVehicles(tokenIDs []int64, meta map[int64]models.RemoteVehicleMetadata,
	local []*dbmodels.Vehicle, favorites map[int64]bool) []models.Vehicle {
	localByToken := make(map[int64]*dbmodels.Vehicle, len(local))
	for _, r := range local {
		localByToken[r.TokenID] = r
	}

	out := make([]models.Vehicle, 0, len(tokenIDs))
	for _, id := range tokenIDs {
		v := models.Vehicle{TokenID: id, MetadataPending: true}
		if m, ok := meta[id]; ok {
			v = rosterToVehicle(m)
		}
		v.IsFavorite = favorites[id]

		if r, ok := localByToken[id]; ok {
			if v.VIN == "" {
				v.VIN = r.Vin.String
			}
			if v.LicensePlate == "" {
				v.LicensePlate = r.LicensePlate.String
			}
			applyLastLocation(&v, r)
		}
		out = append(out, v)
	}

	// Ordering is the local table's, unchanged: most recently seen first,
	// vehicles with no fix last in token order. It cannot come from the roster
	// — last_seen is an app-local column and the roster has never heard of it.
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i].LastSeen, out[j].LastSeen
		switch {
		case li != nil && lj != nil && !li.Equal(*lj):
			return li.After(*lj)
		case li != nil && lj == nil:
			return true
		case li == nil && lj != nil:
			return false
		}
		return out[i].TokenID < out[j].TokenID
	})
	return out
}

// rosterToVehicle turns one roster row into the wire shape.
//
// Device nodes are reconstructed from token ids alone, which is all the roster
// carries and all the UI reads — the list guards on `tokenId > 0` and the
// detail view prints the number. Serial and IMEI stay in kaufmann's device
// table, on the other side of the boundary plan 07 draws.
func rosterToVehicle(m models.RemoteVehicleMetadata) models.Vehicle {
	v := models.Vehicle{
		TokenID:  m.VehicleTokenID,
		MintedAt: m.MintedAt,
		Owner:    m.Owner,
		Definition: models.Definition{
			ID:    m.DefinitionID,
			Make:  m.Make,
			Model: m.Model,
			Year:  m.Year,
		},
		VIN:          m.VIN,
		LicensePlate: m.LicensePlate,
	}
	if m.SyntheticDeviceTokenID != nil {
		v.SyntheticDevice = models.SyntheticDevice{TokenID: *m.SyntheticDeviceTokenID}
	}
	if m.AftermarketDeviceTokenID != nil {
		v.AftermarketDevice = &models.AftermarketDevice{TokenID: *m.AftermarketDeviceTokenID}
	}
	return v
}
