package profile

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

// measureReferences fills the fan-out distribution and orphan_fraction on every
// foreign key of a table (RFC §6.6). These are the moat fields: the catalog
// (P1-T3) gives the FK edges and on_delete, but the DISTRIBUTION of children per
// parent — and any orphan rows — can only be MEASURED by aggregating over the
// data, never inferred from the catalog or column stats. A long-tailed fan-out
// is what turns a cascade delete into an outage.
func (r *reader) measureReferences(ctx context.Context, t tableRef, tbl *fixture.Table) error {
	for i := range tbl.References {
		ref := &tbl.References[i]

		fo, err := r.measureFanout(ctx, t, ref.Column)
		if err != nil {
			return fmt.Errorf("fan-out on %s.%s: %w", t.qualified, ref.Column, err)
		}
		ref.Fanout = fo

		of, err := r.measureOrphanFraction(ctx, t, ref)
		if err != nil {
			return fmt.Errorf("orphan_fraction on %s.%s: %w", t.qualified, ref.Column, err)
		}
		ref.OrphanFraction = of
	}
	return nil
}

// measureFanout computes the children-per-parent distribution {mean, p50, p95,
// max} by grouping child rows on the foreign-key column (RFC §6.6). In fast mode
// a large table is sampled and the counts rescaled, yielding `estimated`
// confidence; a table small enough to read whole yields `measured` — a real full
// pass over the relationship. It is NEVER inferred from column stats.
func (r *reader) measureFanout(ctx context.Context, t tableRef, fkCol string) (*fixture.Fanout, error) {
	from, fraction, sampled := fanoutSample(t.schema, t.name, t.reltuples)
	c := pgx.Identifier{fkCol}.Sanitize()

	q := fmt.Sprintf(`
SELECT avg(cnt)::float8,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY cnt),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY cnt),
       max(cnt)::float8
FROM (SELECT count(*)::float8 AS cnt FROM %s WHERE %s IS NOT NULL GROUP BY %s) g`, from, c, c)

	var mean, p50, p95, mx *float64
	if err := r.tx.QueryRow(ctx, q).Scan(&mean, &p50, &p95, &mx); err != nil {
		return nil, err
	}
	if mean == nil {
		return nil, nil // no non-null foreign keys: no fan-out to report
	}

	conf := fixture.Measured
	if sampled {
		conf = fixture.Estimated
	}
	inv := 1.0 / fraction // rescale sampled counts to production scale
	return &fixture.Fanout{
		Mean:       round6(deref(mean) * inv),
		P50:        round6(deref(p50) * inv),
		P95:        round6(deref(p95) * inv),
		Max:        round6(deref(mx) * inv),
		Confidence: conf,
	}, nil
}

// measureOrphanFraction records the fraction of child rows whose foreign key has
// no matching parent (RFC §6.6). A VALIDATED foreign key proves there are none —
// 0 exact, for free. A NOT VALID foreign key (or one dropped and never restored)
// may carry orphans, so it is measured by a full LEFT JOIN scan — exact. A
// nonzero result is exactly what a VALIDATE finding needs: adding
// `FOREIGN KEY ... VALIDATE` would fail, and the fixture is the only thing that
// knows (RS-DATA).
func (r *reader) measureOrphanFraction(ctx context.Context, t tableRef, ref *fixture.Reference) (*fixture.Fact[float64], error) {
	validated, err := r.fkValidated(ctx, t, ref.Column)
	if err != nil {
		return nil, err
	}
	if validated {
		return &fixture.Fact[float64]{Value: 0, Confidence: fixture.Exact, Via: "constraint"}, nil
	}

	parentTable, parentCol := splitReference(ref.To)
	if parentTable == "" || parentCol == "" {
		return nil, nil
	}
	child := qualifiedIdent(t.schema, t.name)
	parent := qualifiedIdentFromQualified(parentTable)
	fk := pgx.Identifier{ref.Column}.Sanitize()
	pk := pgx.Identifier{parentCol}.Sanitize()

	q := fmt.Sprintf(`
SELECT COALESCE(
  count(*) FILTER (WHERE p.%s IS NULL AND c.%s IS NOT NULL)::float8
    / NULLIF(count(*) FILTER (WHERE c.%s IS NOT NULL), 0), 0)
FROM %s c LEFT JOIN %s p ON c.%s = p.%s`, pk, fk, fk, child, parent, fk, pk)

	var frac float64
	if err := r.tx.QueryRow(ctx, q).Scan(&frac); err != nil {
		return nil, err
	}
	return &fixture.Fact[float64]{Value: round6(frac), Confidence: fixture.Exact, Via: "scan"}, nil
}

// fkValidated reports whether every foreign key on a column is VALIDATED (so no
// orphans are possible). An unvalidated (NOT VALID) FK returns false.
func (r *reader) fkValidated(ctx context.Context, t tableRef, fkCol string) (bool, error) {
	const q = `
SELECT COALESCE(bool_and(con.convalidated), false)
FROM pg_constraint con
JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY (con.conkey)
WHERE con.conrelid = $1 AND con.contype = 'f' AND a.attname = $2`
	var validated bool
	if err := r.tx.QueryRow(ctx, q, t.oid, fkCol).Scan(&validated); err != nil {
		return false, err
	}
	return validated, nil
}

// fanoutSample returns the FROM target for a fan-out aggregation, the sampling
// fraction applied, and whether sampling happened. A small child table is read
// whole (fraction 1, measured); a large one is sampled deterministically.
func fanoutSample(schema, table string, reltuples float64) (from string, fraction float64, sampled bool) {
	qt := pgx.Identifier{schema, table}.Sanitize()
	if reltuples > float64(sampleTargetRows) {
		p := 100.0 * float64(sampleTargetRows) / reltuples
		if p < 0.01 {
			p = 0.01
		}
		return fmt.Sprintf("%s TABLESAMPLE SYSTEM (%s) REPEATABLE (%d)", qt, strconvFloat(p), sampleSeed), p / 100.0, true
	}
	return qt, 1.0, false
}

// splitReference splits schema.table.column into its schema.table and column.
func splitReference(to string) (table, column string) {
	i := strings.LastIndex(to, ".")
	if i < 0 {
		return "", ""
	}
	return to[:i], to[i+1:]
}

// qualifiedIdent quotes a schema and table into schema.table.
func qualifiedIdent(schema, table string) string {
	return pgx.Identifier{schema, table}.Sanitize()
}

// qualifiedIdentFromQualified quotes a schema.table string.
func qualifiedIdentFromQualified(qualified string) string {
	if i := strings.Index(qualified, "."); i >= 0 {
		return pgx.Identifier{qualified[:i], qualified[i+1:]}.Sanitize()
	}
	return pgx.Identifier{qualified}.Sanitize()
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
