package profile

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

const skewSchema = "rowshape_p1t12"

func seedSkew(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + skewSchema + ` CASCADE`,
		`CREATE SCHEMA ` + skewSchema,
		`CREATE TABLE ` + skewSchema + `.metrics (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			score int NOT NULL,
			uniform_val int NOT NULL)`,
		// score: 80% of rows in [0,10], the rest spread to ~1000 (value-concentration
		// skew that lives in most_common_vals, not histogram_bounds).
		`INSERT INTO ` + skewSchema + `.metrics (score, uniform_val)
			SELECT CASE WHEN g%5 < 4 THEN (g%10) ELSE (10 + (g*7)%990) END, (g*13)%10000
			FROM generate_series(1,10000) g`,
		`ANALYZE ` + skewSchema + `.metrics`,
		// A range-partitioned table with skewed partition sizes.
		`CREATE TABLE ` + skewSchema + `.events (id bigint, created_at date) PARTITION BY RANGE (created_at)`,
		`CREATE TABLE ` + skewSchema + `.events_2023 PARTITION OF ` + skewSchema + `.events FOR VALUES FROM ('2023-01-01') TO ('2024-01-01')`,
		`CREATE TABLE ` + skewSchema + `.events_2024 PARTITION OF ` + skewSchema + `.events FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')`,
		`CREATE TABLE ` + skewSchema + `.events_2025 PARTITION OF ` + skewSchema + `.events FOR VALUES FROM ('2025-01-01') TO ('2026-01-01')`,
		`INSERT INTO ` + skewSchema + `.events SELECT g, DATE '2023-06-01' FROM generate_series(1,50) g`,
		`INSERT INTO ` + skewSchema + `.events SELECT g, DATE '2024-06-01' FROM generate_series(1,80) g`,
		`INSERT INTO ` + skewSchema + `.events SELECT g, DATE '2025-06-01' FROM generate_series(1,900) g`,
		`ANALYZE ` + skewSchema + `.events`,
		`ANALYZE ` + skewSchema + `.events_2023`,
		`ANALYZE ` + skewSchema + `.events_2024`,
		`ANALYZE ` + skewSchema + `.events_2025`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seedSkew failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+skewSchema+` CASCADE`)
	})
}

// TestHistogramSkew: a skewed numeric column emits a histogram whose bounds
// cluster in the dense region; a uniform column emits none (RFC §6.2).
func TestHistogramSkew(t *testing.T) {
	conn := adminConn(t)
	seedSkew(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{skewSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}
	cols := f.Tables[skewSchema+".metrics"].Columns

	score := cols["score"]
	if score.Histogram == nil {
		t.Fatalf("skewed column `score` must emit a histogram")
	}
	if score.Histogram.Buckets < 4 || len(score.Histogram.Bounds) != score.Histogram.Buckets+1 {
		t.Errorf("histogram malformed: %+v", score.Histogram)
	}
	// The dense region (80% of rows ≤ 10) must dominate the low buckets: the
	// bound at the ~75th percentile position should still be small.
	lowBound, ok := toF(score.Histogram.Bounds[len(score.Histogram.Bounds)*3/4])
	if !ok || lowBound > 20 {
		t.Errorf("histogram does not capture the low-value concentration: %v", score.Histogram.Bounds)
	}

	// The uniform column must NOT get a histogram — a summary statistic suffices.
	if cols["uniform_val"].Histogram != nil {
		t.Errorf("uniform column should not emit a histogram: %+v", cols["uniform_val"].Histogram)
	}
}

// TestPartitionsBlock: a partitioned table emits {count, strategy, skew} with no
// per-partition entries, and its children are not separate tables (RFC §14.2).
func TestPartitionsBlock(t *testing.T) {
	conn := adminConn(t)
	seedSkew(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{skewSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}

	events, ok := f.Tables[skewSchema+".events"]
	if !ok {
		t.Fatalf("partitioned table not found")
	}
	if events.Partitions == nil {
		t.Fatalf("partitioned table must emit a partitions block")
	}
	p := events.Partitions
	if p.Count != 3 || p.Strategy != "range" {
		t.Errorf("partitions = %+v, want count 3 strategy range", p)
	}
	// The 2025 partition holds 900 of 1030 rows ≈ 0.87.
	if p.Skew < 0.8 || p.Skew > 0.95 {
		t.Errorf("partition skew = %.3f, want ~0.87", p.Skew)
	}
	// Declared rows come from the partitions (parent stores none).
	if events.Rows.Value != 1030 {
		t.Errorf("partitioned rows = %d, want 1030 (sum of partitions)", events.Rows.Value)
	}
	// No child partition appears as its own table.
	for name := range f.Tables {
		if name != skewSchema+".events" && name != skewSchema+".metrics" {
			t.Errorf("child partition leaked as a table: %s", name)
		}
	}
}

// TestStrictDropsHistogram: histogram bounds are real values, dropped under
// strict privacy (RFC §8.2).
func TestStrictDropsHistogram(t *testing.T) {
	f := &fixture.Fixture{
		RowshapeFixture: fixture.FormatVersion,
		Tables: map[string]fixture.Table{
			"public.t": {
				Rows: fixture.Fact[int64]{Value: 100, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{
					"c": {Type: "integer", Nullable: false, Histogram: &fixture.Histogram{Buckets: 2, Bounds: []any{0, 1, 100}}},
				},
			},
		},
	}
	ApplyPrivacy(f, PrivacyStrict, 0)
	if f.Tables["public.t"].Columns["c"].Histogram != nil {
		t.Errorf("strict must drop the histogram (bounds are real values, §8.2)")
	}

	// Standard keeps it.
	f2 := &fixture.Fixture{
		RowshapeFixture: fixture.FormatVersion,
		Tables: map[string]fixture.Table{
			"public.t": {
				Rows:    fixture.Fact[int64]{Value: 100, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{"c": {Type: "integer", Histogram: &fixture.Histogram{Buckets: 2, Bounds: []any{0, 1, 100}}}},
			},
		},
	}
	ApplyPrivacy(f2, PrivacyStandard, 0)
	if f2.Tables["public.t"].Columns["c"].Histogram == nil {
		t.Errorf("standard must keep the histogram")
	}
}

func toF(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}
