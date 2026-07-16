package fixture

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Emit assembles a profiled fixture into human-readable, diffable rowshape.yaml
// bytes (RFC §5). It validates the mandatory meta fields, completes the profile
// block, computes meta.digest over the canonical form (§11), and writes
// two-space-indented YAML with map keys sorted — small enough to commit and
// clean to diff (RFC §3.3).
//
// Emit and Canonical are different by design: Emit is the readable artifact
// (includes meta.digest and meta.generated_at, preserves the document's field
// order); Canonical is the sorted, volatile-field-free byte string that the
// digest is computed over.
func Emit(f *Fixture) ([]byte, error) {
	if err := f.prepareForEmit(); err != nil {
		return nil, err
	}
	// The digest is computed last, over everything else, and stored for readers
	// (§11). Because the canonical form excludes meta.digest, storing it here
	// does not change the value.
	if err := f.SetDigest(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// prepareForEmit validates and fills the meta block so every emitted fixture is
// complete and self-describing.
func (f *Fixture) prepareForEmit() error {
	if f.RowshapeFixture == "" {
		f.RowshapeFixture = FormatVersion
	}
	// engine.version is mandatory: cost models are engine-version-conditional and
	// a validator MUST refuse to extrapolate without it (RFC §9.1).
	if f.Meta.Engine.Name == "" {
		return fmt.Errorf("emit: meta.engine.name is required")
	}
	if f.Meta.Engine.Version == "" {
		return fmt.Errorf("emit: meta.engine.version is mandatory (RFC §9.1)")
	}
	if f.Meta.Profile.Mode == "" {
		f.Meta.Profile.Mode = "fast"
	}
	// Always emit an explicit (possibly empty) escalation list so the profile
	// block is complete and self-describing.
	if f.Meta.Profile.Escalated == nil {
		f.Meta.Profile.Escalated = []string{}
	}
	return nil
}

// VerifyDigest reparses fixture bytes, recomputes the digest over the canonical
// form, and reports whether it matches the stored meta.digest (RFC §11). This is
// how a reader confirms a committed fixture has not been hand-edited out of sync
// with its identity.
func VerifyDigest(data []byte) (ok bool, stored, recomputed string, err error) {
	f, err := Parse(data)
	if err != nil {
		return false, "", "", err
	}
	stored = f.Meta.Digest
	recomputed, err = Digest(f)
	if err != nil {
		return false, stored, "", err
	}
	return stored == recomputed, stored, recomputed, nil
}
