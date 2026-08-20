package service

import (
	"testing"
	"time"

	dbmodels "github.com/DIMO-Network/fleet-lite-app/internal/db/models"
	"github.com/DIMO-Network/fleet-lite-app/internal/models"
	"github.com/aarondl/null/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rosterRow(tokenID int64, brand, model string) models.RemoteVehicleMetadata {
	return models.RemoteVehicleMetadata{
		VehicleTokenID: tokenID,
		Owner:          "0x97B8bA44C66d2C893925dE41BbDF0eE9b9640E7a",
		DefinitionID:   "maxus_t60_2024",
		Make:           brand,
		Model:          model,
		Year:           2024,
	}
}

func localRow(tokenID int64) *dbmodels.Vehicle {
	return &dbmodels.Vehicle{TokenID: tokenID}
}

// The ordinary case: the roster says what the vehicle is, the local row adds
// what only this app knows.
func TestMergeRosterVehiclesUsesRosterForIdentity(t *testing.T) {
	seen := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	local := localRow(1)
	local.LastSeen = null.TimeFrom(seen)
	local.LastLat = null.Float64From(-33.4)
	local.LastLon = null.Float64From(-70.6)

	got := mergeRosterVehicles([]int64{1},
		map[int64]models.RemoteVehicleMetadata{1: rosterRow(1, "Maxus", "T60")},
		[]*dbmodels.Vehicle{local}, map[int64]bool{1: true})

	require.Len(t, got, 1)
	assert.Equal(t, "Maxus", got[0].Definition.Make, "identity comes from the roster")
	assert.Equal(t, "0x97B8bA44C66d2C893925dE41BbDF0eE9b9640E7a", got[0].Owner)
	assert.True(t, got[0].IsFavorite)
	require.NotNil(t, got[0].LastSeen, "the last fix is app-local and still applied")
	assert.True(t, seen.Equal(*got[0].LastSeen))
	require.NotNil(t, got[0].LastLat)
	assert.False(t, got[0].MetadataPending)
}

// THE CASE THE CUTOVER MUST NOT GET WRONG. A token in the resolved set with no
// roster row still appears — it is a vehicle entitled minutes ago, before the
// nightly reconcile. An inner join gives a provably correct set and a short
// response, which is strictly harder to diagnose than an empty one.
func TestMergeRosterVehiclesMissingRosterRow(t *testing.T) {
	got := mergeRosterVehicles([]int64{7},
		map[int64]models.RemoteVehicleMetadata{}, nil, nil)

	require.Len(t, got, 1)
	assert.Equal(t, int64(7), got[0].TokenID)
	assert.True(t, got[0].MetadataPending, "thin, not absent")
}

// The freshly-entitled customer: nothing in the roster, nothing local, and the
// fleet must still render every vehicle it is entitled to.
func TestMergeRosterVehiclesAllMissing(t *testing.T) {
	got := mergeRosterVehicles([]int64{3, 1, 2}, nil, nil, nil)

	require.Len(t, got, 3)
	for _, v := range got {
		assert.True(t, v.MetadataPending)
	}
}

// Device ids drive the list's connection indicator. Losing them was the reason
// the roster gained the columns before this cutover was written.
func TestMergeRosterVehiclesCarriesDeviceIDs(t *testing.T) {
	synthetic := int64(169126)
	m := rosterRow(1, "Maxus", "T60")
	m.SyntheticDeviceTokenID = &synthetic

	got := mergeRosterVehicles([]int64{1, 2},
		map[int64]models.RemoteVehicleMetadata{1: m, 2: rosterRow(2, "Ford", "Ranger")},
		nil, nil)

	require.Len(t, got, 2)
	assert.Equal(t, synthetic, got[0].SyntheticDevice.TokenID)
	assert.Nil(t, got[0].AftermarketDevice)
	assert.Zero(t, got[1].SyntheticDevice.TokenID, "an unpaired vehicle reads as unpaired")
}

// VIN and plate fall back to the local row. identity-api serves neither, so the
// roster's copies are usually empty today while this app's attestation sync has
// filled its own — preferring the roster without a fallback would blank a plate
// the customer can already see.
func TestMergeRosterVehiclesFallsBackToLocalVinAndPlate(t *testing.T) {
	local := localRow(1)
	local.Vin = null.StringFrom("LSGBL1234RA000001")
	local.LicensePlate = null.StringFrom("ABCD12")

	got := mergeRosterVehicles([]int64{1},
		map[int64]models.RemoteVehicleMetadata{1: rosterRow(1, "Maxus", "T60")},
		[]*dbmodels.Vehicle{local}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, "LSGBL1234RA000001", got[0].VIN)
	assert.Equal(t, "ABCD12", got[0].LicensePlate)
}

// And the roster wins when it has an answer, or the cutover would never start
// using the values kaufmann feeds it.
func TestMergeRosterVehiclesPrefersRosterVinAndPlate(t *testing.T) {
	local := localRow(1)
	local.Vin = null.StringFrom("STALE-VIN")
	local.LicensePlate = null.StringFrom("OLD123")

	m := rosterRow(1, "Maxus", "T60")
	m.VIN = "FRESH-VIN"
	m.LicensePlate = "NEW456"

	got := mergeRosterVehicles([]int64{1},
		map[int64]models.RemoteVehicleMetadata{1: m},
		[]*dbmodels.Vehicle{local}, nil)

	assert.Equal(t, "FRESH-VIN", got[0].VIN)
	assert.Equal(t, "NEW456", got[0].LicensePlate)
}

// Ordering is the local table's and must survive the move: most recently seen
// first, no-fix vehicles last in token order. It cannot come from the roster —
// last_seen is an app-local column the roster has never heard of.
func TestMergeRosterVehiclesOrdersByLastSeen(t *testing.T) {
	older, newer := localRow(1), localRow(2)
	older.LastSeen = null.TimeFrom(time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC))
	newer.LastSeen = null.TimeFrom(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))

	got := mergeRosterVehicles([]int64{9, 1, 2},
		map[int64]models.RemoteVehicleMetadata{
			1: rosterRow(1, "A", "A"), 2: rosterRow(2, "B", "B"), 9: rosterRow(9, "C", "C"),
		},
		[]*dbmodels.Vehicle{older, newer}, nil)

	require.Len(t, got, 3)
	assert.Equal(t, int64(2), got[0].TokenID, "most recent fix first")
	assert.Equal(t, int64(1), got[1].TokenID)
	assert.Equal(t, int64(9), got[2].TokenID, "no fix goes last")
}

// Metadata is allowed to age independently of the gates, and that is a
// deliberate difference from entitledTTL/membershipTTL — which are asserted
// EQUAL to each other elsewhere because they gate the set. Stale metadata can
// only make a make and model old; it can never remove a vehicle from a fleet.
func TestMetadataTTLIsIndependentOfTheGates(t *testing.T) {
	assert.NotEqual(t, entitledTTL, metadataTTL,
		"if these are ever made equal, say why — they answer different questions")
	assert.Greater(t, metadataTTL, entitledTTL,
		"metadata may be staler than the set; the reverse would be the bug")
}
