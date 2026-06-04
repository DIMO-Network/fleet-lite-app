package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/gateway"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
)

const extractTimeout = 60 * time.Second

// ExtractResult holds the parsed output of a single document extraction.
type ExtractResult struct {
	VIN      string                 `json:"vin,omitempty"`
	Category string                 `json:"category,omitempty"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
	RawJSON  json.RawMessage        `json:"rawResponse"`
}

// ExtractAPIService is a small HTTP client for extract.dimo.zone.
//
// The single dev license configured in settings.yaml is used to mint a
// developer JWT (cached); each extract call sends the file as multipart with
// that JWT.
type ExtractAPIService interface {
	ExtractDocument(tenant models.Tenant, fileBytes []byte, fileName, mimeType string) (*ExtractResult, error)
}

type extractAPIService struct {
	logger       zerolog.Logger
	authProvider *gateway.DimoAuthProvider
	extractURL   string
}

func NewExtractAPIService(logger zerolog.Logger, settings *config.Settings, authProvider *gateway.DimoAuthProvider) ExtractAPIService {
	return &extractAPIService{
		logger:       logger,
		authProvider: authProvider,
		extractURL:   settings.ExtractAPIURL.String(),
	}
}

func (s *extractAPIService) ExtractDocument(tenant models.Tenant, fileBytes []byte, fileName, mimeType string) (*ExtractResult, error) {
	developerJWT, err := s.authProvider.GetDeveloperJWT(tenant)
	if err != nil {
		return nil, fmt.Errorf("developer JWT: %w", err)
	}

	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Build multipart form with the correct MIME type. We can't use the
	// stdlib's CreateFormFile because it hard-codes application/octet-stream,
	// and the Extract API uses Content-Type to pick the parser.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
	partHeader.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return nil, fmt.Errorf("create form part: %w", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		return nil, fmt.Errorf("write file bytes: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", s.extractURL, &body)
	if err != nil {
		return nil, fmt.Errorf("build extract request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+developerJWT)

	client := &http.Client{Timeout: extractTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("extract API request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read extract response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("extract API status %d: %s", resp.StatusCode, string(respBytes))
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(respBytes, &rawMap); err != nil {
		return nil, fmt.Errorf("parse extract response: %w", err)
	}

	result := &ExtractResult{
		RawJSON:  json.RawMessage(respBytes),
		Fields:   rawMap,
		VIN:      findVINInMap(rawMap),
		Category: findCategory(rawMap),
	}
	return result, nil
}

// findVINInMap recursively searches for a non-empty "vin" field up to 4 levels deep.
func findVINInMap(m map[string]interface{}) string {
	return findVINRecursive(m, 0, 4)
}

func findVINRecursive(m map[string]interface{}, depth, maxDepth int) string {
	if depth > maxDepth {
		return ""
	}
	for _, key := range []string{"vin", "VIN", "Vin"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	for _, key := range []string{"data", "fields", "result", "document"} {
		if v, ok := m[key]; ok {
			if nested, ok := v.(map[string]interface{}); ok {
				if vin := findVINRecursive(nested, depth+1, maxDepth); vin != "" {
					return vin
				}
			}
		}
	}
	return ""
}

func findCategory(m map[string]interface{}) string {
	if s := findStringInMap(m, "type"); s != "" {
		return s
	}
	if fields := getNestedMap(m, "fields"); fields != nil {
		if s := findStringInMap(fields, "type"); s != "" {
			return s
		}
	}
	for _, k := range []string{"category", "Category", "attestationCategory"} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func findStringInMap(m map[string]interface{}, key string) string {
	for _, k := range []string{key, strings.ToUpper(key)} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func getNestedMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok {
		if nested, ok := v.(map[string]interface{}); ok {
			return nested
		}
	}
	return nil
}
