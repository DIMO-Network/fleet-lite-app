package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog"
)

const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// TenantService owns tenant rows, membership, and credential encryption.
type TenantService struct {
	logger      *zerolog.Logger
	pdb         *db.Store
	settings    *config.Settings
	identityAPI gateway.IdentityAPI
	cache       *cache.Cache // tenant_<id> -> *models.Tenant (decrypted)
}

func NewTenantService(logger *zerolog.Logger, pdb *db.Store, settings *config.Settings, identityAPI gateway.IdentityAPI) *TenantService {
	return &TenantService{
		logger:      logger,
		pdb:         pdb,
		settings:    settings,
		identityAPI: identityAPI,
		cache:       cache.New(24*time.Hour, 25*time.Hour),
	}
}

// CreateTenant inserts a tenant (encrypting the API key) and the owner membership.
func (s *TenantService) CreateTenant(ctx context.Context, name, clientID, apiKeyPlain, ownerWallet string) (*dbmodels.Tenant, error) {
	tenant := &dbmodels.Tenant{Name: name}
	if clientID != "" {
		tenant.DimoClientID = null.StringFrom(clientID)
	}
	if apiKeyPlain != "" {
		enc, err := s.encryptSecret(apiKeyPlain)
		if err != nil {
			return nil, fmt.Errorf("encrypt DIMO API key: %w", err)
		}
		tenant.DimoAPIKeyEnc = null.StringFrom(enc)
	}

	if err := tenant.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		return nil, fmt.Errorf("insert tenant: %w", err)
	}
	if err := s.AddMember(ctx, tenant.ID, ownerWallet, RoleOwner, nil); err != nil {
		return nil, fmt.Errorf("add owner membership: %w", err)
	}
	return tenant, nil
}

// GetTenantByID returns a tenant with secrets decrypted and redirect URI resolved.
func (s *TenantService) GetTenantByID(ctx context.Context, tenantID string) (*models.Tenant, error) {
	cacheKey := "tenant_" + tenantID
	if cached, found := s.cache.Get(cacheKey); found {
		return cached.(*models.Tenant), nil
	}

	dbTenant, err := dbmodels.FindTenant(ctx, s.pdb.DBS().Reader, tenantID)
	if err != nil {
		return nil, fmt.Errorf("find tenant %s: %w", tenantID, err)
	}

	tenant := &models.Tenant{ID: dbTenant.ID, Name: dbTenant.Name}
	if dbTenant.DimoClientID.Valid {
		tenant.ClientID = dbTenant.DimoClientID.String
		if tenant.ClientID != "" {
			if lic, lerr := s.identityAPI.FetchDeveloperLicenseByClientID(tenant.ClientID); lerr != nil {
				s.logger.Warn().Err(lerr).Str("client_id", tenant.ClientID).Msg("resolve developer license redirect URI")
			} else if lic != nil && len(lic.RedirectURIs.Edges) > 0 {
				tenant.DIMORedirectURI = lic.RedirectURIs.Edges[0].Node.URI
			}
		}
	}
	if dbTenant.DimoAPIKeyEnc.Valid && dbTenant.DimoAPIKeyEnc.String != "" {
		dec, derr := s.decryptSecret(dbTenant.DimoAPIKeyEnc.String)
		if derr != nil {
			return nil, fmt.Errorf("decrypt DIMO API key: %w", derr)
		}
		tenant.DIMOPrivateKey = dec
	}

	s.cache.Set(cacheKey, tenant, cache.DefaultExpiration)
	return tenant, nil
}

// ListTenantsForWallet returns every tenant the wallet is a member of.
func (s *TenantService) ListTenantsForWallet(ctx context.Context, wallet string) (dbmodels.TenantSlice, error) {
	return dbmodels.Tenants(
		qm.InnerJoin("tenant_users tu on tu.tenant_id = tenants.id"),
		qm.Where("lower(tu.wallet) = lower(?)", wallet),
		qm.OrderBy("tenants.created_at"),
	).All(ctx, s.pdb.DBS().Reader)
}

// GetMembershipRole returns the wallet's role in the tenant, or "" if not a member.
func (s *TenantService) GetMembershipRole(ctx context.Context, tenantID, wallet string) (string, error) {
	tu, err := s.GetMembership(ctx, tenantID, wallet)
	if err != nil {
		return "", err
	}
	return tu.Role, nil
}

// GetMembership returns the wallet's full membership row (role + allowed
// groups) in the tenant.
func (s *TenantService) GetMembership(ctx context.Context, tenantID, wallet string) (*dbmodels.TenantUser, error) {
	return dbmodels.TenantUsers(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("lower(wallet) = lower(?)", wallet),
	).One(ctx, s.pdb.DBS().Reader)
}

// ListMembers returns every membership row for a tenant, oldest first (owner
// typically created first).
func (s *TenantService) ListMembers(ctx context.Context, tenantID string) (dbmodels.TenantUserSlice, error) {
	return dbmodels.TenantUsers(
		qm.Where("tenant_id = ?", tenantID),
		qm.OrderBy("created_at"),
	).All(ctx, s.pdb.DBS().Reader)
}

// AddMember upserts a wallet's membership in a tenant. allowedGroupIDs limits a
// member to those fleet groups; nil = full access. Owners are always
// unrestricted, so the column is forced NULL for them regardless of the
// argument. See docs/GROUP_ACCESS_PLAN.md.
func (s *TenantService) AddMember(ctx context.Context, tenantID, wallet, role string, allowedGroupIDs []string) error {
	if role == "" {
		role = RoleMember
	}
	if role == RoleOwner {
		allowedGroupIDs = nil
	}
	tu := &dbmodels.TenantUser{
		TenantID:        tenantID,
		Wallet:          strings.ToLower(wallet),
		Role:            role,
		AllowedGroupIds: types.StringArray(allowedGroupIDs),
	}
	return tu.Upsert(ctx, s.pdb.DBS().Writer, true,
		[]string{"tenant_id", "wallet"},
		boil.Whitelist("role", "allowed_group_ids", "updated_at"), boil.Infer())
}

// UpdateMemberAccess changes an existing member's allowed fleet groups
// (nil = full access). Owners cannot be limited; attempting to returns an
// error so the caller surfaces it instead of silently ignoring the request.
func (s *TenantService) UpdateMemberAccess(ctx context.Context, tenantID, wallet string, allowedGroupIDs []string) error {
	tu, err := s.GetMembership(ctx, tenantID, wallet)
	if err != nil {
		return fmt.Errorf("membership not found: %w", err)
	}
	if tu.Role == RoleOwner && allowedGroupIDs != nil {
		return fmt.Errorf("owners always have full access; demote to member first")
	}
	tu.AllowedGroupIds = types.StringArray(allowedGroupIDs)
	tu.UpdatedAt = time.Now()
	_, err = tu.Update(ctx, s.pdb.DBS().Writer, boil.Whitelist("allowed_group_ids", "updated_at"))
	return err
}

// TouchLogin records a member's login: bumps last_login_at to now and stores
// the email (the human-readable identity — DIMO's JWT carries neither name nor
// email, so the client supplies the email from the OAuth redirect). A blank
// email leaves any existing one intact. No-op if the wallet isn't a member.
func (s *TenantService) TouchLogin(ctx context.Context, tenantID, wallet, email string) error {
	tu, err := dbmodels.TenantUsers(
		dbmodels.TenantUserWhere.TenantID.EQ(tenantID),
		dbmodels.TenantUserWhere.Wallet.EQ(strings.ToLower(wallet)),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	tu.LastLoginAt = null.TimeFrom(now)
	tu.UpdatedAt = now
	if email != "" {
		tu.Email = null.StringFrom(email)
	}
	_, err = tu.Update(ctx, s.pdb.DBS().Writer,
		boil.Whitelist("last_login_at", "email", "updated_at"))
	return err
}

// HasRecentLogin reports whether any of the tenant's members logged in within
// the given window. Drives the group-sync cron's warm/cold tiering: a tenant is
// "warm" (gets the daily pass) if someone is actively using its fleet.
func (s *TenantService) HasRecentLogin(ctx context.Context, tenantID string, within time.Duration) (bool, error) {
	threshold := time.Now().Add(-within)
	return dbmodels.TenantUsers(
		dbmodels.TenantUserWhere.TenantID.EQ(tenantID),
		dbmodels.TenantUserWhere.LastLoginAt.GTE(null.TimeFrom(threshold)),
	).Exists(ctx, s.pdb.DBS().Reader)
}

// RemoveMember deletes a wallet's membership from a tenant.
func (s *TenantService) RemoveMember(ctx context.Context, tenantID, wallet string) error {
	_, err := dbmodels.TenantUsers(
		qm.Where("tenant_id = ?", tenantID),
		qm.And("lower(wallet) = lower(?)", wallet),
	).DeleteAll(ctx, s.pdb.DBS().Writer)
	return err
}

// UpdateCredentials updates a tenant's DIMO client ID and/or API key (re-encrypting).
func (s *TenantService) UpdateCredentials(ctx context.Context, tenantID string, clientID, apiKeyPlain *string) error {
	tenant, err := dbmodels.FindTenant(ctx, s.pdb.DBS().Reader, tenantID)
	if err != nil {
		return fmt.Errorf("find tenant %s: %w", tenantID, err)
	}
	if clientID != nil {
		tenant.DimoClientID = null.StringFrom(*clientID)
	}
	if apiKeyPlain != nil && *apiKeyPlain != "" {
		enc, eerr := s.encryptSecret(*apiKeyPlain)
		if eerr != nil {
			return fmt.Errorf("encrypt DIMO API key: %w", eerr)
		}
		tenant.DimoAPIKeyEnc = null.StringFrom(enc)
	}
	tenant.UpdatedAt = time.Now()
	if _, err := tenant.Update(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		return fmt.Errorf("update tenant: %w", err)
	}
	s.cache.Delete("tenant_" + tenantID)
	return nil
}

// HasAPIKey reports whether the tenant has a stored (encrypted) API key.
func (s *TenantService) HasAPIKey(ctx context.Context, tenantID string) (bool, error) {
	t, err := dbmodels.FindTenant(ctx, s.pdb.DBS().Reader, tenantID, "dimo_api_key_enc")
	if err != nil {
		return false, err
	}
	return t.DimoAPIKeyEnc.Valid && t.DimoAPIKeyEnc.String != "", nil
}

// encryptSecret encrypts plaintext with AES-256-GCM keyed by sha256(TENANT_SECRET_ENC_KEY).
func (s *TenantService) encryptSecret(plaintext string) (string, error) {
	keyHash := sha256.Sum256([]byte(s.settings.TenantSecretEncKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	combined := append(append([]byte{}, nonce...), ciphertext...)
	return base64.StdEncoding.EncodeToString(combined), nil
}

// decryptSecret reverses encryptSecret.
func (s *TenantService) decryptSecret(encB64 string) (string, error) {
	if encB64 == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encB64)
	if err != nil {
		return "", err
	}
	keyHash := sha256.Sum256([]byte(s.settings.TenantSecretEncKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
