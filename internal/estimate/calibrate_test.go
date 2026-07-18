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
	est, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{20_000, 200}, 1_000_000, fixture.Exact)
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
	est, err := Calibrate(TableRewrite, Point{10_000, 150}, Point{20_000, 250}, 100_000, fixture.Exact)
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
	est, err := Calibrate(BTreeBuild, Point{10_000, 50}, Point{40_000, 240}, 4_000_000, fixture.Exact)
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
	_, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{10_000, 110}, 1_000_000, fixture.Exact)
	if !errors.Is(err, ErrCalibration) {
		t.Errorf("same-scale points must error, got %v", err)
	}
}

// TestCalibrateUpgradesConfidence: calibration is the ONLY path to `measured`;
// single-basis Extrapolate stays `estimated` (no silent upgrade). The upgrade is
// available only when the row count it projects onto is itself exact — see
// TestCalibrateIsCappedByRowsConfidence.
//
// CR-T4 NOTE: this test previously called Calibrate with no row-count confidence
// at all and asserted `measured` unconditionally. That was asserting the BUG as
// correct: Calibrate hardcoded Confidence: measured, so the test could not have
// failed however wrong the answer was. Corrected rather than deleted, because the
// property it meant to pin — calibration is the only route to `measured` — is
// real and still holds.
func TestCalibrateUpgradesConfidence(t *testing.T) {
	cal, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{20_000, 200}, 1_000_000, fixture.Exact)
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

// TestCalibrateIsCappedByRowsConfidence pins the rule Calibrate was breaking
// (RFC §7.4, INV-CONFIDENCE-CAPPING): confidence never exceeds the confidence of
// the row count the estimate rests on.
//
// Calibration measures the OPERATION — two real timings establish how the work
// grows — but it evaluates that curve at the DECLARED row count. If the row count
// is a pg_stats estimate, the projection is an estimate no matter how well the
// curve was fitted. The distinction is not academic: exact/measured may PASS,
// estimated/declared/absent are capped to WARN, so hardcoding `measured` here
// manufactured PASS eligibility out of a fact that never supported it.
func TestCalibrateIsCappedByRowsConfidence(t *testing.T) {
	cases := []struct {
		name     string
		rowsConf fixture.Confidence
		want     fixture.Confidence
	}{
		{"exact rows earn the measured upgrade", fixture.Exact, fixture.Measured},
		{"measured rows earn it too", fixture.Measured, fixture.Measured},
		{"estimated rows cap the result", fixture.Estimated, fixture.Estimated},
		{"declared rows cap the result", fixture.Declared, fixture.Declared},
		// "Absent" is any unnamed value: it ranks below every named level, so a
		// row count whose confidence cannot even be read must never license a
		// measured estimate.
		{"absent rows cap hardest", fixture.Confidence(""), fixture.Confidence("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{20_000, 200}, 1_000_000, tc.rowsConf)
			if err != nil {
				t.Fatal(err)
			}
			if got.Confidence != string(tc.want) {
				t.Errorf("Calibrate with rows confidence %q = %q, want %q — a fitted curve is only "+
					"as good as the row count it is projected onto (RFC §7.4)",
					tc.rowsConf, got.Confidence, tc.want)
			}
		})
	}
}

// TestCalibrateNeverExceedsExtrapolateOnWeakRows: on the same weak row count,
// calibrating must not buy a stronger confidence than extrapolating. Calibration
// improves the BUCKET (the curve is fitted rather than assumed); it must not
// improve how well the row count is known, which is what capping reads.
func TestCalibrateNeverExceedsExtrapolateOnWeakRows(t *testing.T) {
	for _, conf := range []fixture.Confidence{fixture.Estimated, fixture.Declared, fixture.Confidence("")} {
		cal, err := Calibrate(TableRewrite, Point{10_000, 100}, Point{20_000, 200}, 1_000_000, conf)
		if err != nil {
			t.Fatal(err)
		}
		ext := Extrapolate(TableRewrite, 10_000, 100, 1_000_000, conf)
		if fixture.Min(fixture.Confidence(cal.Confidence), fixture.Confidence(ext.Confidence)) != fixture.Confidence(cal.Confidence) {
			t.Errorf("rows confidence %q: calibrated = %q, extrapolated = %q — calibration must not "+
				"outrank extrapolation on the same row count", conf, cal.Confidence, ext.Confidence)
		}
	}
}
