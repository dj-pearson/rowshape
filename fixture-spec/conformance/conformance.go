// Package conformance is the executable conformance suite for the Rowshape
// Fixture Spec (RFC-0001 §13). It encodes the emitter, hydrator, and validator
// MUSTs so that rowshape's own CLI can be held to them, and so that a third
// party can hold their own EMITTER to them — which is what makes the spec a
// position rather than an aspiration (PRD §3, §16: the strategic value is that
// anyone can emit the format).
//
// CheckEmitterYAML is the third-party entry point, and it takes bytes on
// purpose. The rest of this package is typed in rowshape's internal fixture
// model, which no outside module can name — Go forbids importing
// .../internal/... across module boundaries, so a signature like
// CheckEmitter(*fixture.Fixture) is uncallable from anywhere but this repo. An
// emitter has bytes; bytes are the honest interface.
//
// Scope, stated plainly: CheckHydrator and CheckValidator exercise ROWSHAPE's
// hydrator and verdict engine. They are regression tests that the reference
// implementation obeys its own spec, not a harness a third party can plug their
// hydrator into — doing that needs an agreed wire format for hydrated rows,
// which RFC-0001 does not define.
//
// Lives under fixture-spec/ in this monorepo; in the published layout it is the
// rowshape/fixture-spec repository alongside schema/rowshape.schema.json. Note
// that the Go suite cannot move there as-is: it imports rowshape's internal
// packages, which stop compiling the moment it becomes a separate module.
package conformance

import (
	"fmt"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
)

// Violation is one failed conformance MUST, naming the rule and where it broke.
type Violation struct {
	Rule    string // the RFC clause, e.g. "§6.1 no range on text"
	Where   string // the offending path, e.g. "public.users.email"
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("[%s] %s: %s", v.Rule, v.Where, v.Message)
}

// textTypes are the type spellings that MUST NOT carry a range (RFC §6.1): the
// min/max of a text or bytea column would be real production values.
var textTypes = map[string]bool{
	"text": true, "bytea": true, "varchar": true, "character varying": true,
	"char": true, "character": true, "citext": true, "json": true, "jsonb": true,
}

// CheckEmitterYAML runs the emitter MUSTs against fixture bytes. This is the
// entry point for a third-party emitter: hand it what you emitted.
//
// It exists because CheckEmitter takes *fixture.Fixture — a type in
// rowshape's internal tree, which no other module may import. The suite claimed
// anyone could run it while its whole surface was, in fact, uncallable from
// outside this repository.
//
// A parse failure is returned as an error rather than a Violation: unreadable
// bytes are not a conformance verdict, they are the absence of one.
func CheckEmitterYAML(data []byte) ([]Violation, error) {
	f, err := fixture.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	return CheckEmitter(f), nil
}

// CheckEmitter runs the statically-checkable emitter MUSTs (RFC §13) against a
// parsed fixture: a known format version; never `range` on text/bytea (§6.1);
// `unique` is exact or absent, never inferred from a sample (§7.2); every fact
// carries a valid confidence (§6.1); and the canonical digest is stable across
// repeated computation over the unchanged fixture (§11). Returns every violation
// found (empty means conformant).
func CheckEmitter(f *fixture.Fixture) []Violation {
	var vs []Violation

	if major := majorOf(f.RowshapeFixture); major != fixture.FormatVersion {
		vs = append(vs, Violation{"§12 version", "rowshape_fixture", fmt.Sprintf("unknown or missing format version %q (expected %q)", f.RowshapeFixture, fixture.FormatVersion)})
	}

	for tname := range f.Tables {
		tbl := f.Tables[tname]
		vs = append(vs, checkConfidence(tname+".rows", "rows", tbl.Rows.Confidence)...)

		for cname := range tbl.Columns {
			col := tbl.Columns[cname]
			where := tname + "." + cname

			if col.Range != nil && textTypes[strings.ToLower(strings.TrimSpace(baseType(col.Type)))] {
				vs = append(vs, Violation{"§6.1 no range on text", where, "a text/bytea column emitted a range; its min/max are real production values"})
			}
			if col.Unique != nil && col.Unique.Confidence != fixture.Exact {
				vs = append(vs, Violation{"§7.2 uniqueness never from a sample", where, fmt.Sprintf("unique carries confidence %q; unique MUST be exact or absent", col.Unique.Confidence)})
			}
			if col.NullFraction != nil {
				vs = append(vs, checkConfidence(where+".null_fraction", "null_fraction", col.NullFraction.Confidence)...)
			}
			if col.Distinct != nil {
				vs = append(vs, checkConfidence(where+".distinct", "distinct", col.Distinct.Confidence)...)
			}
		}
		for _, ref := range tbl.References {
			if ref.OrphanFraction != nil {
				vs = append(vs, checkConfidence(tname+"."+ref.Column+".orphan_fraction", "orphan_fraction", ref.OrphanFraction.Confidence)...)
			}
			if ref.Fanout != nil {
				vs = append(vs, checkConfidence(tname+"."+ref.Column+".fanout", "fanout", ref.Fanout.Confidence)...)
			}
		}
	}

	// The digest MUST be stable across runs against an unchanged fixture (§11).
	d1, e1 := fixture.Digest(f)
	d2, e2 := fixture.Digest(f)
	if e1 != nil || e2 != nil || d1 != d2 || d1 == "" {
		vs = append(vs, Violation{"§11 stable digest", "meta.digest", "canonical digest is not stable across repeated computation"})
	}

	return vs
}

// checkConfidence enforces that a fact carries a valid confidence (RFC §6.1).
func checkConfidence(where, fact string, c fixture.Confidence) []Violation {
	if !c.Valid() {
		return []Violation{{"§6.1 confidence on every fact", where, fmt.Sprintf("%s fact has no valid confidence (got %q)", fact, c)}}
	}
	return nil
}

// baseType strips a type's length/precision modifier ("varchar(255)" -> "varchar").
func baseType(t string) string {
	if i := strings.IndexByte(t, '('); i >= 0 {
		return t[:i]
	}
	return t
}

// majorOf extracts the major component of a version string.
func majorOf(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}
