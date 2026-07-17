package findings

import (
	"github.com/rowshape/rowshape/internal/estimate"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// estimateFor computes a finding's duration estimate for the statement at
// stmtIdx. When the capture carries a second-scale measurement for this
// statement (validate --calibrate), it fits the cost curve to the two measured
// points and returns a `measured` estimate (RFC §9.2); otherwise it extrapolates
// from the single measured basis, which stays `estimated`. Returns nil when there
// is no usable basis.
func estimateFor(c *validate.Capture, stmtIdx int, op estimate.OpClass, table string, declaredRows int64, rowsConf fixture.Confidence, tableKnown bool) *verdict.Estimate {
	// Refuse to extrapolate for a table the fixture does not carry.
	//
	// Without this, an unknown table reads the zero value and the arithmetic
	// happily reports `instant` — a rewrite of no rows takes no time. That is
	// indistinguishable from a genuinely empty table, and it is exactly the wrong
	// answer for the likeliest cause: a name the fixture has no facts for (a typo,
	// a table never pulled, or an unqualified name in two schemas). Telling
	// someone an unknown migration is instant is worse than telling them nothing.
	//
	// This mirrors RFC §9.1's refusal to extrapolate without engine.version: when
	// the basis is missing, omit the estimate and say why. Callers surface the
	// finding either way — the lock is still real — and an absent dependency caps
	// it to WARN.
	if !tableKnown {
		return nil
	}

	rows1 := c.TableRows[table]
	if rows1 <= 0 {
		rows1 = declaredRows // ground-truth target: hydrated == real
	}
	ms1 := int64(1)
	if stmtIdx >= 0 && stmtIdx < len(c.Statements) && c.Statements[stmtIdx].DurationMs > 0 {
		ms1 = c.Statements[stmtIdx].DurationMs
	}

	if cal := c.Calibration; cal != nil && stmtIdx >= 0 && stmtIdx < len(cal.StatementMs2) {
		rows2, ms2 := cal.TableRows[table], cal.StatementMs2[stmtIdx]
		if rows2 > 0 && rows2 != rows1 && ms2 > 0 {
			if est, err := estimate.Calibrate(op, estimate.Point{Rows: rows1, Ms: ms1}, estimate.Point{Rows: rows2, Ms: ms2}, declaredRows); err == nil {
				return &est
			}
		}
	}
	est := estimate.Extrapolate(op, rows1, ms1, declaredRows, rowsConf)
	return &est
}

// tableKnown reports whether the fixture carries facts for this table. It is the
// difference between "this table has no rows" and "we have never seen this
// table" — arithmetic on the zero value cannot tell them apart, and answers
// `instant` for both.
func tableKnown(f *fixture.Fixture, table string) bool {
	if f == nil {
		return false
	}
	_, ok := f.Tables[table]
	return ok
}
