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
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
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
	vehiclesCtrl := controllers.NewVehiclesController(settings, logger, identity)
	settingsCtrl := controllers.NewSettingsController(settings, logger)

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

	authApp.Get("/vehicles", vehiclesCtrl.GetVehicles)
	authApp.Get("/vehicles/:tokenID", vehiclesCtrl.GetVehicle)

	// Telemetry (vehicle-details charts)
	telemetryCtrl := controllers.NewTelemetryController(logger, settings, identity, telemetryAPI)
	authApp.Get("/telemetry/:tokenID/latest", telemetryCtrl.GetLatest)
	authApp.Get("/telemetry/:tokenID/timeseries", telemetryCtrl.GetTimeSeries)

	// Glovebox / documents
	documentsCtrl := controllers.NewDocumentsController(
		logger, settings, identity, authProvider, extractAPI, attestSvc, fetchAPI,
	)
	authApp.Post("/documents/extract", documentsCtrl.ExtractDocument)
	authApp.Get("/documents/vin-lookup", documentsCtrl.LookupVIN)
	authApp.Post("/documents/attest", documentsCtrl.AttestDocument)
	authApp.Get("/documents/list", documentsCtrl.ListDocuments)
	authApp.Get("/documents/download", documentsCtrl.DownloadDocument)
	authApp.Delete("/documents/:id", documentsCtrl.DeleteDocument)

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
