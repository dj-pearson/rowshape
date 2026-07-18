package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

const exactSchema = "rowshape_p1b_t5"

func seedExact(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + exactSchema + ` CASCADE`,
		`CREATE SCHEMA ` + exactSchema,
		`CREATE TABLE ` + exactSchema + `.t (
			id bigint PRIMARY KEY,
			email text,       -- nullable but 0% null (the trap), all distinct
			category text)`, // nullable, ~14% null, low cardinality
		`INSERT INTO ` + exactSchema + `.t
			SELECT g, 'u'||g||'@x.invalid',
			       CASE WHEN g%7=0 THEN NULL ELSE (ARRAY['a','b','c'])[1+g%3] END
			FROM generate_series(1,3000) g`,
		`ANALYZE ` + exactSchema + `.t`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seedExact failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+exactSchema+` CASCADE`)
	})
}

// TestExactMode: a full pass makes null counts exact and distinct measured (HLL)
// for every column, probes uniqueness exactly, and records mode `exact` (RFC
// §7.3). Only aggregates are emitted — every fact is a count or a boolean, never
// a row value (RFC §13).
func TestExactMode(t *testing.T) {
	conn := adminConn(t)
	seedExact(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{exactSchema}, Exact: true})
	if err != nil {
		t.Fatalf("Fast(exact): %v", err)
	}

	if f.Meta.Profile.Mode != "exact" {
		t.Errorf("mode = %q, want exact", f.Meta.Profile.Mode)
	}
	if len(f.Meta.Profile.Escalated) != 0 {
		t.Errorf("exact mode should not populate escalated: %v", f.Meta.Profile.Escalated)
	}

	cols := f.Tables[exactSchema+".t"].Columns

	// email: nullable but 0% null — proven EXACT (the passes-staging-fails-prod
	// trap, now certain), distinct measured via HLL, unique proven true.
	email := cols["email"]
	if email.NullFraction == nil || email.NullFraction.Confidence != "exact" || email.NullFraction.Value != 0 {
		t.Errorf("email.null_fraction = %+v, want {0 exact}", email.NullFraction)
	}
	if email.Distinct == nil || email.Distinct.Confidence != "measured" || email.Distinct.Via != "hll" {
		t.Errorf("email.distinct = %+v, want measured via hll", email.Distinct)
	}
	if email.Unique == nil || !email.Unique.Value || email.Unique.Confidence != "exact" || email.Unique.Via != "probe" {
		t.Errorf("email.unique = %+v, want exact true via probe", email.Unique)
	}

	// category: ~14.3% null (exact), provably NOT unique.
	cat := cols["category"]
	if cat.NullFraction == nil || cat.NullFraction.Confidence != "exact" {
		t.Errorf("category.null_fraction = %+v, want exact", cat.NullFraction)
	}
	if cat.NullFraction.Value < 0.13 || cat.NullFraction.Value > 0.16 {
		t.Errorf("category null fraction = %.4f, want ~0.143", cat.NullFraction.Value)
	}
	if cat.Unique == nil || cat.Unique.Value || cat.Unique.Confidence != "exact" {
		t.Errorf("category.unique = %+v, want exact false", cat.Unique)
	}

	// id: proven by the PK constraint — the catalog proof is NOT overwritten by a
	// probe.
	id := cols["id"]
	if id.Unique == nil || id.Unique.Via != "constraint" {
		t.Errorf("id.unique = %+v, want via constraint (not re-probed)", id.Unique)
	}
}

// TestExactVsFast: the same table profiled fast vs exact differs exactly where it
// should — fast leans on estimated planner stats, exact proves the facts.
func TestExactVsFast(t *testing.T) {
	conn := adminConn(t)
	seedExact(t, conn)
	ctx := context.Background()

	fast, err := Fast(ctx, conn, Options{Schemas: []string{exactSchema}})
	if err != nil {
		t.Fatalf("fast: %v", err)
	}
	exact, err := Fast(ctx, conn, Options{Schemas: []string{exactSchema}, Exact: true})
	if err != nil {
		t.Fatalf("exact: %v", err)
	}

	if fast.Meta.Profile.Mode == "exact" {
		t.Errorf("fast mode should not be exact")
	}

	fastCat := fast.Tables[exactSchema+".t"].Columns["category"]
	exactCat := exact.Tables[exactSchema+".t"].Columns["category"]

	// Fast leans on the planner's estimate; exact proves it.
	if fastCat.NullFraction == nil || fastCat.NullFraction.Confidence != "estimated" {
		t.Errorf("fast category null_fraction = %+v, want estimated", fastCat.NullFraction)
	}
	if exactCat.NullFraction == nil || exactCat.NullFraction.Confidence != "exact" {
		t.Errorf("exact category null_fraction = %+v, want exact", exactCat.NullFraction)
	}
}

// --- CR-T28: --exact must upgrade the row count it paid for -----------------
//
// Exact mode did a full pass over every table and still left rows at the
// planner's `estimated`. The user bought minutes-to-hours of scanning and did
// not get the confidence upgrade it earns — and under INV-CONFIDENCE-CAPPING
// that upgrade is the difference between a finding that may certify PASS and one
// capped to WARN.
//
// This is the one change in the code-review phase that makes verdicts STRONGER,
// so these tests check the upgrade is EARNED, not just present.

// TestExactModeUpgradesRowCount: the count must be exact in value as well as in
// confidence. reltuples happens to be right for a freshly-ANALYZEd table, so a
// test that only compared the number could pass against the bug; it is the
// CONFIDENCE that distinguishes them.
func TestExactModeUpgradesRowCount(t *testing.T) {
	conn := adminConn(t)
	seedExact(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{exactSchema}, Exact: true})
	if err != nil {
		t.Fatalf("Fast(exact): %v", err)
	}
	tbl, ok := f.Tables[exactSchema+".t"]
	if !ok {
		t.Fatalf("table missing from fixture; have %v", tableNames(f.Tables))
	}
	if tbl.Rows.Confidence != fixture.Exact {
		t.Errorf("rows confidence = %q, want exact — a full pass was run and paid for",
			tbl.Rows.Confidence)
	}
	if tbl.Rows.Value != 3000 {
		t.Errorf("rows = %d, want exactly 3000 (the seeded count)", tbl.Rows.Value)
	}
}

// TestFastModeLeavesRowCountEstimated is the other half, and the one that
// actually protects the invariant: fast mode must NOT claim exact. A change that
// upgraded everywhere would look identical to this fix in the test above.
func TestFastModeLeavesRowCountEstimated(t *testing.T) {
	conn := adminConn(t)
	seedExact(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{exactSchema}})
	if err != nil {
		t.Fatalf("Fast(fast): %v", err)
	}
	tbl, ok := f.Tables[exactSchema+".t"]
	if !ok {
		t.Fatalf("table missing from fixture; have %v", tableNames(f.Tables))
	}
	if tbl.Rows.Confidence == fixture.Exact {
		t.Errorf("fast mode must not claim an exact row count (got %q); only a completed full "+
			"pass earns it, or capping would certify a PASS from the planner's guess",
			tbl.Rows.Confidence)
	}
}
