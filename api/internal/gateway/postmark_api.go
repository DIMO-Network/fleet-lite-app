package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/rs/zerolog"
)

const postmarkBaseURL = "https://api.postmarkapp.com"

// PostmarkAPI wraps the Postmark transactional email + templates API.
// Auth is the server token in the X-Postmark-Server-Token header.
type PostmarkAPI struct {
	settings    *config.Settings
	logger      zerolog.Logger
	serverToken string
	baseURL     string
}

func NewPostmarkAPI(logger zerolog.Logger, settings *config.Settings) *PostmarkAPI {
	return &PostmarkAPI{
		settings:    settings,
		logger:      logger,
		serverToken: settings.PostmarkServerToken,
		baseURL:     postmarkBaseURL,
	}
}

// Enabled reports whether a server token is configured. When false, callers
// should treat email sending as a no-op (e.g. local dev without Postmark).
func (p *PostmarkAPI) Enabled() bool {
	return p.serverToken != ""
}

// InvitationModel is the template model substituted into the Postmark template.
// Field names must match the {{mustache}} variables in the template body.
type InvitationModel struct {
	TenantName string `json:"tenant_name"`
	AcceptURL  string `json:"accept_url"`
	Inviter    string `json:"inviter"`
	ExpiresIn  string `json:"expires_in"`
}

// SendInvitation sends the invitation email via Postmark's templated-email
// endpoint (POST /email/withTemplate) using the given template alias. The alias
// is chosen by the caller from the inviter's locale (e.g. fleet-invitation /
// fleet-invitation-es) — see InvitationService.templateAlias.
//
// metadata is attached to the message and echoed back verbatim in every webhook
// event (delivery/open/bounce), which is how those events are correlated to an
// invitation row — see docs/POSTMARK_WEBHOOK_PLAN.md. Returns Postmark's
// MessageID ("" when sending is disabled) as a secondary correlation key.
func (p *PostmarkAPI) SendInvitation(toEmail, templateAlias string, model InvitationModel, metadata map[string]string) (string, error) {
	if !p.Enabled() {
		// Local-dev sink: with no Postmark token we can't send, so log the accept
		// link at info level — copy it from the logs to exercise the accept flow
		// without any email infrastructure. See docs/MEMBER_INVITATIONS_PLAN.md.
		p.logger.Info().
			Str("to", toEmail).
			Str("template", templateAlias).
			Str("accept_url", model.AcceptURL).
			Msg("postmark not configured; invitation email skipped — use this accept link locally")
		return "", nil
	}
	payload := map[string]any{
		"From":          p.settings.InvitationFromEmail,
		"To":            toEmail,
		"TemplateAlias": templateAlias,
		"TemplateModel": model,
		"MessageStream": "outbound",
		// Open tracking is per-message; the webhook only sees opens for messages
		// sent with it. Best-effort signal (needs the client to load images).
		"TrackOpens": true,
	}
	if len(metadata) > 0 {
		payload["Metadata"] = metadata
	}
	var resp struct {
		ErrorCode int    `json:"ErrorCode"`
		Message   string `json:"Message"`
		MessageID string `json:"MessageID"`
	}
	if err := p.do("POST", "/email/withTemplate", payload, &resp); err != nil {
		return "", err
	}
	if resp.ErrorCode != 0 {
		return "", fmt.Errorf("postmark send error %d: %s", resp.ErrorCode, resp.Message)
	}
	return resp.MessageID, nil
}

// UpsertTemplate creates or updates a Postmark template by alias. Used by the
// push-postmark-templates command to sync repo-stored templates to Postmark.
// Postmark has no single upsert call, so we PUT (update by alias) and fall back
// to POST (create) when the alias doesn't exist yet.
func (p *PostmarkAPI) UpsertTemplate(alias, name, subject, htmlBody, textBody string) error {
	if !p.Enabled() {
		return fmt.Errorf("postmark server token not configured")
	}
	body := map[string]any{
		"Name":         name,
		"Alias":        alias,
		"Subject":      subject,
		"HtmlBody":     htmlBody,
		"TextBody":     textBody,
		"TemplateType": "Standard",
	}
	// Try update first (PUT /templates/{alias}); if the template is missing
	// Postmark returns ErrorCode 1101, in which case we create it.
	var resp struct {
		ErrorCode int    `json:"ErrorCode"`
		Message   string `json:"Message"`
	}
	err := p.do("PUT", "/templates/"+alias, body, &resp)
	if err == nil && resp.ErrorCode == 0 {
		return nil
	}
	if err == nil && resp.ErrorCode != 1101 {
		return fmt.Errorf("postmark update template error %d: %s", resp.ErrorCode, resp.Message)
	}
	// Create.
	resp.ErrorCode, resp.Message = 0, ""
	if cerr := p.do("POST", "/templates", body, &resp); cerr != nil {
		return cerr
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("postmark create template error %d: %s", resp.ErrorCode, resp.Message)
	}
	return nil
}

// do performs a JSON request against the Postmark API and decodes the response
// into out (which may be nil). A non-2xx HTTP status is an error; Postmark-level
// errors (ErrorCode in the body) are left for the caller to inspect.
func (p *PostmarkAPI) do(method, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal postmark request: %w", err)
	}
	req, err := http.NewRequest(method, p.baseURL+path, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("build postmark request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Postmark-Server-Token", p.serverToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("postmark request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read postmark response: %w", err)
	}
	// Postmark uses 422 to carry actionable ErrorCode bodies; decode those too.
	if resp.StatusCode >= 500 {
		return fmt.Errorf("postmark status %d: %s", resp.StatusCode, string(respBytes))
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("parse postmark response (status %d): %w", resp.StatusCode, err)
		}
	}
	return nil
}
