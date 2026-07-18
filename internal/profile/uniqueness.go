package profile

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

// This file implements the two probe-based routes to unique=exact from RFC §7.2.
// Route 1 (a catalog constraint or unique index) already lands for free in
// P1-T3. These probes are the expensive routes auto-escalation (P1b-T3) reaches
// for on a dangerous column that LOOKS unique but has no proof. Each returns a
// single boolean or integer — no row values ever leave (INV-NO-ROWS) — and
// uniqueness is NEVER inferred from a sample (INV-UNIQUENESS): it is exact or
// absent, with no middle.

// probeUniqueExistence uses the existence probe (RFC §7.2 route 2): one boolean
// out, short-circuiting on the first duplicate. On a column that ISN'T unique
// this is usually fast; on one that is, it is a full scan. NULLs are excluded, so
// multiple NULLs (which a unique constraint permits) never read as duplicates.
func (r *reader) probeUniqueExistence(ctx context.Context, schema, table, column string) (*fixture.Fact[bool], error) {
	from := pgx.Identifier{schema, table}.Sanitize()
	c := pgx.Identifier{column}.Sanitize()
	q := "SELECT EXISTS (SELECT 1 FROM " + from + " WHERE " + c + " IS NOT NULL GROUP BY " + c + " HAVING count(*) > 1)"

	var hasDuplicate bool
	if err := r.tx.QueryRow(ctx, q).Scan(&hasDuplicate); err != nil {
		return nil, err
	}
	return &fixture.Fact[bool]{Value: !hasDuplicate, Confidence: fixture.Exact, Via: "probe"}, nil
}
