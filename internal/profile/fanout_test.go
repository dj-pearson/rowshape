package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

const fanoutSchema = "rowshape_p1t11"

// seedFanout builds a schema with a deliberately skewed fan-out (one whale
// parent, a few heavy ones, a long tail) and a NOT VALID foreign key carrying
// known orphans.
func seedFanout(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + fanoutSchema + ` CASCADE`,
		`CREATE SCHEMA ` + fanoutSchema,
		`CREATE TABLE ` + fanoutSchema + `.users (id bigint PRIMARY KEY)`,
		`INSERT INTO ` + fanoutSchema + `.users SELECT g FROM generate_series(1,100) g`,
		`CREATE TABLE ` + fanoutSchema + `.orders (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			user_id bigint REFERENCES ` + fanoutSchema + `.users(id))`,
		// user 1 = 500 orders (the whale), users 2-10 ≈ 40 each, the rest 1-3.
		`INSERT INTO ` + fanoutSchema + `.orders (user_id) SELECT 1 FROM generate_series(1,500)`,
		`INSERT INTO ` + fanoutSchema + `.orders (user_id) SELECT (2 + g%9) FROM generate_series(1,360) g`,
		`INSERT INTO ` + fanoutSchema + `.orders (user_id) SELECT (11 + g%90) FROM generate_series(1,180) g`,
		// events: a NOT VALID FK with 11 orphans among 111 rows.
		`CREATE TABLE ` + fanoutSchema + `.events (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			user_id bigint)`,
		`INSERT INTO ` + fanoutSchema + `.events (user_id) SELECT g FROM generate_series(1,100) g`,
		`INSERT INTO ` + fanoutSchema + `.events (user_id) SELECT g FROM generate_series(9000,9010) g`,
		`ALTER TABLE ` + fanoutSchema + `.events ADD CONSTRAINT events_user_fk
			FOREIGN KEY (user_id) REFERENCES ` + fanoutSchema + `.users(id) NOT VALID`,
		`ANALYZE ` + fanoutSchema + `.users`,
		`ANALYZE ` + fanoutSchema + `.orders`,
		`ANALYZE ` + fanoutSchema + `.events`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seedFanout failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+fanoutSchema+` CASCADE`)
	})
}

// TestFanoutMeasurement: the skewed children-per-parent distribution is
// recovered {mean, p50, p95, max} with a confidence, and it reflects the DATA,
// not the catalog or column stats (RFC §6.6).
func TestFanoutMeasurement(t *testing.T) {
	conn := adminConn(t)
	seedFanout(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{fanoutSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}
	orders := f.Tables[fanoutSchema+".orders"]
	if len(orders.References) != 1 {
		t.Fatalf("orders references = %d, want 1", len(orders.References))
	}
	ref := orders.References[0]
	if ref.Fanout == nil {
		t.Fatalf("orders.user_id has no fan-out")
	}
	fo := ref.Fanout
	t.Logf("fan-out: mean=%.1f p50=%.0f p95=%.0f max=%.0f (%s)", fo.Mean, fo.P50, fo.P95, fo.Max, fo.Confidence)

	// The whale parent (500 children) must surface as the max.
	if fo.Max != 500 {
		t.Errorf("fan-out max = %.0f, want 500 (the whale parent)", fo.Max)
	}
	// The heavy tail (~40) must surface at p95, well above the p50 body.
	if fo.P95 < 30 || fo.P95 > 45 {
		t.Errorf("fan-out p95 = %.0f, want ~40", fo.P95)
	}
	if fo.P50 >= fo.P95 {
		t.Errorf("distribution not skewed: p50=%.0f p95=%.0f", fo.P50, fo.P95)
	}
	// Full read of a small table is a real pass -> measured, not estimated.
	if fo.Confidence != "measured" {
		t.Errorf("fan-out confidence = %q, want measured for a full pass", fo.Confidence)
	}
}

// TestOrphanFraction: a NOT VALID foreign key's orphans are counted exactly via
// a scan, while a VALIDATED foreign key is proven orphan-free for free (RFC §6.6).
func TestOrphanFraction(t *testing.T) {
	conn := adminConn(t)
	seedFanout(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{fanoutSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}

	// events has a NOT VALID FK with 11 orphans / 111 rows ≈ 0.099, exact via scan.
	events := f.Tables[fanoutSchema+".events"]
	if len(events.References) != 1 {
		t.Fatalf("events references = %d, want 1", len(events.References))
	}
	of := events.References[0].OrphanFraction
	if of == nil {
		t.Fatalf("events FK has no orphan_fraction")
	}
	if of.Confidence != "exact" || of.Via != "scan" {
		t.Errorf("orphan_fraction should be exact via scan, got %+v", of)
	}
	if of.Value < 0.09 || of.Value > 0.11 {
		t.Errorf("orphan_fraction = %.4f, want ~0.099 (11/111)", of.Value)
	}

	// orders has a VALIDATED FK: proven orphan-free (0 exact via constraint), no scan.
	orders := f.Tables[fanoutSchema+".orders"]
	oof := orders.References[0].OrphanFraction
	if oof == nil || oof.Value != 0 || oof.Confidence != "exact" || oof.Via != "constraint" {
		t.Errorf("validated FK orphan_fraction should be 0 exact via constraint, got %+v", oof)
	}
}
