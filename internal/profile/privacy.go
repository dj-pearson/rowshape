package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/rowshape/rowshape/internal/fixture"
)

// Privacy is a fixture privacy level (RFC §8.2).
type Privacy string

const (
	// PrivacyStrict emits no numeric/temporal range, no histograms, no
	// values/frequencies, and no verbatim CHECK expressions.
	PrivacyStrict Privacy = "strict"
	// PrivacyStandard is the default: ranges, histograms, and CHECK expressions
	// are emitted, but never value sets.
	PrivacyStandard Privacy = "standard"
	// PrivacyPermissive additionally materializes small, safe value sets.
	PrivacyPermissive Privacy = "permissive"
)

// DefaultK is the minimum per-value occurrence count for a value to appear under
// permissive privacy (RFC §8.2). A value seen fewer than k times could identify
// an individual and is withheld.
const DefaultK = 20

// permissiveMaxDistinct caps the cardinality at which a value set may be
// materialized under permissive privacy (RFC §8.2).
const permissiveMaxDistinct = 50

// sourceSalt is a fixed application salt for meta.source (RFC §8.4). §8.4 wants
// a per-fixture salt, but meta.source is part of the canonical form (§11), so a
// random salt would make the digest unstable across runs. A fixed salt keeps the
// digest stable while still never publishing the hostname — which is all §8.4
// claims to defend ("casual disclosure, not a determined attacker").
const sourceSalt = "rowshape-fixture-source-v1"

// ParsePrivacy validates a privacy level. An empty string is standard — the
// default MUST NOT be permissive (RFC §8.2).
func ParsePrivacy(s string) (Privacy, error) {
	switch Privacy(s) {
	case "", PrivacyStandard:
		return PrivacyStandard, nil
	case PrivacyStrict:
		return PrivacyStrict, nil
	case PrivacyPermissive:
		return PrivacyPermissive, nil
	default:
		return "", fmt.Errorf("unknown privacy level %q (want strict | standard | permissive)", s)
	}
}

// HashSource returns meta.source: a salted SHA-256 hash of the source host,
// never the hostname itself (RFC §8.4). An empty host yields an empty source.
func HashSource(host string) string {
	if host == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sourceSalt + "\x00" + host))
	return fixture.DigestPrefix + hex.EncodeToString(sum[:])
}

// ApplyPrivacy enforces a privacy level over a fixture in place (RFC §8.2). It is
// the single emit-time gate: per-column `redact` overrides are applied first and
// always win, then the level's field matrix. If k <= 0 the default is used.
func ApplyPrivacy(f *fixture.Fixture, level Privacy, k int) {
	if k <= 0 {
		k = DefaultK
	}
	for tname, tbl := range f.Tables {
		rows := tbl.Rows.Value
		for cname, col := range tbl.Columns {
			applyColumnPrivacy(&col, level, k, rows)
			tbl.Columns[cname] = col
		}
		if level == PrivacyStrict {
			// Under strict, verbatim CHECK expressions become opaque (RFC §6.4).
			for i := range tbl.Constraints {
				if tbl.Constraints[i].Kind == "check" && tbl.Constraints[i].Expression != "" {
					tbl.Constraints[i].Expression = "opaque"
				}
			}
		}
		f.Tables[tname] = tbl
	}
}

// applyColumnPrivacy redacts one column: per-column overrides first, then the
// level's rules.
func applyColumnPrivacy(col *fixture.Column, level Privacy, k int, rows int64) {
	redact := redactSet(col.Redact)
	switch {
	case redact["all"]:
		// "opaque free_text only" (RFC §8.2): drop every value-derived stat.
		col.Range = nil
		col.Histogram = nil
		col.Values = nil
		col.Frequencies = nil
		col.Length = nil
		col.Shape = nil
		col.Format = fmtOpaque
	default:
		if redact["range"] {
			col.Range = nil
		}
		if redact["histogram"] {
			col.Histogram = nil
		}
		if redact["values"] {
			col.Values = nil
			col.Frequencies = nil
		}
		if redact["frequencies"] {
			col.Frequencies = nil
		}
		if redact["length"] {
			col.Length = nil
		}
	}

	switch level {
	case PrivacyStrict:
		col.Range = nil
		col.Histogram = nil
		col.Values = nil
		col.Frequencies = nil
	case PrivacyStandard:
		col.Values = nil
		col.Frequencies = nil
	case PrivacyPermissive:
		if !permissiveValuesAllowed(col, rows, k) {
			col.Values = nil
			col.Frequencies = nil
		}
	}
}

// permissiveValuesAllowed reports whether a value set is safe to publish under
// permissive privacy: distinct <= 50 AND every value occurs at least k times
// (RFC §8.2). The per-value count is estimated as frequency × declared rows.
func permissiveValuesAllowed(col *fixture.Column, rows int64, k int) bool {
	if len(col.Values) == 0 {
		return false
	}
	if col.Distinct == nil || col.Distinct.Value > permissiveMaxDistinct {
		return false
	}
	if len(col.Frequencies) != len(col.Values) {
		return false
	}
	for _, fr := range col.Frequencies {
		if fr*float64(rows) < float64(k) {
			return false
		}
	}
	return true
}

// redactSet turns a column's redact list into a lookup set.
func redactSet(r fixture.Redact) map[string]bool {
	if len(r) == 0 {
		return nil
	}
	set := make(map[string]bool, len(r))
	for _, tok := range r {
		set[tok] = true
	}
	return set
}
