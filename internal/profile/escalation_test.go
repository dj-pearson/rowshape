package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

// TestEscalationPredicate: the predicate selects columns that look unique but
// are unproven, and nothing else (RFC §7.3).
func TestEscalationPredicate(t *testing.T) {
	est := func(v int64) *fixture.Fact[int64] {
		return &fixture.Fact[int64]{Value: v, Confidence: fixture.Estimated}
	}
	proven := &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact, Via: "constraint"}

	cases := []struct {
		name string
		col  fixture.Column
		rows int64
		want bool
	}{
		{"looks-unique-unproven", fixture.Column{Distinct: est(9600)}, 10000, true},
		{"exactly-at-threshold", fixture.Column{Distinct: est(9500)}, 10000, false}, // 0.95 is not > 0.95
		{"low-cardinality", fixture.Column{Distinct: est(4)}, 10000, false},
		{"proven-by-constraint", fixture.Column{Distinct: est(10000), Unique: proven}, 10000, false},
		{"no-distinct-fact", fixture.Column{}, 10000, false},
		{"measured-not-estimated", fixture.Column{Distinct: &fixture.Fact[int64]{Value: 9999, Confidence: fixture.Measured}}, 10000, false},
		{"zero-rows", fixture.Column{Distinct: est(1)}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEscalate(tc.col, tc.rows); got != tc.want {
				t.Errorf("shouldEscalate = %v, want %v", got, tc.want)
			}
		})
	}
}

const escSchema = "rowshape_p1b_t3"

func seedEscalation(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + escSchema + ` CASCADE`,
		`CREATE SCHEMA ` + escSchema,
		`CREATE TABLE ` + escSchema + `.users (
			id bigint PRIMARY KEY,
			external_ref text,
			slug text,
			status text)`,
		// external_ref: all distinct, no constraint -> escalate, probe finds unique.
		// slug: looks unique but g=2 duplicates g=1's slug -> escalate, probe finds
		// NOT unique (the wrong-PASS this whole mechanism exists to prevent).
		`INSERT INTO ` + escSchema + `.users
			SELECT g, 'ref-'||g, 'slug-'||(CASE WHEN g=2 THEN 1 ELSE g END),
			       (ARRAY['active','trialing','canceled'])[1+g%3]
			FROM generate_series(1,5000) g`,
		`ANALYZE ` + escSchema + `.users`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seedEscalation failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+escSchema+` CASCADE`)
	})
}

// TestAutoEscalation: the fast-mode profiler auto-escalates exactly the dangerous
// columns, records them, and sets mode `targeted` (RFC §7.3, P1b-T3).
func TestAutoEscalation(t *testing.T) {
	conn := adminConn(t)
	seedEscalation(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{escSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}

	// Mode and the escalated list record exactly the two escalated columns.
	if f.Meta.Profile.Mode != "targeted" {
		t.Errorf("mode = %q, want targeted", f.Meta.Profile.Mode)
	}
	want := []string{escSchema + ".users.external_ref", escSchema + ".users.slug"}
	if got := f.Meta.Profile.Escalated; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("escalated = %v, want %v", got, want)
	}

	cols := f.Tables[escSchema+".users"].Columns

	// external_ref: escalated -> measured distinct via hll, proven unique via probe.
	er := cols["external_ref"]
	if er.Distinct == nil || er.Distinct.Confidence != "measured" || er.Distinct.Via != "hll" {
		t.Errorf("external_ref.distinct = %+v, want measured via hll", er.Distinct)
	}
	if er.Unique == nil || !er.Unique.Value || er.Unique.Confidence != "exact" || er.Unique.Via != "probe" {
		t.Errorf("external_ref.unique = %+v, want exact true via probe", er.Unique)
	}

	// slug: escalated -> the probe catches the hidden duplicate, unique:false exact.
	slug := cols["slug"]
	if slug.Unique == nil || slug.Unique.Value || slug.Unique.Confidence != "exact" {
		t.Errorf("slug.unique = %+v, want exact FALSE (the probe must catch the duplicate)", slug.Unique)
	}

	// id: proven by the PK constraint -> NOT escalated, distinct stays estimated.
	id := cols["id"]
	if id.Distinct == nil || id.Distinct.Confidence != "estimated" {
		t.Errorf("id.distinct = %+v, want still estimated (PK, not escalated)", id.Distinct)
	}
	if id.Unique == nil || id.Unique.Via != "constraint" {
		t.Errorf("id.unique = %+v, want via constraint (not escalated)", id.Unique)
	}

	// status: low cardinality -> not escalated, no unique fact.
	if cols["status"].Unique != nil {
		t.Errorf("status should not be escalated: %+v", cols["status"].Unique)
	}
}

// TestNoEscalation: when no column looks dangerous, mode stays `fast` and nothing
// is escalated.
func TestNoEscalation(t *testing.T) {
	conn := adminConn(t)
	ctx := context.Background()
	const s = "rowshape_p1b_t3_none"
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + s + ` CASCADE`,
		`CREATE SCHEMA ` + s,
		`CREATE TABLE ` + s + `.t (id bigint PRIMARY KEY, status text)`,
		`INSERT INTO ` + s + `.t SELECT g, (ARRAY['a','b','c'])[1+g%3] FROM generate_series(1,3000) g`,
		`ANALYZE ` + s + `.t`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+s+` CASCADE`) })

	f, err := Fast(ctx, conn, Options{Schemas: []string{s}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}
	if f.Meta.Profile.Mode != "fast" {
		t.Errorf("mode = %q, want fast (no escalation)", f.Meta.Profile.Mode)
	}
	if len(f.Meta.Profile.Escalated) != 0 {
		t.Errorf("escalated = %v, want empty", f.Meta.Profile.Escalated)
	}
}
