package gateway

// Document CE types the glovebox reads back from fetch-api.
//
// fetch-api's CloudEventFilter matches types exactly — `type` or `types`
// (IN (...)) — with no prefix wildcard, so every concrete type has to be
// enumerated here. An unfiltered query is not an option: fetch-api orders by
// timestamp DESC and truncates to `limit` across *all* CE types on the
// subject, so a vehicle streaming `dimo.status` telemetry pushes its
// documents out of the window entirely.
//
// Keep in sync with GLOVEBOX_CE_TYPES in dimo-app-backend
// (src/vehicles-v2/vehicles.service.ts). The mobile app is the reference: a
// document the mobile glovebox shows must show here too. Types below the
// mobile list are ones only this app writes.
var (
	// vehicleDocSuffixes mirrors VEHICLE_DOC_SUFFIXES in dimo-app-backend.
	vehicleDocSuffixes = []string{
		// extract-api trained types
		"service",
		"service.invoice",
		"insurance",
		"regulatory",
		"registration",
		"inspection",
		"regulatory.other",
		"ownership",
		"title",
		"finance",
		// agent-extracted types (chat-v3 staging_commit_*)
		"expense",
		"maintenance",
		"condition",
		"note",
		// fleet-lite-app only
		"fuel",
	}

	// TombstoneCEType soft-deletes another CE. fetch-api suppresses tombstoned
	// rows server-side, but only when the tombstone shares the voided row's
	// `source` (voidsSuppressionMod in fetch-api/pkg/eventrepo). We still read
	// tombstones and filter in-process so a cross-source tombstone — and the
	// older `referenceId` payload shape — is honoured too.
	TombstoneCEType = "dimo.tombstone"

	// UnknownDocumentCEType is what an upload gets when the extractor cannot
	// classify it. Mobile hides these until its agent promotes them to a typed
	// CE; we surface them as "Uncategorized" because our upload modal lets the
	// user pick this category explicitly.
	UnknownDocumentCEType = "dimo.document.unknown"
)

// GloveboxCETypes are the CE types the documents list asks fetch-api for:
// parsed documents plus tombstones. Raw blob CEs are deliberately excluded —
// the parsed CE carries `raweventid`, so the list never needs the blobs, and
// leaving them out keeps fetch-api from reading every attachment out of S3 to
// answer a list request (one failed read nulls the whole non-null result).
func GloveboxCETypes() []string {
	types := make([]string, 0, len(vehicleDocSuffixes)+2)
	for _, s := range vehicleDocSuffixes {
		types = append(types, "dimo.document.vehicle."+s)
	}
	return append(types, UnknownDocumentCEType, TombstoneCEType)
}

// TCOCETypes are the types the TCO report reads: the cost-eligible documents,
// the cost-amendment overlays that backfill amounts onto them, and tombstones.
func TCOCETypes(costEligible []string) []string {
	types := make([]string, 0, len(costEligible)+2)
	types = append(types, costEligible...)
	return append(types, CostAmendmentCEType, TombstoneCEType)
}

// CostAmendmentCEType duplicates service.CostAmendmentCloudEventType. It lives
// here as well because service imports gateway, not the other way round.
const CostAmendmentCEType = "dimo.document.vehicle.cost-amendment"
