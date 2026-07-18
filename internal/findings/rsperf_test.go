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

// --- CR-T2: unqualified table names -----------------------------------------
//
// Every other analyzer routed SQL-derived table names through resolveTable;
// rsperf did not. RFC §5 keys fixture tables by QUALIFIED name, so an unqualified
// `UPDATE accounts ...` missed the key `public.accounts` — and the miss is
// silent, because the !ok branch reads "no such table" as "not a large table".
// The finding was not weakened, it was DROPPED. Unqualified DML is ordinary
// hand-written SQL (Postgres resolves it via search_path), so this is the common
// case, not the exotic one.

// perfFixture: a 6M-row parent with a long-tailed cascade child — big enough to
// trip RS-PERF-002's threshold and tailed enough to trip RS-PERF-001.
func perfFixture(t *testing.T) *fixture.Fixture {
	t.Helper()
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.accounts:
    rows: {value: 6000000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
      status: {type: text, nullable: true}
  public.events:
    rows: {value: 90000000, confidence: exact}
    columns:
      account_id: {type: bigint, nullable: false}
    references:
      - {column: account_id, to: public.accounts.id, on_delete: cascade, fanout: {mean: 15, p50: 8, p95: 400, max: 250000, confidence: measured}}
`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// findingsByCode returns the findings rsPerf produced for a migration, keyed by
// code, so each pathology can be compared independently (a DELETE with no WHERE
// legitimately trips both RS-PERF-001 and RS-PERF-002).
func perfFindingByCode(t *testing.T, f *fixture.Fixture, sql, code string) *verdict.Finding {
	t.Helper()
	for _, fnd := range (rsPerf{}).Analyze(f, plainCapture(sql)) {
		if fnd.Code == code {
			return &fnd
		}
	}
	return nil
}

// TestUnqualifiedMassDMLIsStillReported: `UPDATE accounts SET ...` and
// `UPDATE public.accounts SET ...` are the same statement on the same 6M-row
// table. Before resolution the unqualified form produced NO finding at all.
func TestUnqualifiedMassDMLIsStillReported(t *testing.T) {
	f := perfFixture(t)

	qualified := perfFindingByCode(t, f, "UPDATE public.accounts SET status = 'active';", "RS-PERF-002")
	unqualified := perfFindingByCode(t, f, "UPDATE accounts SET status = 'active';", "RS-PERF-002")

	// Guard the guard: if the qualified form does not fire, the comparison proves
	// nothing.
	if qualified == nil {
		t.Fatal("qualified UPDATE produced no RS-PERF-002; this test cannot detect the bug")
	}
	if unqualified == nil {
		t.Fatal("`UPDATE accounts` produced NO RS-PERF-002, but `UPDATE public.accounts` did — " +
			"the unqualified name missed the fixture key and the finding was silently dropped")
	}
	// Same table means same evidence, and DependsOn must cite the canonical key.
	qev, _ := qualified.Evidence.(map[string]any)
	uev, _ := unqualified.Evidence.(map[string]any)
	if qev["rows"] != uev["rows"] {
		t.Errorf("rows evidence differs: qualified=%v unqualified=%v", qev["rows"], uev["rows"])
	}
	if len(unqualified.DependsOn) != 1 || unqualified.DependsOn[0] != "public.accounts.rows" {
		t.Errorf("depends_on = %v, want [public.accounts.rows] (the canonical fixture key, which is "+
			"what a DSSE-signed attestation should cite)", unqualified.DependsOn)
	}
}

// TestUnqualifiedDeleteStillFindsCascadeFanout: the cascade check compares the
// DELETE's target against `refParent(ref.To)`, which is always qualified because
// it comes from the fixture. An unqualified target could never match, so
// RS-PERF-001 was dropped for exactly the statement most likely to cause the
// outage it describes.
func TestUnqualifiedDeleteStillFindsCascadeFanout(t *testing.T) {
	f := perfFixture(t)

	qualified := perfFindingByCode(t, f, "DELETE FROM public.accounts;", "RS-PERF-001")
	unqualified := perfFindingByCode(t, f, "DELETE FROM accounts;", "RS-PERF-001")

	if qualified == nil {
		t.Fatal("qualified DELETE produced no RS-PERF-001; this test cannot detect the bug")
	}
	if unqualified == nil {
		t.Fatal("`DELETE FROM accounts` produced NO RS-PERF-001, but `DELETE FROM public.accounts` did")
	}
	if qualified.Title != unqualified.Title {
		t.Errorf("same statement, different finding:\n qualified: %s\n unqualified: %s",
			qualified.Title, unqualified.Title)
	}
}

// TestUnresolvableTableProducesNoPerfFinding: resolution must not INVENT a match.
// A table the fixture has never seen stays unresolved, finds no facts, and
// produces no finding — rather than being answered from some other table.
func TestUnresolvableTableProducesNoPerfFinding(t *testing.T) {
	f := perfFixture(t)
	if fnd := perfFindingByCode(t, f, "UPDATE typo_nonexistent SET status = 'x';", "RS-PERF-002"); fnd != nil {
		t.Errorf("an unknown table must not produce a finding, got %+v", fnd)
	}
}
