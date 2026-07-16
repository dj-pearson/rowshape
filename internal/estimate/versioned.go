package estimate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
)

// ErrNoVersion is returned when extrapolation is attempted against a fixture with
// no declared engine version. A validator MUST refuse rather than assume a recent
// default — the older databases that lack a version are exactly the ones most
// likely to have the scary migrations (RFC §9.1).
var ErrNoVersion = errors.New("estimate: cannot extrapolate without meta.engine.version — refusing to assume an engine version (RFC §9.1)")

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

// ForFixture extrapolates op against a fixture's DECLARED rows for a table,
// refusing when the engine version is absent (RFC §9/§9.1). basisRows/basisMs are
// the measured basis (e.g. the rows a hydrated run rewrote and how long it took).
// The estimate's confidence is capped by the confidence of the table's `rows`.
func ForFixture(op OpClass, f *fixture.Fixture, table string, basisRows, basisMs int64) (verdict.Estimate, error) {
	if f == nil {
		return verdict.Estimate{}, ErrNoVersion
	}
	if _, ok := Major(f.Meta.Engine.Version); !ok {
		return verdict.Estimate{}, ErrNoVersion
	}
	tbl, ok := f.Tables[table]
	if !ok {
		return verdict.Estimate{}, fmt.Errorf("estimate: no table %q in fixture", table)
	}
	return Extrapolate(op, basisRows, basisMs, tbl.Rows.Value, tbl.Rows.Confidence), nil
}
