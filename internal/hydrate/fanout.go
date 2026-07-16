package hydrate

import (
	"math"

	"github.com/rowshape/rowshape/internal/fixture"
)

// assignForeignKeys returns, for each of childN child rows, the parent ordinal
// (in [0, parentN)) it references. The assignment reproduces the fan-out
// DISTRIBUTION shape — its p50/p95/max children-per-parent — not merely the mean
// (RFC §6.6). A hydrator that only matched the mean would turn a long-tailed
// cascade into a uniform one and miss exactly the migration pathology fan-out
// exists to capture.
func assignForeignKeys(seed int64, table string, ref fixture.Reference, childN, parentN int64) []int64 {
	out := make([]int64, childN)
	if childN <= 0 {
		return out
	}
	if parentN <= 0 {
		return out // no parents to point at; leave zeros
	}
	if ref.Fanout == nil {
		// No fan-out fact: spread children roughly uniformly across parents.
		for i := int64(0); i < childN; i++ {
			out[i] = i % parentN
		}
		return out
	}

	counts := scaledTargetCounts(parentN, childN, ref.Fanout)

	// Fill child slots parent-by-parent following the constructed counts. This is
	// deterministic and reproduces the per-parent count distribution exactly.
	idx := int64(0)
	for p := int64(0); p < parentN && idx < childN; p++ {
		for k := int64(0); k < counts[p] && idx < childN; k++ {
			out[idx] = p
			idx++
		}
	}
	// Any leftover children (from rounding) wrap onto the busiest parent so the
	// tail, not the body, absorbs the remainder.
	for ; idx < childN; idx++ {
		out[idx] = parentN - 1
	}
	return out
}

// scaledTargetCounts builds a per-parent target child-count array whose ascending
// quantiles hit the fixture's p50/p95/max, then rescales it to sum to childN so
// the realized distribution keeps the target shape at the hydrated scale.
func scaledTargetCounts(parentN, childN int64, fo *fixture.Fanout) []int64 {
	p50 := fo.P50
	p95 := fo.P95
	mx := fo.Max
	if p50 <= 0 {
		p50 = fo.Mean
	}
	if p95 < p50 {
		p95 = p50
	}
	if mx < p95 {
		mx = p95
	}

	raw := make([]float64, parentN)
	var total float64
	for i := int64(0); i < parentN; i++ {
		q := (float64(i) + 0.5) / float64(parentN) // this parent's quantile position
		v := fanoutQuantile(q, p50, p95, mx)
		if v < 0 {
			v = 0
		}
		raw[i] = v
		total += v
	}

	counts := make([]int64, parentN)
	if total <= 0 {
		// Degenerate fan-out: spread evenly.
		for i := range counts {
			counts[i] = childN / parentN
		}
		return counts
	}
	scale := float64(childN) / total
	for i := range raw {
		counts[i] = int64(math.Round(raw[i] * scale))
	}
	return counts
}

// fanoutQuantile interpolates the child-count at ascending quantile q from the
// three anchor points: the median (0.5), the 95th percentile (0.95), and the max
// (1.0). The upper segments interpolate GEOMETRICALLY, not linearly: a real
// fan-out is heavy-tailed, so between p50 and p95 (and p95 and max) most parents
// sit near the lower anchor and only the very top spikes. Linear interpolation
// would over-weight the middle and inflate the mean, collapsing the tail after
// rescaling. The lower half ramps linearly from a floor up to the median.
func fanoutQuantile(q, p50, p95, mx float64) float64 {
	switch {
	case q <= 0.5:
		floor := math.Max(0, 2*p50-p95) // reflect p95 about p50 for a plausible lower tail
		return lerp(floor, p50, q/0.5)
	case q <= 0.95:
		return geomLerp(p50, p95, (q-0.5)/0.45)
	default:
		return geomLerp(p95, mx, (q-0.95)/0.05)
	}
}

// lerp is a linear interpolation between a and b at t in [0, 1].
func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

// geomLerp interpolates geometrically: a·(b/a)^t. It grows slowly near a and
// accelerates toward b, matching a heavy tail. Falls back to linear if either
// endpoint is non-positive.
func geomLerp(a, b, t float64) float64 {
	if a <= 0 || b <= 0 {
		return lerp(a, b, t)
	}
	return a * math.Pow(b/a, t)
}
