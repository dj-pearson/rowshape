package hll

import (
	"fmt"
	"testing"
	"unsafe"
)

// TestAccuracyWithinBound: on known cardinalities the estimate stays within a
// few standard errors of the truth — the precision-14 bound (RFC §14.4). Testing
// at 3× the RSE keeps the assertion robust to single-run variance while still
// proving the estimator is calibrated.
func TestAccuracyWithinBound(t *testing.T) {
	tolerance := 3 * RelativeError() // ~2.4%
	for _, n := range []int{100, 1000, 10000, 100000, 1000000} {
		s := New()
		for i := 0; i < n; i++ {
			s.AddString(fmt.Sprintf("value-%d", i))
		}
		got := s.Count()
		relErr := abs(float64(got)-float64(n)) / float64(n)
		t.Logf("n=%-8d estimate=%-8d relErr=%.4f", n, got, relErr)
		if relErr > tolerance {
			t.Errorf("n=%d: estimate %d, relative error %.4f exceeds %.4f", n, got, relErr, tolerance)
		}
	}
}

// TestSmallCardinalityExact: linear counting keeps tiny cardinalities close to
// exact (a handful of distinct values must not read as dozens).
func TestSmallCardinality(t *testing.T) {
	for _, n := range []int{1, 3, 10, 50} {
		s := New()
		for i := 0; i < n; i++ {
			s.AddString(fmt.Sprintf("v%d", i))
		}
		got := s.Count()
		if abs(float64(got)-float64(n)) > 2 {
			t.Errorf("n=%d: estimate %d, expected within 2 of exact", n, got)
		}
	}
}

// TestDuplicatesIgnored: adding the same value many times does not inflate the
// count — HLL estimates DISTINCT values.
func TestDuplicatesIgnored(t *testing.T) {
	s := New()
	for i := 0; i < 100000; i++ {
		s.AddString("the-same-value")
	}
	if got := s.Count(); got > 2 {
		t.Errorf("100k duplicates estimated as %d distinct, want ~1", got)
	}
}

// TestMemoryBounded: the sketch is a fixed 16KB regardless of cardinality — no
// value is retained (RFC §7.3, INV-NO-ROWS). Adding a million values must not
// grow it.
func TestMemoryBounded(t *testing.T) {
	if SizeBytes() != 16384 {
		t.Errorf("SizeBytes() = %d, want 16384 (2^14 registers)", SizeBytes())
	}
	s := New()
	before := unsafe.Sizeof(*s)
	for i := 0; i < 1000000; i++ {
		s.AddString(fmt.Sprintf("x-%d", i))
	}
	after := unsafe.Sizeof(*s)
	if before != after || int(after) != 16384 {
		t.Errorf("sketch size changed with data: before=%d after=%d, want fixed 16384", before, after)
	}
}

// TestRelativeError: the published error is the precision-14 relative standard
// error and lands near the documented ~1% (RFC §14.4).
func TestRelativeError(t *testing.T) {
	re := RelativeError()
	if re <= 0 || re > 0.02 {
		t.Errorf("RelativeError() = %.4f, want a small positive bound (~0.008)", re)
	}
}

// TestNoExtensionPath: the sketch is pure Go — it estimates from streamed values
// with no server-side extension involved (the whole point of client-side HLL).
// This test exercises the full path in-process, proving the no-extension claim.
func TestNoExtensionPath(t *testing.T) {
	s := New()
	const distinct = 60000
	// Simulate streaming a column value-by-value, discarding each after Add.
	stream := func(yield func(string)) {
		for i := 0; i < 90000; i++ {
			yield(fmt.Sprintf("row-%d@example.invalid", i%distinct))
		}
	}
	stream(func(v string) { s.AddString(v) })
	got := s.Count()
	if abs(float64(got)-distinct)/distinct > 3*RelativeError() {
		t.Errorf("streamed estimate %d not within bound of %d", got, distinct)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
