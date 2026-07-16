package findings

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestRSPerfCascadeFanout: a DELETE on a parent referenced ON DELETE CASCADE with
// a long-tailed fan-out fires RS-PERF-001 (WARN) with the fan-out as evidence.
func TestRSPerfCascadeFanout(t *testing.T) {
	f, mig := loadCorpus(t, "cascade_delete_fanout")
	got := rsPerf{}.Analyze(f, plainCapture(mig))
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-PERF finding, got %d", len(got))
	}
	fnd := got[0]
	if fnd.Code != "RS-PERF-001" || fnd.Severity != verdict.SeverityWarn {
		t.Errorf("want RS-PERF-001 warn, got %s/%s", fnd.Code, fnd.Severity)
	}
	ev, _ := fnd.Evidence.(map[string]any)
	if ev["fanout_max"] == nil || ev["fanout_mean"] == nil {
		t.Errorf("evidence must carry the fan-out mean and max, got %v", ev)
	}
	if len(fnd.DependsOn) != 1 || fnd.DependsOn[0] != "public.orders.rows" {
		t.Errorf("depends_on = %v, want [public.orders.rows]", fnd.DependsOn)
	}
}

// TestRSPerfUniformFanoutNotFlagged: a uniform fan-out (max close to mean) is not
// a cascade risk and is not flagged.
func TestRSPerfUniformFanoutNotFlagged(t *testing.T) {
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 1000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
  public.orders:
    rows: {value: 5000, confidence: exact}
    columns:
      user_id: {type: bigint, nullable: false}
    references:
      - {column: user_id, to: public.users.id, on_delete: cascade, fanout: {mean: 5, p50: 5, p95: 7, max: 9, confidence: measured}}
`))
	if err != nil {
		t.Fatal(err)
	}
	mig := "DELETE FROM public.users WHERE id < 10;"
	if got := (rsPerf{}).Analyze(f, plainCapture(mig)); len(got) != 0 {
		t.Errorf("a uniform fan-out must not be flagged, got %+v", got)
	}
}

// TestRSPerfCorpusVerdicts runs the RS-PERF analyzer against the dedicated perf-*
// corpus cases and asserts the expected verdict (the corpus triples land before
// / with the finding, per the ordering discipline).
func TestRSPerfCorpusVerdicts(t *testing.T) {
	for _, name := range []string{"perf-cascade-fanout", "perf-unqualified-update"} {
		t.Run(name, func(t *testing.T) {
			f, mig := loadCorpus(t, name)
			res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsPerf{}}, false)
			if res.Verdict != verdict.VerdictWarn {
				t.Errorf("verdict = %s, want WARN", res.Verdict)
			}
		})
	}
}

// TestRSPerfUnqualifiedDML: an UPDATE/DELETE with no WHERE on a large table fires
// RS-PERF-002; a scoped statement or a small table does not.
func TestRSPerfUnqualifiedDML(t *testing.T) {
	f, mig := loadCorpus(t, "perf-unqualified-update")
	got := rsPerf{}.Analyze(f, plainCapture(mig))
	if len(got) != 1 || got[0].Code != "RS-PERF-002" {
		t.Fatalf("expected RS-PERF-002, got %+v", got)
	}
	if got[0].Severity != verdict.SeverityWarn {
		t.Errorf("severity = %s, want warn", got[0].Severity)
	}

	// A WHERE clause scopes the change — not flagged.
	scoped := "UPDATE public.accounts SET status = 'active' WHERE id = 1;"
	if len(rsPerf{}.Analyze(f, plainCapture(scoped))) != 0 {
		t.Error("a scoped UPDATE must not fire RS-PERF-002")
	}

	// A small table — touching every row is cheap, not flagged.
	small, _ := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.flags: {rows: {value: 20, confidence: exact}, columns: {on: {type: boolean, nullable: false}}}
`))
	if len(rsPerf{}.Analyze(small, plainCapture("UPDATE public.flags SET on = true;"))) != 0 {
		t.Error("an unqualified UPDATE on a tiny table must not be flagged")
	}
}

// TestRSPerfDependsOnAndCapped: RS-PERF findings declare depends_on and carry the
// capped confidence of that dependency (RFC §7.4).
func TestRSPerfDependsOnAndCapped(t *testing.T) {
	f, mig := loadCorpus(t, "perf-unqualified-update")
	res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsPerf{}}, false)
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(res.Findings))
	}
	fnd := res.Findings[0]
	if len(fnd.DependsOn) != 1 || fnd.DependsOn[0] != "public.accounts.rows" {
		t.Errorf("depends_on = %v, want [public.accounts.rows]", fnd.DependsOn)
	}
	// accounts.rows is exact → the finding's capped confidence is exact.
	if fnd.Confidence != string(fixture.Exact) {
		t.Errorf("confidence = %q, want exact (capped by accounts.rows)", fnd.Confidence)
	}
}
