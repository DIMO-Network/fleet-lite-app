package gateway

import (
	"encoding/json"
	"testing"
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
