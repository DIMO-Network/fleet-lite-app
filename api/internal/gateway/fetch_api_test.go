package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DIMO-Network/fleet-lite-app/internal/models"
)

func TestTombstonedIDs(t *testing.T) {
	entries := []AttestationEntry{
		{ID: "doc-1", Type: "dimo.document.vehicle.service.invoice", Data: json.RawMessage(`{"amount":500}`)},
		{ID: "tomb-1", Type: "dimo.tombstone", Data: json.RawMessage(`{"voidsId":"doc-1"}`)},
	}
	got := TombstonedIDs(entries)
	if _, ok := got["doc-1"]; !ok {
		t.Fatalf("expected doc-1 to be tombstoned, got %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one tombstoned id, got %v", got)
	}
}

func TestTombstonedIDs_ReferenceIDFallback(t *testing.T) {
	entries := []AttestationEntry{
		{ID: "tomb-1", Type: "dimo.tombstone", Data: json.RawMessage(`{"referenceId":"doc-2"}`)},
	}
	got := TombstonedIDs(entries)
	if _, ok := got["doc-2"]; !ok {
		t.Fatalf("expected doc-2 to be tombstoned via referenceId fallback, got %v", got)
	}
}

func TestTombstonedIDs_RawReferenceIDIncluded(t *testing.T) {
	entries := []AttestationEntry{
		{ID: "tomb-1", Type: "dimo.tombstone", Data: json.RawMessage(`{"voidsId":"doc-3","rawReferenceId":"raw-3"}`)},
	}
	got := TombstonedIDs(entries)
	if _, ok := got["doc-3"]; !ok {
		t.Fatalf("expected doc-3 (voidsId) tombstoned, got %v", got)
	}
	if _, ok := got["raw-3"]; !ok {
		t.Fatalf("expected raw-3 (rawReferenceId) tombstoned, got %v", got)
	}
}

func TestTombstonedIDs_IgnoresNonTombstoneEntries(t *testing.T) {
	entries := []AttestationEntry{
		{ID: "doc-1", Type: "dimo.document.vehicle.insurance", Data: json.RawMessage(`{"amount":100}`)},
	}
	got := TombstonedIDs(entries)
	if len(got) != 0 {
		t.Fatalf("expected no tombstoned ids, got %v", got)
	}
}

// --- Regression tests for the "mobile documents never appear" bug ---
//
// Three defects combined to hide documents uploaded from the DIMO mobile app:
// the list query carried no type filter, GraphQL errors were swallowed, and
// raw/parsed pairing keyed off a `filehash` field fetch-api does not expose.
// One test each.

// mobileVehicleDocSuffixes are the vehicle document suffixes dimo-app-backend
// writes — VEHICLE_DOC_SUFFIXES in src/vehicles-v2/vehicles.service.ts. A type
// missing from GloveboxCETypes is a document the mobile app shows and the
// fleet app silently does not.
var mobileVehicleDocSuffixes = []string{
	"service", "service.invoice", "insurance", "regulatory", "registration",
	"inspection", "regulatory.other", "ownership", "title", "finance",
	"expense", "maintenance", "condition", "note",
}

func TestGloveboxCETypes_CoversEveryTypeMobileWrites(t *testing.T) {
	got := map[string]bool{}
	for _, ceType := range GloveboxCETypes() {
		got[ceType] = true
	}
	for _, suffix := range mobileVehicleDocSuffixes {
		ceType := "dimo.document.vehicle." + suffix
		if !got[ceType] {
			t.Errorf("GloveboxCETypes() is missing %q — the mobile app writes it, so the fleet app must ask for it", ceType)
		}
	}
	if !got[TombstoneCEType] {
		t.Errorf("GloveboxCETypes() must include %q or deletes cannot be filtered in-process", TombstoneCEType)
	}
}

func TestGloveboxCETypes_ExcludesNonDocumentTypes(t *testing.T) {
	// Geofence and cost-amendment CEs share the dimo.document. prefix but are
	// not glovebox documents. The old prefix match listed them as documents.
	for _, ceType := range GloveboxCETypes() {
		if ceType == "dimo.document.vehicle.geofence" || ceType == CostAmendmentCEType {
			t.Errorf("GloveboxCETypes() must not include %q — it is not a glovebox document", ceType)
		}
	}
}

func TestBuildListQuery_FiltersTypesServerSide(t *testing.T) {
	query, err := buildListQuery("did:erc721:137:0xabc:1", []string{"dimo.document.vehicle.insurance"}, 200)
	if err != nil {
		t.Fatalf("buildListQuery: %v", err)
	}
	// Without a types filter fetch-api orders by time DESC across every CE
	// type on the subject, so telemetry pushes documents out of the window.
	if !strings.Contains(query, `filter: { types: ["dimo.document.vehicle.insurance"] }`) {
		t.Fatalf("query must filter types server-side, got:\n%s", query)
	}
	// raweventid is how a parsed document points at its file. fetch-api's
	// CloudEventHeader has no filehash field, so it must not be selected.
	if !strings.Contains(query, "raweventid") {
		t.Fatalf("query must select raweventid for raw/parsed pairing, got:\n%s", query)
	}
	if strings.Contains(query, "filehash") {
		t.Fatalf("query selects filehash, which fetch-api's CloudEventHeader does not define:\n%s", query)
	}
}

func TestListByDIDAndTypes_RejectsEmptyTypeList(t *testing.T) {
	f := &FetchAPI{}
	if _, err := f.ListByDIDAndTypes(models.Tenant{}, "did:erc721:137:0xabc:1", nil, 200); err == nil {
		t.Fatal("expected an error for an empty type list — an unfiltered list query silently drops documents")
	}
}

func TestDecodeFetchResponse_SurfacesGraphQLErrors(t *testing.T) {
	// fetch-api's cloudEvents is [CloudEvent!]! — one failed blob read nulls
	// the whole list. Decoding data alone turns that into "no documents".
	body := []byte(`{"errors":[{"message":"get object bucket/key: access denied"}],"data":null}`)
	var out struct {
		CloudEvents []rawCloudEvent `json:"cloudEvents"`
	}
	err := decodeFetchResponse(body, &out)
	if err == nil {
		t.Fatal("expected an error, got nil — a nulled result would be reported as an empty document list")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error should carry the GraphQL message, got: %v", err)
	}
}

func TestDecodeFetchResponse_DecodesSuccessfulPayload(t *testing.T) {
	body := []byte(`{"data":{"cloudEvents":[{"header":{"id":"p1","type":"dimo.document.vehicle.insurance","raweventid":"r1"},"data":{"amount":100}}]}}`)
	var out struct {
		CloudEvents []rawCloudEvent `json:"cloudEvents"`
	}
	if err := decodeFetchResponse(body, &out); err != nil {
		t.Fatalf("decodeFetchResponse: %v", err)
	}
	if len(out.CloudEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out.CloudEvents))
	}
	entry := out.CloudEvents[0].toEntry()
	if entry.ID != "p1" || entry.RawEventID != "r1" {
		t.Fatalf("expected id p1 / raweventid r1, got %+v", entry)
	}
}

// Asset JWTs are minted for fetch-api reads only. Token-exchange rejects the
// whole request when the SACD lacks any privilege asked for, so asking for one
// a CloudEvent read does not need silently locks documents away on grants that
// the mobile app reads fine. This set must stay equal to
// dimo-app-backend's FETCH_API_PERMISSIONS.
func TestAssetJWTPrivileges_MatchMobile(t *testing.T) {
	mobile := []string{
		"privilege:GetNonLocationHistory",
		"privilege:GetCurrentLocation",
		"privilege:GetVINCredential",
		"privilege:GetRawData",
	}
	if len(assetJWTPrivileges) != len(mobile) {
		t.Fatalf("asset JWT asks for %v; the mobile app asks for %v", assetJWTPrivileges, mobile)
	}
	want := map[string]bool{}
	for _, p := range mobile {
		want[p] = true
	}
	for _, p := range assetJWTPrivileges {
		if !want[p] {
			t.Errorf("asset JWT asks for %q, which no CloudEvent read needs — it only narrows which shares can read documents", p)
		}
	}
}
