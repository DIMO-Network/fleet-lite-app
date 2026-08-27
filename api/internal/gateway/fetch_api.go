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
//
// The fields here are exactly the ones fetch-api's CloudEventHeader exposes
// (see fetch-api/schema/base.graphqls). Notably there is NO `filehash` — a
// document's parsed CE points at its raw blob through RawEventID, which is
// the `raweventid` CloudEvents extension both this app and dimo-app-backend
// stamp at write time.
type AttestationEntry struct {
	ID              string          `json:"id,omitempty"`
	Type            string          `json:"type,omitempty"`
	Source          string          `json:"source,omitempty"`
	Producer        string          `json:"producer,omitempty"`
	Subject         string          `json:"subject,omitempty"`
	Time            string          `json:"time,omitempty"`
	RawEventID      string          `json:"raweventid,omitempty"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	DataBase64      string          `json:"dataBase64,omitempty"`
	DataURL         string          `json:"dataUrl,omitempty"`
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

// listHeaderFields is the header selection every list query uses. It matches
// what dimo-app-backend selects, minus the fields we have no use for.
const listHeaderFields = "id type source producer subject time datacontenttype raweventid"

// ListByDIDAndTypes pulls the most recent `limit` CEs whose type is one of
// `types` for a subject DID. The filter is applied server-side, so `limit`
// bounds the matched-type count directly and a high-volume unrelated type
// (telemetry, most often) cannot crowd the requested types out of the window.
//
// `types` is required. There is deliberately no "list everything" variant:
// an unfiltered query both truncates documents away on busy vehicles and
// forces fetch-api to read every blob CE out of S3 to answer it.
func (f *FetchAPI) ListByDIDAndTypes(tenant models.Tenant, tokenDID string, types []string, limit int) ([]AttestationEntry, error) {
	if len(types) == 0 {
		return nil, fmt.Errorf("ListByDIDAndTypes requires at least one CE type")
	}
	query, err := buildListQuery(tokenDID, types, limit)
	if err != nil {
		return nil, err
	}

	var resp struct {
		CloudEvents []rawCloudEvent `json:"cloudEvents"`
	}
	if err := f.do(tenant, tokenDID, query, &resp); err != nil {
		return nil, err
	}
	entries := make([]AttestationEntry, 0, len(resp.CloudEvents))
	for _, ce := range resp.CloudEvents {
		entries = append(entries, ce.toEntry())
	}
	return entries, nil
}

// ListByDIDAndType is the single-type convenience wrapper.
func (f *FetchAPI) ListByDIDAndType(tenant models.Tenant, tokenDID, ceType string, limit int) ([]AttestationEntry, error) {
	return f.ListByDIDAndTypes(tenant, tokenDID, []string{ceType}, limit)
}

// GetCloudEventByID returns one CE by its id, with its payload — the inline
// JSON, the base64 blob, or a presigned URL when fetch-api decides the blob is
// too large to inline. Returns nil when the subject has no CE with that id.
//
// This is a point query (`latestCloudEvent` filtered by id), mirroring
// dimo-app-backend's queryCloudEventById. fetch-api scopes the query to the
// DID, so the asset JWT is the authorization boundary: a CE that comes back
// necessarily belongs to `tokenDID`.
func (f *FetchAPI) GetCloudEventByID(tenant models.Tenant, tokenDID, ceID string) (*AttestationEntry, error) {
	if ceID == "" {
		return nil, fmt.Errorf("ceID is required")
	}
	query := fmt.Sprintf(`query {
  latestCloudEvent(did: %q, filter: { id: %q }) {
    data
    dataBase64
    dataUrl
    header { %s }
  }
}`, tokenDID, ceID, listHeaderFields)

	var resp struct {
		LatestCloudEvent *rawCloudEvent `json:"latestCloudEvent"`
	}
	if err := f.do(tenant, tokenDID, query, &resp); err != nil {
		return nil, err
	}
	if resp.LatestCloudEvent == nil {
		return nil, nil
	}
	entry := resp.LatestCloudEvent.toEntry()
	return &entry, nil
}

// buildListQuery renders the cloudEvents query. The `filter: { types: [...] }`
// argument is the whole point: it is what stops fetch-api from filling the
// `limit` window with whatever CE type happens to be most recent.
func buildListQuery(tokenDID string, types []string, limit int) (string, error) {
	filterJSON, err := json.Marshal(types)
	if err != nil {
		return "", fmt.Errorf("marshal type filter: %w", err)
	}
	return fmt.Sprintf(`query {
  cloudEvents(did: %q, limit: %d, filter: { types: %s }) {
    data
    header { %s }
  }
}`, tokenDID, limit, string(filterJSON), listHeaderFields), nil
}

type rawCloudEvent struct {
	Header     map[string]json.RawMessage `json:"header"`
	Data       *json.RawMessage           `json:"data"`
	DataBase64 string                     `json:"dataBase64"`
	DataURL    string                     `json:"dataUrl"`
}

func (r rawCloudEvent) toEntry() AttestationEntry {
	entry := AttestationEntry{DataBase64: r.DataBase64, DataURL: r.DataURL}
	readStr(r.Header, "id", &entry.ID)
	readStr(r.Header, "type", &entry.Type)
	readStr(r.Header, "source", &entry.Source)
	readStr(r.Header, "producer", &entry.Producer)
	readStr(r.Header, "subject", &entry.Subject)
	readStr(r.Header, "time", &entry.Time)
	readStr(r.Header, "raweventid", &entry.RawEventID)
	readStr(r.Header, "datacontenttype", &entry.DataContentType)
	if r.Data != nil {
		entry.Data = json.RawMessage(*r.Data)
	}
	return entry
}

// do runs a GraphQL query against fetch-api under a DID-scoped asset JWT and
// decodes `data` into out.
//
// GraphQL errors are returned as errors rather than ignored. This matters more
// than it looks: fetch-api types its list fields non-null, so a single failed
// blob read nulls the whole result. Reading `data` without checking `errors`
// turns that into a silent empty list — indistinguishable from a vehicle that
// genuinely has no documents.
func (f *FetchAPI) do(tenant models.Tenant, tokenDID, query string, out interface{}) error {
	assetJWT, err := f.authProvider.GetAssetJWT(tenant, tokenDID)
	if err != nil {
		return fmt.Errorf("asset JWT: %w", err)
	}

	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return fmt.Errorf("marshal fetch request: %w", err)
	}
	req, err := http.NewRequest("POST", f.fetchURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("build fetch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+assetJWT)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch API request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read fetch response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch API status %d: %s", resp.StatusCode, string(respBytes))
	}

	if err := decodeFetchResponse(respBytes, out); err != nil {
		f.logger.Error().Err(err).Str("tokenDID", tokenDID).Msg("fetch API response")
		return err
	}
	return nil
}

// decodeFetchResponse unwraps a GraphQL envelope into out, turning a populated
// `errors` array into a Go error.
//
// Checking `errors` is load-bearing rather than tidy. fetch-api types its list
// fields non-null, so one failed blob read nulls the entire result — read
// `data` alone and that arrives as an empty list, which reads as "this vehicle
// has no documents" and is impossible to tell apart from the real thing.
func decodeFetchResponse(respBytes []byte, out interface{}) error {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return fmt.Errorf("parse fetch response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("fetch API GraphQL: %s", strings.Join(msgs, "; "))
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("fetch API returned no data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode fetch data: %w", err)
	}
	return nil
}

// TombstonedIDs scans entries for dimo.tombstone CEs and returns the set of
// CE ids they void. Both the parsed id (voidsId, or the older referenceId
// name) and the paired raw id (rawReferenceId) are included, matching the
// dimo.tombstone data shape: {voidsId, rawReferenceId?}.
func TombstonedIDs(entries []AttestationEntry) map[string]struct{} {
	tombstoned := map[string]struct{}{}
	for _, e := range entries {
		if e.Type != TombstoneCEType {
			continue
		}
		var d struct {
			VoidsID        string `json:"voidsId"`
			ReferenceID    string `json:"referenceId"`
			RawReferenceID string `json:"rawReferenceId"`
		}
		_ = json.Unmarshal(e.Data, &d)
		if d.VoidsID != "" {
			tombstoned[d.VoidsID] = struct{}{}
		} else if d.ReferenceID != "" {
			tombstoned[d.ReferenceID] = struct{}{}
		}
		if d.RawReferenceID != "" {
			tombstoned[d.RawReferenceID] = struct{}{}
		}
	}
	return tombstoned
}

func readStr(hdr map[string]json.RawMessage, key string, dst *string) {
	if v, ok := hdr[key]; ok {
		_ = json.Unmarshal(v, dst)
	}
}
