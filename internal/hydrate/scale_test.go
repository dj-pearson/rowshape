package hydrate

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// TestDeclaredVsHydratedDiverge: hydrating a fixture that declares 1.2M rows at
// --scale 0.01 materializes ~12k rows, while the declared count stays 1.2M
// (RFC §9). The two must diverge in exactly this way.
func TestDeclaredVsHydratedDiverge(t *testing.T) {
	const declared = 1_200_000
	f := oneTable("public.users", declared, map[string]fixture.Column{
		"id": {Type: "bigint", Nullable: false, Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact}},
	})

	res, err := Generate(f, Options{Seed: 42, Scale: 0.01})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gt := res.Tables[0]

	// Materialized ~12k.
	if got := gt.Materialized(); got != 12_000 {
		t.Errorf("materialized rows = %d, want 12000", got)
	}
	if int64(len(gt.Rows)) != gt.Materialized() {
		t.Errorf("Materialized() must equal len(Rows)")
	}

	// Declared still 1.2M — never scaled.
	if gt.DeclaredRows != declared {
		t.Errorf("declared rows = %d, want %d (declared must not be scaled)", gt.DeclaredRows, declared)
	}

	// The plan spells out the separation.
	plan := res.Plan()
	if len(plan) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan))
	}
	p := plan[0]
	if p.Declared != declared || p.Hydrated != 12_000 {
		t.Errorf("plan = %+v, want declared %d hydrated 12000", p, declared)
	}
	if p.Declared == p.Hydrated {
		t.Errorf("declared and hydrated must not be conflated: both %d", p.Declared)
	}
}

// TestPlanRowsMatchesGeneration: the row plan computed without generating data
// agrees with what Generate materializes — so extrapolation can be planned
// cheaply and never silently substitutes the hydrated count for the declared
// one.
func TestPlanRowsMatchesGeneration(t *testing.T) {
	f := oneTable("public.t", 500_000, map[string]fixture.Column{
		"id": {Type: "bigint", Nullable: false},
	})
	opts := Options{Seed: 1, Scale: 0.02}

	plan := PlanRows(f, opts)
	res, err := Generate(f, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(plan) != 1 {
		t.Fatalf("plan len %d", len(plan))
	}
	if plan[0].Declared != 500_000 {
		t.Errorf("plan declared = %d, want 500000", plan[0].Declared)
	}
	if plan[0].Hydrated != res.Tables[0].Materialized() {
		t.Errorf("plan hydrated %d != generated %d", plan[0].Hydrated, res.Tables[0].Materialized())
	}
	// Declared drives extrapolation; it must dwarf what was materialized here.
	if plan[0].Declared <= plan[0].Hydrated {
		t.Errorf("declared (%d) should exceed hydrated (%d) at scale 0.02", plan[0].Declared, plan[0].Hydrated)
	}
}

// TestScaleOneMaterializesAll: at scale 1.0 the hydrated count equals the
// declared count — the separation still holds (they are equal here by choice of
// scale, not by conflation).
func TestScaleOneMaterializesAll(t *testing.T) {
	f := oneTable("public.t", 300, map[string]fixture.Column{
		"id": {Type: "bigint", Nullable: false},
	})
	res, err := Generate(f, Options{Seed: 1, Scale: 1.0})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gt := res.Tables[0]
	if gt.Materialized() != 300 || gt.DeclaredRows != 300 {
		t.Errorf("at scale 1.0 both should be 300: materialized=%d declared=%d", gt.Materialized(), gt.DeclaredRows)
	}
}
