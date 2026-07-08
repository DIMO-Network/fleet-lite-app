package main

import (
	"context"
	"flag"
	"strings"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/google/subcommands"
	"github.com/rs/zerolog"
)

// configurePostmarkWebhookCmd idempotently points the Postmark outbound-stream
// webhook (delivery + bounce + first-open events) at this deployment's
// /webhooks/postmark endpoint, secured with POSTMARK_WEBHOOK_SECRET as the
// basic-auth password. Rerunnable per environment — it updates the existing
// webhook for the URL in place, else creates one. Run it once after a deploy
// that changes the secret or the app's public origin.
type configurePostmarkWebhookCmd struct {
	logger   zerolog.Logger
	settings config.Settings

	url string
}

func (*configurePostmarkWebhookCmd) Name() string { return "configure-postmark-webhook" }
func (*configurePostmarkWebhookCmd) Synopsis() string {
	return "create/update the Postmark webhook feeding invitation email tracking"
}
func (*configurePostmarkWebhookCmd) Usage() string {
	return `configure-postmark-webhook [-url https://host/webhooks/postmark]:
	Ensures the outbound message stream posts Delivery/Bounce/Open events to this
	deployment's /webhooks/postmark, authenticated with POSTMARK_WEBHOOK_SECRET.
	Defaults to APP_BASE_URL + /webhooks/postmark.
  `
}

func (p *configurePostmarkWebhookCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.url, "url", "", "webhook URL override (default APP_BASE_URL + /webhooks/postmark)")
}

func (p *configurePostmarkWebhookCmd) Execute(_ context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if p.settings.PostmarkWebhookSecret == "" {
		p.logger.Fatal().Msg("POSTMARK_WEBHOOK_SECRET is not set; refusing to configure an unauthenticated webhook")
	}
	postmark := gateway.NewPostmarkAPI(p.logger, &p.settings)
	if !postmark.Enabled() {
		p.logger.Fatal().Msg("POSTMARK_SERVER_TOKEN is not set; cannot configure webhook")
	}

	url := p.url
	if url == "" {
		base := p.settings.AppBaseURL
		base.Path = strings.TrimRight(base.Path, "/") + "/webhooks/postmark"
		url = base.String()
	}
	if err := postmark.EnsureInvitationWebhook(url, "postmark", p.settings.PostmarkWebhookSecret); err != nil {
		p.logger.Fatal().Err(err).Str("url", url).Msg("configure postmark webhook")
	}
	return subcommands.ExitSuccess
}
