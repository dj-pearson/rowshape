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
func estimateFor(c *validate.Capture, stmtIdx int, op estimate.OpClass, table string, declaredRows int64, rowsConf fixture.Confidence) *verdict.Estimate {
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
