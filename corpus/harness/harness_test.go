package harness

import (
	"path/filepath"
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

// TestCorpusVerdicts runs the migrations through `validate` and compares to the
// expected verdict — once P2-T7 wires the validator. Until then it is skipped,
// documenting that the corpus is asserted-ready.
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
			verdict, codes, err := validator.Validate(c)
			if err != nil {
				t.Fatalf("validate %s: %v", filepath.Base(c.Dir), err)
			}
			if verdict != c.Expected.Verdict {
				t.Errorf("verdict = %s, want %s", verdict, c.Expected.Verdict)
			}
			for _, want := range c.Expected.Findings {
				if !contains(codes, want.Code) {
					t.Errorf("missing expected finding %s (got %v)", want.Code, codes)
				}
			}
		})
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
