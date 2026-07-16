package profile

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile/hll"
)

// hllDistinct estimates a column's distinct count by streaming it through a
// client-side HyperLogLog (RFC §7.3): each value is hashed into the sketch and
// discarded, so no values are retained (INV-NO-ROWS) and no server extension is
// needed. The result is a `measured` fact — a full pass over the data with a
// bounded, published error — which beats the `estimated` pg_stats value and can
// license a PASS under §7.4.
//
// This is the expensive path; the fast-mode profiler pays it only for the
// dangerous columns auto-escalation selects (P1b-T3).
func (r *reader) hllDistinct(ctx context.Context, schema, table, column string) (*fixture.Fact[int64], error) {
	from := pgx.Identifier{schema, table}.Sanitize()
	c := pgx.Identifier{column}.Sanitize()
	// Cast to text so every column type hashes through one code path. The query
	// streams from a server-side cursor; pgx reads rows incrementally, so the
	// emitter's memory stays bounded regardless of table size.
	q := "SELECT (" + c + ")::text FROM " + from + " WHERE " + c + " IS NOT NULL"

	rows, err := r.tx.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sketch := hll.New()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		sketch.AddString(v)
		// v goes out of scope each iteration — no value is retained.
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &fixture.Fact[int64]{
		Value:      int64(sketch.Count()),
		Confidence: fixture.Measured,
		Via:        "hll",
		Error:      round6(hll.RelativeError()),
	}, nil
}
