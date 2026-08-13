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
	invitationSvc *service.InvitationService,
	tenancyAPI *gateway.TenancyAPI,
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
	geofenceSvc := service.NewGeofenceService(logger, pdb)
	// P5: every group-scoped read — the vehicle scope filter, a group-scoped
	// geofence, an invite's group list — resolves through the FleetGroupService
	// index instead of the local fleet_groups tables. It is a no-op while
	// GROUPS_FROM_TENANCY is off, which is the revert path.
	vehicleSvc.UseGroupIndex(groupSvc)
	geofenceSvc.UseGroupIndex(groupSvc)
	invitationSvc.UseGroupIndex(groupSvc)
	geofenceDetectionSvc := service.NewGeofenceDetectionService(logger, pdb, telemetryAPI, geofenceSvc)
	geofencesCtrl := controllers.NewGeofencesController(logger, geofenceSvc, attestSvc, geofenceDetectionSvc, vehicleSvc)
	settingsCtrl := controllers.NewSettingsController(settings, logger)
	tenantsCtrl := controllers.NewTenantsController(logger, settings, tenantSvc, vehicleSvc, identity, authProvider)
	invitationsCtrl := controllers.NewInvitationsController(logger, tenantSvc, invitationSvc)
	webhooksCtrl := controllers.NewWebhooksController(logger, settings, invitationSvc)
	userPrefsSvc := service.NewUserPrefsService(logger, pdb)
	userPrefsCtrl := controllers.NewUserPrefsController(logger, userPrefsSvc)

	// Public endpoints (no auth)
	app.Get("/public/settings", settingsCtrl.GetPublicSettings)
	// Postmark can't do DIMO JWTs — the webhook authenticates with basic auth
	// against POSTMARK_WEBHOOK_SECRET inside the handler.
	app.Post("/webhooks/postmark", webhooksCtrl.HandlePostmark)
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
	authApp.Put("/tenants/:id/members/:wallet/access", tenantsCtrl.UpdateMemberAccess)
	authApp.Post("/tenants/:id/login", tenantsCtrl.LoginTouch)

	// Per-user UI preferences (units, locale, …), keyed by the caller's wallet.
	// Wallet-global, not tenant-scoped — JWT only. See USER_PREFERENCES_PLAN.md.
	authApp.Get("/me/preferences", userPrefsCtrl.GetPreferences)
	authApp.Put("/me/preferences", userPrefsCtrl.PutPreferences)

	// Member invitations. Create/list/revoke/resend authorize against the :id
	// path tenant (owner-only except list). Accept is JWT-only and NOT
	// membership-gated — the invitee isn't a member yet; the token authorizes it
	// and resolves the tenant, so it carries no :id.
	authApp.Post("/tenants/:id/invitations", invitationsCtrl.CreateInvitation)
	authApp.Get("/tenants/:id/invitations", invitationsCtrl.ListInvitations)
	authApp.Delete("/tenants/:id/invitations/:invID", invitationsCtrl.RevokeInvitation)
	authApp.Post("/tenants/:id/invitations/:invID/resend", invitationsCtrl.ResendInvitation)
	authApp.Post("/invitations/accept", invitationsCtrl.AcceptInvitation)

	// Tenant-scoped data routes (JWT + Tenant-Id header membership check).
	tenantApp := authApp.Group("", NewTenantMiddleware(tenantSvc, tenancyAPI, logger))

	// The caller's own role + group scope for the current tenant (drives
	// role/scope-aware UI). See docs/GROUP_ACCESS_PLAN.md.
	tenantApp.Get("/me/access", tenantsCtrl.GetMyAccess)

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
	tenantApp.Post("/fleet/vehicles/:tokenID/groups/sync", fleetGroupsCtrl.SyncVehicleGroups)

	// Geofences (tenant-scoped CRUD + manual vehicle assignment). Definitions are
	// attested at the tenant client-id level in a later phase; see GEOFENCES_PLAN.md.
	tenantApp.Get("/fleet/geofences", geofencesCtrl.GetGeofences)
	tenantApp.Post("/fleet/geofences", geofencesCtrl.CreateGeofence)
	tenantApp.Get("/fleet/geofences/:id", geofencesCtrl.GetGeofence)
	tenantApp.Get("/fleet/geofences/:id/vehicles", geofencesCtrl.GetGeofenceVehicles)
	tenantApp.Patch("/fleet/geofences/:id", geofencesCtrl.UpdateGeofence)
	tenantApp.Delete("/fleet/geofences/:id", geofencesCtrl.DeleteGeofence)
	tenantApp.Post("/fleet/vehicles/:tokenID/geofence/:geofenceID", geofencesCtrl.AddVehicleToGeofence)
	tenantApp.Delete("/fleet/vehicles/:tokenID/geofence/:geofenceID", geofencesCtrl.RemoveVehicleFromGeofence)
	// Geofence event detection (Phase 2). Entry point 1: which geofences a
	// vehicle's telemetry crossed in a trip window — on-demand, cached.
	tenantApp.Get("/telemetry/:tokenID/trip-geofences", geofencesCtrl.GetTripGeofences)
	// Entry point 2: which vehicles passed through a geofence in a window. The
	// client first reads scan-targets (effective vehicles, capped), then pages
	// them through /passes in batches so results stream in progressively.
	tenantApp.Get("/fleet/geofences/:id/scan-targets", geofencesCtrl.GetGeofenceScanTargets)
	tenantApp.Get("/fleet/geofences/:id/passes", geofencesCtrl.GetGeofencePasses)

	// Telemetry (vehicle-details charts)
	telemetryCtrl := controllers.NewTelemetryController(logger, settings, vehicleSvc, telemetryAPI)
	tenantApp.Get("/telemetry/locations", telemetryCtrl.GetFleetLocations)
	tenantApp.Get("/telemetry/:tokenID/latest", telemetryCtrl.GetLatest)
	tenantApp.Get("/telemetry/:tokenID/timeseries", telemetryCtrl.GetTimeSeries)
	tenantApp.Get("/telemetry/:tokenID/segments", telemetryCtrl.GetSegments)
	tenantApp.Get("/telemetry/:tokenID/route", telemetryCtrl.GetTripRoute)
	tenantApp.Get("/telemetry/:tokenID/replay", telemetryCtrl.GetTripReplay)

	// Glovebox / documents
	plateSvc := service.NewLicensePlateSyncService(logger, pdb, fetchAPI, authProvider, telemetryAPI)
	documentsCtrl := controllers.NewDocumentsController(
		logger, settings, vehicleSvc, authProvider, extractAPI, attestSvc, fetchAPI, plateSvc,
	)
	tenantApp.Post("/documents/extract", documentsCtrl.ExtractDocument)
	tenantApp.Get("/documents/vin-lookup", documentsCtrl.LookupVIN)
	tenantApp.Post("/documents/attest", documentsCtrl.AttestDocument)
	tenantApp.Get("/documents/list", documentsCtrl.ListDocuments)
	tenantApp.Get("/documents/download", documentsCtrl.DownloadDocument)
	tenantApp.Delete("/documents/:id", documentsCtrl.DeleteDocument)

	// Total cost of ownership reporting (operating costs from Glovebox
	// documents + optional acquisition/depreciation settings per vehicle).
	tcoSvc := service.NewTCOService(logger, pdb, fetchAPI, authProvider, vehicleSvc, attestSvc)
	tcoCtrl := controllers.NewTCOController(logger, tcoSvc, vehicleSvc)
	tenantApp.Get("/tco/settings", tcoCtrl.GetSettings)
	tenantApp.Put("/tco/settings", tcoCtrl.PutSettings)
	tenantApp.Get("/tco/summary", tcoCtrl.GetSummary)
	tenantApp.Get("/tco/vehicle/:tokenId", tcoCtrl.GetVehicleDetail)
	tenantApp.Get("/tco/export.csv", tcoCtrl.ExportCSV)
	tenantApp.Put("/tco/vehicle/:tokenId/backfill/:documentId", tcoCtrl.BackfillAmount)

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
//
// Client errors log at warn, server errors at error. The level is the point:
// since authorization moved to fleet-tenancy-api, a 403 is the ordinary answer
// for a wallet that is not a member of the requested tenant, and a 401 is the
// ordinary answer to an expired session. Logging those at error level makes
// routine enforcement indistinguishable from the app being broken, and feeds
// any error-rate alerting built on this stream.
//
// The rejection is still recorded, at a level that says "this happened" rather
// than "something is wrong". 404 stays silent: an unrouted path is neither a
// fault nor worth a line per scan.
func ErrorHandler(c *fiber.Ctx, err error, logger *zerolog.Logger) error {
	code := fiber.StatusInternalServerError
	var e *fiber.Error
	if errors.As(err, &e) {
		code = e.Code
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	if code != fiber.StatusNotFound {
		ev := logger.Warn()
		if code >= fiber.StatusInternalServerError {
			ev = logger.Error()
		}
		ev.Err(err).
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
