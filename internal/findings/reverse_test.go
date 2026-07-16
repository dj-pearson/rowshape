package findings

import (
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestRSReverseDropColumn: DROP COLUMN fires RS-REVERSE-001 (WARN) declaring the
// table's rows as what is lost, with mandatory remediation.
func TestRSReverseDropColumn(t *testing.T) {
	f, mig := loadCorpus(t, "reverse-drop-column")
	got := rsReverse{}.Analyze(f, plainCapture(mig))
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-REVERSE finding, got %d: %+v", len(got), got)
	}
	fnd := got[0]
	if fnd.Code != "RS-REVERSE-001" || fnd.Severity != verdict.SeverityWarn {
		t.Errorf("want RS-REVERSE-001 warn, got %s/%s", fnd.Code, fnd.Severity)
	}
	if len(fnd.DependsOn) != 1 || fnd.DependsOn[0] != "public.users.rows" {
		t.Errorf("depends_on = %v, want [public.users.rows]", fnd.DependsOn)
	}
	if strings.TrimSpace(fnd.Remediation) == "" {
		t.Error("RS-REVERSE-001 must carry mandatory remediation")
	}
}

// TestRSReverseDropTable: DROP TABLE fires RS-REVERSE-002 (WARN) with the row
// count as evidence and mandatory remediation.
func TestRSReverseDropTable(t *testing.T) {
	f, mig := loadCorpus(t, "reverse-drop-table")
	got := rsReverse{}.Analyze(f, plainCapture(mig))
	if len(got) != 1 || got[0].Code != "RS-REVERSE-002" {
		t.Fatalf("expected RS-REVERSE-002, got %+v", got)
	}
	fnd := got[0]
	if fnd.Severity != verdict.SeverityWarn {
		t.Errorf("severity = %s, want warn", fnd.Severity)
	}
	ev, _ := fnd.Evidence.(map[string]any)
	if ev["rows"] == nil {
		t.Errorf("evidence must carry the lost row count, got %v", ev)
	}
	if strings.TrimSpace(fnd.Remediation) == "" {
		t.Error("RS-REVERSE-002 must carry mandatory remediation")
	}
}

// TestRSReverseNarrowType: a narrowing type change fires RS-REVERSE-003; a
// widening change does not.
func TestRSReverseNarrowType(t *testing.T) {
	f, mig := loadCorpus(t, "reverse-narrow-type")
	got := rsReverse{}.Analyze(f, plainCapture(mig))
	if len(got) != 1 || got[0].Code != "RS-REVERSE-003" {
		t.Fatalf("expected RS-REVERSE-003, got %+v", got)
	}
	if got[0].Severity != verdict.SeverityWarn {
		t.Errorf("severity = %s, want warn", got[0].Severity)
	}

	// A widening change (integer -> bigint) restores no loss — not flagged.
	wide, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.ledger:
    rows: {value: 2500000, confidence: exact}
    columns:
      amount: {type: integer, nullable: false}
`))
	if err != nil {
		t.Fatal(err)
	}
	widen := "ALTER TABLE public.ledger ALTER COLUMN amount TYPE bigint;"
	if got := (rsReverse{}).Analyze(wide, plainCapture(widen)); len(got) != 0 {
		t.Errorf("a widening type change must not be flagged, got %+v", got)
	}
}

// TestRSReverseCorpusVerdicts runs the RS-REVERSE analyzer against the dedicated
// reverse-* corpus cases and asserts each expected verdict — the corpus triples
// land with the finding (the ordering discipline, PRD §14).
func TestRSReverseCorpusVerdicts(t *testing.T) {
	for _, name := range []string{"reverse-drop-column", "reverse-drop-table", "reverse-narrow-type"} {
		t.Run(name, func(t *testing.T) {
			f, mig := loadCorpus(t, name)
			res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsReverse{}}, false)
			if res.Verdict != verdict.VerdictWarn {
				t.Errorf("verdict = %s, want WARN", res.Verdict)
			}
		})
	}
}

// TestRSReverseDependsOnAndCapped: RS-REVERSE findings declare depends_on and
// carry the capped confidence of that dependency (RFC §7.4).
func TestRSReverseDependsOnAndCapped(t *testing.T) {
	f, mig := loadCorpus(t, "reverse-drop-table")
	res := validate.BuildResult(f, plainCapture(mig), []validate.Analyzer{rsReverse{}}, false)
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(res.Findings))
	}
	fnd := res.Findings[0]
	if len(fnd.DependsOn) != 1 || fnd.DependsOn[0] != "public.audit_log.rows" {
		t.Errorf("depends_on = %v, want [public.audit_log.rows]", fnd.DependsOn)
	}
	// audit_log.rows is exact → the finding's capped confidence is exact.
	if fnd.Confidence != string(fixture.Exact) {
		t.Errorf("confidence = %q, want exact (capped by audit_log.rows)", fnd.Confidence)
	}
}

// TestRSReverseRemediationInCatalog: every RS-REVERSE code the analyzer can emit
// has a catalog entry, so `rowshape explain` and the finding's remediation are
// the same text (no drift).
func TestRSReverseRemediationInCatalog(t *testing.T) {
	for _, code := range []string{"RS-REVERSE-001", "RS-REVERSE-002", "RS-REVERSE-003"} {
		e, ok := Explain(code)
		if !ok {
			t.Errorf("%s has no catalog entry", code)
			continue
		}
		if strings.TrimSpace(e.Remediation) == "" {
			t.Errorf("%s catalog entry has empty remediation", code)
		}
	}
}
