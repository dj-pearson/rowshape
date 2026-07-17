package verdict

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// PredicateType is the URI rowshape owns for its attestation predicate (PRD
// §9.1). It is part of the DSSE contract (INV-DSSE-SHAPE) and permanent.
const PredicateType = "https://rowshape.com/attestation/v1"

// StatementType is the in-toto Statement v1 type URI. The verdict is shaped as a
// signable in-toto/DSSE predicate from day one (PRD §9.1); phase 5 decides only
// where signatures go, not the shape.
const StatementType = "https://in-toto.io/Statement/v1"

// Statement is an in-toto Statement wrapping a Result as its predicate (PRD
// §9.1). Its subject is the fixture digest plus the digests of the migration
// files validated; predicateType is the owned URI; predicate is the §10 verdict
// body verbatim. This is what a Sigstore/DSSE envelope signs.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Result    `json:"predicate"`
}

// Subject is one item the attestation is about: a name and its digest(s) keyed
// by algorithm (in-toto DigestSet). Hex is bare — no "sha256:" prefix — per the
// in-toto spec.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// MigrationFile is a migration validated by a Result, paired with its bytes so
// Statement can hash it into a subject.
type MigrationFile struct {
	Path     string
	Contents []byte
}

// Statement builds the in-toto Statement for this Result: subject = the fixture
// digest (from r.Fixture.Digest) plus a sha256 subject per migration file
// (PRD §9.1). The fixture digest is expected as "sha256:<hex>"; the "sha256:"
// prefix is stripped into the DigestSet key/value split the in-toto spec wants.
func (r Result) Statement(migrations []MigrationFile) Statement {
	subjects := make([]Subject, 0, 1+len(migrations))
	if r.Fixture.Digest != "" {
		alg, hexsum := splitDigest(r.Fixture.Digest)
		subjects = append(subjects, Subject{
			Name:   "fixture:" + r.Fixture.ID,
			Digest: map[string]string{alg: hexsum},
		})
	}
	for _, m := range migrations {
		sum := sha256.Sum256(m.Contents)
		subjects = append(subjects, Subject{
			Name:   m.Path,
			Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])},
		})
	}
	return Statement{
		Type:          StatementType,
		Subject:       subjects,
		PredicateType: PredicateType,
		Predicate:     r,
	}
}

// MarshalStatement builds the Statement and serializes it as indented JSON —
// the bytes a DSSE envelope carries as its payload.
func (r Result) MarshalStatement(migrations []MigrationFile) ([]byte, error) {
	return json.MarshalIndent(r.Statement(migrations), "", "  ")
}

// splitDigest splits "sha256:<hex>" into ("sha256", "<hex>"). A digest with no
// recognizable prefix is treated as a bare sha256 hex.
func splitDigest(d string) (alg, hexsum string) {
	if i := strings.IndexByte(d, ':'); i >= 0 {
		return d[:i], d[i+1:]
	}
	return "sha256", d
}
