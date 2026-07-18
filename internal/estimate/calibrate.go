package estimate

import (
	"errors"
	"math"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
)

// Point is one measured (rows, ms) sample: an operation applied to a hydrated
// database of Rows rows took Ms milliseconds.
type Point struct {
	Rows int64
	Ms   int64
}

// ErrCalibration is returned when two calibration points do not pin the curve —
// they measured the same amount of work, so the slope is underdetermined.
var ErrCalibration = errors.New("estimate: calibration needs two points at different scales")

// Calibrate fits an operation's cost model to TWO measured points and projects
// to declaredRows, returning an Estimate marked `measured` (RFC §9.2). This is
// the honest upgrade over single-basis Extrapolate (which is only `estimated`):
// the curve is fitted to the operation's real behavior at two scales, not
// assumed from one.
//
// The fit is ms = a·work(rows) + b through the two points, where work is the
// model's growth term (n for linear, n·log₂n for n_log_n, constant for O(1)).
// The intercept b absorbs fixed per-statement overhead so the slope reflects the
// marginal per-row cost.
// rowsConfidence is the confidence of the DECLARED row count the fitted curve is
// projected onto, and it caps the result (RFC §7.4). Calibration measures the
// operation, not the table: fitting a curve through two real timings establishes
// how the work grows, but the answer is only ever as good as the row count it is
// evaluated at. A perfectly measured curve projected onto an `estimated` 50M
// rows is an estimate — and under INV-CONFIDENCE-CAPPING that difference decides
// whether a finding may PASS or is capped to WARN, so claiming `measured` here
// is exactly the "finding downgrades its dependency to reach a stronger verdict"
// that the invariant forbids.
func Calibrate(op OpClass, p1, p2 Point, declaredRows int64, rowsConfidence fixture.Confidence) (verdict.Estimate, error) {
	model := op.Model()
	basis := p2
	if p1.Rows > p2.Rows {
		basis = p1
	}

	var predicted float64
	if model == Constant {
		// Catalog-only work does not grow with rows; the measured time is the time.
		predicted = math.Max(float64(p1.Ms), float64(p2.Ms))
	} else {
		w1, w2 := work(model, p1.Rows), work(model, p2.Rows)
		if w1 == w2 {
			return verdict.Estimate{}, ErrCalibration
		}
		a := (float64(p2.Ms) - float64(p1.Ms)) / (w2 - w1)
		b := float64(p1.Ms) - a*w1
		predicted = a*work(model, declaredRows) + b
		if predicted < 0 {
			predicted = 0
		}
	}

	return verdict.Estimate{
		Bucket:       Bucket(predicted),
		Model:        string(model),
		BasisRows:    basis.Rows,
		BasisMs:      basis.Ms,
		DeclaredRows: declaredRows,
		// Symmetric with Extrapolate's Min(Estimated, rowsConfidence): calibration
		// raises the ceiling from `estimated` to `measured`, it does not bypass it.
		Confidence: string(fixture.Min(fixture.Measured, rowsConfidence)),
	}, nil
}

// work is the cost model's growth term at n rows.
func work(m Model, rows int64) float64 {
	switch m {
	case Constant:
		return 1
	case NLogN:
		if rows <= 1 {
			return float64(rows)
		}
		return float64(rows) * math.Log2(float64(rows))
	default: // Linear
		return float64(rows)
	}
}
