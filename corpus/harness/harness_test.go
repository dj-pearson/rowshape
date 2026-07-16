package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

// corpusRoot is the corpus directory relative to this test.
const corpusRoot = ".."

// requiredPathologies are the ways a Postgres migration breaks that the corpus
// MUST cover (PRD §12). The corpus is written before the findings that consume it.
var requiredPathologies = []string{
	"volatile_default_rewrite",
	"set_not_null_fullscan",
	"unique_index_cant_build",
	"validate_orphans",
	"cascade_delete_fanout",
	"not_valid_validated_same_tx",
}

// validator is the hook `validate` fills in P2-T7. While nil, the harness asserts
// triple well-formedness and coverage; once set, it runs each migration and
// compares the verdict to expected.json.
var validator Validator

// TestCorpusWellFormed: every triple loads and is internally consistent — a real
// migration, a parseable fixture with an engine version, and an expected verdict
// using only known verdicts and finding codes.
func TestCorpusWellFormed(t *testing.T) {
	cases, err := LoadCases(corpusRoot)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	for _, c := range cases {
		if err := c.Validate(); err != nil {
			t.Errorf("case %s is malformed: %v", c.Name, err)
		}
	}
	t.Logf("loaded %d corpus cases (PG %s)", len(cases), PGMajor())
}

// TestCorpusCoversPathologies: at least one triple exists per documented
// pathology (PRD §12).
func TestCorpusCoversPathologies(t *testing.T) {
	cases, err := LoadCases(corpusRoot)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	have := map[string]bool{}
	for _, c := range cases {
		have[c.Name] = true
	}
	for _, p := range requiredPathologies {
		if !have[p] {
			t.Errorf("corpus is missing a triple for pathology %q", p)
		}
	}
}

// TestCorpusExpectedFindings: each triple names at least one expected finding, so
// a passing verdict is never the silent default.
func TestCorpusExpectedFindings(t *testing.T) {
	cases, _ := LoadCases(corpusRoot)
	for _, c := range cases {
		if c.Expected.Verdict != "PASS" && len(c.Expected.Findings) == 0 {
			t.Errorf("case %s expects verdict %s but names no findings", c.Name, c.Expected.Verdict)
		}
	}
}

// TestCappingContract asserts the wrong-PASS regression suite ENCODES the RFC
// §7.4 contract, before any finding exists to satisfy it (PRD §14 ordering):
//   - a case resting on an estimated/unproven fact must expect WARN (never PASS)
//     and name a resolving command, and
//   - its proven-fact boundary twin must expect PASS.
//
// This is green now (it validates the contract's encoding); the runtime check
// that findings honor it is TestCorpusVerdicts, RED until the capping engine
// (P2-T4) lands.
func TestCappingContract(t *testing.T) {
	cases, err := LoadCases(corpusRoot)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	seenCapped, seenBoundary := false, false
	for _, c := range cases {
		if !strings.HasPrefix(c.Name, "capping-") {
			continue
		}
		capped := strings.Contains(c.Name, "estimated") || strings.Contains(c.Name, "unproven")
		if capped {
			seenCapped = true
			if c.Expected.Verdict != "WARN" {
				t.Errorf("%s rests on an estimated/unproven fact: expected WARN, got %s (a PASS here would be a wrong PASS)", c.Name, c.Expected.Verdict)
			}
			if !namesResolveCommand(c.Expected) {
				t.Errorf("%s must name a resolving command (resolve_contains) so the WARN is actionable (§7.4)", c.Name)
			}
		} else {
			seenBoundary = true
			if c.Expected.Verdict != "PASS" {
				t.Errorf("%s rests on a proven fact: expected PASS boundary, got %s", c.Name, c.Expected.Verdict)
			}
		}
	}
	if !seenCapped || !seenBoundary {
		t.Errorf("capping suite must include both a capped (WARN) case and a proven (PASS) boundary; capped=%v boundary=%v", seenCapped, seenBoundary)
	}
}

func namesResolveCommand(e Expected) bool {
	for _, f := range e.Findings {
		if f.ResolveContains != "" {
			return true
		}
	}
	return false
}

// TestCorpusVerdicts runs the migrations through `validate` and compares to the
// expected verdict AND the capping contract (a capped WARN's remediation must
// name the resolving command) — once P2-T7 wires the validator. Until then it is
// skipped; for the capping cases it stays RED until P2-T4's capping engine makes
// them pass (PRD §14).
func TestCorpusVerdicts(t *testing.T) {
	if validator == nil {
		t.Skip("validate not yet wired (P2-T7): corpus verdict comparison pending")
	}
	cases, err := LoadCases(corpusRoot)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			verdict, findings, err := validator.Validate(c)
			if err != nil {
				t.Fatalf("validate %s: %v", filepath.Base(c.Dir), err)
			}
			if verdict != c.Expected.Verdict {
				t.Errorf("verdict = %s, want %s", verdict, c.Expected.Verdict)
			}
			for _, want := range c.Expected.Findings {
				got := findingByCode(findings, want.Code)
				if got == nil {
					t.Errorf("missing expected finding %s", want.Code)
					continue
				}
				if want.ResolveContains != "" && !strings.Contains(got.Remediation, want.ResolveContains) {
					t.Errorf("%s remediation %q must name the resolving command %q (§7.4)", want.Code, got.Remediation, want.ResolveContains)
				}
			}
		})
	}
}

func findingByCode(findings []ProducedFinding, code string) *ProducedFinding {
	for i := range findings {
		if findings[i].Code == code {
			return &findings[i]
		}
	}
	return nil
}
