package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
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
// the fiber error to send, or nil when the vehicle is in scope.
//
// It returns an error rather than a bool so an unresolvable group scope can
// surface as 503 instead of being flattened into "not part of this tenant".
// Either way the caller is refused: this never opens up on failure.
func (d *DocumentsController) vehicleInTenant(c *fiber.Ctx, tenant models.Tenant, tokenID uint64) error {
	allowed, _ := GetAllowedGroups(c)
	if _, err := d.vehicleSvc.GetVehicle(c.Context(), tenant, int64(tokenID), allowed); err != nil {
		if serr := ScopeUnavailable(err); serr != nil {
			return serr
		}
		return fiber.NewError(fiber.StatusForbidden, "vehicle is not part of this tenant")
	}
	return nil
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

	if err := d.vehicleInTenant(c, tenant, uint64(req.TokenID)); err != nil {
		return err
	}

	fileBytes, err := base64.StdEncoding.DecodeString(req.FileBase64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid base64 file data")
	}
	sum := sha256.Sum256(fileBytes)
	fileHash := hex.EncodeToString(sum[:])

	tokenDID := d.authProvider.BuildVehicleDID(uint64(req.TokenID))

	result, err := d.attestSvc.AttestDocumentPair(tenant, service.AttestInput{
		TokenID:    strconv.FormatInt(req.TokenID, 10),
		TokenDID:   tokenDID,
		Category:   req.Category,
		FileBytes:  fileBytes,
		MimeType:   req.MimeType,
		FileHash:   fileHash,
		ParsedData: req.ParsedData,
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

// ListDocuments — GET /documents/list?tokenId=N. Returns parsed CEs only;
// the frontend uses filehash to download the raw via /documents/download.
func (d *DocumentsController) ListDocuments(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseUint(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	if err := d.vehicleInTenant(c, tenant, tokenID); err != nil {
		return err
	}

	tokenDID := d.authProvider.BuildVehicleDID(tokenID)
	entries, err := d.fetchAPI.ListByDID(tenant, tokenDID, 200)
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

	// Build tombstoned-id set so we hide deleted docs.
	tombstoned := gateway.TombstonedIDs(entries)

	// rawByHash: filehash -> raw ID, so we can include raw IDs alongside parsed
	// (for delete path; download uses filehash directly).
	rawByHash := map[string]string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Type, "dimo.raw.") && e.FileHash != "" {
			rawByHash[e.FileHash] = e.ID
		}
	}

	parsed := make([]fiber.Map, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Type, "dimo.document.") {
			continue
		}
		if _, gone := tombstoned[e.ID]; gone {
			continue
		}
		parsed = append(parsed, fiber.Map{
			"id":       e.ID,
			"type":     e.Type,
			"source":   e.Source,
			"time":     e.Time,
			"fileHash": e.FileHash,
			"data":     e.Data,
			"rawId":    rawByHash[e.FileHash],
		})
	}

	return c.JSON(fiber.Map{
		"documents": parsed,
		"tokenDid":  tokenDID,
	})
}

// DownloadDocument — GET /documents/download?tokenId=N&filehash=X.
// Streams the raw bytes with Content-Disposition so the browser saves.
func (d *DocumentsController) DownloadDocument(c *fiber.Ctx) error {
	tenant, err := GetTenant(c)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tokenID, err := strconv.ParseUint(c.Query("tokenId"), 10, 64)
	if err != nil || tokenID == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "valid tokenId query param required")
	}
	fileHash := c.Query("filehash")
	if fileHash == "" {
		return fiber.NewError(fiber.StatusBadRequest, "filehash query param required")
	}
	if err := d.vehicleInTenant(c, tenant, tokenID); err != nil {
		return err
	}

	tokenDID := d.authProvider.BuildVehicleDID(tokenID)
	entry, err := d.fetchAPI.FindRawByFilehash(tenant, tokenDID, fileHash)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "fetch raw document failed: "+err.Error())
	}
	if entry == nil {
		return fiber.NewError(fiber.StatusNotFound, "document not found")
	}

	fileBytes, err := base64.StdEncoding.DecodeString(entry.DataBase64)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "decode raw document base64")
	}
	contentType := entry.DataContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	filename := buildDownloadFilename(entry.Type, entry.Time, contentType, fileHash)
	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.Send(fileBytes)
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
	if err := d.vehicleInTenant(c, tenant, tokenID); err != nil {
		return err
	}

	// Look up the paired raw id by filehash (best-effort — if we don't find
	// it we still emit a tombstone for the parsed id).
	tokenDID := d.authProvider.BuildVehicleDID(tokenID)
	rawID := ""
	if entries, err := d.fetchAPI.ListByDID(tenant, tokenDID, 200); err == nil {
		for _, e := range entries {
			if e.ID == parsedID && e.FileHash != "" {
				for _, e2 := range entries {
					if strings.HasPrefix(e2.Type, "dimo.raw.") && e2.FileHash == e.FileHash {
						rawID = e2.ID
						break
					}
				}
				break
			}
		}
	}

	result, err := d.attestSvc.Tombstone(tenant, parsedID, rawID, tokenDID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "tombstone failed: "+err.Error())
	}
	return c.JSON(result)
}

func isAllowedMime(m string) bool {
	switch m {
	case "application/pdf", "image/jpeg", "image/png":
		return true
	}
	return false
}

// buildDownloadFilename: <last-segment-of-type>-YYYY-MM-DD.<ext>
func buildDownloadFilename(ceType, ceTime, mimeType, fileHash string) string {
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
	if base == "" && len(fileHash) >= 8 {
		base = "document-" + fileHash[:8]
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
