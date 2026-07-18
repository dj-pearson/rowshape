package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/corpus/harness"
	"github.com/rowshape/rowshape/internal/findings"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestExplainEveryCode: `explain <CODE>` returns structured docs + remediation
// for every shipped finding code, and the remediation is non-empty (a finding an
// agent can't act on is a bug — PRD §10).
func TestExplainEveryCode(t *testing.T) {
	codes := findings.Codes()
	if len(codes) == 0 {
		t.Fatal("no finding codes registered")
	}
	for _, code := range codes {
		var buf bytes.Buffer
		if err := explainCode(&buf, code, false); err != nil {
			t.Errorf("explain %s errored: %v", code, err)
			continue
		}
		out := buf.String()
		e, _ := findings.Explain(code)
		if e.Remediation == "" {
			t.Errorf("%s has no remediation", code)
		}
		for _, want := range []string{code, e.Title, e.Remediation} {
			if !strings.Contains(out, want) {
				t.Errorf("explain %s output missing %q:\n%s", code, want, out)
			}
		}
	}
}

// TestExplainJSON: --json emits the structured explanation.
func TestExplainJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := explainCode(&buf, "rs-lock-001", true); err != nil { // case-insensitive
		t.Fatalf("explain --json errored: %v", err)
	}
	for _, want := range []string{`"code"`, `"RS-LOCK-001"`, `"remediation"`, `"references"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("json explanation missing %q:\n%s", want, buf.String())
		}
	}
}

// TestExplainUnknownCode: an unknown code is a tool error, not a silent success.
func TestExplainUnknownCode(t *testing.T) {
	var buf bytes.Buffer
	err := explainCode(&buf, "RS-NOPE-999", false)
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("unknown code should be a tool error, got %v", err)
	}
	if ee.Code != verdict.ExitToolError {
		t.Errorf("exit code = %d, want %d", ee.Code, verdict.ExitToolError)
	}
}

// TestExplainUnknownCodeHonorsJSON: CR-T16. --json was honored on the success
// path and ignored on the unknown-code path, which wrote plain text. That is the
// one path an agent must handle programmatically — an unknown code is exactly
// what an agent hits when it mistypes or invents one — so it was the worst place
// to hand back prose. Every other command's failure path returns structured JSON.
func TestExplainUnknownCodeHonorsJSON(t *testing.T) {
	var runErr error
	stdout, stderr := captureOutput(t, func() error {
		runErr = explainCode(os.Stdout, "RS-NOPE-999", true)
		return runErr
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("--json must emit parseable JSON on an unknown code; stdout=%q stderr=%q err=%v",
			stdout, stderr, err)
	}
	if payload["error"] != "tool_error" {
		t.Errorf(`payload["error"] = %v, want "tool_error"`, payload["error"])
	}
	if msg, _ := payload["message"].(string); !strings.Contains(msg, "RS-NOPE-999") {
		t.Errorf("message should name the unknown code, got %q", msg)
	}
	// The known codes stay discoverable, so the caller can recover.
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "RS-LOCK-001") {
		t.Errorf("hint should list the known codes, got %q", hint)
	}

	// The exit code is part of the contract and must not change with --json.
	var ee *ExitError
	if !errors.As(runErr, &ee) || ee.Code != verdict.ExitToolError {
		t.Errorf("exit code = %v, want %d regardless of --json", runErr, verdict.ExitToolError)
	}
}

// TestExplainCoversEmittedCodes is the anti-drift guarantee: run every registered
// analyzer over every corpus case, and assert each finding it emits (a) has an
// explain entry, (b) cites that code in its Explain field, and (c) carries the
// catalog's remediation verbatim (capping may append a resolve command). This
// proves the finding and its explanation share one source and can never drift.
func TestExplainCoversEmittedCodes(t *testing.T) {
	cases, err := harness.LoadCases("../corpus")
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		var stmts []validate.Statement
		for _, s := range validate.SplitStatements(c.Migration) {
			stmts = append(stmts, validate.Statement{SQL: s, LockMode: "AccessExclusiveLock"})
		}
		cap := &validate.Capture{Success: true, Statements: stmts}
		res := validate.BuildResult(c.Fixture, cap, validate.Registered(), false)
		for _, f := range res.Findings {
			seen[f.Code] = true
			e, ok := findings.Explain(f.Code)
			if !ok {
				t.Errorf("case %s: finding %s has no explain entry", c.Name, f.Code)
				continue
			}
			if !strings.Contains(f.Explain, f.Code) {
				t.Errorf("case %s: finding %s Explain %q does not cite its code", c.Name, f.Code, f.Explain)
			}
			if !strings.Contains(f.Remediation, e.Remediation) {
				t.Errorf("case %s: finding %s remediation drifted from the catalog:\n finding: %q\n catalog: %q", c.Name, f.Code, f.Remediation, e.Remediation)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no findings were emitted across the corpus; the coverage check is vacuous")
	}
	t.Logf("verified explain coverage + no-drift for %d emitted codes", len(seen))
}
