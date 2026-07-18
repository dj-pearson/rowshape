package verdict

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- CR-T9: a byte-based estimate must not fabricate a row basis ------------
//
// Estimate's numeric fields are not pointers, so "not applicable" and "measured
// zero" have the same representation. The human renderer printed the row basis
// unconditionally, so a REINDEX bucketed from an index's on-disk BYTES rendered
// as "extrapolated from 0 rows in 0ms" — the struct's zero values stated as
// measurements, on the one line whose job is to say how the answer was reached.
//
// Fixed as a RENDERING change: the DSSE-signable struct is untouched, because
// findings.estimateFor floors its basis at 1ms for every row-based estimate, so
// BasisMs == 0 can only mean "not a row/time extrapolation".
func TestEstimateHumanDoesNotFabricateABasis(t *testing.T) {
	byteBased := &Estimate{Bucket: BucketOutage, Model: "reindex_bytes"}
	got := byteBased.human()

	if strings.Contains(got, "0 rows") || strings.Contains(got, "0ms") {
		t.Errorf("byte-based estimate invented a row/time basis: %q", got)
	}
	if !strings.Contains(got, BucketOutage) {
		t.Errorf("the bucket must survive: %q", got)
	}
	// It should still say where the number came from — dropping the basis
	// entirely would trade a wrong answer for no answer.
	if !strings.Contains(got, "reindex_bytes") {
		t.Errorf("the model should still be named so the reader knows the source: %q", got)
	}
}

// TestEstimateHumanKeepsRealBasis is the other half: the fix must not suppress a
// genuine extrapolation basis, which INV-DURATIONS-BUCKETS requires be attached.
func TestEstimateHumanKeepsRealBasis(t *testing.T) {
	rowBased := &Estimate{
		Bucket: BucketOutage, Model: "linear",
		BasisRows: 2000, BasisMs: 40, DeclaredRows: 50_000_000,
	}
	got := rowBased.human()
	for _, want := range []string{"2000 rows", "40ms", "linear", "50000000"} {
		if !strings.Contains(got, want) {
			t.Errorf("row-based estimate lost %q from its basis: %q", want, got)
		}
	}
}

// TestEstimateJSONUnchangedByRenderFix guards the reason this was done as a
// render-only change: the emitted document is DSSE-signable (INV-DSSE-SHAPE), so
// the field set must be exactly what it was before.
func TestEstimateJSONUnchangedByRenderFix(t *testing.T) {
	raw, err := json.Marshal(&Estimate{Bucket: BucketOutage, Model: "reindex_bytes"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"bucket", "model", "basis_rows", "basis_ms", "declared_rows"}
	if len(got) != len(want) {
		t.Errorf("Estimate JSON has %d fields, want %d — the render fix must not have changed the "+
			"signed document (INV-DSSE-SHAPE): %v", len(got), len(want), got)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("Estimate JSON lost field %q", k)
		}
	}
}
