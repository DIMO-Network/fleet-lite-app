package controllers

import (
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/valyala/fasthttp"
)

// A tenant's vehicle set is whatever its dev license is SACD-privileged on, so
// it contains vehicles owned by other people. The platform's sharing contract
// is read + append for those — never delete. These tests pin that boundary.

func ctxWithWallet(app *fiber.App, wallet string) *fiber.Ctx {
	c := app.AcquireCtx(&fasthttp.RequestCtx{})
	if wallet != "" {
		c.Locals("user", &jwt.Token{Claims: jwt.MapClaims{"ethereum_address": wallet}})
	}
	return c
}

func TestRequireVehicleOwner(t *testing.T) {
	const owner = "0x264BC41755BA9F5a00DCEC07F96cB14339dBD970"
	const other = "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB"

	tests := []struct {
		name       string
		wallet     string
		vehicle    *models.Vehicle
		wantAllow  bool
		wantStatus int
	}{
		{
			name:      "owner may delete",
			wallet:    owner,
			vehicle:   &models.Vehicle{Owner: owner},
			wantAllow: true,
		},
		{
			// The JWT and the roster can disagree on casing; authority must not.
			name:      "owner may delete regardless of address casing",
			wallet:    "0x264bc41755ba9f5a00dcec07f96cb14339dbd970",
			vehicle:   &models.Vehicle{Owner: owner},
			wantAllow: true,
		},
		{
			// The share case: vehicle is in the fleet, caller is not the owner.
			name:       "sharee may not delete",
			wallet:     other,
			vehicle:    &models.Vehicle{Owner: owner},
			wantStatus: fiber.StatusForbidden,
		},
		{
			// Refuse rather than guess — a wrong allow destroys someone's file.
			name:       "unknown owner refuses",
			wallet:     owner,
			vehicle:    &models.Vehicle{Owner: ""},
			wantStatus: fiber.StatusForbidden,
		},
		{
			name:       "missing JWT refuses",
			wallet:     "",
			vehicle:    &models.Vehicle{Owner: owner},
			wantStatus: fiber.StatusUnauthorized,
		},
	}

	app := fiber.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctxWithWallet(app, tt.wallet)
			defer app.ReleaseCtx(c)

			err := requireVehicleOwner(c, tt.vehicle)
			if tt.wantAllow {
				if err != nil {
					t.Fatalf("expected the owner to be allowed, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected a refusal, got nil — a non-owner could delete a shared vehicle's documents")
			}
			fe, ok := err.(*fiber.Error)
			if !ok {
				t.Fatalf("expected a fiber.Error, got %T", err)
			}
			if fe.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d (%s)", tt.wantStatus, fe.Code, fe.Message)
			}
		})
	}
}

func TestIsVehicleOwner(t *testing.T) {
	const owner = "0x264BC41755BA9F5a00DCEC07F96cB14339dBD970"
	app := fiber.New()

	cases := []struct {
		name    string
		wallet  string
		vehicle *models.Vehicle
		want    bool
	}{
		{"owner", owner, &models.Vehicle{Owner: owner}, true},
		{"sharee", "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB", &models.Vehicle{Owner: owner}, false},
		{"unknown owner is not ownership", owner, &models.Vehicle{Owner: ""}, false},
		{"nil vehicle is not ownership", owner, nil, false},
		{"no JWT is not ownership", "", &models.Vehicle{Owner: owner}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := ctxWithWallet(app, tt.wallet)
			defer app.ReleaseCtx(c)
			if got := isVehicleOwner(c, tt.vehicle); got != tt.want {
				t.Fatalf("isVehicleOwner = %v, want %v", got, tt.want)
			}
		})
	}
}

// weAttested decides whether our tombstone would actually suppress a document
// in fetch-api, which matches on (source, voids_id). Getting it wrong in the
// permissive direction offers a delete that silently does nothing outside this
// app — so every uncertain case must resolve to false.
func TestWeAttested(t *testing.T) {
	const ours = "0x51dacC165f1306Abfbf0a6312ec96E13AAA826DB"
	const theirs = "0x264BC41755BA9F5a00DCEC07F96cB14339dBD970"

	cases := []struct {
		name       string
		ourLicense string
		source     string
		want       bool
	}{
		{"our own document", ours, ours, true},
		{"casing does not change authorship", ours, "0x51dacc165f1306abfbf0a6312ec96e13aaa826db", true},
		{"another app's document", ours, theirs, false},
		// Managed tenants carry no ClientID of their own; an unresolved
		// license must not make every document look like ours.
		{"unresolved license claims nothing", "", ours, false},
		{"unknown source claims nothing", ours, "", false},
		{"both unknown claims nothing", "", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := weAttested(tt.ourLicense, tt.source); got != tt.want {
				t.Fatalf("weAttested(%q, %q) = %v, want %v", tt.ourLicense, tt.source, got, tt.want)
			}
		})
	}
}
