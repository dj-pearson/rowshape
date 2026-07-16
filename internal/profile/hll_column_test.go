package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

const hllSchema = "rowshape_p1b_t1"

func seedHLL(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + hllSchema + ` CASCADE`,
		`CREATE SCHEMA ` + hllSchema,
		// A column with a KNOWN cardinality of exactly 50000 distinct values among
		// 200000 rows (each value repeated ~4x).
		`CREATE TABLE ` + hllSchema + `.t (id bigint, external_ref text)`,
		`INSERT INTO ` + hllSchema + `.t
			SELECT g, 'ref-' || (g % 50000) FROM generate_series(1, 200000) g`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seedHLL failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+hllSchema+` CASCADE`)
	})
}

// TestHLLDistinctMeasured: streaming a known-cardinality column through HLL
// yields a `measured` distinct fact, via hll, with a populated error, and the
// estimate lands within the precision-14 bound (RFC §7.3, P1b-T1).
func TestHLLDistinctMeasured(t *testing.T) {
	conn := adminConn(t)
	seedHLL(t, conn)

	tx, err := conn.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(context.Background())
	r := &reader{tx: tx, attNames: map[uint32]map[int16]string{}}

	fact, err := r.hllDistinct(context.Background(), hllSchema, "t", "external_ref")
	if err != nil {
		t.Fatalf("hllDistinct: %v", err)
	}

	if fact.Confidence != "measured" {
		t.Errorf("confidence = %q, want measured", fact.Confidence)
	}
	if fact.Via != "hll" {
		t.Errorf("via = %q, want hll", fact.Via)
	}
	if fact.Error <= 0 {
		t.Errorf("error not populated: %v", fact.Error)
	}
	// Known cardinality is 50000; the estimate must land within a few standard
	// errors (~2.4%).
	const truth = 50000
	relErr := float64(fact.Value-truth) / truth
	if relErr < 0 {
		relErr = -relErr
	}
	t.Logf("HLL distinct = %d (truth %d, relErr %.4f, error %.4f)", fact.Value, truth, relErr, fact.Error)
	if relErr > 0.03 {
		t.Errorf("HLL estimate %d off by %.4f from %d, beyond the precision-14 bound", fact.Value, relErr, truth)
	}
}
