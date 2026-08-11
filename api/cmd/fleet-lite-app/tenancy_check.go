package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// tenancyCheckCmd calls fleet-tenancy-api's /v1/authz with this app's real
// credentials and reports what happened, layer by layer.
//
// It exists because nothing in the request path calls the tenancy service yet,
// so without it the only way to know whether TENANCY_API_KEY and the per-tenant
// developer licenses actually work in production is to wait until cutover and
// find out. This turns that into a command.
//
// Read the layer in the output rather than the status code. All three of
// "unknown application", "bad JWT" and — before scope — "unknown tenant" reach
// the caller as 401, which is why gateway.TenancyError classifies them.
type tenancyCheckCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID string
	wallet   string
	all      bool
}

func (*tenancyCheckCmd) Name() string { return "tenancy-check" }
func (*tenancyCheckCmd) Synopsis() string {
	return "call fleet-tenancy-api /v1/authz with this app's key and a tenant's developer JWT"
}
func (*tenancyCheckCmd) Usage() string {
	return `tenancy-check [-tenant-id ID | -all] [-wallet 0x...]:
	Verifies this app can reach fleet-tenancy-api's authenticated surface, using
	TENANCY_API_KEY as X-Tenancy-Key and the tenant's own developer license as
	the Authorization bearer token.

	-tenant-id ID  the tenant to authenticate as and ask about
	-all           run once per tenant that holds a client id (each is a
	               separate developer license, so each can fail separately)
	-wallet 0x...  the wallet to ask about; defaults to the zero address, which
	               is a member of nothing and so exercises all three layers
	               while returning a predictable via=none

	Reading the result:

	  via=direct/none         all three layers passed; the service answered
	  trusted-caller-key      TENANCY_API_KEY is wrong or absent (layer 1)
	  developer-license-jwt   key accepted; the JWT is bad, or its client id is
	                          registered to no tenant (layer 2)
	  caller-scope            both credentials fine; this tenant may not ask
	                          about that tenant (layer 3)

	A via=none answer is a success, not a failure — the wallet simply has no
	access. Only a non-2xx is a failure.
  `
}

func (p *tenancyCheckCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "tenant to authenticate as and ask about")
	f.StringVar(&p.wallet, "wallet", zeroWallet, "wallet to ask about")
	f.BoolVar(&p.all, "all", false, "check every tenant holding a client id")
}

// zeroWallet is a syntactically valid address that is a member of nothing, so
// the check needs no knowledge of who belongs where. The service answers
// via=none for it, which still proves every layer was passed.
const zeroWallet = "0x0000000000000000000000000000000000000000"

func (p *tenancyCheckCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if p.tenantID == "" && !p.all {
		p.logger.Error().Msg("one of -tenant-id or -all is required")
		return subcommands.ExitUsageError
	}

	// Report the configuration before dialling: an empty key produces a 401
	// that looks exactly like a wrong one, and this is the cheapest place to
	// tell them apart.
	p.logger.Info().
		Str("url", p.settings.TenancyAPIURL.String()).
		Bool("key_set", p.settings.TenancyAPIKey != "").
		Msg("tenancy api configuration")

	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	tenancyAPI := gateway.NewTenancyAPI(p.logger, &p.settings, authProvider)

	tenantIDs, err := p.resolveTenantIDs(ctx)
	if err != nil {
		p.logger.Err(err).Msg("resolve tenants")
		return subcommands.ExitFailure
	}

	failures := 0
	for _, id := range tenantIDs {
		// Credentials are decrypted here, so a tenant whose secret does not
		// decrypt fails before any network call — worth distinguishing from a
		// rejection by the service.
		tenant, terr := tenantSvc.GetTenantByID(ctx, id)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", id).Msg("load tenant credentials")
			failures++
			continue
		}

		res, aerr := tenancyAPI.Authz(ctx, *tenant, p.wallet)
		if aerr != nil {
			failures++
			log := p.logger.Error().Str("tenant_id", id).Str("tenant", tenant.Name)

			var tErr *gateway.TenancyError
			if errors.As(aerr, &tErr) {
				log = log.Int("status", tErr.StatusCode).
					Str("layer", string(tErr.Layer)).
					Str("service_message", tErr.Message)
			} else {
				log = log.Err(aerr)
			}
			log.Msg("tenancy check FAILED")
			continue
		}

		p.logger.Info().
			Str("tenant_id", id).
			Str("tenant", tenant.Name).
			Str("via", res.Via).
			Bool("member", res.Member).
			Str("role", res.Role).
			Strs("permissions", res.Permissions).
			Bool("unrestricted", res.Unrestricted()).
			Int("scope_group_ids", len(res.ScopeGroupIDs)).
			Str("tenant_status", res.TenantStatus).
			Int("cache_ttl_seconds", res.CacheTTLSeconds).
			Msg("tenancy check ok — all three layers passed")

		if body, jerr := json.MarshalIndent(res, "", "  "); jerr == nil {
			_, _ = fmt.Fprintln(os.Stdout, string(body))
		}
	}

	p.logger.Info().Int("checked", len(tenantIDs)).Int("failed", failures).Msg("tenancy check complete")
	if failures > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// resolveTenantIDs returns the tenants to check: the one named, or every tenant
// holding a client id. Tenants without one have no developer license to
// authenticate with, so including them would report a failure that says nothing
// about the wiring.
func (p *tenancyCheckCmd) resolveTenantIDs(ctx context.Context) ([]string, error) {
	if !p.all {
		return []string{p.tenantID}, nil
	}

	// The column is nullable and the backfill also observed empty strings, so
	// both have to be excluded — neither is a usable developer license.
	tenants, err := dbmodels.Tenants(
		dbmodels.TenantWhere.DimoClientID.IsNotNull(),
		dbmodels.TenantWhere.DimoClientID.NEQ(null.StringFrom("")),
		qm.OrderBy(dbmodels.TenantColumns.Name),
	).All(ctx, p.pdb.DBS().Reader)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}

	ids := make([]string, 0, len(tenants))
	for _, t := range tenants {
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		return nil, errors.New("no tenants hold a client id")
	}
	return ids, nil
}
