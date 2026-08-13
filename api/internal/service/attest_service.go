package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// AttestService builds, signs, and submits CloudEvents to the DIMO attest API.
//
// Signing uses the dev license private key in settings.yaml
// (DIMO_AUTH_PRIVATE_KEY). The CE pair is "raw" (data_base64 = file bytes)
// and "parsed" (data = extracted fields); both reference the same filehash so
// fetch-api can join them.
type AttestService interface {
	AttestDocumentPair(tenant models.Tenant, input AttestInput) (*AttestResult, error)
	Tombstone(tenant models.Tenant, parsedID, rawID, tokenDID string) (*AttestResult, error)
	// AttestTenantGeofences publishes the tenant's geofence catalog as a single
	// CE whose subject is the tenant client-id DID (see docs/GEOFENCES_PLAN.md).
	AttestTenantGeofences(tenant models.Tenant, geofences []GeofenceDef) (string, error)
	// AttestVehicleGeofences publishes a vehicle's manual geofence ids as a
	// single CE whose subject is the vehicle DID.
	AttestVehicleGeofences(tenant models.Tenant, tokenID uint64, geofenceIDs []string) (string, error)
	// AttestCostAmendment publishes a small overlay CE that attaches a dollar
	// amount to an already-attested, immutable document — used to backfill
	// TCO figures onto documents uploaded before/without an amount. See
	// CostAmendmentCloudEventType.
	AttestCostAmendment(tenant models.Tenant, tokenID uint64, documentID string, amount float64, currency string) (string, error)
}

// CostAmendmentCloudEventType is the CE type for a TCO cost-amendment overlay
// (see AttestCostAmendment). Mirrors dimo.tombstone's "reference another CE by
// id" pattern instead of mutating the original, immutable document.
const CostAmendmentCloudEventType = "dimo.document.vehicle.cost-amendment"

// TCOAttestationProducer stamps our app's cost-amendment CEs, mirroring
// GeofenceAttestationProducer.
const TCOAttestationProducer = "fleet-lite-app"

// Geofence CE types + producer. The tenant catalog is attested at the client-id
// DID; per-vehicle manual membership at the vehicle DID. Both stamped with a
// stable producer so consumers can attribute our app's assertions.
const (
	TenantGeofencesCloudEventType  = "dimo.document.fleet.geofence"
	VehicleGeofencesCloudEventType = "dimo.document.vehicle.geofence"
	GeofenceAttestationProducer    = "fleet-lite-app"
)

type AttestInput struct {
	TokenID       string // numeric tokenID as string, for logging
	TokenDID      string // canonical asset DID, used as CE subject
	Category      string // CE type (e.g. "dimo.document.vehicle.insurance") or short form
	FileBytes     []byte
	MimeType      string
	FileHash      string                 // SHA256 hex (computed if empty)
	ParsedData    map[string]interface{} // from Extract API
	RawDocumentID string                 // UUID for raw event (generated if empty)
}

type AttestResult struct {
	RawSubmission    SubmissionInfo `json:"rawSubmission"`
	ParsedSubmission SubmissionInfo `json:"parsedSubmission"`
}

type SubmissionInfo struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Source string `json:"source"`
}

// signedCloudEvent — parsed/structured attestation.
type signedCloudEvent struct {
	SpecVersion     string                 `json:"specversion"`
	ID              string                 `json:"id"`
	Source          string                 `json:"source"`
	Producer        string                 `json:"producer,omitempty"`
	Type            string                 `json:"type"`
	Subject         string                 `json:"subject"`
	Time            string                 `json:"time"`
	DataContentType string                 `json:"datacontenttype"`
	Data            map[string]interface{} `json:"data"`
	Signature       string                 `json:"signature"`
	FileHash        string                 `json:"filehash"`
}

// signedRawCloudEvent — raw/binary attestation.
type signedRawCloudEvent struct {
	SpecVersion     string `json:"specversion"`
	ID              string `json:"id"`
	Source          string `json:"source"`
	Type            string `json:"type"`
	Subject         string `json:"subject"`
	Time            string `json:"time"`
	DataContentType string `json:"datacontenttype"`
	DataBase64      string `json:"data_base64"`
	Signature       string `json:"signature"`
	FileHash        string `json:"filehash"`
}

type attestService struct {
	logger       zerolog.Logger
	settings     *config.Settings
	attestURL    string
	authProvider *gateway.DimoAuthProvider
}

func NewAttestService(logger zerolog.Logger, settings *config.Settings, authProvider *gateway.DimoAuthProvider) AttestService {
	return &attestService{
		logger:       logger,
		settings:     settings,
		attestURL:    settings.AttestAPIURL.String(),
		authProvider: authProvider,
	}
}

// signDataSecp256k1 signs dataBytes using the Ethereum personal-sign format.
// Returns a 0x-prefixed hex signature with V normalized to 27/28.
func signDataSecp256k1(dataBytes []byte, privateKeyHex string) (string, error) {
	pkHex := strings.TrimPrefix(privateKeyHex, "0x")
	privateKey, err := gethcrypto.HexToECDSA(pkHex)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	prefix := "\x19Ethereum Signed Message:\n" + strconv.Itoa(len(dataBytes))
	prefixedMsg := append([]byte(prefix), dataBytes...)
	hash := gethcrypto.Keccak256(prefixedMsg)
	sig, err := gethcrypto.Sign(hash, privateKey)
	if err != nil {
		return "", fmt.Errorf("sign data: %w", err)
	}
	switch sig[64] {
	case 0:
		sig[64] = 27
	case 1:
		sig[64] = 28
	}
	return "0x" + hex.EncodeToString(sig), nil
}

// parsedEventType: pass through if already canonical, else prefix.
func parsedEventType(category string) string {
	if strings.HasPrefix(category, "dimo.") {
		return category
	}
	return "dimo.document.vehicle." + category
}

// rawEventType: derive the raw CE type from the parsed one.
func rawEventType(category string) string {
	parsed := parsedEventType(category)
	return strings.Replace(parsed, "dimo.document.", "dimo.raw.", 1)
}

func (s *attestService) buildParsedCloudEvent(input AttestInput, signature, source string) signedCloudEvent {
	return signedCloudEvent{
		SpecVersion:     "1.0",
		ID:              uuid.New().String(),
		Source:          source,
		Type:            parsedEventType(input.Category),
		Subject:         input.TokenDID,
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data:            input.ParsedData,
		Signature:       signature,
		FileHash:        input.FileHash,
	}
}

func (s *attestService) buildRawCloudEvent(input AttestInput, signature, source string) signedRawCloudEvent {
	mimeType := input.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	rawID := input.RawDocumentID
	if rawID == "" {
		rawID = uuid.New().String()
	}
	return signedRawCloudEvent{
		SpecVersion:     "1.0",
		ID:              rawID,
		Source:          source,
		Type:            rawEventType(input.Category),
		Subject:         input.TokenDID,
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: mimeType,
		DataBase64:      base64.StdEncoding.EncodeToString(input.FileBytes),
		Signature:       signature,
		FileHash:        input.FileHash,
	}
}

func (s *attestService) submitCloudEvent(event interface{}, developerJWT string) error {
	eventBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal cloud event: %w", err)
	}
	req, err := http.NewRequest("POST", s.attestURL, bytes.NewBuffer(eventBytes))
	if err != nil {
		return fmt.Errorf("build attest request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cloudevents+json")
	req.Header.Set("Authorization", "Bearer "+developerJWT)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("attest API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("attest API status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (s *attestService) AttestDocumentPair(tenant models.Tenant, input AttestInput) (*AttestResult, error) {
	if input.TokenDID == "" {
		return nil, fmt.Errorf("missing TokenDID")
	}
	if input.FileHash == "" {
		sum := sha256.Sum256(input.FileBytes)
		input.FileHash = hex.EncodeToString(sum[:])
	}
	if input.RawDocumentID == "" {
		input.RawDocumentID = uuid.New().String()
	}

	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return nil, fmt.Errorf("developer JWT: %w", err)
	}
	apiKey := tenant.DIMOPrivateKey
	source := tenant.ClientID

	rawSig, err := signDataSecp256k1(input.FileBytes, apiKey)
	if err != nil {
		return nil, fmt.Errorf("sign raw: %w", err)
	}
	rawEvent := s.buildRawCloudEvent(input, rawSig, source)
	s.logger.Debug().Str("eventType", rawEvent.Type).Str("tokenID", input.TokenID).Msg("Submitting raw cloud event")
	if err := s.submitCloudEvent(rawEvent, developerJWT); err != nil {
		return nil, fmt.Errorf("submit raw cloud event: %w", err)
	}

	parsedDataBytes, err := json.Marshal(input.ParsedData)
	if err != nil {
		return nil, fmt.Errorf("marshal parsed data: %w", err)
	}
	parsedSig, err := signDataSecp256k1(parsedDataBytes, apiKey)
	if err != nil {
		return nil, fmt.Errorf("sign parsed: %w", err)
	}
	parsedEvent := s.buildParsedCloudEvent(input, parsedSig, source)
	s.logger.Info().Str("eventType", parsedEvent.Type).Str("tokenID", input.TokenID).Msg("Submitting parsed cloud event")
	if err := s.submitCloudEvent(parsedEvent, developerJWT); err != nil {
		return nil, fmt.Errorf("parsed attestation after raw (raw=%s): %w", rawEvent.ID, err)
	}

	return &AttestResult{
		RawSubmission:    SubmissionInfo{ID: rawEvent.ID, Type: rawEvent.Type, Source: rawEvent.Source},
		ParsedSubmission: SubmissionInfo{ID: parsedEvent.ID, Type: parsedEvent.Type, Source: parsedEvent.Source},
	}, nil
}

// Tombstone emits a single dimo.tombstone CE referencing the parsed CE id and
// (optionally) the paired raw CE id. CEs on DIS are immutable; tombstones are
// the soft-delete convention.
func (s *attestService) Tombstone(tenant models.Tenant, parsedID, rawID, tokenDID string) (*AttestResult, error) {
	if parsedID == "" || tokenDID == "" {
		return nil, fmt.Errorf("missing parsedID or tokenDID")
	}
	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return nil, fmt.Errorf("developer JWT: %w", err)
	}
	apiKey := tenant.DIMOPrivateKey

	data := map[string]interface{}{"voidsId": parsedID}
	if rawID != "" {
		data["rawReferenceId"] = rawID
	}
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal tombstone data: %w", err)
	}
	sig, err := signDataSecp256k1(dataBytes, apiKey)
	if err != nil {
		return nil, fmt.Errorf("sign tombstone: %w", err)
	}
	ev := signedCloudEvent{
		SpecVersion:     "1.0",
		ID:              uuid.New().String(),
		Source:          tenant.ClientID,
		Type:            "dimo.tombstone",
		Subject:         tokenDID,
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data:            data,
		Signature:       sig,
	}
	if err := s.submitCloudEvent(ev, developerJWT); err != nil {
		return nil, fmt.Errorf("submit tombstone cloud event: %w", err)
	}
	return &AttestResult{
		ParsedSubmission: SubmissionInfo{ID: ev.ID, Type: ev.Type, Source: ev.Source},
	}, nil
}

// AttestTenantGeofences publishes the tenant's full geofence catalog as a single
// parsed CloudEvent. The subject is the tenant client-id DID
// (BuildTenantDID); data is {"geofences":[{id,name,color,geometry,…},…]}. An
// empty slice is valid ("no geofences").
func (s *attestService) AttestTenantGeofences(tenant models.Tenant, geofences []GeofenceDef) (string, error) {
	if geofences == nil {
		geofences = []GeofenceDef{}
	}
	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return "", fmt.Errorf("developer JWT: %w", err)
	}
	dataMap := map[string]interface{}{"geofences": geofences}
	dataBytes, err := json.Marshal(dataMap)
	if err != nil {
		return "", fmt.Errorf("marshal geofences data: %w", err)
	}
	sig, err := signDataSecp256k1(dataBytes, tenant.DIMOPrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign geofences data: %w", err)
	}
	ev := signedCloudEvent{
		SpecVersion:     "1.0",
		ID:              uuid.New().String(),
		Source:          tenant.ClientID,
		Producer:        GeofenceAttestationProducer,
		Type:            TenantGeofencesCloudEventType,
		Subject:         s.authProvider.BuildTenantDID(tenant.ClientID),
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data:            dataMap,
		Signature:       sig,
	}
	s.logger.Info().Str("tenant", tenant.ID).Int("geofences", len(geofences)).Msg("Submitting tenant geofences cloud event")
	if err := s.submitCloudEvent(ev, developerJWT); err != nil {
		return "", fmt.Errorf("submit tenant geofences cloud event: %w", err)
	}
	return ev.ID, nil
}

// AttestVehicleGeofences publishes a vehicle's manual geofence membership as a
// single parsed CloudEvent. Subject is the vehicle DID; data is
// {"geofences":[id,…]}. all/group-scope membership is derived from the tenant
// catalog and is not part of this document. An empty slice is valid.
func (s *attestService) AttestVehicleGeofences(tenant models.Tenant, tokenID uint64, geofenceIDs []string) (string, error) {
	if geofenceIDs == nil {
		geofenceIDs = []string{}
	}
	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return "", fmt.Errorf("developer JWT: %w", err)
	}
	dataMap := map[string]interface{}{"geofences": geofenceIDs}
	dataBytes, err := json.Marshal(dataMap)
	if err != nil {
		return "", fmt.Errorf("marshal vehicle geofences data: %w", err)
	}
	sig, err := signDataSecp256k1(dataBytes, tenant.DIMOPrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign vehicle geofences data: %w", err)
	}
	ev := signedCloudEvent{
		SpecVersion:     "1.0",
		ID:              uuid.New().String(),
		Source:          tenant.ClientID,
		Producer:        GeofenceAttestationProducer,
		Type:            VehicleGeofencesCloudEventType,
		Subject:         s.authProvider.BuildVehicleDID(tokenID),
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data:            dataMap,
		Signature:       sig,
	}
	s.logger.Info().Uint64("tokenID", tokenID).Int("geofences", len(geofenceIDs)).Msg("Submitting vehicle geofences cloud event")
	if err := s.submitCloudEvent(ev, developerJWT); err != nil {
		return "", fmt.Errorf("submit vehicle geofences cloud event: %w", err)
	}
	return ev.ID, nil
}

// AttestCostAmendment publishes a single parsed CloudEvent that attaches a
// dollar amount to an already-attested document, without mutating or
// re-attesting the original (CEs on DIS are immutable). Subject is the
// vehicle DID; data is {"documentId","amount","currency"}. TCOService reads
// these back and overlays the amount onto documentId's line item wherever
// the original document itself has no amount.
//
// The signature is computed over the JSON of the data map, matching the parsed
// CE convention in AttestDocumentPair so fetch-api/verifiers can re-derive it.
func (s *attestService) AttestCostAmendment(tenant models.Tenant, tokenID uint64, documentID string, amount float64, currency string) (string, error) {
	if documentID == "" {
		return "", fmt.Errorf("documentID is required")
	}
	if currency == "" {
		currency = "USD"
	}
	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return "", fmt.Errorf("developer JWT: %w", err)
	}
	dataMap := map[string]interface{}{
		"documentId": documentID,
		"amount":     amount,
		"currency":   currency,
	}
	dataBytes, err := json.Marshal(dataMap)
	if err != nil {
		return "", fmt.Errorf("marshal cost amendment data: %w", err)
	}
	sig, err := signDataSecp256k1(dataBytes, tenant.DIMOPrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign cost amendment data: %w", err)
	}
	ev := signedCloudEvent{
		SpecVersion:     "1.0",
		ID:              uuid.New().String(),
		Source:          tenant.ClientID,
		Producer:        TCOAttestationProducer,
		Type:            CostAmendmentCloudEventType,
		Subject:         s.authProvider.BuildVehicleDID(tokenID),
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data:            dataMap,
		Signature:       sig,
	}
	s.logger.Info().Uint64("tokenID", tokenID).Str("documentID", documentID).Msg("Submitting cost amendment cloud event")
	if err := s.submitCloudEvent(ev, developerJWT); err != nil {
		return "", fmt.Errorf("submit cost amendment cloud event: %w", err)
	}
	return ev.ID, nil
}
