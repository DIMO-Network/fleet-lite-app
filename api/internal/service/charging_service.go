// api/internal/service/charging_service.go
package service

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

// chargingFleetSummaryConcurrency bounds parallel per-vehicle telemetry
// fetches in FleetSummary. Mirrors fleetLocationsConcurrency in telemetry_api.go.
const chargingFleetSummaryConcurrency = 10

// ChargingSessionView is one session, priced against current tenant
// settings, shaped for the API and CSV export.
type ChargingSessionView struct {
	VehicleTokenID int64     `json:"tokenId"`
	VehicleLabel   string    `json:"vehicleLabel"`
	VIN            string    `json:"vin,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	// EndedAt is the session's last reading. For a session still in progress
	// that is "so far", not an end — see InProgress.
	EndedAt time.Time `json:"endedAt"`
	// InProgress: the session's last reading is recent enough that it may
	// still grow, so it isn't final (or stored) yet.
	InProgress     bool     `json:"inProgress,omitempty"`
	AddedEnergyKwh *float64 `json:"addedEnergyKwh,omitempty"`
	AvgPowerKw     *float64 `json:"avgPowerKw,omitempty"`
	SocStartPct    *float64 `json:"socStartPct,omitempty"`
	SocEndPct      *float64 `json:"socEndPct,omitempty"`
	Lat            *float64 `json:"lat,omitempty"`
	Lng            *float64 `json:"lng,omitempty"`
	Currency       string   `json:"currency"`
	ChargingSessionCost
}

// ChargingFleetTotals sums each session's figures across the fleet. Cost
// fields are nil (not zero) when no tenant settings are configured, so the
// UI can distinguish "$0 saved" from "not configured".
type ChargingFleetTotals struct {
	AddedEnergyKwh float64  `json:"addedEnergyKwh"`
	Cost           *float64 `json:"cost,omitempty"`
	Savings        *float64 `json:"savings,omitempty"`
}

// ChargingFleetSummary is the fleet-wide rollup: one point per session (for
// the map) plus fleet totals.
type ChargingFleetSummary struct {
	Sessions []ChargingSessionView `json:"sessions"`
	Fleet    ChargingFleetTotals   `json:"fleet"`
}

// ChargingService aggregates detected sessions (ChargingDetectionService)
// with tenant pricing settings (ChargingSettingsService) into the fleet
// summary and per-vehicle views the controller exposes.
type ChargingService struct {
	logger       *zerolog.Logger
	detectionSvc *ChargingDetectionService
	settingsSvc  *ChargingSettingsService
	vehicleSvc   *VehicleService
}

func NewChargingService(logger *zerolog.Logger, detectionSvc *ChargingDetectionService, settingsSvc *ChargingSettingsService, vehicleSvc *VehicleService) *ChargingService {
	return &ChargingService{logger: logger, detectionSvc: detectionSvc, settingsSvc: settingsSvc, vehicleSvc: vehicleSvc}
}

func toView(r ChargingSessionRow, label, vin string, settings ChargingSettings) ChargingSessionView {
	row := r.ChargingSession
	energy := row.AddedEnergyKWH.Ptr()
	v := ChargingSessionView{
		InProgress:          r.InProgress,
		VehicleTokenID:      row.TokenID,
		VehicleLabel:        label,
		VIN:                 vin,
		StartedAt:           row.StartedAt,
		EndedAt:             row.EndedAt,
		AddedEnergyKwh:      energy,
		AvgPowerKw:          row.AvgPowerKW.Ptr(),
		SocStartPct:         row.SocStartPCT.Ptr(),
		SocEndPct:           row.SocEndPCT.Ptr(),
		Lat:                 row.Lat.Ptr(),
		Lng:                 row.LNG.Ptr(),
		Currency:            settings.Currency,
		ChargingSessionCost: computeSessionCost(energy, settings),
	}
	return v
}

// VehicleSessions returns one vehicle's priced charging sessions in
// [from, to]. allowedGroupIDs scopes the lookup to a limited member's
// accessible groups (nil for owners/full-access members).
func (s *ChargingService) VehicleSessions(ctx context.Context, tenant models.Tenant, tokenID int64, allowedGroupIDs []string, from, to time.Time) ([]ChargingSessionView, error) {
	vehicle, err := s.vehicleSvc.GetVehicle(ctx, tenant, tokenID, allowedGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	label := vehicleLabel(*vehicle)

	rows, err := s.detectionSvc.Sessions(ctx, tenant, tokenID, from, to)
	if err != nil {
		return nil, fmt.Errorf("charging sessions: %w", err)
	}
	settings, err := s.settingsSvc.GetSettings(ctx, tenant.ID)
	if err != nil {
		return nil, fmt.Errorf("get charging settings: %w", err)
	}
	out := make([]ChargingSessionView, len(rows))
	for i, r := range rows {
		out[i] = toView(r, label, vehicle.VIN, settings)
	}
	return out, nil
}

// FleetSummary builds the charging rollup for every vehicle in the tenant
// that reports charging telemetry. A vehicle with no charging signals (an
// ICE vehicle, or an unsupported connection) simply contributes no sessions
// — never an error for the fleet as a whole. A single vehicle's detection
// failure is logged and skipped, same isolation FleetLocations/TCO use.
func (s *ChargingService) FleetSummary(ctx context.Context, tenant models.Tenant, allowedGroupIDs []string, from, to time.Time) (*ChargingFleetSummary, error) {
	vehicles, err := s.vehicleSvc.ListVehicles(ctx, tenant, allowedGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	settings, err := s.settingsSvc.GetSettings(ctx, tenant.ID)
	if err != nil {
		return nil, fmt.Errorf("get charging settings: %w", err)
	}
	var (
		mu      sync.Mutex
		allRows []ChargingSessionView
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(chargingFleetSummaryConcurrency)
	for _, v := range vehicles {
		v := v
		g.Go(func() error {
			rows, serr := s.detectionSvc.Sessions(gctx, tenant, v.TokenID, from, to)
			if serr != nil {
				s.logger.Warn().Err(serr).Int64("tokenID", v.TokenID).Msg("charging sessions failed, skipping")
				return nil
			}
			label := vehicleLabel(v)
			views := make([]ChargingSessionView, len(rows))
			for i, r := range rows {
				views[i] = toView(r, label, v.VIN, settings)
			}
			mu.Lock()
			allRows = append(allRows, views...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	out := &ChargingFleetSummary{Sessions: allRows}
	if out.Sessions == nil {
		out.Sessions = []ChargingSessionView{}
	}
	for _, view := range out.Sessions {
		if view.AddedEnergyKwh != nil {
			out.Fleet.AddedEnergyKwh += *view.AddedEnergyKwh
		}
		if view.Cost != nil {
			if out.Fleet.Cost == nil {
				out.Fleet.Cost = new(float64)
			}
			*out.Fleet.Cost += *view.Cost
		}
		if view.Savings != nil {
			if out.Fleet.Savings == nil {
				out.Fleet.Savings = new(float64)
			}
			*out.Fleet.Savings += *view.Savings
		}
	}
	return out, nil
}

// BuildCSV renders sessions as CSV text, one row per session. Cost fields
// render as empty cells (not "0") when unpriced, so a spreadsheet can't
// misread "not configured" as "no savings".
func BuildChargingCSV(sessions []ChargingSessionView) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	// inProgress is appended last so existing column positions don't move.
	_ = w.Write([]string{"vehicle", "vin", "startedAt", "endedAt", "addedEnergyKwh", "avgPowerKw", "cost", "gasCostAvoided", "savings", "currency", "inProgress"})
	for _, s := range sessions {
		_ = w.Write([]string{
			s.VehicleLabel,
			s.VIN,
			s.StartedAt.Format(time.RFC3339),
			s.EndedAt.Format(time.RFC3339),
			floatOrBlank(s.AddedEnergyKwh),
			floatOrBlank(s.AvgPowerKw),
			floatOrBlank(s.Cost),
			floatOrBlank(s.GasCostAvoided),
			floatOrBlank(s.Savings),
			s.Currency,
			boolOrBlank(s.InProgress),
		})
	}
	w.Flush()
	return b.String()
}

func floatOrBlank(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', 2, 64)
}

// boolOrBlank renders true as "true" and false as an empty cell, matching how
// unset numbers render.
func boolOrBlank(b bool) string {
	if b {
		return "true"
	}
	return ""
}
