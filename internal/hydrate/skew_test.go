package hydrate

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// TestHydrateReproducesSkew: given a column with a skewed histogram (most rows in
// a narrow low band, a long sparse tail), hydrate reproduces that skew — the bulk
// of generated values land in the dense band, not spread uniformly (RFC §6.2).
func TestHydrateReproducesSkew(t *testing.T) {
	// An equi-depth histogram where 12 of 16 buckets sit in [0,10] and 4 stretch
	// to 1000 — i.e. ~75% of rows in the dense low band.
	bounds := []any{0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0, 10.0, 10.0, 200.0, 500.0, 800.0, 1000.0}
	f := oneTable("public.t", 10000, map[string]fixture.Column{
		"score": {
			Type:      "integer",
			Nullable:  false,
			Distinct:  &fixture.Fact[int64]{Value: 500, Confidence: fixture.Estimated},
			Histogram: &fixture.Histogram{Buckets: 16, Bounds: bounds},
		},
		// A control column with the same range but no histogram — should spread out.
		"uniform": {
			Type:     "integer",
			Nullable: false,
			Distinct: &fixture.Fact[int64]{Value: 1000, Confidence: fixture.Estimated},
			Range:    &fixture.Range{Min: 0, Max: 1000},
		},
	})

	res, err := Generate(f, Options{Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gt := res.Tables[0]
	scoreIdx, uniformIdx := indexOf(gt.Columns, "score"), indexOf(gt.Columns, "uniform")

	var scoreLow, uniformLow int
	for _, row := range gt.Rows {
		if v, ok := row[scoreIdx].(int64); ok && v <= 10 {
			scoreLow++
		}
		if v, ok := row[uniformIdx].(int64); ok && v <= 10 {
			uniformLow++
		}
	}
	scoreFrac := float64(scoreLow) / float64(len(gt.Rows))
	uniformFrac := float64(uniformLow) / float64(len(gt.Rows))
	t.Logf("score ≤10: %.3f, uniform ≤10: %.3f", scoreFrac, uniformFrac)

	// The skewed column concentrates in the low band (~12/16 buckets ≈ 0.75).
	if scoreFrac < 0.6 || scoreFrac > 0.9 {
		t.Errorf("skew not reproduced: %.3f of scores ≤10, want ~0.75", scoreFrac)
	}
	// The uniform column spreads out — only ~1% of a 0..1000 range is ≤10.
	if uniformFrac > 0.05 {
		t.Errorf("uniform column should not concentrate low: %.3f ≤10", uniformFrac)
	}
	// The skew must be a real difference, not noise.
	if scoreFrac <= uniformFrac*5 {
		t.Errorf("histogram skew (%.3f) not clearly above uniform (%.3f)", scoreFrac, uniformFrac)
	}
}
