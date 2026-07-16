package profile

import (
	"context"

	"github.com/rowshape/rowshape/internal/fixture"
)

// escalationThreshold is the n_distinct/rows ratio above which a column with no
// uniqueness proof is escalated to a full pass (RFC §7.3). It selects exactly the
// dangerous columns: the ones that LOOK unique but aren't proven — a handful of
// email/slug/external_ref columns in practice — so a fast pull stays fast while
// the columns where a wrong answer costs an outage get the expensive treatment.
const escalationThreshold = 0.95

// shouldEscalate reports whether a column is dangerous enough to pay for a full
// pass: it looks nearly unique (estimated distinct/rows > 0.95) yet has no
// catalog proof of uniqueness. A column already proven unique by a constraint has
// nothing to gain and is left alone; the cheap default is safe by construction,
// not by remembering a flag.
func shouldEscalate(col fixture.Column, rows int64) bool {
	if col.Unique != nil {
		return false // already proven exact by a constraint (P1-T3)
	}
	if col.Distinct == nil || col.Distinct.Confidence != fixture.Estimated || rows <= 0 {
		return false
	}
	return float64(col.Distinct.Value)/float64(rows) > escalationThreshold
}

// escalateColumn upgrades a dangerous column with a full pass: a measured
// distinct count via client-side HyperLogLog (P1b-T1) and an exact uniqueness
// verdict via the existence probe (P1b-T2). After escalation the column's two
// most decision-critical facts are no longer estimated — a validator can trust
// them to license a PASS (§7.4).
func (r *reader) escalateColumn(ctx context.Context, schema, table, column string, col *fixture.Column) error {
	distinct, err := r.hllDistinct(ctx, schema, table, column)
	if err != nil {
		return err
	}
	col.Distinct = distinct

	unique, err := r.probeUniqueExistence(ctx, schema, table, column)
	if err != nil {
		return err
	}
	col.Unique = unique
	return nil
}
