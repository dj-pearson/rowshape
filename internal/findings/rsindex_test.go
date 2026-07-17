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
