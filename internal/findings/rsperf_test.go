package findings

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
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
