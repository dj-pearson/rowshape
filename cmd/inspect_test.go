package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leakyFixture has one of every value-derived field plus a "plain" column with
// none, so the audit can be checked for completeness AND for not inventing leaks.
const leakyFixture = `
rowshape_fixture: "1"
meta:
  id: leaky
  engine: {name: postgres, version: "16"}
  privacy: permissive
  profile: {mode: fast, escalated: []}
tables:
  public.t:
    rows: 1000
    columns:
      amount: {type: integer, nullable: false, distinct: 900, range: {min: 0, max: 4999, mean: 82.3}}
      scores: {type: integer, nullable: false, distinct: 900, histogram: {buckets: 2, bounds: [0, 1, 100]}}
      status: {type: text, nullable: false, distinct: 3, format: enum_like, values: [active, canceled, trialing], frequencies: [0.7, 0.2, 0.1]}
      notes:  {type: text, nullable: true, distinct: 950, format: free_text, length: {min: 3, max: 240, mean: 40}}
      plain:  {type: bigint, nullable: false, distinct: 1000}
    constraints:
      - {name: t_status_check, kind: check, expression: "status = ANY (ARRAY['active'::text])"}
      - {name: t_pkey, kind: primary_key, columns: [plain]}
`

func runInspect(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// TestInspectLeaksComplete: the audit lists every value-derived field and nothing
// spurious (RFC §8.3).
func TestInspectLeaksComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leaky.yaml")
	if err := os.WriteFile(path, []byte(leakyFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	out, err := runInspect(t, "inspect", "--leaks", path)
	if err != nil {
		t.Fatalf("inspect --leaks: %v\n%s", err, out)
	}
	t.Log("\n" + out)

	// Every seeded leak must appear, with its location.
	wantLines := []struct{ loc, field, privacy string }{
		{"public.t.amount", "range", "standard"},
		{"public.t.scores", "histogram", "standard"},
		{"public.t.status", "values", "permissive"},
		{"public.t.status", "frequencies", "permissive"},
		{"public.t.notes", "length.max", "strict"},
		{"public.t (constraint t_status_check)", "check_expression", "standard"},
	}
	for _, w := range wantLines {
		if !containsRow(out, w.loc, w.field, w.privacy) {
			t.Errorf("missing leak: %s %s %s\n%s", w.loc, w.field, w.privacy, out)
		}
	}

	// It reports exactly six — nothing spurious. The `plain` column and the PK
	// constraint contribute nothing.
	if !strings.Contains(out, "6 value-derived field(s)") {
		t.Errorf("expected exactly 6 leaks reported:\n%s", out)
	}
	if strings.Contains(out, "plain") {
		t.Errorf("the `plain` column has no value-derived fields and must not appear:\n%s", out)
	}
	if strings.Contains(out, "t_pkey") {
		t.Errorf("a primary key is not a leak and must not appear:\n%s", out)
	}
}

// TestInspectFailOnLeak: --fail-on-leak exits non-zero when any value-derived
// field is present (CI gating), and cleanly when there are none.
func TestInspectFailOnLeak(t *testing.T) {
	dir := t.TempDir()
	leaky := filepath.Join(dir, "leaky.yaml")
	_ = os.WriteFile(leaky, []byte(leakyFixture), 0o644)

	_, err := runInspect(t, "inspect", "--leaks", "--fail-on-leak", leaky)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code == 0 {
		t.Errorf("--fail-on-leak should exit non-zero on a leaky fixture, got %v", err)
	}

	// A structure-only fixture has no leaks and exits cleanly.
	clean := filepath.Join(dir, "clean.yaml")
	const cleanFixture = `
rowshape_fixture: "1"
meta:
  id: clean
  engine: {name: postgres, version: "16"}
  privacy: strict
  profile: {mode: fast, escalated: []}
tables:
  public.t:
    rows: 100
    columns:
      id: {type: bigint, nullable: false, distinct: 100, unique: {value: true, confidence: exact, via: constraint}}
    constraints: [{name: t_pkey, kind: primary_key, columns: [id]}]
`
	_ = os.WriteFile(clean, []byte(cleanFixture), 0o644)
	out, err := runInspect(t, "inspect", "--leaks", "--fail-on-leak", clean)
	if err != nil {
		t.Errorf("clean fixture should exit 0, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "No value-derived fields") {
		t.Errorf("clean fixture should report no leaks:\n%s", out)
	}
}

// containsRow reports whether the tabwriter output has a row with all three
// fields (order-independent within the line).
func containsRow(out, loc, field, privacy string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, loc) && strings.Contains(line, field) && strings.Contains(line, privacy) {
			return true
		}
	}
	return false
}
