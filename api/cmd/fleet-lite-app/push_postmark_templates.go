package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/google/subcommands"
	"github.com/rs/zerolog"
)

// pushPostmarkTemplatesCmd syncs the repo-stored Postmark templates (the source
// of truth under api/templates/postmark) up to Postmark by alias. Rerunnable per
// environment — it upserts, so existing aliases are updated in place.
type pushPostmarkTemplatesCmd struct {
	logger   zerolog.Logger
	settings config.Settings

	dir string
}

func (*pushPostmarkTemplatesCmd) Name() string { return "push-postmark-templates" }
func (*pushPostmarkTemplatesCmd) Synopsis() string {
	return "push api/templates/postmark up to Postmark (upsert by alias)"
}
func (*pushPostmarkTemplatesCmd) Usage() string {
	return `push-postmark-templates [-dir templates/postmark]:
	Reads manifest.json + body files and upserts each template to Postmark by alias.
  `
}

func (p *pushPostmarkTemplatesCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.dir, "dir", "templates/postmark", "directory holding manifest.json + template bodies")
}

type templateManifest struct {
	Templates []struct {
		Alias    string `json:"alias"`
		Name     string `json:"name"`
		Subject  string `json:"subject"`
		HTMLFile string `json:"htmlFile"`
		TextFile string `json:"textFile"`
	} `json:"templates"`
}

func (p *pushPostmarkTemplatesCmd) Execute(_ context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	postmark := gateway.NewPostmarkAPI(p.logger, &p.settings)
	if !postmark.Enabled() {
		p.logger.Fatal().Msg("POSTMARK_SERVER_TOKEN is not set; cannot push templates")
	}

	manifestPath := filepath.Join(p.dir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		p.logger.Fatal().Err(err).Str("path", manifestPath).Msg("read manifest")
	}
	var manifest templateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		p.logger.Fatal().Err(err).Msg("parse manifest")
	}
	if len(manifest.Templates) == 0 {
		p.logger.Fatal().Msg("manifest has no templates")
	}

	for _, t := range manifest.Templates {
		htmlBody, err := os.ReadFile(filepath.Join(p.dir, t.HTMLFile))
		if err != nil {
			p.logger.Fatal().Err(err).Str("alias", t.Alias).Msg("read html body")
		}
		var textBody []byte
		if t.TextFile != "" {
			textBody, err = os.ReadFile(filepath.Join(p.dir, t.TextFile))
			if err != nil {
				p.logger.Fatal().Err(err).Str("alias", t.Alias).Msg("read text body")
			}
		}
		if err := postmark.UpsertTemplate(t.Alias, t.Name, t.Subject, string(htmlBody), string(textBody)); err != nil {
			p.logger.Fatal().Err(err).Str("alias", t.Alias).Msg("upsert template")
		}
		fmt.Printf("pushed template %q\n", t.Alias)
	}
	p.logger.Info().Int("count", len(manifest.Templates)).Msg("postmark templates pushed")
	return subcommands.ExitSuccess
}
