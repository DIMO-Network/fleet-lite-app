package models

import "time"

// GraphQLRequest wraps a single-query GraphQL POST body.
type GraphQLRequest struct {
	Query string `json:"query"`
}

// GraphQlData is the standard GraphQL response envelope (`{ "data": T }`).
type GraphQlData[T any] struct {
	Data T `json:"data"`
}

// GroupRef is the slim, public view of a fleet group a vehicle belongs to. It is
// the shape embedded both in the /vehicles response (for the map/list filter)
// and in the per-vehicle group-membership attestation `data.groups`.
type GroupRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Vehicle is the slim view of an identity-api vehicle node that fleet-lite-app cares about.
type Vehicle struct {
	ID                string             `json:"id"`
	TokenID           int64              `json:"tokenId"`
	MintedAt          *time.Time         `json:"mintedAt"`
	Owner             string             `json:"owner"`
	Definition        Definition         `json:"definition"`
	SyntheticDevice   SyntheticDevice    `json:"syntheticDevice"`
	AftermarketDevice *AftermarketDevice `json:"aftermarketDevice,omitempty"`
	// IsFavorite reflects whether the current tenant has starred this vehicle.
	// Populated by VehicleService when assembling responses — it isn't part of
	// the identity-api shape and is never present in the stored `raw` JSON.
	IsFavorite bool `json:"isFavorite"`
	// LicensePlate is cached from the vehicle's latest registration attestation
	// (see LicensePlateSyncService). Like IsFavorite it isn't part of the
	// identity-api shape, so VehicleService sets it from the DB column after
	// reconstructing the vehicle from `raw`. Empty when no plate is known.
	LicensePlate string `json:"licensePlate,omitempty"`
	// Groups the vehicle belongs to, for the fleet-overview map/list filter.
	// Always a (possibly empty) slice in the /vehicles response.
	Groups []GroupRef `json:"groups"`
}

type SyntheticDevice struct {
	ID       string `json:"id"`
	TokenID  int64  `json:"tokenId"`
	MintedAt string `json:"mintedAt"`
}

type AftermarketDevice struct {
	TokenID int64  `json:"tokenId"`
	Serial  string `json:"serial"`
	IMEI    string `json:"imei"`
}

type Definition struct {
	ID    string `json:"id"`
	Make  string `json:"make"`
	Model string `json:"model"`
	Year  int    `json:"year"`
}

type SingleVehicle struct {
	Vehicle Vehicle `json:"vehicle"`
}

type PageInfo struct {
	HasPreviousPage bool   `json:"hasPreviousPage"`
	HasNextPage     bool   `json:"hasNextPage"`
	StartCursor     string `json:"startCursor"`
	EndCursor       string `json:"endCursor"`
}

type PagedVehiclesNodes struct {
	Nodes    []Vehicle `json:"nodes"`
	PageInfo PageInfo  `json:"pageInfo"`
}

type PagedVehicles struct {
	VehicleNodes PagedVehiclesNodes `json:"vehicles"`
}

// Tenant is the in-memory view of a tenant with its DIMO developer credentials
// decrypted, used for all outbound DIMO data calls. Secrets are tagged `json:"-"`.
type Tenant struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ClientID        string `json:"clientId"`
	DIMOPrivateKey  string `json:"-"` // decrypted DIMO developer API key
	DIMORedirectURI string `json:"dimoRedirectUri,omitempty"`
}

// DeveloperLicense is the identity-api view of a DIMO developer license, used to
// resolve the redirect URI for a tenant's client ID during the auth challenge.
type DeveloperLicense struct {
	TokenID      int64        `json:"tokenId"`
	Owner        string       `json:"owner"`
	Alias        string       `json:"alias"`
	RedirectURIs RedirectURIs `json:"redirectURIs"`
}

type RedirectURIs struct {
	Edges []RedirectURIEdge `json:"edges"`
}

type RedirectURIEdge struct {
	Node RedirectURINode `json:"node"`
}

type RedirectURINode struct {
	URI string `json:"uri"`
}

type SingleDeveloperLicense struct {
	DeveloperLicense DeveloperLicense `json:"developerLicense"`
}
