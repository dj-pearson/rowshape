package findings

import (
	"fmt"
	"strings"

	"github.com/rowshape/rowshape/internal/estimate"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

func init() { validate.Register(rsIndex{}) }

// rsIndex detects index pathologies (RFC §6.5, §9.1, PRD §10):
//
//   - RS-INDEX-001: a non-concurrent CREATE INDEX takes a lock that blocks writes
//     for the whole O(n log n) build; recommend CREATE INDEX CONCURRENTLY.
//   - RS-INDEX-010: a CREATE UNIQUE INDEX on a column whose profiled uniqueness is
//     violated (proven duplicates) cannot build; when uniqueness is merely
//     unproven, capping declines to certify (WARN).
//   - RS-INDEX-020: a non-concurrent REINDEX rebuilds under lock; its duration is
//     bucketed from the index's on-disk bytes/bloat (RFC §6.5).
type rsIndex struct{}

func (rsIndex) Analyze(f *fixture.Fixture, c *validate.Capture) []verdict.Finding {
	_, hasVersion := estimate.Major(f.Meta.Engine.Version)

	var out []verdict.Finding
	for i, st := range c.Statements {
		clean := collapseSpaces(stripSQLComments(st.SQL))
		upper := strings.ToUpper(clean)

		if unique, concurrent, table, cols, ok := parseCreateIndex(clean, upper); ok {
			// Resolve at the caller (RFC §5), as every other analyzer does. Without
			// it `CREATE INDEX ON orders (...)` missed the fixture key
			// `public.orders`, and the zero-value table meant rows=0 — reported as
			// an `instant` build for what may be a huge table.
			table = resolveTable(f, table)
			if !concurrent {
				out = append(out, nonConcurrentFinding(f, c, i, table, hasVersion))
			}
			if unique {
				out = append(out, indexUniqueFinding(f, table, cols))
			}
			continue
		}
		if name, isTable, concurrent, ok := parseReindex(clean, upper); ok && !concurrent {
			// Only REINDEX TABLE names a table. REINDEX INDEX names an INDEX, which
			// findIndex matches against index names rather than fixture table keys,
			// so resolving it there would be wrong.
			if isTable {
				name = resolveTable(f, name)
			}
			if fnd, ok := reindexFinding(f, name, isTable); ok {
				out = append(out, fnd)
			}
		}
	}
	return out
}

// nonConcurrentFinding flags a plain CREATE INDEX (statement i) and recommends
// CONCURRENTLY.
func nonConcurrentFinding(f *fixture.Fixture, c *validate.Capture, i int, table string, hasVersion bool) verdict.Finding {
	tbl := f.Tables[table]
	fnd := verdict.Finding{
		Code:        "RS-INDEX-001",
		Severity:    verdict.SeverityWarn,
		Title:       fmt.Sprintf("Non-concurrent CREATE INDEX on %s locks out writes during the build", shortTable(table)),
		Detail:      "A plain CREATE INDEX holds a SHARE lock that blocks writes for the whole build.",
		DependsOn:   []string{table + ".rows"},
		Remediation: remediation("RS-INDEX-001"),
		Explain:     "rowshape explain RS-INDEX-001",
	}
	if hasVersion {
		fnd.Estimate = estimateFor(c, i, estimate.BTreeBuild, table, tbl.Rows.Value, tbl.Rows.Confidence, tableKnown(f, table))
	}
	return fnd
}

// indexUniqueFinding certifies (or refuses) a CREATE UNIQUE INDEX by the column's
// profiled uniqueness, reusing the shared uniqueness classification.
func indexUniqueFinding(f *fixture.Fixture, table string, cols []string) verdict.Finding {
	dep, target := uniqueDependency(table, cols)
	if uniquenessState(f, table, cols) == uniqViolated {
		return verdict.Finding{
			Code:        "RS-INDEX-010",
			Severity:    verdict.SeverityError,
			Title:       fmt.Sprintf("CREATE UNIQUE INDEX on %s will fail: the column has duplicate values", target),
			Detail:      fmt.Sprintf("%s is proven non-unique (unique=false, exact); the unique index cannot build.", target),
			Evidence:    map[string]any{"unique": false},
			DependsOn:   []string{dep},
			Remediation: remediation("RS-INDEX-010"),
			Explain:     "rowshape explain RS-INDEX-010",
		}
	}
	return verdict.Finding{
		Code:        "RS-INDEX-010",
		Severity:    verdict.SeverityInfo,
		Title:       fmt.Sprintf("Uniqueness of %s not confirmed for CREATE UNIQUE INDEX", target),
		Detail:      fmt.Sprintf("A unique index on %s can only PASS if uniqueness is proven exact; a sample never establishes it (INV-UNIQUENESS).", target),
		DependsOn:   []string{dep},
		Remediation: remediation("RS-INDEX-010"),
		Explain:     "rowshape explain RS-INDEX-010",
	}
}

// reindexFinding flags a non-concurrent REINDEX and buckets its duration from the
// index's on-disk bytes/bloat (RFC §6.5).
func reindexFinding(f *fixture.Fixture, name string, isTable bool) (verdict.Finding, bool) {
	table, idx, ok := findIndex(f, name, isTable)
	if !ok {
		return verdict.Finding{}, false
	}
	bytes := idx.Bytes
	bloat := 0.0
	if idx.BloatEstimate != nil {
		bloat = *idx.BloatEstimate
	}
	fnd := verdict.Finding{
		Code:        "RS-INDEX-020",
		Severity:    verdict.SeverityWarn,
		Title:       fmt.Sprintf("Non-concurrent REINDEX of %s rebuilds under lock (%s)", name, estimate.BucketFromBytes(bytes)),
		Detail:      fmt.Sprintf("REINDEX rewrites the whole index (%d bytes, bloat estimate %.0f%%) while holding a lock that blocks writes.", bytes, bloat*100),
		Evidence:    map[string]any{"index_bytes": bytes, "bloat_estimate": bloat},
		DependsOn:   []string{table + ".rows"},
		Estimate:    &verdict.Estimate{Bucket: estimate.BucketFromBytes(bytes), Model: "reindex_bytes"},
		Remediation: remediation("RS-INDEX-020"),
		Explain:     "rowshape explain RS-INDEX-020",
	}
	return fnd, true
}

// findIndex locates an index by name (REINDEX INDEX) or the largest index of a
// table (REINDEX TABLE), returning the owning table and the index.
func findIndex(f *fixture.Fixture, name string, isTable bool) (string, fixture.Index, bool) {
	if isTable {
		tbl, ok := f.Tables[name]
		if !ok || len(tbl.Indexes) == 0 {
			return "", fixture.Index{}, false
		}
		big := tbl.Indexes[0]
		for _, ix := range tbl.Indexes[1:] {
			if ix.Bytes > big.Bytes {
				big = ix
			}
		}
		return name, big, true
	}
	for tname, tbl := range f.Tables {
		for _, ix := range tbl.Indexes {
			if strings.EqualFold(ix.Name, name) {
				return tname, ix, true
			}
		}
	}
	return "", fixture.Index{}, false
}

// parseCreateIndex recognizes CREATE [UNIQUE] INDEX [CONCURRENTLY] name ON table
// (cols), returning the flags, table, and columns.
func parseCreateIndex(clean, upper string) (unique, concurrent bool, table string, cols []string, ok bool) {
	if !strings.HasPrefix(upper, "CREATE") || (!strings.Contains(upper, "CREATE INDEX") && !strings.Contains(upper, "CREATE UNIQUE INDEX")) {
		return false, false, "", nil, false
	}
	unique = strings.Contains(upper, "UNIQUE INDEX")
	concurrent = strings.Contains(upper, "CONCURRENTLY")
	i := strings.Index(upper, " ON ")
	if i < 0 {
		return false, false, "", nil, false
	}
	after := strings.Fields(clean[i+4:])
	if len(after) == 0 {
		return false, false, "", nil, false
	}
	table = strings.Trim(strings.TrimRight(after[0], "("), `"`)
	cols = colsAfter(clean[i:], "ON")
	if table == "" || len(cols) == 0 {
		return false, false, "", nil, false
	}
	return unique, concurrent, table, cols, true
}

// parseReindex recognizes REINDEX [INDEX|TABLE|...] [CONCURRENTLY] name.
func parseReindex(clean, upper string) (name string, isTable, concurrent bool, ok bool) {
	if !strings.HasPrefix(upper, "REINDEX") {
		return "", false, false, false
	}
	fields := strings.Fields(clean)
	if len(fields) < 2 {
		return "", false, false, false
	}
	concurrent = strings.Contains(upper, "CONCURRENTLY")
	isTable = strings.Contains(upper, " TABLE ")
	name = strings.Trim(strings.TrimRight(fields[len(fields)-1], ";"), `"`)
	return name, isTable, concurrent, true
}
