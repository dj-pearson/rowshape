package validate

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestSplitStatements: a SQL script splits on top-level semicolons only —
// semicolons inside strings, line comments, and dollar-quoted bodies do not
// split a statement.
func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"two", "ALTER TABLE a ADD c int; ALTER TABLE b ADD d int;", 2},
		{"trailing-none", "SELECT 1", 1},
		{"string-semicolon", "INSERT INTO t VALUES ('a;b'); SELECT 1;", 2},
		{"line-comment", "-- drop; this\nSELECT 1;", 1},
		{"dollar-quote", "CREATE FUNCTION f() RETURNS void AS $$ BEGIN raise notice 'x;y'; END; $$ LANGUAGE plpgsql; SELECT 1;", 2},
		{"block-comment", "/* a;b */ SELECT 1;", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitStatements(c.in)
			if len(got) != c.want {
				t.Errorf("SplitStatements(%q) = %d statements %v, want %d", c.in, len(got), got, c.want)
			}
		})
	}
}

// fakeAnalyzer emits one fixed finding, standing in for the P2-T8+ detectors.
type fakeAnalyzer struct{ f verdict.Finding }

func (a fakeAnalyzer) Analyze(*fixture.Fixture, *Capture) []verdict.Finding {
	return []verdict.Finding{a.f}
}

func estimatedFixture() *fixture.Fixture {
	return &fixture.Fixture{
		Meta: fixture.Meta{ID: "t", Digest: "sha256:abc"},
		Tables: map[string]fixture.Table{
			"public.users": {
				Rows: fixture.Fact[int64]{Value: 1_000_000, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{
					"email": {Type: "text", Distinct: &fixture.Fact[int64]{Value: 999_000, Confidence: fixture.Estimated}},
				},
			},
		},
	}
}

// TestBuildResultCaps: an info finding that wants to certify PASS but rests on an
// absent/estimated fact is capped to a resolving WARN (RFC §7.4). The overall
// verdict follows.
func TestBuildResultCaps(t *testing.T) {
	f := estimatedFixture()
	analyzer := fakeAnalyzer{verdict.Finding{
		Code:      "RS-DATA-014",
		Severity:  verdict.SeverityInfo,
		Title:     "ADD UNIQUE(email)",
		DependsOn: []string{"public.users.email.unique"}, // absent → weakest
	}}
	res := BuildResult(f, &Capture{Success: true}, []Analyzer{analyzer}, false)
	if res.Verdict != verdict.VerdictWarn {
		t.Errorf("verdict = %s, want WARN (capped)", res.Verdict)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected the capped WARN to surface, got %d findings", len(res.Findings))
	}
	if got := res.Findings[0]; got.Severity != verdict.SeverityWarn || got.Remediation == "" {
		t.Errorf("capped finding must be an actionable warn with a resolve command, got %+v", got)
	}
}

// TestBuildResultGroundTruth: against a provided live branch the evidence is
// exact, so the same finding is NOT capped — it certifies PASS (PRD §15).
func TestBuildResultGroundTruth(t *testing.T) {
	f := estimatedFixture()
	analyzer := fakeAnalyzer{verdict.Finding{
		Code:      "RS-DATA-014",
		Severity:  verdict.SeverityInfo,
		Title:     "ADD UNIQUE(email)",
		DependsOn: []string{"public.users.email.unique"},
	}}
	res := BuildResult(f, &Capture{Success: true}, []Analyzer{analyzer}, true)
	if res.Verdict != verdict.VerdictPass {
		t.Errorf("verdict = %s, want PASS (ground truth, capping cannot fire)", res.Verdict)
	}
	if len(res.Findings) != 0 {
		t.Errorf("a clean PASS certification should be the silent default, got %+v", res.Findings)
	}
}

// TestBuildResultEmptyIsPass: with no analyzers and a clean apply, the verdict is
// PASS — the orchestration returns a well-formed verdict before any detector
// exists (P2-T7 before P2-T8+).
func TestBuildResultEmptyIsPass(t *testing.T) {
	res := BuildResult(estimatedFixture(), &Capture{Success: true}, nil, false)
	if res.Verdict != verdict.VerdictPass || len(res.Findings) != 0 {
		t.Errorf("empty analyzers + clean apply = PASS with no findings, got %s / %d", res.Verdict, len(res.Findings))
	}
	if res.Rowshape != verdict.Rowshape {
		t.Errorf("result must carry the contract version tag")
	}
}

// TestBuildResultFailedApplyIsNeverPass: a migration that did not apply cleanly
// is floored to FAIL, never certified PASS — even with no analyzers.
func TestBuildResultFailedApplyIsNeverPass(t *testing.T) {
	cap := &Capture{Success: false, Statements: []Statement{{SQL: "ALTER ...", ErrCode: "23502", ErrMsg: "not null violation"}}}
	res := BuildResult(estimatedFixture(), cap, nil, false)
	if res.Verdict != verdict.VerdictFail {
		t.Errorf("a failed apply must floor to FAIL, got %s", res.Verdict)
	}
}

// TestConstraintViolationClass: a class-23 SQLSTATE is recognized as a constraint
// violation (the migration hitting real data), distinct from a tool error.
func TestConstraintViolationClass(t *testing.T) {
	if !(Statement{ErrCode: "23505"}).ConstraintViolation() {
		t.Error("23505 (unique_violation) must be a constraint violation")
	}
	if (Statement{ErrCode: "42P01"}).ConstraintViolation() {
		t.Error("42P01 (undefined_table) is not a constraint violation")
	}
}
