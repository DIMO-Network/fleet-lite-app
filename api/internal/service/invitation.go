package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/rs/zerolog"
)

const (
	InviteStatusPending  = "pending"
	InviteStatusAccepted = "accepted"
	InviteStatusRevoked  = "revoked"

	// EmailStatusSent is stamped when Postmark accepts the message; the webhook
	// (docs/POSTMARK_WEBHOOK_PLAN.md phase 2) upgrades it to delivered/opened/bounced.
	EmailStatusSent = "sent"

	localeEN = "en"
	localeES = "es"

	defaultInviteExpiryHours = 168 // 7 days
)

// ErrInviteInvalid is returned when an accept token does not match a usable
// (pending, unexpired) invitation. It is deliberately vague so callers can map
// it to a single user-facing message without leaking which check failed.
var ErrInviteInvalid = errors.New("invitation is invalid, already used, or expired")

// ErrEmailNotSent signals a partial success: the invitation row was persisted
// (create) or its token refreshed (resend), but the email failed to dispatch.
// Callers should treat this as success-with-warning, not a hard failure — the
// invite is usable and can be resent. Wrapped around the underlying send error.
var ErrEmailNotSent = errors.New("invitation saved but the email could not be sent")

// postmarkSender is the slice of the Postmark gateway the service needs. Kept as
// an interface so the service is testable without a live Postmark.
type postmarkSender interface {
	SendInvitation(toEmail, templateAlias string, model gateway.InvitationModel, metadata map[string]string) (messageID string, err error)
}

// InvitationService owns the email-invitation lifecycle: issue (with a hashed
// single-use token + Postmark email), accept (bind the invitee's wallet to the
// tenant), list, and revoke.
type InvitationService struct {
	logger    *zerolog.Logger
	pdb       *db.Store
	settings  *config.Settings
	tenantSvc *TenantService
	postmark  postmarkSender
}

func NewInvitationService(logger *zerolog.Logger, pdb *db.Store, settings *config.Settings, tenantSvc *TenantService, postmark postmarkSender) *InvitationService {
	return &InvitationService{
		logger:    logger,
		pdb:       pdb,
		settings:  settings,
		tenantSvc: tenantSvc,
		postmark:  postmark,
	}
}

// Create issues an invitation: it supersedes any existing pending invite for the
// same (tenant, email), generates a single-use token (storing only its hash),
// persists the row, and sends the accept-link email via Postmark. Returns the
// stored invitation.
func (s *InvitationService) Create(ctx context.Context, tenantID, inviterWallet, email, role, locale string) (*dbmodels.Invitation, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if role != RoleOwner {
		role = RoleMember
	}

	// Supersede prior pending invites for this email so only one link is live.
	superseded, err := dbmodels.Invitations(
		dbmodels.InvitationWhere.TenantID.EQ(tenantID),
		qm.And("lower(email) = ?", email),
		dbmodels.InvitationWhere.Status.EQ(InviteStatusPending),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{
		"status":     InviteStatusRevoked,
		"updated_at": time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("supersede pending invites: %w", err)
	}
	if superseded > 0 {
		s.logger.Info().Str("tenant", tenantID).Str("email", email).Int64("count", superseded).
			Msg("invite flow: superseded prior pending invitations (their links are now dead)")
	}

	token, hash, err := generateInviteToken()
	if err != nil {
		return nil, err
	}

	inv := &dbmodels.Invitation{
		TenantID:        tenantID,
		Email:           email,
		Role:            role,
		TokenHash:       hash,
		Status:          InviteStatusPending,
		InvitedByWallet: strings.ToLower(inviterWallet),
		ExpiresAt:       time.Now().Add(s.expiry()),
	}
	if err := inv.Insert(ctx, s.pdb.DBS().Writer, boil.Infer()); err != nil {
		return nil, fmt.Errorf("insert invitation: %w", err)
	}
	s.logger.Info().Str("invitation", inv.ID).Str("tenant", tenantID).Str("email", email).
		Str("role", role).Str("invitedBy", inv.InvitedByWallet).Time("expiresAt", inv.ExpiresAt).
		Msg("invite flow: invitation created")

	messageID, err := s.sendEmail(ctx, tenantID, inviterWallet, email, token, locale, inv.ID)
	if err != nil {
		// The invite is persisted and usable; report the email failure as a partial
		// success (ErrEmailNotSent) so the caller returns the invite + a warning.
		s.logger.Err(err).Str("tenant", tenantID).Str("email", email).Msg("send invitation email")
		return inv, fmt.Errorf("%w: %v", ErrEmailNotSent, err)
	}
	s.markEmailSent(ctx, inv, messageID)
	return inv, nil
}

// markEmailSent stamps the Postmark message id + email_status='sent' on the
// invitation after a successful dispatch; the Postmark webhook later upgrades
// the status (see docs/POSTMARK_WEBHOOK_PLAN.md). Best-effort: tracking is
// advisory, so a failure is logged, never surfaced. No-op when sending is
// disabled (empty messageID, local dev).
func (s *InvitationService) markEmailSent(ctx context.Context, inv *dbmodels.Invitation, messageID string) {
	if messageID == "" {
		return
	}
	inv.PostmarkMessageID = null.StringFrom(messageID)
	inv.EmailStatus = null.StringFrom(EmailStatusSent)
	inv.EmailStatusAt = null.TimeFrom(time.Now())
	inv.EmailStatusDetail = null.String{} // clear stale detail from a prior bounce
	if _, err := inv.Update(ctx, s.pdb.DBS().Writer,
		boil.Whitelist("postmark_message_id", "email_status", "email_status_at", "email_status_detail")); err != nil {
		s.logger.Warn().Err(err).Str("invitation", inv.ID).Str("messageId", messageID).
			Msg("invite flow: could not record email tracking status")
	}
}

// Accept validates the token and binds the invitee's wallet to the tenant with
// the invited role. It is idempotent-ish: a second accept of the same token
// fails with ErrInviteInvalid (single-use). Returns the tenant id so the caller
// can redirect the invitee into it.
func (s *InvitationService) Accept(ctx context.Context, token, inviteeWallet string) (string, error) {
	hash := hashInviteToken(token)
	inv, err := dbmodels.Invitations(
		dbmodels.InvitationWhere.TokenHash.EQ(hash),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		// The client response is deliberately vague; log the real reason so the
		// flow is traceable. An unknown hash means the link was superseded by a
		// newer invite/resend, or was never issued.
		s.logger.Info().Str("wallet", strings.ToLower(inviteeWallet)).Str("tokenHashPrefix", hash[:8]).
			Msg("invite flow: accept failed — token not found (superseded by a newer invite/resend, or never issued)")
		return "", ErrInviteInvalid
	}
	if inv.Status != InviteStatusPending || time.Now().After(inv.ExpiresAt) {
		s.logger.Info().Str("invitation", inv.ID).Str("tenant", inv.TenantID).Str("email", inv.Email).
			Str("status", inv.Status).Time("expiresAt", inv.ExpiresAt).
			Str("acceptedByWallet", inv.InviteeWallet.String).Str("attemptingWallet", strings.ToLower(inviteeWallet)).
			Msg("invite flow: accept failed — invitation not pending or expired")
		return "", ErrInviteInvalid
	}

	if err := s.tenantSvc.AddMember(ctx, inv.TenantID, inviteeWallet, inv.Role); err != nil {
		return "", fmt.Errorf("add member: %w", err)
	}

	now := time.Now()
	inv.Status = InviteStatusAccepted
	inv.InviteeWallet = null.StringFrom(strings.ToLower(inviteeWallet))
	inv.AcceptedAt = null.TimeFrom(now)
	inv.UpdatedAt = now
	if _, err := inv.Update(ctx, s.pdb.DBS().Writer,
		boil.Whitelist("status", "invitee_wallet", "accepted_at", "updated_at")); err != nil {
		return inv.TenantID, fmt.Errorf("mark invitation accepted: %w", err)
	}
	s.logger.Info().Str("invitation", inv.ID).Str("tenant", inv.TenantID).Str("email", inv.Email).
		Str("role", inv.Role).Str("inviteeWallet", inv.InviteeWallet.String).
		Msg("invite flow: invitation accepted")
	return inv.TenantID, nil
}

// List returns a tenant's invitations, newest first.
func (s *InvitationService) List(ctx context.Context, tenantID string) (dbmodels.InvitationSlice, error) {
	return dbmodels.Invitations(
		dbmodels.InvitationWhere.TenantID.EQ(tenantID),
		qm.OrderBy("created_at desc"),
	).All(ctx, s.pdb.DBS().Reader)
}

// Revoke marks a pending invitation revoked. No-op (no error) if it isn't
// pending or doesn't belong to the tenant.
func (s *InvitationService) Revoke(ctx context.Context, tenantID, invitationID string) error {
	n, err := dbmodels.Invitations(
		dbmodels.InvitationWhere.ID.EQ(invitationID),
		dbmodels.InvitationWhere.TenantID.EQ(tenantID),
		dbmodels.InvitationWhere.Status.EQ(InviteStatusPending),
	).UpdateAll(ctx, s.pdb.DBS().Writer, dbmodels.M{
		"status":     InviteStatusRevoked,
		"updated_at": time.Now(),
	})
	if err == nil && n > 0 {
		s.logger.Info().Str("invitation", invitationID).Str("tenant", tenantID).
			Msg("invite flow: invitation revoked")
	}
	return err
}

// Resend re-sends the email for a pending invitation by minting a fresh token
// (the old token is invalidated). Returns ErrInviteInvalid if not pending.
func (s *InvitationService) Resend(ctx context.Context, tenantID, invitationID, inviterWallet, locale string) error {
	inv, err := dbmodels.Invitations(
		dbmodels.InvitationWhere.ID.EQ(invitationID),
		dbmodels.InvitationWhere.TenantID.EQ(tenantID),
		dbmodels.InvitationWhere.Status.EQ(InviteStatusPending),
	).One(ctx, s.pdb.DBS().Reader)
	if err != nil {
		return ErrInviteInvalid
	}
	token, hash, err := generateInviteToken()
	if err != nil {
		return err
	}
	inv.TokenHash = hash
	inv.ExpiresAt = time.Now().Add(s.expiry())
	inv.UpdatedAt = time.Now()
	if _, err := inv.Update(ctx, s.pdb.DBS().Writer,
		boil.Whitelist("token_hash", "expires_at", "updated_at")); err != nil {
		return fmt.Errorf("refresh invitation token: %w", err)
	}
	s.logger.Info().Str("invitation", inv.ID).Str("tenant", tenantID).Str("email", inv.Email).
		Time("expiresAt", inv.ExpiresAt).
		Msg("invite flow: token refreshed for resend (previous link is now dead)")
	messageID, err := s.sendEmail(ctx, tenantID, inviterWallet, inv.Email, token, locale, inv.ID)
	if err != nil {
		// Token was refreshed but the email didn't go out — partial success, the
		// invite stays pending and can be resent again once email is fixed.
		s.logger.Err(err).Str("tenant", tenantID).Str("email", inv.Email).Msg("resend invitation email")
		return fmt.Errorf("%w: %v", ErrEmailNotSent, err)
	}
	s.markEmailSent(ctx, inv, messageID)
	return nil
}

// sendEmail builds the accept link + template model and dispatches via Postmark,
// picking the template whose language matches the inviter's locale. The
// invitation id rides along as message metadata so delivery/open/bounce
// webhooks can be correlated back to the row; the returned MessageID ("" when
// sending is disabled) is the secondary correlation key.
func (s *InvitationService) sendEmail(ctx context.Context, tenantID, inviterWallet, email, token, locale, invitationID string) (string, error) {
	tenant, err := s.tenantSvc.GetTenantByID(ctx, tenantID)
	tenantName := tenantID
	if err == nil && tenant.Name != "" {
		tenantName = tenant.Name
	}
	model := gateway.InvitationModel{
		TenantName: tenantName,
		AcceptURL:  s.acceptURL(token),
		Inviter:    inviterWallet,
		ExpiresIn:  s.expiryLabel(locale),
	}
	alias := s.templateAlias(locale)
	messageID, err := s.postmark.SendInvitation(email, alias, model, map[string]string{"invitation_id": invitationID})
	if err != nil {
		return "", err
	}
	s.logger.Info().Str("tenant", tenantID).Str("email", email).Str("template", alias).
		Str("messageId", messageID).
		Msg("invite flow: invitation email dispatched")
	return messageID, nil
}

// templateAlias maps an inviter locale to the Postmark template alias. English
// (the default) uses the configured base alias; Spanish appends "-es". Any
// unknown/empty locale falls back to English. See docs/MEMBER_INVITATIONS_PLAN.md.
func (s *InvitationService) templateAlias(locale string) string {
	base := s.settings.InvitationTemplateAlias
	if normalizeLocale(locale) == localeES {
		return base + "-es"
	}
	return base
}

// normalizeLocale collapses a locale tag to one of the app's shipped locales
// ("en" or "es"). Anything not Spanish (incl. empty/"en"/region tags like
// "en-US") resolves to English.
func normalizeLocale(locale string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "es") {
		return localeES
	}
	return localeEN
}

func (s *InvitationService) expiry() time.Duration {
	h := s.settings.InviteExpiryHours
	if h <= 0 {
		h = defaultInviteExpiryHours
	}
	return time.Duration(h) * time.Hour
}

// expiryLabel renders the token lifetime as human copy in the inviter's locale,
// e.g. "7 days" / "7 días". Used for the {{expires_in}} template variable.
func (s *InvitationService) expiryLabel(locale string) string {
	h := s.settings.InviteExpiryHours
	if h <= 0 {
		h = defaultInviteExpiryHours
	}
	es := normalizeLocale(locale) == localeES
	if h%24 == 0 {
		days := h / 24
		switch {
		case days == 1 && es:
			return "1 día"
		case days == 1:
			return "1 day"
		case es:
			return fmt.Sprintf("%d días", days)
		default:
			return fmt.Sprintf("%d days", days)
		}
	}
	if es {
		return fmt.Sprintf("%d horas", h)
	}
	return fmt.Sprintf("%d hours", h)
}

// acceptURL builds the public accept link the email points at:
//
//	{APP_BASE_URL}/accept-invite.html?token=<token>
func (s *InvitationService) acceptURL(token string) string {
	base := s.settings.AppBaseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/accept-invite.html"
	q := url.Values{}
	q.Set("token", token)
	base.RawQuery = q.Encode()
	return base.String()
}

// generateInviteToken returns a URL-safe random token and its SHA-256 hash. Only
// the hash is persisted; the raw token lives only in the email link.
func generateInviteToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate invite token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, hashInviteToken(token), nil
}

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
