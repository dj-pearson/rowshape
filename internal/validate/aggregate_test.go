package validate

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
)

// verdict.Combine is unit-tested in isolation, but BuildResult — the real
// pipeline that assembles many analyzers' findings into one verdict — was only
// ever tested with a SINGLE finding. The genuinely interesting aggregations
// (one FAIL co-occurring with a WARN, two WARNs, the failed-apply floor
// combined with findings, and info-suppression alongside a surfacing warn) lived
// only in the DSN-gated corpus. These drive them offline by composing several
// single-finding fakeAnalyzers. docs/TESTING-GAPS.md item 8.

func finding(code, severity, title string) verdict.Finding {
	return verdict.Finding{Code: code, Severity: severity, Title: title}
}

// analyzers turns fixed findings into a slice of one-finding analyzers.
func analyzers(fs ...verdict.Finding) []Analyzer {
	out := make([]Analyzer, len(fs))
	for i, f := range fs {
		out[i] = fakeAnalyzer{f}
	}
	return out
}

func codesOf(r verdict.Result) map[string]bool {
	m := map[string]bool{}
	for _, f := range r.Findings {
		m[f.Code] = true
	}
	return m
}

// TestAggregateFailDominatesWarn: an error finding and a warn finding in the same
// run produce FAIL overall, and BOTH surface (a FAIL must never hide the WARNs
// that accompany it).
func TestAggregateFailDominatesWarn(t *testing.T) {
	f := estimatedFixture()
	res := BuildResult(f, &Capture{Success: true}, analyzers(
		finding("RS-DATA-020", verdict.SeverityError, "orphaned rows"),
		finding("RS-LOCK-001", verdict.SeverityWarn, "table rewrite"),
	), false)

	if res.Verdict != verdict.VerdictFail {
		t.Errorf("verdict = %s, want FAIL (one error dominates a co-occurring warn)", res.Verdict)
	}
	got := codesOf(res)
	if !got["RS-DATA-020"] || !got["RS-LOCK-001"] {
		t.Errorf("both findings must surface, got %v", got)
	}
}

// TestAggregateTwoWarns: two warn findings combine to WARN, both surfaced.
func TestAggregateTwoWarns(t *testing.T) {
	f := estimatedFixture()
	res := BuildResult(f, &Capture{Success: true}, analyzers(
		finding("RS-LOCK-001", verdict.SeverityWarn, "table rewrite"),
		finding("RS-INDEX-001", verdict.SeverityWarn, "non-concurrent index"),
	), false)

	if res.Verdict != verdict.VerdictWarn {
		t.Errorf("verdict = %s, want WARN (two warns combine to WARN, not FAIL)", res.Verdict)
	}
	if len(res.Findings) != 2 {
		t.Errorf("both warns must surface, got %d findings", len(res.Findings))
	}
}

// TestAggregateFailedApplyFloorWithFindings: a failed apply floors the verdict to
// FAIL even when the only detector finding is a WARN — the floor and the analyzer
// findings interact, a path the no-analyzers floor test never exercised. The WARN
// finding still surfaces alongside the floor.
func TestAggregateFailedApplyFloorWithFindings(t *testing.T) {
	f := estimatedFixture()
	res := BuildResult(f, &Capture{
		Success:    false,
		Statements: []Statement{{SQL: "ALTER ...", ErrCode: "23505", ErrMsg: "unique violation"}},
	}, analyzers(
		finding("RS-LOCK-001", verdict.SeverityWarn, "table rewrite"),
	), false)

	if res.Verdict != verdict.VerdictFail {
		t.Errorf("verdict = %s, want FAIL (failed apply floors even a WARN-only finding set)", res.Verdict)
	}
	if !codesOf(res)["RS-LOCK-001"] {
		t.Errorf("the warn finding must still surface under the failed-apply floor, got %v", codesOf(res))
	}
}

// TestAggregateInfoSuppressedAlongsideWarn: a clean info finding (certifying PASS
// on facts strong enough that capping cannot fire) is suppressed as the silent
// default, while a co-occurring warn surfaces — the mixed suppress/surface path,
// previously tested only in the all-suppressed case.
func TestAggregateInfoSuppressedAlongsideWarn(t *testing.T) {
	f := estimatedFixture()
	// The info finding depends on nothing, so capping has no weak fact to bite —
	// it stays a PASS and is suppressed.
	info := verdict.Finding{Code: "RS-DATA-014", Severity: verdict.SeverityInfo, Title: "unique OK"}
	warn := finding("RS-LOCK-001", verdict.SeverityWarn, "table rewrite")
	res := BuildResult(f, &Capture{Success: true}, analyzers(info, warn), false)

	if res.Verdict != verdict.VerdictWarn {
		t.Errorf("verdict = %s, want WARN", res.Verdict)
	}
	got := codesOf(res)
	if got["RS-DATA-014"] {
		t.Errorf("a clean PASS info finding should be suppressed, but it surfaced: %v", got)
	}
	if !got["RS-LOCK-001"] {
		t.Errorf("the warn must surface, got %v", got)
	}
	if len(res.Findings) != 1 {
		t.Errorf("exactly the warn should surface, got %d findings", len(res.Findings))
	}
}

// TestAggregateEmptyFindingsSlice: an analyzer that returns no findings does not
// perturb the verdict (a PASS with a clean apply stays PASS).
func TestAggregateEmptyFindingsSlice(t *testing.T) {
	f := estimatedFixture()
	res := BuildResult(f, &Capture{Success: true}, []Analyzer{fakeAnalyzerNoFindings{}}, false)
	if res.Verdict != verdict.VerdictPass || len(res.Findings) != 0 {
		t.Errorf("an analyzer with no findings = PASS, no findings; got %s / %d", res.Verdict, len(res.Findings))
	}
}

type fakeAnalyzerNoFindings struct{}

func (fakeAnalyzerNoFindings) Analyze(*fixture.Fixture, *Capture) []verdict.Finding { return nil }
