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

	// AttestedAt is the time of the CloudEvent this reference was read from.
	// Not part of the wire document — it is stamped by the reader.
	//
	// It exists because group metadata is carried redundantly on every member
	// vehicle's attestation, so two vehicles in the same group can disagree
	// about its name: one attested before a rename, one after. Without a
	// timestamp there is no way to tell which is current, and the last vehicle
	// processed wins — which is to say the name is decided by iteration order.
	AttestedAt time.Time `json:"-"`
}

// RemoteFleetGroup is one fleet group as fleet-tenancy-api serves it from
// GET /v1/tenants/{id}/vehicle-groups: the group plus its full member set.
// Keep it in step with that service's models.FleetGroupVehicles.
type RemoteFleetGroup struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenantId"`
	Name         string    `json:"name"`
	Color        string    `json:"color"`
	VehicleCount int       `json:"vehicleCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	TokenIDs     []int64   `json:"tokenIds"`
}

// RemoteActiveMemberships is the gate read from
// GET /v1/tenants/{id}/active-vehicle-memberships: whether membership
// enforcement is on for this tenant, and the token ids currently paid for.
// Both in one response, deliberately — two calls could straddle a toggle.
// Keep it in step with that endpoint's envelope.
type RemoteActiveMemberships struct {
	Enforced bool    `json:"enforced"`
	TokenIDs []int64 `json:"tokenIds"`
}

// RemoteWalletTenant is one row of GET /v1/tenants?wallet=&surface=fleet_lite:
// a tenant the wallet holds a direct membership in, with the membership riding
// along. Keep it in step with that service's models.WalletTenant.
type RemoteWalletTenant struct {
	TenantID        string   `json:"tenantId"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	EntitlementMode string   `json:"entitlementMode"`
	Role            string   `json:"role"`
	Permissions     []string `json:"permissions"`
	// ScopeGroupIDs nil means unrestricted; an empty array means restricted to
	// nothing — the same three-valued encoding as the authz answer.
	ScopeGroupIDs []string `json:"scopeGroupIds"`
}

// RemoteTenantDetail is GET /v1/tenants/{id} — the fields this app reads. The
// service sends more (counts, external ref); decoding only what is consumed
// keeps the coupling narrow. Keep field names in step with models.Tenant there.
type RemoteTenantDetail struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	EntitlementMode  string `json:"entitlementMode"`
	FleetLiteEnabled bool   `json:"fleetLiteEnabled"`
}

// RemoteMintedToken is GET /v1/tenants/{id}/dimo-token: a developer JWT for
// the tenant's EFFECTIVE credential — the operator's license for a managed
// customer. ClientID names whose license the token is, which the entitlement
// sync needs to enumerate the operator's privileged set.
type RemoteMintedToken struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expiresAt"`
	ClientID           string    `json:"clientId"`
	CredentialTenantID string    `json:"credentialTenantId"`
}

// RemoteEntitlement is one row of GET /v1/tenants/{id}/vehicles: a vehicle an
// explicit-mode tenant may see. Token id and provenance only — metadata comes
// from identity-api. Keep it in step with that service's models.Entitlement.
type RemoteEntitlement struct {
	VehicleTokenID int64   `json:"vehicleTokenId"`
	Source         string  `json:"source"`
	SourceGroupID  *string `json:"sourceGroupId"`
}

// RemoteMember is one row of GET /v1/tenants/{id}/members — the shared
// membership record, which for an operator-managed tenant is the only member
// list there is. Keep it in step with that service's models.Member.
type RemoteMember struct {
	Wallet      string   `json:"wallet"`
	Email       *string  `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	// Same three-valued scope encoding as everywhere else.
	ScopeGroupIDs []string `json:"scopeGroupIds"`
	LastLoginAt   *string  `json:"lastLoginAt"`
}

// RemoteInvitation is one invitation as fleet-tenancy-api serves it. That
// service owns the records outright since P4. Keep this in step with its
// models.Invitation.
//
// The token never appears here, and must not: the plaintext exists only in
// the email that service sent. This shape is what the owner's screen shows.
type RemoteInvitation struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Status   string `json:"status"`

	InvitedBy     string  `json:"invitedBy,omitempty"`
	InviteeWallet *string `json:"inviteeWallet,omitempty"`
	// CreatedByTenantID marks an invitation the OPERATOR sent from the console
	// rather than one this tenant sent itself. Nil for everything this app
	// creates; the UI can distinguish the two.
	CreatedByTenantID *string `json:"createdByTenantId,omitempty"`

	// ScopeGroupIDs is the same three-valued encoding as everywhere else: nil
	// unrestricted, empty restricted to nothing. It becomes the membership's
	// scope verbatim when the invitation is accepted.
	ScopeGroupIDs []string `json:"scopeGroupIds"`

	EmailStatus       *string `json:"emailStatus,omitempty"`
	EmailStatusAt     *string `json:"emailStatusAt,omitempty"`
	EmailStatusDetail *string `json:"emailStatusDetail,omitempty"`

	CreatedAt  string  `json:"createdAt"`
	ExpiresAt  string  `json:"expiresAt"`
	AcceptedAt *string `json:"acceptedAt,omitempty"`

	// EmailSent rides on create/resend responses only: false means the record
	// was written but Postmark did not accept the message. A partial success,
	// not a failure — the invite is usable and can be resent.
	EmailSent *bool `json:"emailSent,omitempty"`
}

// RemoteMembership is one vehicle membership as fleet-tenancy-api serves it
// from GET /v1/tenants/{id}/vehicle-memberships. Token id only — VIN, plate
// and model are joined from this app's own vehicle list, which owns them.
// Keep it in step with that service's models.VehicleMembership.
type RemoteMembership struct {
	ID             string  `json:"id"`
	VehicleTokenID int64   `json:"vehicleTokenId"`
	TermMonths     int     `json:"termMonths"`
	StartsAt       string  `json:"startsAt"`
	ExpiresAt      string  `json:"expiresAt"`
	CanceledAt     *string `json:"canceledAt"`
	Status         string  `json:"status"`
}

// RemoteMembershipList is the display list for the memberships page, enforced
// flag riding along for the same straddle reason as RemoteActiveMemberships.
type RemoteMembershipList struct {
	Enforced    bool               `json:"enforced"`
	Memberships []RemoteMembership `json:"memberships"`
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
	// CanShare reports whether this vehicle can be shared with another wallet
	// without its owner's passkey — i.e. whether the owner's kernel account
	// registered the operator's signer, resolved by fleet-tenancy-api against
	// accounts-api.
	//
	// A display gate only. The share endpoint re-checks it, and the worker
	// re-checks it again before spending gas, so a stale true here costs a
	// clear error rather than an unauthorized grant. It is omitted when false
	// so the vehicle list does not grow a field for every vehicle that cannot
	// be shared.
	CanShare bool `json:"canShare,omitempty"`
	// IsFavorite reflects whether the current tenant has starred this vehicle.
	// Populated by VehicleService when assembling responses — it isn't part of
	// the identity-api shape and is never present in the stored `raw` JSON.
	IsFavorite bool `json:"isFavorite"`
	// LicensePlate is cached from the vehicle's latest registration attestation
	// (see LicensePlateSyncService). Like IsFavorite it isn't part of the
	// identity-api shape, so VehicleService sets it from the DB column after
	// reconstructing the vehicle from `raw`. Empty when no plate is known.
	LicensePlate string `json:"licensePlate,omitempty"`
	// VIN is cached from the same registration attestation as LicensePlate (see
	// LicensePlateSyncService). Not part of the identity-api shape; VehicleService
	// sets it from the DB column. Empty when no VIN is known.
	VIN string `json:"vin,omitempty"`
	// LastLat/LastLon/LastSeen are the display cache of the vehicle's most recent
	// GPS fix, written through by the telemetry fan-out (see
	// TelemetryAPIService.FleetLocations + VehicleService.UpsertLastLocations).
	// They let the map paint markers instantly from the DB on first load and the
	// list show a "last seen" relative time; the live fan-out then reconciles
	// them. nil when no fix has ever been fetched (no permission / no data yet).
	LastLat  *float64   `json:"lastLat,omitempty"`
	LastLon  *float64   `json:"lastLon,omitempty"`
	LastSeen *time.Time `json:"lastSeen,omitempty"`
	// LocationPulledAt is when we last fetched this vehicle's location from
	// telemetry-api (a real fan-out query, not a cache serve) — distinct from
	// LastSeen (the GPS fix time). The frontend uses it to skip re-pulling a
	// vehicle fetched within the freshness window. nil = never pulled.
	LocationPulledAt *time.Time `json:"locationPulledAt,omitempty"`
	// Groups the vehicle belongs to, for the fleet-overview map/list filter.
	// Always a (possibly empty) slice in the /vehicles response.
	Groups []GroupRef `json:"groups"`
	// MetadataPending marks a vehicle that is in the tenant's resolved set —
	// entitled, membered and in scope — but has no local metadata row yet, so
	// everything except its token id is unknown. It appears anyway, because the
	// set is the authoritative answer to "which vehicles are yours" and dropping
	// a token for want of a cached row is what turned a stale cache into an
	// empty fleet on 2026-08-19.
	//
	// Expect it briefly after an operator grants an entitlement and before the
	// next sync-vehicles run. The client should render a placeholder rather than
	// a blank card. Omitted when false, so a fully-cached fleet is unchanged on
	// the wire.
	MetadataPending bool `json:"metadataPending,omitempty"`
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
