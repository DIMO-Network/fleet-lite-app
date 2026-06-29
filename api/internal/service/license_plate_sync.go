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

// licensePlateField and vinField are the keys the plate and VIN live under in
// the registration document's parsed data. Both are read from the same
// dimo.document.vehicle.registration attestation in one pass.
const (
	licensePlateField = "license_plate"
	vinField          = "vin"
)

// LicensePlateSyncService is the read/cache half of the vehicle-registration
// feature: it reads a vehicle's latest dimo.document.vehicle.registration
// attestation and caches the registration fields we surface — license_plate and
// vin — into vehicles.license_plate / vehicles.vin. It is a pure consumer —
// there is no publish path here (mirrors GroupSyncService's pull, but these are
// single scalars so there is no membership reconcile). The
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

// PlateSyncResult reports what a SyncVehicle call did across the registration
// fields it caches (license_plate and vin).
type PlateSyncResult struct {
	Changed    bool   // the cached license_plate was updated
	Plate      string // the resolved plate (empty when none found)
	VINChanged bool   // the cached vin was updated
	VIN        string // the resolved vin (empty when none found)
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

	// A read that returned no value-bearing document for a field never clears an
	// existing cached value — the document set is eventually consistent and a
	// missing field is "unknown", not "removed". So we only ever write changes.
	res := PlateSyncResult{Plate: v.LicensePlate.String, VIN: v.Vin.String}
	updates := dbmodels.M{}
	if plate, found := latestRegistrationField(entries, licensePlateField); found && plate != v.LicensePlate.String {
		updates["license_plate"] = null.StringFrom(plate)
		res.Changed = true
		res.Plate = plate
	}
	if vin, found := latestRegistrationField(entries, vinField); found && vin != v.Vin.String {
		updates["vin"] = null.StringFrom(vin)
		res.VINChanged = true
		res.VIN = vin
	}

	if len(updates) == 0 {
		return res, nil
	}

	if opts.DryRun {
		s.logger.Info().Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
			Str("plate", res.Plate).Str("vin", res.VIN).Msg("would update registration fields")
		return res, nil
	}

	updates["updated_at"] = time.Now()
	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).UpdateAll(ctx, s.pdb.DBS().Writer, updates); err != nil {
		return PlateSyncResult{}, fmt.Errorf("update registration fields: %w", err)
	}
	return res, nil
}

// latestRegistrationField returns the value of the given string field from the
// most-recent (by CE time) registration document that carries a non-empty one,
// and whether such a document was found. Used for both license_plate and vin.
func latestRegistrationField(entries []gateway.AttestationEntry, field string) (string, bool) {
	var val string
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
		raw, ok := doc[field]
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
			val = p
			latest = t
			found = true
		}
	}
	return val, found
}
