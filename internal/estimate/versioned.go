package estimate

import (
	"strconv"
	"strings"
)

// defaultFastPathMajor is the first Postgres major that fast-paths a NON-volatile
// column DEFAULT into the catalog instead of rewriting the table (RFC §9.1). This
// is the version boundary that makes ADD COLUMN ... DEFAULT cost version-conditional.
const defaultFastPathMajor = 11

// Major parses a Postgres major version from an engine.version string
// ("16" → 16, "11.5" → 11, "9.6" → 9). ok is false when the string is empty or
// its leading component is not a number.
func Major(version string) (major int, ok bool) {
	v := strings.TrimSpace(version)
	if v == "" {
		return 0, false
	}
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ClassifyAddColumnDefault resolves the cost class of `ADD COLUMN ... DEFAULT`,
// which is engine-version-conditional (RFC §9.1):
//
//   - a VOLATILE default (e.g. gen_random_uuid()) ALWAYS rewrites the table,
//     on every version;
//   - a non-volatile default is fast-pathed into the catalog on PG 11+
//     (CatalogOnly, O(1)), but rewrites the table on PG 10 and earlier.
//
// A validator applying one table of models across all versions would be
// confidently wrong on the older databases most likely to have scary migrations.
func ClassifyAddColumnDefault(volatileDefault bool, major int) OpClass {
	if volatileDefault {
		return TableRewrite
	}
	if major >= defaultFastPathMajor {
		return CatalogOnly
	}
	return TableRewrite
}
