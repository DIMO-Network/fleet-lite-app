package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DIMO-Network/fleet-lite-app/internal/config"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/rs/zerolog"
)

// AttestationEntry is a CE returned by fetch-api, flattened for callers.
type AttestationEntry struct {
	ID              string          `json:"id,omitempty"`
	Type            string          `json:"type,omitempty"`
	Source          string          `json:"source,omitempty"`
	Producer        string          `json:"producer,omitempty"`
	Subject         string          `json:"subject,omitempty"`
	Time            string          `json:"time,omitempty"`
	FileHash        string          `json:"filehash,omitempty"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	DataBase64      string          `json:"dataBase64,omitempty"`
}

// FetchAPI wraps fetch-api.dimo.zone (asset-JWT auth).
type FetchAPI struct {
	authProvider *DimoAuthProvider
	settings     *config.Settings
	logger       zerolog.Logger
	fetchURL     string
}

func NewFetchAPI(logger zerolog.Logger, settings *config.Settings, authProvider *DimoAuthProvider) *FetchAPI {
	return &FetchAPI{
		authProvider: authProvider,
		settings:     settings,
		logger:       logger,
		fetchURL:     settings.FetchAPIURL.String(),
	}
}

// ListByDID pulls the most recent `limit` CEs for a vehicle DID and returns
// only the ones whose type prefixes match document attestations (parsed or raw).
// Document filtering is client-side; for a single exact type prefer the
// server-side-filtered ListByDIDAndType.
func (f *FetchAPI) ListByDID(tenant models.Tenant, tokenDID string, limit int) ([]AttestationEntry, error) {
	entries, err := f.queryCloudEvents(tenant, tokenDID, limit, "")
	if err != nil {
		return nil, err
	}
	out := entries[:0]
	for _, e := range entries {
		if isDocumentType(e.Type) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ListByDIDAndType pulls the most recent `limit` CEs of exactly `ceType` (e.g.
// dimo.document.vehicle.groups) for a vehicle DID. The type filter is applied
// server-side via fetch-api's cloudEvents `filter: { type: ... }` argument, so
// `limit` bounds the matched-type count directly — a high-volume unrelated CE
// type can't crowd the target type out of the window.
func (f *FetchAPI) ListByDIDAndType(tenant models.Tenant, tokenDID, ceType string, limit int) ([]AttestationEntry, error) {
	return f.queryCloudEvents(tenant, tokenDID, limit, ceType)
}

// queryCloudEvents runs the fetch-api GraphQL cloudEvents query for a DID and
// returns the parsed entries verbatim (no client-side filtering). When
// typeFilter is non-empty it is pushed into the query's `filter: { type: ... }`
// argument so fetch-api filters by CE type server-side.
func (f *FetchAPI) queryCloudEvents(tenant models.Tenant, tokenDID string, limit int, typeFilter string) ([]AttestationEntry, error) {
	assetJWT, err := f.authProvider.GetAssetJWT(tenant, tokenDID)
	if err != nil {
		return nil, fmt.Errorf("asset JWT: %w", err)
	}

	// fetch-api is a GraphQL endpoint (POST /query). filter:{type} narrows by CE
	// type server-side; omitted when typeFilter is empty (callers filter in-process).
	filterArg := ""
	if typeFilter != "" {
		filterArg = fmt.Sprintf(", filter: { type: %q }", typeFilter)
	}
	gqlQuery := fmt.Sprintf(`query {
  cloudEvents(did: %q, limit: %d%s) {
    data
    header { id type source producer subject time }
  }
}`, tokenDID, limit, filterArg)
	body, err := json.Marshal(map[string]string{"query": gqlQuery})
	if err != nil {
		return nil, fmt.Errorf("marshal fetch request: %w", err)
	}

	req, err := http.NewRequest("POST", f.fetchURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("build fetch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+assetJWT)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch API request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read fetch response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch API status %d: %s", resp.StatusCode, string(respBytes))
	}

	var fetchResp struct {
		Data struct {
			CloudEvents []struct {
				Header     map[string]json.RawMessage `json:"header"`
				Data       *json.RawMessage           `json:"data"`
				DataBase64 string                     `json:"dataBase64"`
			} `json:"cloudEvents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &fetchResp); err != nil {
		f.logger.Error().Err(err).Bytes("body", respBytes).Str("tokenDID", tokenDID).Msg("parse fetch response")
		return nil, fmt.Errorf("parse fetch response: %w", err)
	}

	entries := make([]AttestationEntry, 0, len(fetchResp.Data.CloudEvents))
	for _, ce := range fetchResp.Data.CloudEvents {
		entry := AttestationEntry{}
		readStr(ce.Header, "id", &entry.ID)
		readStr(ce.Header, "type", &entry.Type)
		readStr(ce.Header, "source", &entry.Source)
		readStr(ce.Header, "producer", &entry.Producer)
		readStr(ce.Header, "subject", &entry.Subject)
		readStr(ce.Header, "time", &entry.Time)
		readStr(ce.Header, "filehash", &entry.FileHash)
		readStr(ce.Header, "datacontenttype", &entry.DataContentType)
		if ce.Data != nil {
			entry.Data = json.RawMessage(*ce.Data)
		}
		entry.DataBase64 = ce.DataBase64
		entries = append(entries, entry)
	}
	return entries, nil
}

// FindRawByFilehash returns the raw CE (data_base64 + datacontenttype) matching
// the given filehash for a vehicle, or nil if absent. fetch-api has no
// filehash filter — we pull recent and match in-process. Per-vehicle history
// is bounded in practice.
func (f *FetchAPI) FindRawByFilehash(tenant models.Tenant, tokenDID, fileHash string) (*AttestationEntry, error) {
	if fileHash == "" {
		return nil, fmt.Errorf("fileHash is required")
	}
	entries, err := f.ListByDID(tenant, tokenDID, 200)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		e := &entries[i]
		if strings.HasPrefix(e.Type, "dimo.raw.") && e.FileHash == fileHash {
			return e, nil
		}
	}
	return nil, nil
}

func isDocumentType(t string) bool {
	return strings.HasPrefix(t, "dimo.document.") ||
		strings.HasPrefix(t, "dimo.raw.") ||
		t == "dimo.tombstone"
}

func readStr(hdr map[string]json.RawMessage, key string, dst *string) {
	if v, ok := hdr[key]; ok {
		_ = json.Unmarshal(v, dst)
	}
}
