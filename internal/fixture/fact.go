package fixture

import "gopkg.in/yaml.v3"

// Fact is a scalar fact together with the confidence it is known at (RFC §6.1).
//
// Facts are {value, confidence, via} objects rather than bare scalars. This is
// verbose and it is the point: a reader that wants to ignore confidence must do
// so deliberately. A bare scalar is accepted as shorthand for
// confidence:estimated — the weakest reading, never the strongest (RFC §6.1).
//
// T is the value's Go type: int64 for counts, float64 for fractions, bool for
// uniqueness.
type Fact[T any] struct {
	Value      T          `yaml:"value"`
	Confidence Confidence `yaml:"confidence,omitempty"`
	Via        string     `yaml:"via,omitempty"`   // the method: unique_index, hll, pg_stats, scan, ...
	Error      float64    `yaml:"error,omitempty"` // bounded error for measured facts (e.g. HLL ±0.02)
}

// UnmarshalYAML reads a fact either as a mapping ({value, confidence, via, ...})
// or as a bare scalar. A bare scalar takes confidence:estimated (RFC §6.1).
// Unknown keys inside a fact mapping are ignored (RFC §12).
func (f *Fact[T]) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if err := node.Decode(&f.Value); err != nil {
			return err
		}
		f.Confidence = Estimated
		return nil
	}
	// Decode the known keys by hand so unknown keys are ignored without a
	// recursive call back into this method.
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		var err error
		switch key {
		case "value":
			err = val.Decode(&f.Value)
		case "confidence":
			err = val.Decode(&f.Confidence)
		case "via":
			err = val.Decode(&f.Via)
		case "error":
			err = val.Decode(&f.Error)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
