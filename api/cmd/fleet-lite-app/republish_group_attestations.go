package main

import (
	"context"
	"flag"
	"sync"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/subcommands"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
)

// republishGroupAttestationsCmd is the "push" half of the group sync: it
// publishes fleet-lite's OWN dimo.document.vehicle.groups CloudEvent for every
// vehicle that currently belongs to at least one group.
//
// WHY THIS EXISTS, and why it is a correctness gate rather than cleanup:
//
// fleet-lite's production group structure was never published by fleet-lite. As
// measured on 2026-08-05, 0 of 287 grouped vehicles had ever been edited
// locally, so AttestVehicleGroups had never fired for any of them — every group
// on screen came from kaufmann's CloudEvents, adopted by ensureGroup because
// desiredGroups unions every producer.
//
// That is fine only while DROP_FOREIGN_TENANT_GROUPS is false. The moment
// tenant-matching is enforced, kaufmann's assertions stop counting as `desired`
// for a fleet-lite tenant, and reconcile removes any membership missing from
// `desired` whenever removalAllowed is open — which it is for all 287, since
// none has groups_updated_at set. Enforcing without republishing first would
// delete 370 of 378 memberships.
//
// So this command writes fleet-lite's own assertion of what it already believes,
// making that belief survive the switch. Run it, verify it, and only then flip
// the flag — never in the same release. See
// docs/operator-tenancy/07-r1-group-id-migration.md §5 and §6.
//
// Scope: vehicles with at least one membership. A vehicle in no groups has no
// membership to lose, so publishing an empty CE for it would be a signature and
// a network round trip that protects nothing.
type republishGroupAttestationsCmd struct {
	logger   zerolog.Logger
	settings config.Settings
	pdb      db.Store

	tenantID    string
	tokenID     int64
	dryRun      bool
	concurrency int
}

func (*republishGroupAttestationsCmd) Name() string { return "republish-group-attestations" }
func (*republishGroupAttestationsCmd) Synopsis() string {
	return "publish fleet-lite's own group-membership attestation for every grouped vehicle"
}
func (*republishGroupAttestationsCmd) Usage() string {
	return `republish-group-attestations [-tenant-id ID] [-token-id N] [-concurrency N] [-dry-run]:
	Publishes one signed dimo.document.vehicle.groups CloudEvent per vehicle that
	belongs to at least one group, asserting that vehicle's current membership
	under this tenant's own developer license.

	This must run — and be verified — before DROP_FOREIGN_TENANT_GROUPS is
	enabled. Until it does, fleet-lite has published nothing of its own and
	enforcing tenant-matching would remove memberships that only exist because
	another producer asserted them.

	-dry-run reports exactly what would be published, per tenant and in total,
	without signing or submitting anything. Reconcile its count against:

	  SELECT count(DISTINCT (tenant_id, token_id)) FROM vehicle_fleet_groups;

	-concurrency caps in-flight publishes (default 4). Each one is a signature
	plus a submit call, so this is the knob that keeps a full run from becoming a
	fan-out spike.
  `
}

func (p *republishGroupAttestationsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.tenantID, "tenant-id", "", "only this tenant (default: all tenants)")
	f.Int64Var(&p.tokenID, "token-id", 0, "only this vehicle token id")
	f.BoolVar(&p.dryRun, "dry-run", false, "report what would be published without signing or submitting")
	f.IntVar(&p.concurrency, "concurrency", 4, "maximum concurrent publishes")
}

// vehicleRef is one (tenant, vehicle) pair that needs an attestation.
type vehicleRef struct {
	TenantID string `boil:"tenant_id"`
	TokenID  int64  `boil:"token_id"`
}

// groupByTenant buckets the flat (tenant, vehicle) list by tenant, returning the
// buckets and the tenant order to walk them in.
//
// Grouping is what keeps each tenant resolved — and its signing key decrypted —
// exactly once per run rather than once per vehicle. The returned order is
// first-appearance, so with the query's ORDER BY it is deterministic: two runs
// over unchanged data process tenants in the same sequence, which makes a
// partial run's logs comparable to the run that follows it.
func groupByTenant(refs []vehicleRef) (map[string][]int64, []string) {
	byTenant := make(map[string][]int64, len(refs))
	order := make([]string, 0, len(refs))
	for _, r := range refs {
		if _, seen := byTenant[r.TenantID]; !seen {
			order = append(order, r.TenantID)
		}
		byTenant[r.TenantID] = append(byTenant[r.TenantID], r.TokenID)
	}
	return byTenant, order
}

func (p *republishGroupAttestationsCmd) Execute(ctx context.Context, _ *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if p.concurrency < 1 {
		p.concurrency = 1
	}

	p.pdb = db.NewDbConnectionFromSettings(ctx, &p.settings.DB, true)
	p.pdb.WaitForDB(p.logger)

	identityService := gateway.NewIdentityAPIService(p.logger, &p.settings)
	tenantSvc := service.NewTenantService(&p.logger, &p.pdb, &p.settings, identityService)
	authProvider := gateway.NewDimoAuthProvider(p.logger, &p.settings)
	attestSvc := service.NewAttestService(p.logger, &p.settings, authProvider)
	groupSvc := service.NewFleetGroupService(&p.logger, &p.pdb)

	// Every (tenant, vehicle) with at least one membership. DISTINCT because
	// vehicle_fleet_groups holds one row per group, and we publish one CE per
	// vehicle carrying all of them.
	//
	// tenant_id is uuid, not text: the ::text cast is what lets the "unset means
	// all" empty-string sentinel compare against it at all. Without it Postgres
	// pins $1 to text from `$1 = ''` and then rejects `uuid = text`.
	q := `SELECT DISTINCT vfg.tenant_id::text AS tenant_id, vfg.token_id
	        FROM vehicle_fleet_groups vfg
	       WHERE ($1 = '' OR vfg.tenant_id::text = $1)
	         AND ($2::bigint = 0 OR vfg.token_id = $2::bigint)
	       ORDER BY 1, 2`
	var refs []vehicleRef
	if err := queries.Raw(q, p.tenantID, p.tokenID).Bind(ctx, p.pdb.DBS().Reader, &refs); err != nil {
		p.logger.Err(err).Msg("list grouped vehicles")
		return subcommands.ExitFailure
	}
	if len(refs) == 0 {
		p.logger.Warn().Msg("no grouped vehicles matched — nothing to republish")
		return subcommands.ExitSuccess
	}

	byTenant, order := groupByTenant(refs)

	p.logger.Info().Int("vehicles", len(refs)).Int("tenants", len(order)).
		Int("concurrency", p.concurrency).Bool("dry_run", p.dryRun).
		Msg("republishing group attestations")

	var published, emptyGroups, failed, skippedVehicles int
	for _, tid := range order {
		tokenIDs := byTenant[tid]

		tenant, terr := tenantSvc.GetTenantByID(ctx, tid)
		if terr != nil {
			p.logger.Err(terr).Str("tenant_id", tid).Int("vehicles", len(tokenIDs)).
				Msg("load tenant — skipping, its vehicles stay unpublished")
			skippedVehicles += len(tokenIDs)
			continue
		}
		// Both are required to sign and submit. Without them every publish for
		// this tenant would fail one at a time; fail the tenant once instead, and
		// say how many vehicles it costs — each is a vehicle the foreign-drop
		// would later strip.
		if tenant.ClientID == "" || !common.IsHexAddress(tenant.ClientID) {
			p.logger.Warn().Str("tenant_id", tid).Int("vehicles", len(tokenIDs)).
				Msg("tenant has no DIMO client id — skipping, its vehicles stay unpublished")
			skippedVehicles += len(tokenIDs)
			continue
		}
		if tenant.DIMOPrivateKey == "" {
			p.logger.Warn().Str("tenant_id", tid).Int("vehicles", len(tokenIDs)).
				Msg("tenant has no signing key — skipping, its vehicles stay unpublished")
			skippedVehicles += len(tokenIDs)
			continue
		}

		tPublished, tEmpty, tFailed := p.republishTenant(ctx, attestSvc, groupSvc, *tenant, tokenIDs)
		published += tPublished
		emptyGroups += tEmpty
		failed += tFailed

		p.logger.Info().Str("tenant_id", tid).Str("tenant", tenant.Name).
			Int("published", tPublished).Int("empty", tEmpty).Int("failed", tFailed).
			Bool("dry_run", p.dryRun).Msg("tenant complete")
	}

	ev := p.logger.Info()
	if failed > 0 || skippedVehicles > 0 {
		ev = p.logger.Error()
	}
	ev.Int("published", published).Int("failed", failed).
		Int("skipped_vehicles", skippedVehicles).Int("empty_groups", emptyGroups).
		Int("total_vehicles", len(refs)).Bool("dry_run", p.dryRun).
		Msg("republish-group-attestations complete")

	// A partial run is not a success: every unpublished vehicle is one the
	// foreign-drop would strip, and the operator needs a non-zero exit to notice.
	if failed > 0 || skippedVehicles > 0 {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// republishTenant publishes one tenant's vehicles with a bounded worker pool.
func (p *republishGroupAttestationsCmd) republishTenant(
	ctx context.Context,
	attestSvc service.AttestService,
	groupSvc *service.FleetGroupService,
	tenant models.Tenant,
	tokenIDs []int64,
) (published, empty, failed int) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.concurrency)

	for _, tokenID := range tokenIDs {
		select {
		case <-ctx.Done():
			mu.Lock()
			failed += 1
			mu.Unlock()
			continue
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(tid int64) {
			defer wg.Done()
			defer func() { <-sem }()

			groups, err := groupSvc.VehicleGroups(ctx, tenant.ID, tid)
			if err != nil {
				p.logger.Err(err).Str("tenant_id", tenant.ID).Int64("token_id", tid).
					Msg("load vehicle groups")
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			// The query selected only vehicles with memberships, so an empty
			// result here means the rows changed under us. Publishing "in no
			// groups" on that basis could retract a real membership, so don't.
			if len(groups) == 0 {
				p.logger.Warn().Str("tenant_id", tenant.ID).Int64("token_id", tid).
					Msg("vehicle has no groups at publish time — skipping rather than asserting empty")
				mu.Lock()
				empty++
				mu.Unlock()
				return
			}

			if p.dryRun {
				p.logger.Info().Str("tenant_id", tenant.ID).Int64("token_id", tid).
					Int("groups", len(groups)).Bool("dry_run", true).
					Msg("would publish vehicle groups attestation")
				mu.Lock()
				published++
				mu.Unlock()
				return
			}

			if err := p.attestWithRetry(ctx, attestSvc, tenant, tid, groups); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			mu.Lock()
			published++
			mu.Unlock()
		}(tokenID)
	}

	wg.Wait()
	return published, empty, failed
}

// attestWithRetry publishes one vehicle's groups with bounded retry. Unlike the
// write-path helper in FleetGroupsController, this returns the final error: a
// missed vehicle here is one the foreign-drop would later strip, so the caller
// has to be able to count it.
func (p *republishGroupAttestationsCmd) attestWithRetry(
	ctx context.Context,
	attestSvc service.AttestService,
	tenant models.Tenant,
	tokenID int64,
	groups []service.GroupRef,
) error {
	const maxAttempts = 3
	backoff := time.Second
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		eventID, err := attestSvc.AttestVehicleGroups(tenant, uint64(tokenID), groups)
		if err == nil {
			p.logger.Info().Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
				Str("event_id", eventID).Int("groups", len(groups)).
				Msg("published vehicle groups attestation")
			return nil
		}
		lastErr = err
		p.logger.Warn().Err(err).Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
			Int("attempt", attempt).Msg("publish vehicle groups attestation failed")
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	p.logger.Error().Err(lastErr).Str("tenant_id", tenant.ID).Int64("token_id", tokenID).
		Msg("gave up publishing vehicle groups attestation after retries")
	return lastErr
}
