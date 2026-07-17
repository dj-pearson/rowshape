package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
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
