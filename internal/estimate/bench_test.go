package estimate

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// The repo had no benchmarks. These pin the cost of the estimate hot paths — the
// extrapolation model and the byte/duration bucketers run for every finding that
// carries a duration estimate — so a future change that makes them quadratic or
// allocation-heavy is visible under `go test -bench`. docs/TESTING-GAPS.md item 11.
//
// Run with:  go test ./internal/estimate/ -run x -bench . -benchmem

func BenchmarkExtrapolate(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Extrapolate(BTreeBuild, 10_000, 250, 50_000_000, fixture.Estimated)
	}
}

func BenchmarkBucketFromBytes(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = BucketFromBytes(4_200_000_000)
	}
}

func BenchmarkBucket(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Bucket(1234.5)
	}
}
