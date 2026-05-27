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

// Vehicle is the slim view of an identity-api vehicle node that fleet-lite-app cares about.
type Vehicle struct {
	ID                string             `json:"id"`
	TokenID           int64              `json:"tokenId"`
	MintedAt          *time.Time         `json:"mintedAt"`
	Owner             string             `json:"owner"`
	Definition        Definition         `json:"definition"`
	SyntheticDevice   SyntheticDevice    `json:"syntheticDevice"`
	AftermarketDevice *AftermarketDevice `json:"aftermarketDevice,omitempty"`
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
