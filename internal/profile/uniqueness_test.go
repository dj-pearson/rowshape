package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

const uniqSchema = "rowshape_p1b_t2"

func seedUniqueness(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + uniqSchema + ` CASCADE`,
		`CREATE SCHEMA ` + uniqSchema,
		// No constraints, so uniqueness has no catalog proof — the probes must
		// establish it (or not) from the data.
		`CREATE TABLE ` + uniqSchema + `.t (
			uniq_col bigint,      -- all distinct, some NULLs
			dup_col bigint,       -- has duplicates
			nullable_uniq text)`, // distinct non-null values plus repeated NULLs
		`INSERT INTO ` + uniqSchema + `.t (uniq_col, dup_col, nullable_uniq)
			SELECT g, g % 100, CASE WHEN g % 10 = 0 THEN NULL ELSE 'v' || g END
			FROM generate_series(1, 5000) g`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seedUniqueness failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+uniqSchema+` CASCADE`)
	})
}

func uniqReader(t *testing.T, conn *pgx.Conn) (*reader, func()) {
	t.Helper()
	tx, err := conn.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	return &reader{tx: tx, attNames: map[uint32]map[int16]string{}}, func() { _ = tx.Rollback(context.Background()) }
}

// TestProbeExistence: the existence probe reaches exact and correctly separates
// unique from non-unique columns (RFC §7.2 route 2).
func TestProbeExistence(t *testing.T) {
	conn := adminConn(t)
	seedUniqueness(t, conn)
	r, done := uniqReader(t, conn)
	defer done()
	ctx := context.Background()

	uniq, err := r.probeUniqueExistence(ctx, uniqSchema, "t", "uniq_col")
	if err != nil {
		t.Fatalf("probe uniq_col: %v", err)
	}
	if !uniq.Value || uniq.Confidence != "exact" || uniq.Via != "probe" {
		t.Errorf("uniq_col = %+v, want {true exact probe}", uniq)
	}

	dup, err := r.probeUniqueExistence(ctx, uniqSchema, "t", "dup_col")
	if err != nil {
		t.Fatalf("probe dup_col: %v", err)
	}
	if dup.Value || dup.Confidence != "exact" {
		t.Errorf("dup_col = %+v, want {false exact}", dup)
	}

	// A column with distinct non-null values plus many NULLs is still unique —
	// NULLs are excluded, so they must not read as duplicates.
	nu, err := r.probeUniqueExistence(ctx, uniqSchema, "t", "nullable_uniq")
	if err != nil {
		t.Fatalf("probe nullable_uniq: %v", err)
	}
	if !nu.Value {
		t.Errorf("nullable_uniq should be unique despite NULLs, got %+v", nu)
	}
}

// TestUniquenessNeverFromSample is the property test (INV-UNIQUENESS): fast-mode
// profiling, which only samples, must NEVER produce unique:true for a column that
// merely looks unique. uniq_col has distinct == rows but no catalog proof, so
// after Fast() it has no unique fact at all — omitted, not guessed.
func TestUniquenessNeverFromSample(t *testing.T) {
	conn := adminConn(t)
	seedUniqueness(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{uniqSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}
	cols := f.Tables[uniqSchema+".t"].Columns
	for name, c := range cols {
		if c.Unique != nil {
			// The only legal unique fact is exact; sampling must never set it.
			if c.Unique.Confidence != "exact" {
				t.Errorf("%s.unique = %+v — uniqueness must be exact or absent, never sampled", name, c.Unique)
			}
			// And without a catalog constraint, there should be no unique fact.
			t.Errorf("%s should have NO unique fact from fast-mode sampling, got %+v", name, c.Unique)
		}
	}
}
