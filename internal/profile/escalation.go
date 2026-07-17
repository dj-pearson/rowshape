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

// DefaultMaxEscalationRows is the soft cost ceiling for auto-escalation (RFC
// §14.5 / OQ-ESCALATION-CEILING). A fast pull that quietly full-scans a
// 400M-row table because one column looked unique is a bad surprise, so above
// this row count escalation is skipped — `unique` is omitted (safe, §7.4) — and
// a WARN names exactly what was skipped and why. Silent truncation is forbidden.
const DefaultMaxEscalationRows = 50_000_000

// effectiveCap resolves the configured cap: 0 means the default, a negative
// value means no cap (unlimited).
func effectiveCap(configured int64) int64 {
	if configured == 0 {
		return DefaultMaxEscalationRows
	}
	return configured
}

// overEscalationCap reports whether a table is too large to escalate under the
// active cap. A non-positive cap disables the ceiling.
func (r *reader) overEscalationCap(rows int64) bool {
	return r.maxEscalationRows > 0 && rows > r.maxEscalationRows
}

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
