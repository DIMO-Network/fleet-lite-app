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
	"github.com/rs/zerolog"
)

// VehicleRegistrationCloudEventType is the document attestation that carries a
// vehicle's license plate. fleet-lite-app does not publish it — the source-of-
// truth fleet system does (kaufmann-oracle ADR 0004) — we only read the latest
// one that carries a license_plate and cache it locally.
const VehicleRegistrationCloudEventType = "dimo.document.vehicle.registration"

// licensePlateField is the key the plate lives under in the registration
// document's parsed data.
const licensePlateField = "license_plate"

// LicensePlateSyncService is the read/cache half of the license-plate feature:
// it reads a vehicle's latest dimo.document.vehicle.registration attestation that
// carries a license_plate and caches it into vehicles.license_plate. It is a pure
// consumer — there is no publish path here (mirrors GroupSyncService's pull, but
// the plate is a single scalar so there is no membership reconcile). The
// import-group-attestations cron drives it per vehicle alongside the group sync.
type LicensePlateSyncService struct {
	logger       *zerolog.Logger
	pdb          *db.Store
	fetchAPI     *gateway.FetchAPI
	authProvider *gateway.DimoAuthProvider
}

func NewLicensePlateSyncService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider) *LicensePlateSyncService {
	return &LicensePlateSyncService{logger: logger, pdb: pdb, fetchAPI: fetchAPI, authProvider: authProvider}
}

// PlateSyncResult reports what a SyncVehicle call did.
type PlateSyncResult struct {
	Changed bool   // the cached license_plate was updated
	Plate   string // the resolved plate (empty when none found)
}

// SyncVehicle pulls one vehicle's registration attestations and caches the latest
// license_plate into vehicles.license_plate. The authoritative value is the
// license_plate from the most-recent (by CE time) registration document that
// actually carries one — user-uploaded registration documents without a plate, or
// older plate documents, are ignored. A successful-but-plateless read never clears
// an existing cached plate. Returns ErrVehicleNotFound for an unknown vehicle.
func (s *LicensePlateSyncService) SyncVehicle(ctx context.Context, tenant models.Tenant, tokenID int64, opts SyncOpts) (PlateSyncResult, error) {
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
			return PlateSyncResult{}, ErrVehicleNotFound
		}
		return PlateSyncResult{}, fmt.Errorf("load vehicle: %w", err)
	}

	did := s.authProvider.BuildVehicleDID(uint64(tokenID))
	entries, err := s.fetchAPI.ListByDIDAndType(tenant, did, VehicleRegistrationCloudEventType, limit)
	if err != nil {
		return PlateSyncResult{}, fmt.Errorf("fetch registration attestations: %w", err)
	}

	plate, found := latestPlate(entries)
	// A read that returned no plate-bearing document never clears an existing
	// cached value — the document set is eventually consistent and a missing plate
	// is "unknown", not "removed".
	if !found || plate == v.LicensePlate.String {
		return PlateSyncResult{Plate: v.LicensePlate.String}, nil
	}

	if opts.DryRun {
		s.logger.Info().Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
			Str("from", v.LicensePlate.String).Str("to", plate).Msg("would update license plate")
		return PlateSyncResult{Changed: true, Plate: plate}, nil
	}

	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{"license_plate": null.StringFrom(plate), "updated_at": time.Now()}); err != nil {
		return PlateSyncResult{}, fmt.Errorf("update license plate: %w", err)
	}
	return PlateSyncResult{Changed: true, Plate: plate}, nil
}

// latestPlate returns the license_plate from the most-recent (by CE time)
// registration document that carries a non-empty one, and whether such a
// document was found.
func latestPlate(entries []gateway.AttestationEntry) (string, bool) {
	var plate string
	var found bool
	var latest time.Time
	for i := range entries {
		e := &entries[i]
		if e.Type != VehicleRegistrationCloudEventType {
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(e.Data, &doc); err != nil {
			continue
		}
		raw, ok := doc[licensePlateField]
		if !ok {
			continue
		}
		var p string
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		t, _ := time.Parse(time.RFC3339, e.Time)
		if !found || t.After(latest) {
			plate = p
			latest = t
			found = true
		}
	}
	return plate, found
}
