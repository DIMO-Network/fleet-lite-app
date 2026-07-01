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
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/rs/zerolog"
)

// UserPrefsService reads and writes per-user UI preferences, keyed by wallet.
// Preferences are personal to the wallet (not the tenant): a wallet in multiple
// tenants has a single preferences row. The stored value is an opaque JSON blob
// of string values; the controller whitelists which keys/values are allowed.
type UserPrefsService struct {
	logger *zerolog.Logger
	pdb    *db.Store
}

func NewUserPrefsService(logger *zerolog.Logger, pdb *db.Store) *UserPrefsService {
	return &UserPrefsService{logger: logger, pdb: pdb}
}

// Get returns the wallet's stored preferences, or an empty (non-nil) map if the
// wallet has never saved any.
func (s *UserPrefsService) Get(ctx context.Context, wallet string) (map[string]string, error) {
	row, err := dbmodels.FindUserPreference(ctx, s.pdb.DBS().Reader, strings.ToLower(wallet))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("get user preferences: %w", err)
	}
	prefs := map[string]string{}
	if len(row.Prefs) > 0 {
		if err := json.Unmarshal(row.Prefs, &prefs); err != nil {
			return nil, fmt.Errorf("decode user preferences: %w", err)
		}
	}
	return prefs, nil
}

// Upsert full-replaces the wallet's preferences blob.
func (s *UserPrefsService) Upsert(ctx context.Context, wallet string, prefs map[string]string) error {
	raw, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("encode user preferences: %w", err)
	}
	now := time.Now()
	row := &dbmodels.UserPreference{
		Wallet:    strings.ToLower(wallet),
		Prefs:     types.JSON(raw),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := row.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{dbmodels.UserPreferenceColumns.Wallet},
		boil.Whitelist(dbmodels.UserPreferenceColumns.Prefs, dbmodels.UserPreferenceColumns.UpdatedAt),
		boil.Infer(),
	); err != nil {
		return fmt.Errorf("upsert user preferences: %w", err)
	}
	return nil
}
