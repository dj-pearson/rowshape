package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal JSON verdict, as `rowshape validate --json` would emit.
const sampleVerdictJSON = `{
  "rowshape": "1",
  "verdict": "FAIL",
  "fixture": {"id": "orders-2026", "digest": "sha256:abcdef0123456789"},
  "duration_ms": 88,
  "findings": [
    {
      "code": "RS-LOCK-001",
      "severity": "error",
      "title": "rewrites the table under ACCESS EXCLUSIVE",
      "location": {"file": "db/migrations/003.sql", "line": 7},
      "estimate": {"bucket": "outage"},
      "remediation": "Split into add-nullable, backfill, then SET NOT NULL."
    }
  ]
}`

// TestAnnotateFromFile: annotate reads a verdict file, writes an inline
// annotation to stdout, and appends the Markdown summary to --summary.
func TestAnnotateFromFile(t *testing.T) {
	dir := t.TempDir()
	vp := filepath.Join(dir, "verdict.json")
	writeFile(t, vp, sampleVerdictJSON)
	summary := filepath.Join(dir, "summary.md")

	stdout, _ := captureOutput(t, func() error { return runAnnotate(vp, summary) })

	if !strings.Contains(stdout, "::error ") ||
		!strings.Contains(stdout, "file=db/migrations/003.sql") ||
		!strings.Contains(stdout, "line=7") {
		t.Errorf("expected an inline error annotation on stdout, got:\n%s", stdout)
	}

	body, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("summary not written: %v", err)
	}
	for _, want := range []string{"rowshape: FAIL", "RS-LOCK-001", "outage", "Split into add-nullable"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("summary missing %q:\n%s", want, body)
		}
	}
}

// TestAnnotateSummaryAppends: annotate appends to the summary file (GitHub's
// $GITHUB_STEP_SUMMARY is append-only across steps), preserving prior content.
func TestAnnotateSummaryAppends(t *testing.T) {
	dir := t.TempDir()
	vp := filepath.Join(dir, "verdict.json")
	writeFile(t, vp, sampleVerdictJSON)
	summary := filepath.Join(dir, "summary.md")
	writeFile(t, summary, "PRIOR CONTENT\n")

	_, _ = captureOutput(t, func() error { return runAnnotate(vp, summary) })

	body, _ := os.ReadFile(summary)
	if !strings.HasPrefix(string(body), "PRIOR CONTENT") {
		t.Errorf("summary should be appended, not truncated:\n%s", body)
	}
	if !strings.Contains(string(body), "rowshape: FAIL") {
		t.Errorf("summary content missing after append:\n%s", body)
	}
}

// TestAnnotateRejectsNonVerdict: non-JSON input is a tool error (exit 3), never
// a silent success — a broken input must not read as "no findings".
func TestAnnotateRejectsNonVerdict(t *testing.T) {
	dir := t.TempDir()
	vp := filepath.Join(dir, "bad.json")
	writeFile(t, vp, "this is not json")

	_, stderr := captureOutput(t, func() error {
		err := runAnnotate(vp, "")
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code != 3 {
			t.Errorf("expected a tool error (exit 3), got %v", err)
		}
		return err
	})
	if !strings.Contains(stderr, "not a JSON verdict") {
		t.Errorf("expected a parse tool-error on stderr, got:\n%s", stderr)
	}
}
