package findings

import (
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestRSIndexCorpusVerdicts runs the RS-INDEX analyzer against the corpus cases
// it owns and asserts each verdict matches expected.json.
func TestRSIndexCorpusVerdicts(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"unique_index_cant_build", verdict.VerdictFail},
		{"rsindex-non-concurrent", verdict.VerdictWarn},
		{"rsindex-reindex-bloat", verdict.VerdictWarn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, mig := loadCorpus(t, c.name)
			res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsIndex{}}, false)
			if res.Verdict != c.want {
				t.Errorf("verdict = %s, want %s (corpus expected.json)", res.Verdict, c.want)
			}
		})
	}
}

// TestRSIndexNonConcurrent: a plain CREATE INDEX fires RS-INDEX-001 (WARN),
// recommends CONCURRENTLY, and buckets the build via the O(n log n) model.
func TestRSIndexNonConcurrent(t *testing.T) {
	f, mig := loadCorpus(t, "rsindex-non-concurrent")
	got := rsIndex{}.Analyze(f, captureOf(mig, "public.orders", 10_000))
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-INDEX finding, got %d", len(got))
	}
	fnd := got[0]
	if fnd.Code != "RS-INDEX-001" || fnd.Severity != verdict.SeverityWarn {
		t.Errorf("want RS-INDEX-001 warn, got %s/%s", fnd.Code, fnd.Severity)
	}
	if !strings.Contains(fnd.Remediation, "CONCURRENTLY") {
		t.Errorf("remediation must recommend CONCURRENTLY: %q", fnd.Remediation)
	}
	if fnd.Estimate == nil || fnd.Estimate.Model != "n_log_n" {
		t.Errorf("build must be bucketed via the n_log_n model, got %+v", fnd.Estimate)
	}
}

// TestRSIndexUniqueCantBuild: CREATE UNIQUE INDEX on a proven non-unique column
// is an error/FAIL — the index cannot build (RFC §6.5).
func TestRSIndexUniqueCantBuild(t *testing.T) {
	f, mig := loadCorpus(t, "unique_index_cant_build")
	got := rsIndex{}.Analyze(f, plainCapture(mig))
	// CONCURRENTLY, so no non-concurrent lock finding — only the uniqueness one.
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-INDEX finding, got %d: %+v", len(got), got)
	}
	if got[0].Code != "RS-INDEX-010" || got[0].Severity != verdict.SeverityError {
		t.Errorf("want RS-INDEX-010 error, got %s/%s", got[0].Code, got[0].Severity)
	}
	if got[0].DependsOn[0] != "public.users.email.unique" {
		t.Errorf("depends_on = %v, want [public.users.email.unique]", got[0].DependsOn)
	}
}

// TestRSIndexUniqueUnprovenWarns: CREATE UNIQUE INDEX on a column whose
// uniqueness is unproven is capped to a resolving WARN, never PASS
// (INV-UNIQUENESS).
func TestRSIndexUniqueUnprovenWarns(t *testing.T) {
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 1000000, confidence: exact}
    columns:
      email: {type: text, nullable: false, distinct: {value: 999000, confidence: estimated}}
`))
	if err != nil {
		t.Fatal(err)
	}
	mig := "CREATE UNIQUE INDEX CONCURRENTLY users_email_uniq ON public.users (email);"
	res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsIndex{}}, false)
	if res.Verdict != verdict.VerdictWarn {
		t.Errorf("verdict = %s, want WARN (uniqueness unproven)", res.Verdict)
	}
	if len(res.Findings) == 0 || !strings.Contains(res.Findings[0].Remediation, "pull --exact") {
		t.Errorf("capped WARN must name the resolving command, got %+v", res.Findings)
	}
}

// TestRSIndexUniqueProvenPasses: CREATE UNIQUE INDEX on proven-unique data
// certifies PASS.
func TestRSIndexUniqueProvenPasses(t *testing.T) {
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 1000000, confidence: exact}
    columns:
      email: {type: text, nullable: false, unique: {value: true, confidence: exact, via: probe}}
`))
	if err != nil {
		t.Fatal(err)
	}
	mig := "CREATE UNIQUE INDEX CONCURRENTLY users_email_uniq ON public.users (email);"
	res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsIndex{}}, false)
	if res.Verdict != verdict.VerdictPass {
		t.Errorf("verdict = %s, want PASS (uniqueness proven exact)", res.Verdict)
	}
}

// TestRSIndexReindexBytesBasis: a non-concurrent REINDEX buckets its duration
// from the index's on-disk bytes and reports the bloat (RFC §6.5).
func TestRSIndexReindexBytesBasis(t *testing.T) {
	f, mig := loadCorpus(t, "rsindex-reindex-bloat")
	got := rsIndex{}.Analyze(f, plainCapture(mig))
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-INDEX finding, got %d", len(got))
	}
	fnd := got[0]
	if fnd.Code != "RS-INDEX-020" || fnd.Severity != verdict.SeverityWarn {
		t.Errorf("want RS-INDEX-020 warn, got %s/%s", fnd.Code, fnd.Severity)
	}
	ev, _ := fnd.Evidence.(map[string]any)
	if ev["index_bytes"] != int64(4200000000) {
		t.Errorf("index_bytes evidence = %v, want 4200000000", ev["index_bytes"])
	}
	// 4.2 GB / ~50 MB/s is well over a minute → an outage bucket.
	if fnd.Estimate == nil || fnd.Estimate.Bucket != verdict.BucketOutage {
		t.Errorf("4.2 GB reindex should bucket as outage, got %+v", fnd.Estimate)
	}
}

// TestRSIndexConcurrentNotFlagged: CREATE INDEX CONCURRENTLY takes no exclusive
// lock, so it is not flagged for locking.
func TestRSIndexConcurrentNotFlagged(t *testing.T) {
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.orders:
    rows: {value: 1000000, confidence: exact}
    columns:
      created_at: {type: timestamptz, nullable: false}
`))
	if err != nil {
		t.Fatal(err)
	}
	mig := "CREATE INDEX CONCURRENTLY orders_created_at_idx ON public.orders (created_at);"
	if got := (rsIndex{}).Analyze(f, plainCapture(mig)); len(got) != 0 {
		t.Errorf("CREATE INDEX CONCURRENTLY must not be flagged for locking, got %+v", got)
	}
}

// --- CR-T2: unqualified table names -----------------------------------------
//
// rsindex skipped resolveTable, so `CREATE INDEX ON orders (...)` missed the
// fixture key `public.orders`, read the zero value, and reported `instant` for a
// build on a 50M-row table. Unlike rsperf (where the finding vanished), here the
// finding survives and states a confidently wrong duration, which is worse: the
// user gets an answer, and it is the reassuring one.

func indexFixture(t *testing.T) *fixture.Fixture {
	t.Helper()
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.orders:
    rows: {value: 50000000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
      email: {type: text, nullable: false, unique: {value: false, confidence: exact, via: probe}}
    indexes:
      - {name: orders_email_idx, method: btree, columns: [email], bytes: 4200000000, bloat_estimate: 0.45}
`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// indexCapture mirrors what a real validate run carries and a bare fixture does
// not: a hydrated row count and a measured duration. Without both, estimateFor
// extrapolates from declared==hydrated and every bucket is `instant` — which is
// how a version of this test could pass against the very bug it targets.
func indexCapture(sql string) *validate.Capture {
	return &validate.Capture{
		Success:    true,
		Statements: []validate.Statement{{SQL: sql, DurationMs: 40}},
		TableRows:  map[string]int64{"public.orders": 2000},
	}
}

func indexFindingByCode(f *fixture.Fixture, c *validate.Capture, code string) *verdict.Finding {
	for _, fnd := range (rsIndex{}).Analyze(f, c) {
		if fnd.Code == code {
			return &fnd
		}
	}
	return nil
}

// TestUnqualifiedCreateIndexGetsTheSameEstimate is the rsindex twin of
// rslock's TestUnqualifiedTableGetsTheSameEstimate.
func TestUnqualifiedCreateIndexGetsTheSameEstimate(t *testing.T) {
	f := indexFixture(t)

	bucketFor := func(t *testing.T, table string) string {
		t.Helper()
		fnd := indexFindingByCode(f, indexCapture("CREATE INDEX ix ON "+table+" (id)"), "RS-INDEX-001")
		if fnd == nil || fnd.Estimate == nil {
			return ""
		}
		return fnd.Estimate.Bucket
	}

	qualified := bucketFor(t, "public.orders")
	unqualified := bucketFor(t, "orders")
	t.Logf("qualified=%q unqualified=%q", qualified, unqualified)

	// Guard the guard.
	if qualified == "" || qualified == "instant" {
		t.Fatalf("qualified estimate = %q; a 50M-row index build extrapolated from 40ms/2k rows must "+
			"be alarming or this test cannot detect the bug", qualified)
	}
	if unqualified != qualified {
		t.Errorf("estimate for `CREATE INDEX ix ON orders (id)` = %q, but the qualified form = %q — "+
			"the same build on the same 50M-row table. The unqualified form read rows=0.",
			unqualified, qualified)
	}
}

// TestUnqualifiedUniqueIndexKeepsItsVerdict: uniqueness is looked up per table,
// so an unresolved name made a column with PROVEN duplicates look merely
// unproven — downgrading a definite failure (this index cannot build) into a
// soft "not confirmed".
func TestUnqualifiedUniqueIndexKeepsItsVerdict(t *testing.T) {
	f := indexFixture(t)

	qualified := indexFindingByCode(f, indexCapture("CREATE UNIQUE INDEX ix ON public.orders (email)"), "RS-INDEX-010")
	unqualified := indexFindingByCode(f, indexCapture("CREATE UNIQUE INDEX ix ON orders (email)"), "RS-INDEX-010")

	if qualified == nil || qualified.Severity != verdict.SeverityError {
		t.Fatalf("qualified form must be a proven-duplicate error, got %+v", qualified)
	}
	if unqualified == nil {
		t.Fatal("unqualified CREATE UNIQUE INDEX produced no RS-INDEX-010")
	}
	if unqualified.Severity != qualified.Severity {
		t.Errorf("severity for the unqualified form = %q, qualified = %q — the same index on the same "+
			"column with proven duplicates", unqualified.Severity, qualified.Severity)
	}
}

// TestUnqualifiedReindexTableResolves: REINDEX TABLE names a TABLE, so it needs
// resolution. REINDEX INDEX names an index, matched against index names rather
// than fixture table keys, and must NOT be resolved — the two cases share a
// parser and are deliberately treated differently.
func TestUnqualifiedReindexTableResolves(t *testing.T) {
	f := indexFixture(t)

	qualified := indexFindingByCode(f, indexCapture("REINDEX TABLE public.orders"), "RS-INDEX-020")
	unqualified := indexFindingByCode(f, indexCapture("REINDEX TABLE orders"), "RS-INDEX-020")

	if qualified == nil {
		t.Fatal("qualified REINDEX TABLE produced no RS-INDEX-020; cannot detect the bug")
	}
	if unqualified == nil {
		t.Fatal("`REINDEX TABLE orders` produced NO RS-INDEX-020, but the qualified form did")
	}
	if unqualified.DependsOn[0] != qualified.DependsOn[0] {
		t.Errorf("depends_on differs: unqualified=%v qualified=%v", unqualified.DependsOn, qualified.DependsOn)
	}

	// REINDEX INDEX still resolves by index name, unaffected by table resolution.
	if fnd := indexFindingByCode(f, indexCapture("REINDEX INDEX orders_email_idx"), "RS-INDEX-020"); fnd == nil {
		t.Error("REINDEX INDEX <index-name> must still be found by index name")
	}
}
