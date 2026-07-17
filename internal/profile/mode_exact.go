package profile

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

// This file implements the `exact` profile mode (RFC §7.3): a full streaming pass
// per table rather than a sample. Distinct becomes measured (HLL), null counts
// and uniqueness become exact — the manual counterpart to auto-escalation, for
// the whole database. It reads values into the emitter to aggregate them, but
// emits ONLY aggregates; no row value ever leaves (RFC §13, INV-NO-ROWS).
//
// Exact mode is minutes-to-hours work; it is opt-in via `pull --exact`.

// exactNullFraction computes a column's null fraction with a full scan, so the
// fact is `exact` rather than the planner's `estimated`. A nullable column at
// exactly 0% null — the passes-staging-fails-prod trap (§6.1) — is now proven,
// not guessed.
func (r *reader) exactNullFraction(ctx context.Context, schema, table, column string) (*fixture.Fact[float64], error) {
	from := pgx.Identifier{schema, table}.Sanitize()
	c := pgx.Identifier{column}.Sanitize()
	q := "SELECT count(*) FILTER (WHERE " + c + " IS NULL)::float8 / NULLIF(count(*), 0) FROM " + from

	var frac *float64
	if err := r.tx.QueryRow(ctx, q).Scan(&frac); err != nil {
		return nil, err
	}
	value := 0.0
	if frac != nil {
		value = round6(*frac)
	}
	return &fixture.Fact[float64]{Value: value, Confidence: fixture.Exact}, nil
}

// exactColumn upgrades a column's two most decision-critical facts to their
// strongest confidence via a full pass: null_fraction (exact) for a nullable
// column, and distinct (measured, via HLL). Uniqueness is handled separately so
// a catalog proof is never overwritten.
func (r *reader) exactColumn(ctx context.Context, schema, table string, name string, col *fixture.Column) error {
	if col.Nullable {
		nf, err := r.exactNullFraction(ctx, schema, table, name)
		if err != nil {
			return err
		}
		col.NullFraction = nf
	}
	dist, err := r.hllDistinct(ctx, schema, table, name)
	if err != nil {
		return err
	}
	col.Distinct = dist
	return nil
}
