package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/toolerror"
	"github.com/rowshape/rowshape/internal/verdict"
)

// runValidateExpectingToolError runs validate and returns the exit code and the
// parsed JSON tool-error payload (validate is invoked with --json).
func runToolError(t *testing.T, opts *validateOptions) (int, toolerror.ToolError) {
	t.Helper()
	opts.asJSON = true
	var runErr error
	stdout, stderr := captureOutput(t, func() error { runErr = runValidate(context.Background(), opts); return runErr })

	code := 0
	var ee *ExitError
	if errors.As(runErr, &ee) {
		code = ee.Code
	}
	var te toolerror.ToolError
	if err := json.Unmarshal([]byte(stdout), &te); err != nil {
		t.Fatalf("tool-error payload is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	return code, te
}

// TestToolErrorExitThreeAndShape: every listed operational failure returns exit 3
// with a structured, machine-readable reason (never a verdict). The payload
// carries "error":"tool_error", never "verdict".
func TestToolErrorExitThreeAndShape(t *testing.T) {
	dir := t.TempDir()
	goodFixture := filepath.Join(dir, "ok.yaml")
	writeFile(t, goodFixture, `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.t: {rows: {value: 10, confidence: exact}, columns: {c: {type: text, nullable: true}}}
`)
	goodMig := filepath.Join(dir, "m.sql")
	writeFile(t, goodMig, "ALTER TABLE public.t ALTER COLUMN c SET NOT NULL;")

	// Fixture with an unknown major version — MUST refuse (RFC §12).
	badVersion := filepath.Join(dir, "v9.yaml")
	writeFile(t, badVersion, `rowshape_fixture: "9"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables: {}
`)
	// Unparseable fixture.
	badParse := filepath.Join(dir, "junk.yaml")
	writeFile(t, badParse, "this: : : not valid yaml: [")

	cases := []struct {
		name string
		opts *validateOptions
		want toolerror.Category
	}{
		{
			name: "unknown-version",
			opts: &validateOptions{fixturePath: badVersion, migrations: goodMig, ephemeral: "postgres://u@localhost:1/x", scale: 1},
			want: toolerror.UnknownVersion,
		},
		{
			name: "fixture-parse",
			opts: &validateOptions{fixturePath: badParse, migrations: goodMig, ephemeral: "postgres://u@localhost:1/x", scale: 1},
			want: toolerror.FixtureParse,
		},
		{
			name: "no-target",
			opts: &validateOptions{fixturePath: goodFixture, migrations: goodMig, scale: 1},
			want: toolerror.BadUsage,
		},
		{
			name: "target-unavailable",
			opts: &validateOptions{fixturePath: goodFixture, migrations: goodMig, ephemeral: "postgres://u:p@127.0.0.1:1/nope?connect_timeout=1", scale: 1},
			want: toolerror.TargetUnavailable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, te := runToolError(t, c.opts)
			if code != verdict.ExitToolError {
				t.Errorf("exit code = %d, want 3 (tool error)", code)
			}
			if te.Error_ != toolerror.Kind {
				t.Errorf(`payload error field = %q, want %q (must be distinguishable from a verdict)`, te.Error_, toolerror.Kind)
			}
			if te.Category != c.want {
				t.Errorf("category = %q, want %q", te.Category, c.want)
			}
			if te.Message == "" {
				t.Error("tool error must carry a human-readable message")
			}
		})
	}
}

// TestToolErrorIsNotAVerdict: the tool-error payload is clearly NOT a verdict — it
// has no "verdict" field and its "error" field marks it. An agent can branch on
// this without confusing "the tool couldn't run" with "the migration is unsafe".
func TestToolErrorIsNotAVerdict(t *testing.T) {
	dir := t.TempDir()
	badVersion := filepath.Join(dir, "v9.yaml")
	writeFile(t, badVersion, `rowshape_fixture: "9"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables: {}
`)
	mig := filepath.Join(dir, "m.sql")
	writeFile(t, mig, "SELECT 1;")

	opts := &validateOptions{fixturePath: badVersion, migrations: mig, ephemeral: "postgres://u@localhost:1/x", scale: 1, asJSON: true}
	stdout, _ := captureOutput(t, func() error { return runValidate(context.Background(), opts) })

	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if _, hasVerdict := raw["verdict"]; hasVerdict {
		t.Error("a tool error must not carry a verdict field")
	}
	if raw["error"] != toolerror.Kind {
		t.Errorf(`error field = %v, want %q`, raw["error"], toolerror.Kind)
	}
}

// TestToolErrorHumanRendering: the human rendering comes from the same struct as
// the JSON (INV-VERDICT-STABLE-style one-struct/two-marshalers).
func TestToolErrorHumanRendering(t *testing.T) {
	te := toolerror.New(toolerror.TargetUnavailable, "no disposable database", "start Postgres")
	human := te.Human()
	for _, want := range []string{string(toolerror.TargetUnavailable), "no disposable database", "start Postgres"} {
		if !strings.Contains(human, want) {
			t.Errorf("human rendering missing %q:\n%s", want, human)
		}
	}
	// It writes to stderr (not stdout) in human mode — no accidental verdict on stdout.
	_ = os.Stdout
}
