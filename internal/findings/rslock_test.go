package findings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// loadCorpus reads a corpus case's fixture and migration.
func loadCorpus(t *testing.T, name string) (*fixture.Fixture, string) {
	t.Helper()
	dir := filepath.Join("..", "..", "corpus", "cases", name)
	fx, err := os.ReadFile(filepath.Join(dir, "fixture.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f, err := fixture.Parse(fx)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	mig, err := os.ReadFile(filepath.Join(dir, "migration.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return f, string(mig)
}

// captureOf builds a synthetic capture of applying a migration: the rewrite
// statement is recorded with the ACCESS EXCLUSIVE lock it takes and a small
// hydrated basis, standing in for a real run.
func captureOf(migration, table string, hydrated int64) *validate.Capture {
	var stmts []validate.Statement
	for _, s := range validate.SplitStatements(migration) {
		stmts = append(stmts, validate.Statement{SQL: s, LockMode: "AccessExclusiveLock", DurationMs: 90})
	}
	return &validate.Capture{Success: true, Statements: stmts, TableRows: map[string]int64{table: hydrated}}
}

// TestRSLockVolatileDefault: RS-LOCK-001 fires on the volatile-default rewrite
// corpus case with the right lock mode, rows_rewritten evidence, a bucketed
// duration with basis, and the expand/backfill/contract remediation (PRD §10).
func TestRSLockVolatileDefault(t *testing.T) {
	f, mig := loadCorpus(t, "rslock-volatile-default")
	c := captureOf(mig, "public.orders", 12_000)

	got := rsLock{}.Analyze(f, c)
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-LOCK finding, got %d", len(got))
	}
	fnd := got[0]
	if fnd.Code != "RS-LOCK-001" {
		t.Errorf("code = %s, want RS-LOCK-001", fnd.Code)
	}
	ev, _ := fnd.Evidence.(map[string]any)
	if ev["lock_mode"] != "ACCESS EXCLUSIVE" {
		t.Errorf("lock_mode evidence = %v, want ACCESS EXCLUSIVE", ev["lock_mode"])
	}
	if ev["rows_rewritten"] != int64(5_000_000) {
		t.Errorf("rows_rewritten evidence = %v, want 5000000", ev["rows_rewritten"])
	}
	if fnd.Estimate == nil || fnd.Estimate.Bucket == "" || fnd.Estimate.BasisRows != 12_000 || fnd.Estimate.DeclaredRows != 5_000_000 {
		t.Errorf("estimate must be a bucket with basis, got %+v", fnd.Estimate)
	}
	for _, want := range []string{"backfill", "batches"} {
		if !strings.Contains(strings.ToLower(fnd.Remediation), want) {
			t.Errorf("remediation must be the expand/backfill/contract recipe, missing %q: %q", want, fnd.Remediation)
		}
	}
	if len(fnd.DependsOn) != 1 || fnd.DependsOn[0] != "public.orders.rows" {
		t.Errorf("depends_on = %v, want [public.orders.rows]", fnd.DependsOn)
	}
}

// TestRSLockCappedVerdict: run through the pipeline, the finding produces a WARN
// (matching the corpus) and carries the capped confidence of its dependency —
// public.orders.rows is exact, so the finding is exact.
func TestRSLockCappedVerdict(t *testing.T) {
	f, mig := loadCorpus(t, "rslock-volatile-default")
	c := captureOf(mig, "public.orders", 12_000)

	res := validate.BuildResult(f, c, []validate.Analyzer{rsLock{}}, false)
	if res.Verdict != verdict.VerdictWarn {
		t.Errorf("verdict = %s, want WARN (matches corpus expected.json)", res.Verdict)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected the RS-LOCK finding to surface, got %d", len(res.Findings))
	}
	if res.Findings[0].Confidence != string(fixture.Exact) {
		t.Errorf("finding confidence = %q, want exact (capped by public.orders.rows)", res.Findings[0].Confidence)
	}
}

// TestRSLockTypeChange: an in-place column type change rewrites the table and
// fires RS-LOCK with the online-swap remediation.
func TestRSLockTypeChange(t *testing.T) {
	f, mig := loadCorpus(t, "rslock-type-change")
	c := captureOf(mig, "public.events", 9_000)

	got := rsLock{}.Analyze(f, c)
	if len(got) != 1 {
		t.Fatalf("expected 1 RS-LOCK finding for the type change, got %d", len(got))
	}
	if !strings.Contains(strings.ToLower(got[0].Remediation), "type") {
		t.Errorf("type-change remediation should describe the online type swap: %q", got[0].Remediation)
	}
}

// TestRSLockVersionConditional: the SAME non-volatile-default ADD COLUMN fires on
// PG 10 (rewrite) but not on PG 11 or PG 16 (catalog fast-path) — RS-LOCK is
// version-conditional (RFC §9.1, PRD §12). A volatile default fires on all.
func TestRSLockVersionConditional(t *testing.T) {
	base := `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "%s"}}
tables:
  public.t:
    rows: {value: 4000000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
`
	fixtureAt := func(t *testing.T, ver string) *fixture.Fixture {
		f, err := fixture.Parse([]byte(strings.Replace(base, "%s", ver, 1)))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	nonVolatile := "ALTER TABLE public.t ADD COLUMN flag boolean NOT NULL DEFAULT false;"
	volatile := "ALTER TABLE public.t ADD COLUMN token uuid NOT NULL DEFAULT gen_random_uuid();"

	count := func(t *testing.T, ver, mig string) int {
		f := fixtureAt(t, ver)
		c := captureOf(mig, "public.t", 10_000)
		return len(rsLock{}.Analyze(f, c))
	}

	// Non-volatile default: rewrite pre-11, catalog fast-path on 11 and 16.
	if got := count(t, "10", nonVolatile); got != 1 {
		t.Errorf("PG 10 non-volatile default must fire RS-LOCK (rewrite), got %d findings", got)
	}
	if got := count(t, "11", nonVolatile); got != 0 {
		t.Errorf("PG 11 non-volatile default must NOT fire (catalog fast-path), got %d", got)
	}
	if got := count(t, "16", nonVolatile); got != 0 {
		t.Errorf("PG 16 non-volatile default must NOT fire (catalog fast-path), got %d", got)
	}
	// Volatile default: rewrites on every version.
	for _, ver := range []string{"10", "11", "16"} {
		if got := count(t, ver, volatile); got != 1 {
			t.Errorf("PG %s volatile default must fire RS-LOCK on every version, got %d", ver, got)
		}
	}
}

// TestUnqualifiedTableGetsTheSameEstimate is the defect resolution exists to
// prevent, pinned end to end rather than at the resolver.
//
// `ALTER TABLE orders` and `ALTER TABLE public.orders` are the same statement.
// Before resolution the unqualified form missed the fixture key (RFC §5 keys
// tables by qualified name), the analyzer read the zero value, and a 50M-row
// rewrite was reported as `instant` instead of `outage` — a confident, materially
// wrong answer to the exact question rowshape is for. Both verdicts were WARN, so
// only the estimate gave it away.
//
// The capture below carries what a real validate run has and a bare fixture does
// not: a hydrated row count and a measured duration. Without those, estimateFor
// extrapolates from 1ms at declared==hydrated and every bucket is `instant` — an
// earlier version of this test omitted them and passed against the bug.
func TestUnqualifiedTableGetsTheSameEstimate(t *testing.T) {
	f := &fixture.Fixture{
		Meta: fixture.Meta{Engine: fixture.Engine{Name: "postgres", Version: "16"}},
		Tables: map[string]fixture.Table{
			"public.orders": {
				Rows:    fixture.Fact[int64]{Value: 50_000_000, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{"id": {Type: "bigint"}},
			},
		},
	}
	const stmt = `ALTER TABLE %s ADD COLUMN note text DEFAULT clock_timestamp()::text NOT NULL`

	bucketFor := func(t *testing.T, table string) string {
		t.Helper()
		c := &validate.Capture{
			Success:    true,
			Statements: []validate.Statement{{SQL: fmt.Sprintf(stmt, table), DurationMs: 40}},
			// 2,000 rows were hydrated and the rewrite took 40ms; production
			// declares 50M. That is a 25,000x extrapolation — an outage.
			TableRows: map[string]int64{"public.orders": 2000},
		}
		for _, fnd := range (rsLock{}).Analyze(f, c) {
			if fnd.Estimate != nil {
				return fnd.Estimate.Bucket
			}
		}
		return ""
	}

	qualified := bucketFor(t, "public.orders")
	unqualified := bucketFor(t, "orders")
	t.Logf("qualified=%q unqualified=%q", qualified, unqualified)

	// Guard the guard: if the qualified form is not itself alarming, the
	// comparison below proves nothing.
	if qualified == "" || qualified == "instant" {
		t.Fatalf("qualified estimate = %q; a 50M-row rewrite extrapolated from 40ms/2k rows must be "+
			"alarming or this test cannot detect the bug", qualified)
	}
	if unqualified != qualified {
		t.Errorf("estimate for `ALTER TABLE orders` = %q, but `ALTER TABLE public.orders` = %q — "+
			"the same statement on the same 50M-row table. The unqualified form read rows=0 and would "+
			"tell a user an outage is %q.", unqualified, qualified, unqualified)
	}
}
