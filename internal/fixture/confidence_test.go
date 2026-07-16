package fixture

import "testing"

// TestConfidenceOrdering enforces exact > measured > estimated > declared
// (RFC §7.1), with an unknown/absent level ranking below all of them (RFC §7.4).
func TestConfidenceOrdering(t *testing.T) {
	// Strictly descending order of strength.
	order := []Confidence{Exact, Measured, Estimated, Declared}
	for i := 0; i < len(order)-1; i++ {
		stronger, weaker := order[i], order[i+1]
		if !stronger.Stronger(weaker) {
			t.Errorf("%s should be stronger than %s", stronger, weaker)
		}
		if weaker.Stronger(stronger) {
			t.Errorf("%s should not be stronger than %s", weaker, stronger)
		}
		if !stronger.AtLeast(weaker) {
			t.Errorf("%s should be at least %s", stronger, weaker)
		}
	}

	// Absent/unknown confidence is weaker than declared (RFC §7.4: declared /
	// absent → WARN, but absence must never out-rank a named level).
	var absent Confidence = ""
	if absent.Stronger(Declared) {
		t.Errorf("absent confidence must not out-rank declared")
	}
	if Declared.Stronger(absent) != true {
		t.Errorf("declared should out-rank absent")
	}
	if absent.Valid() {
		t.Errorf("absent confidence should be invalid")
	}
	for _, c := range order {
		if !c.Valid() {
			t.Errorf("%s should be a valid level", c)
		}
	}
}

// TestConfidenceMin: verdict capping rests on Min returning the weaker level
// across a finding's dependencies (RFC §7.4).
func TestConfidenceMin(t *testing.T) {
	cases := []struct {
		a, b, want Confidence
	}{
		{Exact, Measured, Measured},
		{Measured, Estimated, Estimated},
		{Estimated, Declared, Declared},
		{Exact, Exact, Exact},
		{Exact, Declared, Declared},
		{Measured, "", ""}, // absent wins (stays weakest)
	}
	for _, tc := range cases {
		if got := Min(tc.a, tc.b); got != tc.want {
			t.Errorf("Min(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
		if got := Min(tc.b, tc.a); got != tc.want {
			t.Errorf("Min(%q, %q) = %q, want %q (commutativity)", tc.b, tc.a, got, tc.want)
		}
	}
}
