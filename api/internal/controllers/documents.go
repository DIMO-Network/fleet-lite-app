package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// maxDownloadBytes caps a single presigned-blob read. Uploads are capped at
// 25 MB by the fiber body limit, so anything larger is a bad key, not a file.
const maxDownloadBytes = 32 << 20

type DocumentsController struct {
	logger       *zerolog.Logger
	settings     *config.Settings
	vehicleSvc   *service.VehicleService
	authProvider *gateway.DimoAuthProvider
	extractAPI   service.ExtractAPIService
	attestSvc    service.AttestService
	fetchAPI     *gateway.FetchAPI
	plateSvc     *service.LicensePlateSyncService
}

func NewDocumentsController(
	logger *zerolog.Logger,
	settings *config.Settings,
	vehicleSvc *service.VehicleService,
	authProvider *gateway.DimoAuthProvider,
	extractAPI service.ExtractAPIService,
	attestSvc service.AttestService,
	fetchAPI *gateway.FetchAPI,
	plateSvc *service.LicensePlateSyncService,
) *DocumentsController {
	return &DocumentsController{
		logger:       logger,
		settings:     settings,
		vehicleSvc:   vehicleSvc,
		authProvider: authProvider,
		extractAPI:   extractAPI,
		attestSvc:    attestSvc,
		fetchAPI:     fetchAPI,
		plateSvc:     plateSvc,
	}
}

// vehicleInTenant checks that the tokenID is one of the tenant's synced
// vehicles — and, for limited members, inside their allowed groups. It returns
// the vehicle when it is in scope, or the fiber error to send.
//
// It returns an error rather than a bool so an unresolvable group scope can
// surface as 503 instead of being flattened into "not part of this tenant".
// Either way the caller is refused: this never opens up on failure.
//
// In scope is NOT the same as owned. A tenant's vehicle set is whatever its
// dev license is SACD-privileged on, so it routinely contains vehicles owned
// by someone else. Reads and appends are fine on those; deletes are not —
// see requireVehicleOwner.
func (d *DocumentsController) vehicleInTenant(c *fiber.Ctx, tenant models.Tenant, tokenID uint64) (*models.Vehicle, error) {
	allowed, _ := GetAllowedGroups(c)
	vehicle, err := d.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed)
	if err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return nil, serr
		}
		return nil, fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	return vehicle, nil
}

// requireVehicleOwner refuses a caller who is not the vehicle's on-chain owner.
//
// This is the platform's document-sharing contract, and it is deliberately
// stricter than "the vehicle is in my fleet": a share grants READ and APPEND,
// never delete. dimo-app-backend enforces the same rule — its authorizeSubject
// hands back a 'grantee' mode that "MUST NOT be treated as delete
// authorization" — and both apps write to the same CloudEvents, so a fleet
// grantee who could tombstone here would be deleting documents the mobile app
// would have refused to let them touch.
func requireVehicleOwner(c *fiber.Ctx, vehicle *models.Vehicle) error {
	caller, err := GetWalletAddressFromJWT(c)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	if vehicle.Owner == "" {
		// Owner unknown (roster gap) — refuse rather than guess. A wrong
		// allow here destroys someone else's document.
		return fiber.NewError(fiber.StatusForbidden, "vehicle owner is unknown; cannot verify delete authority")
	}
	if !strings.EqualFold(vehicle.Owner, caller.Hex()) {
		return fiber.NewError(fiber.StatusForbidden,
			"only the vehicle owner can delete its documents; a share grants read and append only")
	}
	return nil
}

// isVehicleOwner reports whether the caller owns the vehicle, for annotating
// reads. Unlike requireVehicleOwner it never errors — an unknown owner simply
// means "not the owner", which renders the document read-only.
func isVehicleOwner(c *fiber.Ctx, vehicle *models.Vehicle) bool {
	caller, err := GetWalletAddressFromJWT(c)
	if err != nil || vehicle == nil || vehicle.Owner == "" {
		return false
	}
	return strings.EqualFold(vehicle.Owner, caller.Hex())
}

// ExtractDocument — POST /documents/extract (multipart file).
// Returns {vin, category, fields, fileHash, rawResponse}.
func (d *DocumentsController) ExtractDocument(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "file is required")
	}
	mimeType := fileHeader.Header.Get("Content-Type")
	if !isAllowedMime(mimeType) {
		return fiber.NewError(fiber.StatusBadRequest, "unsupported file type — allowed: application/pdf, image/jpeg, image/png")
	}

	f, err := fileHeader.Open()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "open uploaded file")
	}
	defer f.Close()

	fileBytes, err := io.ReadAll(f)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "read uploaded file")
	}
	sum := sha256.Sum256(fileBytes)
	fileHash := hex.EncodeToString(sum[:])

	result, err := d.extractAPI.ExtractDocument(tenant, fileBytes, fileHeader.Filename, mimeType)
	if err != nil {
		d.logger.Err(err).Msg("extract failed")
		return fiber.NewError(fiber.StatusBadGateway, "document extraction failed: "+err.Error())
	}

	return c.JSON(fiber.Map{
		"vin":         result.VIN,
		"category":    result.Category,
		"fields":      result.Fields,
		"fileHash":    fileHash,
		"rawResponse": result.RawJSON,
	})
}

// LookupVIN — GET /documents/vin-lookup?vin=X.
// MVP: returns {found: false}. Identity-api Vehicle doesn't expose VIN
// directly; iterating fetch-api for a vin CE per vehicle is expensive on
// every upload. The frontend always offers a manual vehicle picker.
// TODO(glovebox): wire real VIN lookup once we have a cheap source — either
// the dimo.attestation.vin CE cached server-side or an identity-api field.
func (d *DocumentsController) LookupVIN(c *fiber.Ctx) error {
	if _, err := GetWalletAddressFromJWT(c); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	vin := c.Query("vin")
	if vin == "" {
		return fiber.NewError(fiber.StatusBadRequest, "vin query parameter is required")
	}
	return c.JSON(fiber.Map{"found": false, "vin": vin})
}

// AttestRequest is the body for /documents/attest.
type AttestRequest struct {
	TokenID    int64                  `json:"tokenId"`
	Category   string                 `json:"category"`   // CE type, e.g. "dimo.document.vehicle.insurance" or short form
	FileBase64 string                 `json:"fileBase64"` // body of the file, base64 (no data: prefix)
	MimeType   string                 `json:"mimeType"`
	FileName   string                 `json:"fileName"`
	ParsedData map[string]interface{} `json:"parsedData"`
}

// AttestDocument — POST /documents/attest. Builds and submits a raw+parsed CE pair.
func (d *DocumentsController) AttestDocument(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	var req AttestRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
	}
	if req.TokenID == 0 || req.Category == "" || req.FileBase64 == "" {
		return fiber.NewError(fiber.StatusBadRequest, "tokenId, category, fileBase64 are required")
	}

	if _, err := d.vehicleInTenant(c, tenant, uint64(req.TokenID)); err != nil {
		return err
	}

	fileBytes, err := base64.StdEncoding.DecodeString(req.FileBase64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid base64 file data")
	}
	sum := sha256.Sum256(fileBytes)
	fileHash := hex.EncodeToString(sum[:])

	tokenDID := d.authProvider.BuildVehicleDID(uint64(req.TokenID))

	// Stamp who uploaded this. `source` records that our dev license attested
	// it; `producer` records the person behind the request, which is the only
	// way an owner can later tell their own uploads from a sharee's.
	producer := ""
	if caller, werr := GetWalletAddressFromJWT(c); werr == nil {
		producer = d.authProvider.BuildAccountDID(caller)
	}

	result, err := d.attestSvc.AttestDocumentPair(tenant, service.AttestInput{
		TokenID:    strconv.FormatInt(req.TokenID, 10),
		TokenDID:   tokenDID,
		Category:   req.Category,
		FileBytes:  fileBytes,
		MimeType:   req.MimeType,
		FileHash:   fileHash,
		ParsedData: req.ParsedData,
		Producer:   producer,
	})
	if err != nil {
		d.logger.Err(err).Int64("tokenID", req.TokenID).Msg("attest failed")
		return fiber.NewError(fiber.StatusBadGateway, "attestation failed: "+err.Error())
	}
	if req.Category == service.VehicleRegistrationCloudEventType {
		go func() {
			// fetch-api needs a few seconds to index the newly attested document
			// before SyncVehicle can read it back; without this delay the goroutine
			// races the indexer and writes nothing.
			time.Sleep(10 * time.Second)
			if _, err := d.plateSvc.SyncVehicle(context.Background(), tenant, req.TokenID, service.SyncOpts{}); err != nil {
				d.logger.Warn().Err(err).Int64("tokenID", req.TokenID).Msg("post-upload plate/VIN sync failed")
			}
		}()
	}
	return c.JSON(result)
}

// ListDocuments — GET /documents/list?tokenId=N. Returns parsed document CEs;
// each carries the `rawId` of its blob CE, which /documents/download takes.
func (d *DocumentsController) ListDocuments(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseUint(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	vehicle, err := d.vehicleInTenant(c, tenant, tokenID)
	if err != nil {
		return err
	}
	callerOwnsVehicle := isVehicleOwner(c, vehicle)
	// The license our attestations are signed with — our own, or the
	// operator's for a managed tenant. "" means we could not resolve it, and
	// ownsDocument below is false for everything, which is the safe direction.
	ourLicense := d.authProvider.EffectiveClientID(tenant)

	tokenDID := d.authProvider.BuildVehicleDID(tokenID)
	// Enumerated types, filtered server-side. An unfiltered query returns the
	// most recent CEs of *every* type on the vehicle — on anything streaming
	// telemetry the documents never make the window.
	entries, err := d.fetchAPI.ListByDIDAndTypes(tenant, tokenDID, gateway.GloveboxCETypes(), 200)
	if err != nil {
		// 403 from token-exchange-api means the dev license lacks SACD
		// permissions on this vehicle. Surface that so the frontend can
		// prompt the user to grant permissions via console.dimo.org rather
		// than blowing up.
		if strings.Contains(err.Error(), "lacks permissions") || strings.Contains(err.Error(), "status code 403") {
			d.logger.Warn().Str("did", tokenDID).Msg("dev license lacks SACD permissions on this vehicle")
			return c.JSON(fiber.Map{
				"documents":           []interface{}{},
				"tokenDid":            tokenDID,
				"permissionsRequired": true,
				"devLicense":          tenant.ClientID,
			})
		}
		d.logger.Err(err).Str("did", tokenDID).Msg("list failed")
		return fiber.NewError(fiber.StatusBadGateway, "list documents failed: "+err.Error())
	}

	// fetch-api already suppresses tombstoned rows, but only when the tombstone
	// carries the same `source` as the row it voids. Filter again in-process so
	// a tombstone written by another app — or one using the older referenceId
	// payload shape — is honoured too.
	tombstoned := gateway.TombstonedIDs(entries)

	parsed := make([]fiber.Map, 0, len(entries))
	for _, e := range entries {
		if e.Type == gateway.TombstoneCEType {
			continue
		}
		if _, gone := tombstoned[e.ID]; gone {
			continue
		}
		doc := fiber.Map{
			"id":     e.ID,
			"type":   e.Type,
			"source": e.Source,
			"time":   e.Time,
			"data":   e.Data,
			// The raw blob CE this document was extracted from, or "" for
			// documents attested before raweventid pairing. /documents/download
			// takes this id; without it there is nothing to download.
			"rawId": e.RawEventID,
			// Provenance and authority, so a shared vehicle's documents can be
			// told apart in the UI instead of all looking locally owned.
			//
			// isThirdParty: attested by some other dev license — a document
			// this vehicle's owner added from the DIMO mobile app reads as
			// third-party here, and vice versa. It is NOT a permission.
			//
			// isReadOnly: the caller cannot modify this document. True for
			// everyone but the vehicle's owner, matching the delete gate, and
			// true for any document we did not attest, because our tombstone
			// would not suppress it platform-wide.
			"isThirdParty": !weAttested(ourLicense, e.Source),
			"isReadOnly":   !callerOwnsVehicle || !weAttested(ourLicense, e.Source),
		}
		// uploadedBy is the account DID of the wallet that added the document,
		// from the CE's `producer`. Absent on documents written before
		// provenance stamping — omitted rather than sent empty so the UI can
		// tell "nobody recorded" from "recorded as nobody".
		if e.Producer != "" {
			doc["uploadedBy"] = e.Producer
		}
		parsed = append(parsed, doc)
	}

	return c.JSON(fiber.Map{
		"documents": parsed,
		"tokenDid":  tokenDID,
		// Whether the caller owns this vehicle or merely holds it under a
		// share. Drives whether the UI offers delete at all.
		"isOwner": callerOwnsVehicle,
	})
}

// DownloadDocument — GET /documents/download?tokenId=N&rawId=X.
// Streams the raw bytes with Content-Disposition so the browser saves.
//
// rawId is the raw blob CE's id, taken from a document's `rawId` in
// /documents/list. It used to be a filehash, which could never work:
// fetch-api's CloudEventHeader has no filehash field, so the lookup matched
// nothing and the download button stayed disabled.
func (d *DocumentsController) DownloadDocument(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseUint(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	rawID := c.Query("rawId")
	if rawID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "rawId query param required")
	}
	if _, err := d.vehicleInTenant(c, tenant, tokenID); err != nil {
		return err
	}

	// The point query is DID-scoped, so a CE that comes back necessarily
	// belongs to this vehicle — the asset JWT is the authorization boundary.
	tokenDID := d.authProvider.BuildVehicleDID(tokenID)
	entry, err := d.fetchAPI.GetCloudEventByID(tenant, tokenDID, rawID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "fetch raw document failed: "+err.Error())
	}
	if entry == nil {
		return fiber.NewError(fiber.StatusNotFound, "document not found")
	}
	// This endpoint serves document attachments, not arbitrary CEs. Without
	// this a caller could name any event id on a vehicle in their fleet — a
	// telemetry row, say — and have it streamed back as a file download.
	if !strings.HasPrefix(entry.Type, "dimo.raw.") {
		return fiber.NewError(fiber.StatusBadRequest, "rawId does not identify a document attachment")
	}

	fileBytes, err := d.rawDocumentBytes(entry)
	if err != nil {
		d.logger.Err(err).Str("rawId", rawID).Msg("read raw document payload")
		return fiber.NewError(fiber.StatusBadGateway, "read raw document failed")
	}

	contentType := entry.DataContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	filename := buildDownloadFilename(entry.Type, entry.Time, contentType, rawID)
	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.Send(fileBytes)
}

// rawDocumentBytes pulls the file out of a raw CE. fetch-api inlines small
// blobs as dataBase64 but hands back a presigned S3 URL for larger ones, so
// both have to be handled. We fetch the presigned URL server-side rather than
// redirecting: it keeps the S3 URL off the client and preserves the
// Content-Disposition filename the browser saves under.
func (d *DocumentsController) rawDocumentBytes(entry *gateway.AttestationEntry) ([]byte, error) {
	if entry.DataBase64 != "" {
		return base64.StdEncoding.DecodeString(entry.DataBase64)
	}
	if entry.DataURL == "" {
		return nil, fmt.Errorf("raw cloud event %s carries no payload", entry.ID)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(entry.DataURL)
	if err != nil {
		return nil, fmt.Errorf("get presigned blob: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("presigned blob status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
}

// DeleteDocument — DELETE /documents/:id?tokenId=N. Emits a tombstone CE.
func (d *DocumentsController) DeleteDocument(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	parsedID := c.Params("id")
	if parsedID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "id is required")
	}
	tokenID, err := strconv.ParseUint(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	vehicle, err := d.vehicleInTenant(c, tenant, tokenID)
	if err != nil {
		return err
	}
	// Deletes are owner-only. Being in the tenant's fleet is not enough: the
	// fleet includes vehicles held under a share, and a share is read+append.
	if err := requireVehicleOwner(c, vehicle); err != nil {
		return err
	}

	// Resolve the document on this vehicle before tombstoning it. The point
	// query is DID-scoped, so a hit proves the id really belongs to this
	// vehicle — without it any id could be tombstoned by anyone holding a
	// vehicle in their fleet.
	tokenDID := d.authProvider.BuildVehicleDID(tokenID)
	ce, err := d.fetchAPI.GetCloudEventByID(tenant, tokenDID, parsedID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "look up document failed: "+err.Error())
	}
	if ce == nil {
		return fiber.NewError(fiber.StatusNotFound, "document not found on this vehicle")
	}
	rawID := ce.RawEventID

	result, err := d.attestSvc.Tombstone(tenant, parsedID, rawID, tokenDID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "tombstone failed: "+err.Error())
	}

	// fetch-api suppresses a tombstoned row only when the tombstone carries the
	// same `source` as the row it voids (voidsSuppressionMod). We sign with our
	// own dev license, so a document another app attested stays visible in that
	// app even after we tombstone it — we hide it locally and no further. Tell
	// the caller which of the two happened rather than reporting a clean delete.
	deletedEverywhere := weAttested(d.authProvider.EffectiveClientID(tenant), ce.Source)
	return c.JSON(fiber.Map{
		"rawSubmission":     result.RawSubmission,
		"parsedSubmission":  result.ParsedSubmission,
		"deletedEverywhere": deletedEverywhere,
	})
}

// weAttested reports whether a document's `source` is the license we sign
// with — i.e. whether a tombstone from us would actually suppress it in
// fetch-api, which matches on (source, voids_id).
//
// An unresolved license is never a match. Claiming a document we cannot
// tombstone would offer a delete that silently does nothing outside this app.
func weAttested(ourLicense, source string) bool {
	if ourLicense == "" || source == "" {
		return false
	}
	return strings.EqualFold(ourLicense, source)
}

func isAllowedMime(m string) bool {
	switch m {
	case "application/pdf", "image/jpeg", "image/png":
		return true
	}
	return false
}

// buildDownloadFilename: <last-segment-of-type>-YYYY-MM-DD.<ext>
// ceID is only a last-resort fallback when the type yields no usable name.
func buildDownloadFilename(ceType, ceTime, mimeType, ceID string) string {
	base := "document"
	if ceType != "" {
		parts := strings.Split(ceType, ".")
		if last := parts[len(parts)-1]; last != "" {
			base = last
		}
	}
	stamp := ""
	if ceTime != "" {
		if t, err := time.Parse(time.RFC3339, ceTime); err == nil {
			stamp = t.Format("2006-01-02")
		}
	}
	if stamp == "" {
		stamp = time.Now().Format("2006-01-02")
	}
	ext := extensionForMIME(mimeType)
	base = sanitizeFilenameSegment(base)
	if base == "" && len(ceID) >= 8 {
		base = "document-" + ceID[:8]
	}
	if base == "" {
		base = "document"
	}
	if ext == "" {
		return fmt.Sprintf("%s-%s", base, stamp)
	}
	return fmt.Sprintf("%s-%s.%s", base, stamp, ext)
}

func extensionForMIME(mimeType string) string {
	primary := strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
	switch strings.ToLower(primary) {
	case "application/pdf":
		return "pdf"
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	}
	return ""
}

func sanitizeFilenameSegment(s string) string {
	bad := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\x00"}
	for _, b := range bad {
		s = strings.ReplaceAll(s, b, "")
	}
	return strings.TrimSpace(s)
}
