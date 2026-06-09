package app

import (
	"errors"
	"os"
	"strconv"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/controllers"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/DIMO-Network/shared/pkg/middleware/metrics"
	jwtware "github.com/gofiber/contrib/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
)

var appCommitHash string

// App wires the fiber app together with middleware, routes, and static-file
// serving. It's the only function /cmd needs to call to get a working
// server.
func App(
	settings *config.Settings,
	logger *zerolog.Logger,
	commitHash string,
	pdb *db.Store,
	identity gateway.IdentityAPI,
	authProvider *gateway.DimoAuthProvider,
	extractAPI service.ExtractAPIService,
	attestSvc service.AttestService,
	fetchAPI *gateway.FetchAPI,
	telemetryAPI service.TelemetryAPIService,
	tenantSvc *service.TenantService,
	vehicleSvc *service.VehicleService,
	groupSvc *service.FleetGroupService,
) *fiber.App {
	appCommitHash = commitHash

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			return ErrorHandler(c, err, logger)
		},
		DisableStartupMessage: true,
		ReadBufferSize:        16000,
		BodyLimit:             25 * 1024 * 1024,
	})
	app.Use(metrics.HTTPMetricsMiddleware)
	app.Use(fiberrecover.New(fiberrecover.Config{
		EnableStackTrace: true,
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins:     settings.AllowedOrigins,
		AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS,PATCH",
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, Tenant-Id",
		AllowCredentials: true,
	}))

	// Serve the built frontend in production / when running the binary alone.
	app.Get("/", loadStaticIndex)
	staticConfig := fiber.Static{Compress: true, MaxAge: 0, Index: "index.html"}
	app.Static("/", "./dist", staticConfig)
	app.Static("/assets", "./dist/assets", staticConfig)

	// Health & version
	app.Get("/health", HealthCheck(pdb))
	app.Get("/version", getVersion)

	// Controllers
	identityCtrl := controllers.NewIdentityController(settings, logger)
	vehiclesCtrl := controllers.NewVehiclesController(settings, logger, vehicleSvc, groupSvc)
	fleetGroupsCtrl := controllers.NewFleetGroupsController(logger, groupSvc)
	settingsCtrl := controllers.NewSettingsController(settings, logger)
	tenantsCtrl := controllers.NewTenantsController(logger, settings, tenantSvc, vehicleSvc, identity, authProvider)

	// Public endpoints (no auth)
	app.Get("/public/settings", settingsCtrl.GetPublicSettings)
	app.Get("/identity/vehicle/:tokenID", identityCtrl.GetVehicleByTokenID)
	app.Get("/identity/definition/:id", identityCtrl.GetDefinitionByID)
	app.Get("/identity/owner/:owner", identityCtrl.GetOwnerBy0x)
	app.Post("/identity/proxy", identityCtrl.ProxyGraphQLQuery)

	// JWT auth middleware against DIMO JWKS
	jwtAuth := jwtware.New(jwtware.Config{
		JWKSetURLs: []string{settings.JwtKeySetURL.String()},
	})
	authApp := app.Group("", jwtAuth)

	// Tenant management (JWT only — these precede / manage tenant selection;
	// each handler authorizes against the :id path param itself).
	authApp.Get("/tenants", tenantsCtrl.GetTenants)
	authApp.Post("/tenants", tenantsCtrl.CreateTenant)
	authApp.Post("/tenants/:id/sync-vehicles", tenantsCtrl.SyncVehicles)
	authApp.Get("/tenants/:id/settings", tenantsCtrl.GetSettings)
	authApp.Put("/tenants/:id/settings", tenantsCtrl.UpdateSettings)
	authApp.Get("/tenants/:id/members", tenantsCtrl.GetMembers)
	authApp.Post("/tenants/:id/members", tenantsCtrl.AddMember)
	authApp.Delete("/tenants/:id/members/:wallet", tenantsCtrl.RemoveMember)

	// Tenant-scoped data routes (JWT + Tenant-Id header membership check).
	tenantApp := authApp.Group("", NewTenantMiddleware(tenantSvc, logger))

	tenantApp.Get("/vehicles", vehiclesCtrl.GetVehicles)
	tenantApp.Get("/vehicles/:tokenID", vehiclesCtrl.GetVehicle)
	tenantApp.Post("/vehicles/:tokenID/favorite", vehiclesCtrl.AddFavorite)
	tenantApp.Delete("/vehicles/:tokenID/favorite", vehiclesCtrl.RemoveFavorite)

	// Fleet groups (tenant-scoped CRUD + vehicle membership)
	tenantApp.Get("/fleet/groups", fleetGroupsCtrl.GetGroups)
	tenantApp.Post("/fleet/groups", fleetGroupsCtrl.CreateGroup)
	tenantApp.Get("/fleet/groups/:id", fleetGroupsCtrl.GetGroup)
	tenantApp.Patch("/fleet/groups/:id", fleetGroupsCtrl.UpdateGroup)
	tenantApp.Delete("/fleet/groups/:id", fleetGroupsCtrl.DeleteGroup)
	tenantApp.Post("/fleet/vehicles/:tokenID/group/:groupID", fleetGroupsCtrl.AddVehicleToGroup)
	tenantApp.Delete("/fleet/vehicles/:tokenID/group/:groupID", fleetGroupsCtrl.RemoveVehicleFromGroup)

	// Telemetry (vehicle-details charts)
	telemetryCtrl := controllers.NewTelemetryController(logger, settings, vehicleSvc, telemetryAPI)
	tenantApp.Get("/telemetry/locations", telemetryCtrl.GetFleetLocations)
	tenantApp.Get("/telemetry/:tokenID/latest", telemetryCtrl.GetLatest)
	tenantApp.Get("/telemetry/:tokenID/timeseries", telemetryCtrl.GetTimeSeries)

	// Glovebox / documents
	documentsCtrl := controllers.NewDocumentsController(
		logger, settings, vehicleSvc, authProvider, extractAPI, attestSvc, fetchAPI,
	)
	tenantApp.Post("/documents/extract", documentsCtrl.ExtractDocument)
	tenantApp.Get("/documents/vin-lookup", documentsCtrl.LookupVIN)
	tenantApp.Post("/documents/attest", documentsCtrl.AttestDocument)
	tenantApp.Get("/documents/list", documentsCtrl.ListDocuments)
	tenantApp.Get("/documents/download", documentsCtrl.DownloadDocument)
	tenantApp.Delete("/documents/:id", documentsCtrl.DeleteDocument)

	return app
}

func HealthCheck(pdb *db.Store) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pdb == nil {
			return c.JSON(fiber.Map{"status": "up", "db": "skipped"})
		}
		if err := pdb.DBS().Reader.Ping(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "error",
				"error":  "Database connection failed",
			})
		}
		return c.JSON(fiber.Map{"status": "up"})
	}
}

func getVersion(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"commit": appCommitHash})
}

func loadStaticIndex(ctx *fiber.Ctx) error {
	dat, err := os.ReadFile("dist/index.html")
	if err != nil {
		return err
	}
	ctx.Set("Content-Type", "text/html; charset=utf-8")
	return ctx.Status(fiber.StatusOK).Send(dat)
}

// ErrorHandler logs the recovered error and returns JSON instead of a plain string.
func ErrorHandler(c *fiber.Ctx, err error, logger *zerolog.Logger) error {
	code := fiber.StatusInternalServerError
	var e *fiber.Error
	if errors.As(err, &e) {
		code = e.Code
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	if code != fiber.StatusNotFound {
		logger.Err(err).
			Str("httpStatusCode", strconv.Itoa(code)).
			Str("httpMethod", c.Method()).
			Str("httpPath", c.Path()).
			Msg("caught an error from http request")
	}
	return c.Status(code).JSON(ErrorRes{Code: code, Message: err.Error()})
}

type ErrorRes struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
