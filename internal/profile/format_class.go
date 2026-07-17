package profile

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// The closed format-class vocabulary (RFC §6.3). Emitters MUST use one of these;
// opaque is always legal and is the mandatory fallback.
const (
	fmtUUID          = "uuid"
	fmtEmail         = "email"
	fmtURL           = "url"
	fmtHostname      = "hostname"
	fmtIPv4          = "ipv4"
	fmtIPv6          = "ipv6"
	fmtPhone         = "phone"
	fmtJSON          = "json"
	fmtJSONBShape    = "jsonb_shape"
	fmtBase64        = "base64"
	fmtHex           = "hex"
	fmtSlug          = "slug"
	fmtISODate       = "iso_date"
	fmtNumericString = "numeric_string"
	fmtEnumLike      = "enum_like"
	fmtFreeText      = "free_text"
	fmtOpaque        = "opaque"
)

// enumLikeMaxDistinct is the cardinality ceiling under which a text column reads
// as enum_like rather than free_text (a hint to the hydrator, §6.3).
const enumLikeMaxDistinct = 32

var (
	reEmail    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	reURL      = regexp.MustCompile(`^https?://[^\s]+$`)
	reUUID     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reIPv4     = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	reIPv6     = regexp.MustCompile(`^[0-9a-fA-F:]+:[0-9a-fA-F:]+$`)
	reHostname = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)
	rePhone    = regexp.MustCompile(`^\+?[0-9][0-9\s().-]{6,}$`)
	reHex      = regexp.MustCompile(`^(0x)?[0-9a-fA-F]+$`)
	reBase64   = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
	reSlug     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	reISODate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2})?)?`)
	reNumeric  = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
)

// inferTextFormat classifies a text column from a sample of its values against
// the closed §6.3 vocabulary. It is deliberately conservative: a specific class
// is assigned only when nearly every sampled value matches it, because a wrong
// class produces confidently-wrong synthetic data — worse than obviously-fake
// data (§6.3). When unsure it returns opaque (the mandatory fallback), or
// enum_like / free_text as weak, safe hints.
func inferTextFormat(samples []string, distinct int64, distinctKnown bool) string {
	nonEmpty := make([]string, 0, len(samples))
	for _, s := range samples {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) == 0 {
		return fmtOpaque
	}

	// Strong, unambiguous patterns win outright — even for a low-cardinality
	// column, "generate an email" is a better hint than "pick from a set".
	if c := dominantPattern(nonEmpty, strongChecks); c != "" {
		return c
	}

	// A low-cardinality column reads as enum_like: a useful hint that needs no
	// value set emitted (that is privacy-gated, §8.2). This is checked before the
	// weak patterns so a 3-value status column is enum_like, not slug.
	if distinctKnown && distinct > 0 && distinct <= enumLikeMaxDistinct {
		return fmtEnumLike
	}

	// Weaker patterns only apply to higher-cardinality columns.
	if c := dominantPattern(nonEmpty, weakChecks); c != "" {
		return c
	}

	// Wordy, spaced content is free_text; otherwise opaque.
	if looksLikeProse(nonEmpty) {
		return fmtFreeText
	}
	return fmtOpaque
}

type formatCheck struct {
	class string
	re    *regexp.Regexp
}

// strongChecks are specific enough that a match is a confident classification.
var strongChecks = []formatCheck{
	{fmtEmail, reEmail},
	{fmtURL, reURL},
	{fmtUUID, reUUID},
	{fmtIPv4, reIPv4},
	{fmtISODate, reISODate},
}

// weakChecks match broad shapes that only make sense for higher-cardinality
// columns (a two-value column of digits is enum_like, not numeric_string).
var weakChecks = []formatCheck{
	{fmtHostname, reHostname},
	// numeric_string before slug/hex: an all-digit value matches all three, and
	// "it's a number in a string" is the most specific reading.
	{fmtNumericString, reNumeric},
	{fmtIPv6, reIPv6},
	{fmtPhone, rePhone},
	{fmtSlug, reSlug},
	{fmtHex, reHex},
	{fmtBase64, reBase64},
}

// dominantPattern returns a format class if ≥95% of the sample matches exactly
// one of the given checks, else "".
func dominantPattern(vals []string, checks []formatCheck) string {
	const threshold = 0.95
	for _, c := range checks {
		matched := 0
		for _, v := range vals {
			if c.re.MatchString(v) {
				matched++
			}
		}
		if float64(matched)/float64(len(vals)) >= threshold {
			return c.class
		}
	}
	return ""
}

// looksLikeProse reports whether the sample reads as natural-language free text
// (most values contain a space and some letters).
func looksLikeProse(vals []string) bool {
	spaced := 0
	for _, v := range vals {
		if strings.ContainsRune(v, ' ') && strings.ContainsFunc(v, isLetter) {
			spaced++
		}
	}
	return float64(spaced)/float64(len(vals)) >= 0.5
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// jsonSkeleton builds a key skeleton (key names, structure, leaf types) merged
// across a sample of JSON documents. It never retains a leaf value: JSON columns
// are the richest leak vector in the format and are treated with suspicion
// (RFC §6.3).
func jsonSkeleton(samples []string) any {
	var merged any
	seen := false
	for _, s := range samples {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			continue
		}
		merged = mergeSkeleton(merged, skeletonOf(v))
		seen = true
	}
	if !seen {
		return nil
	}
	return merged
}

// skeletonOf reduces a decoded JSON value to its type skeleton. Objects keep
// their keys; arrays collapse to a single merged element skeleton; every scalar
// becomes its type name only.
func skeletonOf(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[k] = skeletonOf(val)
		}
		return m
	case []any:
		var el any
		for _, e := range x {
			el = mergeSkeleton(el, skeletonOf(e))
		}
		if el == nil {
			el = "empty"
		}
		return []any{el}
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

// mergeSkeleton unions two skeletons. Objects union their keys; arrays merge
// their element skeletons; conflicting leaf types become a sorted "a|b" union.
// The result is order-independent so a fixture's digest is stable (RFC §11).
func mergeSkeleton(a, b any) any {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		out := make(map[string]any, len(am)+len(bm))
		for k, v := range am {
			out[k] = v
		}
		for k, v := range bm {
			if existing, ok := out[k]; ok {
				out[k] = mergeSkeleton(existing, v)
			} else {
				out[k] = v
			}
		}
		return out
	}
	aa, aaok := a.([]any)
	ba, baok := b.([]any)
	if aaok && baok {
		var el any
		for _, e := range aa {
			el = mergeSkeleton(el, e)
		}
		for _, e := range ba {
			el = mergeSkeleton(el, e)
		}
		return []any{el}
	}
	as, asok := a.(string)
	bs, bsok := b.(string)
	if asok && bsok {
		return unionLeaf(as, bs)
	}
	// Structural mismatch (e.g. object vs scalar).
	return "mixed"
}

// unionLeaf merges two leaf-type strings into a sorted, de-duplicated union.
func unionLeaf(a, b string) string {
	set := map[string]struct{}{}
	for _, part := range strings.Split(a, "|") {
		set[part] = struct{}{}
	}
	for _, part := range strings.Split(b, "|") {
		set[part] = struct{}{}
	}
	parts := make([]string, 0, len(set))
	for p := range set {
		parts = append(parts, p)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
