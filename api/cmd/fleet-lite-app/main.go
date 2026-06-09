package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/DIMO-Network/fleet-lite-app/internal/app"
	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	ssettings "github.com/DIMO-Network/shared/pkg/settings"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/google/subcommands"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

// CommitHash is set at build time via -ldflags "-X main.CommitHash=$(COMMIT)".
var CommitHash = "dev"

const LocalDevDomain = "local-fleet-lite.dimo.org"

func main() {
	logger := zerolog.New(os.Stdout).Level(zerolog.InfoLevel).With().
		Timestamp().
		Str("app", "fleet-lite-app").
		Logger()

	settings, err := ssettings.LoadConfig[config.Settings]("settings.yaml")
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load settings")
	}

	if settings.LogLevel != "" {
		lvl, err := zerolog.ParseLevel(settings.LogLevel)
		if err != nil {
			logger.Fatal().Err(err).Msg("Couldn't parse log level setting.")
		}
		zerolog.SetGlobalLevel(lvl)
		logger = logger.Level(lvl)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Subcommands (migrate, etc.) — invoked when extra positional args are present.
	if len(os.Args) > 1 {
		subcommands.Register(subcommands.HelpCommand(), "")
		subcommands.Register(subcommands.FlagsCommand(), "")
		subcommands.Register(subcommands.CommandsCommand(), "")
		subcommands.Register(&migrateDBCmd{logger: logger, settings: settings}, "database")
		subcommands.Register(&importGroupAttestationsCmd{logger: logger, settings: settings}, "attestations")
		flag.Parse()
		os.Exit(int(subcommands.Execute(ctx)))
	}

	// Connect to database — required for /health, even though no controllers
	// hit a table yet. The skeleton is wired so that the first DB-backed
	// feature has somewhere to land without restructuring boot.
	pdb := db.NewDbConnectionFromSettings(ctx, &settings.DB, true)
	pdb.WaitForDB(logger)

	identityService := gateway.NewIdentityAPIService(logger, &settings)
	authProvider := gateway.NewDimoAuthProvider(logger, &settings)
	extractAPI := service.NewExtractAPIService(logger, &settings, authProvider)
	attestSvc := service.NewAttestService(logger, &settings, authProvider)
	fetchAPI := gateway.NewFetchAPI(logger, &settings, authProvider)
	telemetryAPI := service.NewTelemetryAPIService(logger, &settings, authProvider)
	tenantSvc := service.NewTenantService(&logger, &pdb, &settings, identityService)
	vehicleSvc := service.NewVehicleService(&logger, &pdb, identityService)
	groupSvc := service.NewFleetGroupService(&logger, &pdb)

	monApp := createMonitoringServer()
	group, gCtx := errgroup.WithContext(ctx)

	webAPI := app.App(&settings, &logger, CommitHash, &pdb, identityService,
		authProvider, extractAPI, attestSvc, fetchAPI, telemetryAPI,
		tenantSvc, vehicleSvc, groupSvc,
	)

	logger.Info().Int("port", settings.MonitoringPort).Msg("Starting monitoring server")
	runFiber(gCtx, monApp, ":"+strconv.Itoa(settings.MonitoringPort), group, false)

	logger.Info().Int("port", settings.APIPort).Msg("Starting web server")
	runFiber(gCtx, webAPI, ":"+strconv.Itoa(settings.APIPort), group, settings.UseDevCerts)

	if err := group.Wait(); err != nil {
		logger.Fatal().Err(err).Msg("Server failed.")
	}
	logger.Info().Msg("Server stopped.")
}

func runFiber(ctx context.Context, fiberApp *fiber.App, addr string, group *errgroup.Group, useTLS bool) {
	group.Go(func() error {
		if useTLS {
			if err := fiberApp.ListenTLS(LocalDevDomain+addr, "../web/.mkcert/cert.pem", "../web/.mkcert/dev.pem"); err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}
		} else {
			if err := fiberApp.Listen(addr); err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}
		}
		return nil
	})
	group.Go(func() error {
		<-ctx.Done()
		if err := fiberApp.Shutdown(); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
		return nil
	})
}

func createMonitoringServer() *fiber.App {
	monApp := fiber.New(fiber.Config{DisableStartupMessage: true})
	monApp.Get("/", func(_ *fiber.Ctx) error { return nil })
	monApp.Get("/metrics", adaptor.HTTPHandler(promhttp.Handler()))
	return monApp
}
