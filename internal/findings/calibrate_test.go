package findings

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
)

// TestCalibrationUpgradesFindingEstimate: when a capture carries a second-scale
// measurement (validate --calibrate), the finding's duration estimate is fitted
// to the two points and marked `measured`; without it, the estimate stays
// `estimated` (no silent upgrade). Exercised through the RS-LOCK analyzer.
func TestCalibrationUpgradesFindingEstimate(t *testing.T) {
	f, mig := loadCorpus(t, "rslock-volatile-default")

	// Without calibration: a single-basis extrapolation, `estimated`.
	plain := captureOf(mig, "public.orders", 10_000) // statement DurationMs = 90
	got := rsLock{}.Analyze(f, plain)
	if len(got) != 1 || got[0].Estimate == nil {
		t.Fatalf("expected 1 RS-LOCK finding with an estimate, got %d", len(got))
	}
	if got[0].Estimate.Confidence != string(fixture.Estimated) {
		t.Errorf("uncalibrated estimate confidence = %s, want estimated (no silent upgrade)", got[0].Estimate.Confidence)
	}

	// With calibration: a second run at half scale (5000 rows, 45ms) pins the
	// curve, upgrading the estimate to `measured`.
	calibrated := captureOf(mig, "public.orders", 10_000)
	calibrated.Calibration = &validate.Calibration{
		TableRows:    map[string]int64{"public.orders": 5_000},
		StatementMs2: statementMs(calibrated),
	}
	// Fill in the second-scale measurement for the rewrite statement.
	for i := range calibrated.Calibration.StatementMs2 {
		calibrated.Calibration.StatementMs2[i] = 45
	}
	gotc := rsLock{}.Analyze(f, calibrated)
	if len(gotc) != 1 || gotc[0].Estimate == nil {
		t.Fatalf("expected 1 calibrated RS-LOCK finding with an estimate")
	}
	if gotc[0].Estimate.Confidence != string(fixture.Measured) {
		t.Errorf("calibrated estimate confidence = %s, want measured", gotc[0].Estimate.Confidence)
	}
}

// statementMs returns a zeroed per-statement slice aligned with the capture.
func statementMs(c *validate.Capture) []int64 {
	return make([]int64, len(c.Statements))
}
