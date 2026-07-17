package estimate

import (
	"errors"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestCalibrateLinearFit: a linear rewrite measured at two scales fits a straight
// line and projects to declared rows, marked `measured`.
func TestCalibrateLinearFit(t *testing.T) {
	// 10k rows -> 100ms, 20k rows -> 200ms: 0.01 ms/row through the origin.
	est, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{20_000, 200}, 1_000_000)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	// 0.01 ms/row * 1,000,000 = 10,000 ms -> slow (10-60s).
	if est.Bucket != verdict.BucketSlow {
		t.Errorf("bucket = %s, want slow", est.Bucket)
	}
	if est.Confidence != string(fixture.Measured) {
		t.Errorf("confidence = %s, want measured", est.Confidence)
	}
	// Basis is reported from the larger measured point.
	if est.BasisRows != 20_000 || est.BasisMs != 200 || est.DeclaredRows != 1_000_000 {
		t.Errorf("basis should come from the two points (larger reported), got %+v", est)
	}
}

// TestCalibrateFitsIntercept: a fixed per-statement overhead is absorbed by the
// intercept, so the slope reflects the marginal per-row cost.
func TestCalibrateFitsIntercept(t *testing.T) {
	// 10k -> 150ms, 20k -> 250ms: slope 0.01 ms/row, intercept 50ms overhead.
	est, err := Calibrate(TableRewrite, Point{10_000, 150}, Point{20_000, 250}, 100_000)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	// 0.01*100000 + 50 = 1050 ms -> noticeable (1-10s), not fast: the intercept
	// does not get multiplied by scale.
	if est.Bucket != verdict.BucketNoticeable {
		t.Errorf("bucket = %s, want noticeable (slope 0.01, intercept 50)", est.Bucket)
	}
}

// TestCalibrateNLogN: an n_log_n build fits the n·log₂n curve.
func TestCalibrateNLogN(t *testing.T) {
	est, err := Calibrate(BTreeBuild, Point{10_000, 50}, Point{40_000, 240}, 4_000_000)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if est.Model != string(NLogN) {
		t.Errorf("model = %s, want n_log_n", est.Model)
	}
	if est.Confidence != string(fixture.Measured) {
		t.Errorf("confidence = %s, want measured", est.Confidence)
	}
	if est.Bucket == "" {
		t.Error("expected a bucket")
	}
}

// TestCalibrateNeedsTwoScales: two points at the same scale cannot pin the slope.
func TestCalibrateNeedsTwoScales(t *testing.T) {
	_, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{10_000, 110}, 1_000_000)
	if !errors.Is(err, ErrCalibration) {
		t.Errorf("same-scale points must error, got %v", err)
	}
}

// TestCalibrateUpgradesConfidence: calibration is the ONLY path to `measured`;
// single-basis Extrapolate stays `estimated` (no silent upgrade).
func TestCalibrateUpgradesConfidence(t *testing.T) {
	cal, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{20_000, 200}, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	ext := Extrapolate(TableRewrite, 10_000, 100, 1_000_000, fixture.Exact)
	if cal.Confidence != string(fixture.Measured) {
		t.Errorf("calibrated confidence = %s, want measured", cal.Confidence)
	}
	if ext.Confidence != string(fixture.Estimated) {
		t.Errorf("extrapolated confidence = %s, want estimated (no silent upgrade)", ext.Confidence)
	}
}
