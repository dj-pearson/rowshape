package fixture

// Confidence is the load-bearing idea of the fixture format (RFC §7): every
// fact carries the confidence with which it is known, and a validator's verdict
// is capped by the minimum confidence of the facts it relied on (RFC §7.4).
//
// The four levels are strictly ordered exact > measured > estimated > declared
// (RFC §7.1). An absent/unknown confidence is treated as weaker than every
// named level, so a fact whose confidence cannot be read can never license a
// PASS.
type Confidence string

// The four confidence levels (RFC §7.1). These strings are part of the fixture
// format and are permanent.
const (
	// Exact is proven: a scan, a count, or a structural guarantee.
	Exact Confidence = "exact"
	// Measured is a full pass over the data with bounded error (e.g. HLL).
	Measured Confidence = "measured"
	// Estimated is extrapolated from a sample or the planner's stats.
	Estimated Confidence = "estimated"
	// Declared is asserted by a human in the file and is not verified.
	Declared Confidence = "declared"
)

// rank maps each level onto its position in the ordering. An unknown or absent
// confidence ranks below declared so it is always the weakest reading (RFC §7.4:
// "declared / absent → WARN").
func (c Confidence) rank() int {
	switch c {
	case Exact:
		return 3
	case Measured:
		return 2
	case Estimated:
		return 1
	case Declared:
		return 0
	default:
		return -1
	}
}

// Valid reports whether c is one of the four named levels (RFC §7.1).
func (c Confidence) Valid() bool {
	return c.rank() >= 0
}

// Stronger reports whether c is strictly higher in the ordering than o.
func (c Confidence) Stronger(o Confidence) bool {
	return c.rank() > o.rank()
}

// AtLeast reports whether c is at least as strong as o.
func (c Confidence) AtLeast(o Confidence) bool {
	return c.rank() >= o.rank()
}

// Min returns the weaker of two confidences. This is the operation verdict
// capping is built on: a finding's confidence is the Min across every fixture
// fact it depends on (RFC §7.4). An absent confidence wins (stays weakest).
func Min(a, b Confidence) Confidence {
	if a.rank() <= b.rank() {
		return a
	}
	return b
}
