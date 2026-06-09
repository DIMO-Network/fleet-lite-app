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
	AttestVehicleGroups(tenant models.Tenant, tokenID uint64, groups []GroupRef) (string, error)
}

// VehicleGroupsCloudEventType is the CE type for a vehicle's group-membership
// document. fetch-api admits it via its dimo.document.* prefix filter.
const VehicleGroupsCloudEventType = "dimo.document.vehicle.groups"

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

	data := map[string]interface{}{"referenceId": parsedID}
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

// AttestVehicleGroups publishes the vehicle's current group membership as a
// single signed, parsed CloudEvent (no raw pair). The subject is the vehicle
// DID; data is {"groups":[{id,name,color},...]}. An empty groups slice is valid
// and represents "in no groups". Returns the new event id.
//
// The signature is computed over the JSON of the data map, matching the parsed
// CE convention in AttestDocumentPair so fetch-api/verifiers can re-derive it.
func (s *attestService) AttestVehicleGroups(tenant models.Tenant, tokenID uint64, groups []GroupRef) (string, error) {
	if groups == nil {
		groups = []GroupRef{}
	}
	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return "", fmt.Errorf("developer JWT: %w", err)
	}

	dataMap := map[string]interface{}{"groups": groups}
	dataBytes, err := json.Marshal(dataMap)
	if err != nil {
		return "", fmt.Errorf("marshal groups data: %w", err)
	}
	sig, err := signDataSecp256k1(dataBytes, tenant.DIMOPrivateKey)
	if err != nil {
		return "", fmt.Errorf("sign groups data: %w", err)
	}

	ev := signedCloudEvent{
		SpecVersion:     "1.0",
		ID:              uuid.New().String(),
		Source:          tenant.ClientID,
		Type:            VehicleGroupsCloudEventType,
		Subject:         s.authProvider.BuildVehicleDID(tokenID),
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Data:            dataMap,
		Signature:       sig,
	}
	s.logger.Info().Uint64("tokenID", tokenID).Int("groups", len(groups)).Msg("Submitting vehicle groups cloud event")
	if err := s.submitCloudEvent(ev, developerJWT); err != nil {
		return "", fmt.Errorf("submit vehicle groups cloud event: %w", err)
	}
	return ev.ID, nil
}
