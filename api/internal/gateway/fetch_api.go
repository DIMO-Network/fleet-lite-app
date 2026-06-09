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
func (f *FetchAPI) ListByDID(tenant models.Tenant, tokenDID string, limit int) ([]AttestationEntry, error) {
	assetJWT, err := f.authProvider.GetAssetJWT(tenant, tokenDID)
	if err != nil {
		return nil, fmt.Errorf("asset JWT: %w", err)
	}

	// fetch-api is a GraphQL endpoint (POST /query). Request the recent cloud
	// events for the DID; callers filter by type in-process.
	gqlQuery := fmt.Sprintf(`query {
  cloudEvents(did: %q, limit: %d) {
    data
    header { id type source producer subject time }
  }
}`, tokenDID, limit)
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
		if isDocumentType(entry.Type) {
			entries = append(entries, entry)
		}
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
