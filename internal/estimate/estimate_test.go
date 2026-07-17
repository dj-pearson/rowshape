package estimate

import (
	"errors"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestOpClassModels pins the RFC §9.1 model table: each operation class maps to
// its cost-growth model.
func TestOpClassModels(t *testing.T) {
	cases := []struct {
		op   OpClass
		want Model
	}{
		{CatalogOnly, Constant},
		{TableRewrite, Linear},
		{ConstraintValidation, Linear},
		{BTreeBuild, NLogN},
		{IndexConcurrently, NLogN},
	}
	for _, c := range cases {
		if got := c.op.Model(); got != c.want {
			t.Errorf("OpClass(%d).Model() = %s, want %s", c.op, got, c.want)
		}
	}
	// CREATE INDEX CONCURRENTLY is the one build that holds no exclusive lock.
	if IndexConcurrently.HoldsExclusiveLock() {
		t.Error("CREATE INDEX CONCURRENTLY must not hold an exclusive lock")
	}
	if !BTreeBuild.HoldsExclusiveLock() {
		t.Error("a plain b-tree build holds an exclusive lock")
	}
}

// TestRFCExampleBucket reproduces the RFC §9.2 worked example: an n_log_n build
// extrapolated from 12k rows in 91ms to 1.2M declared rows lands in the `slow`
// bucket (10–60s), using DECLARED rows, not the hydrated basis count.
func TestRFCExampleBucket(t *testing.T) {
	est := Extrapolate(BTreeBuild, 12_000, 91, 1_200_000, fixture.Exact)
	if est.Bucket != verdict.BucketSlow {
		t.Errorf("bucket = %s, want slow (RFC §9.2 example)", est.Bucket)
	}
	// The basis is attached — never a point estimate (INV-DURATIONS-BUCKETS).
	if est.Model != string(NLogN) || est.BasisRows != 12_000 || est.BasisMs != 91 || est.DeclaredRows != 1_200_000 {
		t.Errorf("estimate must carry its basis, got %+v", est)
	}
	// Extrapolation is never better than estimated, even from exact rows (§9.2).
	if est.Confidence != string(fixture.Estimated) {
		t.Errorf("estimate confidence = %s, want estimated", est.Confidence)
	}
}

// TestDeclaredRowsDriveExtrapolation: the bucket reflects the DECLARED production
// rows, not the small hydrated basis. A linear rewrite measured at 12k/90ms but
// declared at 50M rows is an outage, though the basis alone reads instant.
func TestDeclaredRowsDriveExtrapolation(t *testing.T) {
	basisAlone := Bucket(90)
	if basisAlone != verdict.BucketInstant {
		t.Fatalf("90ms basis should read instant, got %s", basisAlone)
	}
	est := Extrapolate(TableRewrite, 12_000, 90, 50_000_000, fixture.Exact)
	if est.Bucket != verdict.BucketOutage {
		t.Errorf("50M-row rewrite bucket = %s, want outage (declared rows drive it)", est.Bucket)
	}
}

// TestConfidenceNeverExceedsRows: the estimate's confidence is capped by the
// confidence of `rows` (RFC §7.4). Declared rows → declared estimate.
func TestConfidenceNeverExceedsRows(t *testing.T) {
	est := Extrapolate(TableRewrite, 12_000, 90, 1_000_000, fixture.Declared)
	if est.Confidence != string(fixture.Declared) {
		t.Errorf("estimate confidence = %s, want declared (never exceeds rows)", est.Confidence)
	}
}

// TestVersionConditionalDivergence: the SAME migration (ADD COLUMN with a
// non-volatile DEFAULT) is classified differently by engine version (RFC §9.1).
// The fast-path landed in PG 11, so the real divergence is at the 10→11 boundary:
// PG 10 rewrites the whole table (outage on 5M rows) while PG 11 and PG 16 are a
// catalog-only instant. PG 11 and PG 16 agree — correctly, since both have the
// fast-path — which is itself a version-conditional fact worth asserting.
func TestVersionConditionalDivergence(t *testing.T) {
	const rows = 5_000_000

	// Non-volatile default: version-conditional.
	op10 := ClassifyAddColumnDefault(false, 10)
	op11 := ClassifyAddColumnDefault(false, 11)
	op16 := ClassifyAddColumnDefault(false, 16)
	if op10 != TableRewrite {
		t.Errorf("PG 10 non-volatile DEFAULT must rewrite, got %v", op10)
	}
	if op11 != CatalogOnly || op16 != CatalogOnly {
		t.Errorf("PG 11+ non-volatile DEFAULT must be catalog-only, got 11=%v 16=%v", op11, op16)
	}

	b10 := Extrapolate(op10, 12_000, 90, rows, fixture.Exact).Bucket
	b16 := Extrapolate(op16, 12_000, 90, rows, fixture.Exact).Bucket
	if b10 == b16 {
		t.Errorf("PG 10 and PG 16 must diverge for the same migration: both %s", b10)
	}
	if b16 != verdict.BucketInstant {
		t.Errorf("PG 16 catalog fast-path must be instant, got %s", b16)
	}
	// PG 10 rewrites all 5M rows: a heavy, user-visible bucket, never instant.
	if b10 == verdict.BucketInstant || b10 == verdict.BucketFast {
		t.Errorf("PG 10 full rewrite of 5M rows must be a heavy bucket, got %s", b10)
	}

	// A VOLATILE default rewrites on every version — no fast-path, no divergence.
	for _, major := range []int{10, 11, 16} {
		if op := ClassifyAddColumnDefault(true, major); op != TableRewrite {
			t.Errorf("volatile DEFAULT must rewrite on PG %d, got %v", major, op)
		}
	}
}

// TestRefusesWithoutVersion: ForFixture refuses to extrapolate when the engine
// version is absent, rather than assuming a recent default (RFC §9.1).
func TestRefusesWithoutVersion(t *testing.T) {
	noVer := &fixture.Fixture{
		Meta:   fixture.Meta{Engine: fixture.Engine{Name: "postgres"}}, // no version
		Tables: map[string]fixture.Table{"public.t": {Rows: fixture.Fact[int64]{Value: 1_000_000, Confidence: fixture.Exact}}},
	}
	if _, err := ForFixture(TableRewrite, noVer, "public.t", 12_000, 90); !errors.Is(err, ErrNoVersion) {
		t.Errorf("ForFixture must refuse without a version, got err=%v", err)
	}

	withVer := &fixture.Fixture{
		Meta:   fixture.Meta{Engine: fixture.Engine{Name: "postgres", Version: "16"}},
		Tables: map[string]fixture.Table{"public.t": {Rows: fixture.Fact[int64]{Value: 1_000_000, Confidence: fixture.Exact}}},
	}
	est, err := ForFixture(TableRewrite, withVer, "public.t", 12_000, 90)
	if err != nil {
		t.Fatalf("ForFixture with a version must succeed, got %v", err)
	}
	if est.Bucket == "" || est.DeclaredRows != 1_000_000 {
		t.Errorf("ForFixture must use declared rows and produce a bucket, got %+v", est)
	}
}

// TestMajorParse pins engine-version parsing (RFC §9.1).
func TestMajorParse(t *testing.T) {
	cases := []struct {
		in    string
		major int
		ok    bool
	}{
		{"16", 16, true},
		{"11.5", 11, true},
		{"9.6", 9, true},
		{"", 0, false},
		{"unknown", 0, false},
	}
	for _, c := range cases {
		m, ok := Major(c.in)
		if m != c.major || ok != c.ok {
			t.Errorf("Major(%q) = (%d,%v), want (%d,%v)", c.in, m, ok, c.major, c.ok)
		}
	}
}
