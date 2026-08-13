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

// licensePlateFieldNames and vinFieldNames are the keys the plate and VIN live
// under in the registration document's parsed data. Both are read from the
// same dimo.document.vehicle.registration attestation in one pass. The extract
// API's raw response is passed through to attestation verbatim and is not
// flat — the real fields live nested (e.g. under "data"."fields") and the
// extract API calls the plate "plateNumber", not "license_plate", so multiple
// names and a nested search are required. See findFieldInDoc.
var (
	licensePlateFieldNames = []string{"license_plate", "plateNumber"}
	vinFieldNames          = []string{"vin"}
)

// registrationWrapperKeys are the keys under which the real registration
// fields may be nested, mirroring the wrapper shape the extract API uses.
var registrationWrapperKeys = []string{"data", "fields", "result", "document"}

// DefaultFetchLimit bounds how many recent CEs we pull per vehicle when looking
// for the latest value-bearing document.
const DefaultFetchLimit = 50

// SyncOpts tunes a single vehicle sync.
type SyncOpts struct {
	// DryRun logs the changes that would be made without writing.
	DryRun bool
	// Limit overrides how many recent CEs to pull (DefaultFetchLimit when 0).
	Limit int
}

// LicensePlateSyncService is the read/cache half of the vehicle-registration
// feature: it reads a vehicle's latest dimo.document.vehicle.registration
// attestation and caches the registration fields we surface — license_plate and
// vin — into vehicles.license_plate / vehicles.vin. It is a pure consumer —
// there is no publish path here, and these are single scalars so there is no
// membership-style reconcile. The documents controller drives it lazily after
// a registration document is attested.
type LicensePlateSyncService struct {
	logger       *zerolog.Logger
	pdb          *db.Store
	fetchAPI     *gateway.FetchAPI
	authProvider *gateway.DimoAuthProvider
	telemetry    TelemetryAPIService
}

func NewLicensePlateSyncService(logger *zerolog.Logger, pdb *db.Store, fetchAPI *gateway.FetchAPI, authProvider *gateway.DimoAuthProvider, telemetry TelemetryAPIService) *LicensePlateSyncService {
	return &LicensePlateSyncService{logger: logger, pdb: pdb, fetchAPI: fetchAPI, authProvider: authProvider, telemetry: telemetry}
}

// PlateSyncResult reports what a SyncVehicle call did across the registration
// fields it caches (license_plate and vin).
type PlateSyncResult struct {
	Changed    bool   // the cached license_plate was updated
	Plate      string // the resolved plate (empty when none found)
	VINChanged bool   // the cached vin was updated
	VIN        string // the resolved vin (empty when none found)
	VINSource  string // where a newly-cached vin came from: "registration" | "vc"
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
	if plate, found := latestRegistrationField(entries, licensePlateFieldNames); found && plate != v.LicensePlate.String {
		updates["license_plate"] = null.StringFrom(plate)
		res.Changed = true
		res.Plate = plate
	}
	if vin, found := latestRegistrationField(entries, vinFieldNames); found && vin != v.Vin.String {
		updates["vin"] = null.StringFrom(vin)
		res.VINChanged = true
		res.VIN = vin
		res.VINSource = "registration"
	}

	// VIN VC fallback: most vehicles never get a registration document
	// uploaded, but almost all carry a DIMO VIN verifiable credential — read
	// it when the VIN is still unknown after the document pass. Pull-once by
	// construction: once vehicles.vin is set, res.VIN is non-empty here and
	// the vehicle never costs another telemetry query. See
	// docs/VIN_SYNC_PLAN.md.
	if res.VIN == "" {
		if vin, found := s.vinFromVC(tenant, tokenID); found {
			updates["vin"] = null.StringFrom(vin)
			res.VINChanged = true
			res.VIN = vin
			res.VINSource = "vc"
		}
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

// vinFromVC wraps TelemetryAPIService.VINFromVC with this service's skip-
// quietly posture: no SACD, no VC, or a transient error all come back as
// found=false and are retried on a future pass (only while vin IS NULL).
func (s *LicensePlateSyncService) vinFromVC(tenant models.Tenant, tokenID int64) (string, bool) {
	if s.telemetry == nil {
		return "", false
	}
	vin, found, err := s.telemetry.VINFromVC(tenant, uint64(tokenID))
	if err != nil {
		s.logger.Debug().Err(err).Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
			Msg("vin vc read failed, skipping")
		return "", false
	}
	return vin, found
}

// SyncVINOnly fills vehicles.vin from the DIMO VIN VC for one vehicle,
// skipping the fetch-api registration pull — the lean path for the cron's
// -vin-only backfill. No-op when the vehicle already has a VIN (pull-once).
// Same fill-if-missing and dry-run semantics as SyncVehicle.
func (s *LicensePlateSyncService) SyncVINOnly(ctx context.Context, tenant models.Tenant, tokenID int64, dryRun bool) (PlateSyncResult, error) {
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

	res := PlateSyncResult{Plate: v.LicensePlate.String, VIN: v.Vin.String}
	if v.Vin.String != "" {
		return res, nil
	}
	vin, found := s.vinFromVC(tenant, tokenID)
	if !found {
		return res, nil
	}
	res.VINChanged = true
	res.VIN = vin
	res.VINSource = "vc"

	if dryRun {
		s.logger.Info().Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
			Str("vin", vin).Msg("would cache vin from vc")
		return res, nil
	}

	if _, err := dbmodels.Vehicles(
		dbmodels.VehicleWhere.TenantID.EQ(tenant.ID),
		dbmodels.VehicleWhere.TokenID.EQ(tokenID),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{
		"vin":        null.StringFrom(vin),
		"updated_at": time.Now(),
	}); err != nil {
		return PlateSyncResult{}, fmt.Errorf("update vin: %w", err)
	}
	return res, nil
}

// latestRegistrationField returns the value of the first matching name in
// names from the most-recent (by CE time) registration document that carries
// a non-empty one, and whether such a document was found. Used for both
// license_plate and vin. Multiple names support the extract API's field
// aliases (e.g. "plateNumber" alongside "license_plate"); the search also
// recurses into registrationWrapperKeys since the extract API's raw response
// is attested verbatim and is not flat.
func latestRegistrationField(entries []gateway.AttestationEntry, names []string) (string, bool) {
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
		p, ok := findFieldInDoc(doc, names, 0, 4)
		if !ok {
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

// findFieldInDoc looks for the first of names as a non-empty string at doc's
// top level, then recurses into registrationWrapperKeys up to maxDepth levels
// deep (mirrors extract_api.go's findVINInMap, which needs the same nested
// search over the extract API's raw response shape).
func findFieldInDoc(doc map[string]json.RawMessage, names []string, depth, maxDepth int) (string, bool) {
	if depth > maxDepth {
		return "", false
	}
	for _, name := range names {
		raw, ok := doc[name]
		if !ok {
			continue
		}
		var p string
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p = strings.TrimSpace(p); p != "" {
			return p, true
		}
	}
	for _, key := range registrationWrapperKeys {
		raw, ok := doc[key]
		if !ok {
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			continue
		}
		if v, ok := findFieldInDoc(nested, names, depth+1, maxDepth); ok {
			return v, true
		}
	}
	return "", false
}
