// Package findings holds the RS-* analyzers that turn a fixture plus the capture
// of applying a migration into verdict findings. Each analyzer registers itself
// with the validate pipeline; importing this package for side effects wires them
// all in (see cmd/root.go). Finding codes are permanent and namespaced
// (INV-VERDICT-STABLE).
package findings

import (
	"fmt"
	"strings"

	"github.com/rowshape/rowshape/internal/estimate"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

func init() { validate.Register(rsLock{}) }

// rsLock detects lock-mode and table-rewrite pathologies (PRD §10 RS-LOCK-001):
// an ACCESS EXCLUSIVE lock held for a full table rewrite — a volatile-default
// ADD COLUMN or a column type change. It reports the lock mode, the rows
// rewritten, a bucketed duration extrapolated to DECLARED rows, and the
// mandatory expand/backfill/contract remediation. It is version-conditional:
// a non-volatile default is a catalog-only fast-path on PG 11+ and does not fire
// (RFC §9.1, PRD §12).
type rsLock struct{}

func (rsLock) Analyze(f *fixture.Fixture, c *validate.Capture) []verdict.Finding {
	major, hasVersion := estimate.Major(f.Meta.Engine.Version)

	var out []verdict.Finding
	for i, st := range c.Statements {
		op, table, kind, rewrites := classifyRewrite(st.SQL, major, hasVersion)
		if !rewrites {
			continue
		}
		out = append(out, rsLock{}.finding(f, c, i, st, op, table, kind, hasVersion))
	}
	return out
}

// finding builds one RS-LOCK-001 finding for a rewrite statement at index i.
func (rsLock) finding(f *fixture.Fixture, c *validate.Capture, i int, st validate.Statement, op estimate.OpClass, table, kind string, hasVersion bool) verdict.Finding {
	tbl := f.Tables[table]
	declared := tbl.Rows.Value

	lockMode := humanLock(st.LockMode)
	if lockMode == "" {
		lockMode = "ACCESS EXCLUSIVE" // a rewrite always takes it, even if unobserved
	}

	fnd := verdict.Finding{
		Code:      "RS-LOCK-001",
		Severity:  verdict.SeverityWarn,
		Location:  locationFor(st),
		Evidence:  map[string]any{"lock_mode": lockMode, "rows_rewritten": declared},
		DependsOn: []string{table + ".rows"},
		// The rewrite recipe is mandatory on this finding (INV-VERDICT-STABLE): a
		// finding an agent can't act on is a bug. It comes from the shared catalog
		// so it never drifts from `rowshape explain RS-LOCK-001`.
		Remediation: remediation("RS-LOCK-001"),
		Explain:     "rowshape explain RS-LOCK-001",
	}

	// Durations are buckets with the basis attached, never point estimates
	// (INV-DURATIONS-BUCKETS). Extrapolation refuses without an engine version.
	if hasVersion {
		est := estimateFor(c, i, op, table, declared, tbl.Rows.Confidence)
		fnd.Estimate = est
		fnd.Title = fmt.Sprintf("%s lock on %s, %s rewrite of %s rows", lockMode, shortTable(table), est.Bucket, humanCount(declared))
		fnd.Detail = fmt.Sprintf("%s holds %s and rewrites all %d rows.", kind, lockMode, declared)
	} else {
		fnd.Title = fmt.Sprintf("%s lock on %s rewrites %s rows (duration not extrapolated: no engine version)", lockMode, shortTable(table), humanCount(declared))
		fnd.Detail = fmt.Sprintf("%s holds %s and rewrites all %d rows. meta.engine.version is absent, so the duration is not extrapolated (RFC §9.1).", kind, lockMode, declared)
	}
	return fnd
}

// classifyRewrite recognizes a rewrite-causing ALTER TABLE and returns its cost
// class, the target table, a human description of the change, and whether it
// rewrites. It is version-conditional for ADD COLUMN ... DEFAULT (RFC §9.1).
func classifyRewrite(rawSQL string, major int, hasVersion bool) (op estimate.OpClass, table, kind string, rewrites bool) {
	sql := stripSQLComments(rawSQL)
	upper := strings.ToUpper(collapseSpaces(sql))
	if !strings.HasPrefix(upper, "ALTER TABLE") {
		return 0, "", "", false
	}
	table = alterTableTarget(sql)

	switch {
	case strings.Contains(upper, "ADD COLUMN") || addsColumn(upper):
		def, hasDefault := defaultExpr(sql)
		if !hasDefault {
			return 0, "", "", false // ADD COLUMN without a default does not rewrite
		}
		if isVolatile(def) {
			return estimate.TableRewrite, table, "ADD COLUMN with a volatile default", true
		}
		// Non-volatile default: catalog fast-path on PG 11+, rewrite before that.
		// Without an engine version, assume the worst (a rewrite) rather than a
		// recent default (RFC §9.1).
		m := major
		if !hasVersion {
			m = 0
		}
		op = estimate.ClassifyAddColumnDefault(false, m)
		return op, table, "ADD COLUMN with a non-volatile default", op == estimate.TableRewrite
	case strings.Contains(upper, "ALTER COLUMN") && (strings.Contains(upper, " TYPE ") || strings.Contains(upper, "SET DATA TYPE")):
		return estimate.TableRewrite, table, "ALTER COLUMN ... TYPE", true
	}
	return 0, "", "", false
}

// addsColumn matches "ADD <col>" without the optional COLUMN keyword.
func addsColumn(upper string) bool {
	return strings.Contains(upper, " ADD ") && !strings.Contains(upper, "ADD CONSTRAINT") &&
		!strings.Contains(upper, "ADD PRIMARY") && !strings.Contains(upper, "ADD UNIQUE") &&
		!strings.Contains(upper, "ADD FOREIGN") && !strings.Contains(upper, "ADD CHECK")
}

// defaultExpr extracts the DEFAULT expression of an ADD COLUMN, if present.
func defaultExpr(sql string) (string, bool) {
	up := strings.ToUpper(sql)
	i := strings.Index(up, "DEFAULT ")
	if i < 0 {
		return "", false
	}
	rest := strings.TrimSpace(sql[i+len("DEFAULT "):])
	// Cut at the end of the column definition (next clause or statement end).
	for _, stop := range []string{";", " NOT NULL", " NULL", ","} {
		if j := strings.Index(strings.ToUpper(rest), strings.ToUpper(stop)); j >= 0 {
			rest = rest[:j]
		}
	}
	return strings.TrimSpace(rest), true
}

// volatileFns are the common volatile expressions whose column default forces a
// full table rewrite on every Postgres version (they cannot live in the catalog
// as a single constant). now()/current_timestamp are STABLE, not volatile, and
// so are deliberately absent.
var volatileFns = []string{
	"gen_random_uuid", "uuid_generate_v1", "uuid_generate_v4",
	"random(", "clock_timestamp", "timeofday", "nextval",
}

// isVolatile reports whether a DEFAULT expression is volatile.
func isVolatile(expr string) bool {
	lower := strings.ToLower(expr)
	for _, fn := range volatileFns {
		if strings.Contains(lower, fn) {
			return true
		}
	}
	return false
}

// alterTableTarget extracts the (possibly schema-qualified) table name from an
// ALTER TABLE statement, dropping an optional ONLY.
func alterTableTarget(sql string) string {
	fields := strings.Fields(collapseSpaces(sql))
	// fields: ALTER TABLE [ONLY] <table> ...
	i := 2
	if i < len(fields) && strings.EqualFold(fields[i], "ONLY") {
		i++
	}
	if i < len(fields) {
		return strings.Trim(fields[i], `"`)
	}
	return ""
}

// humanLock renders a pg_locks mode ("AccessExclusiveLock") as the SQL lock name
// ("ACCESS EXCLUSIVE"), matching the PRD §10 evidence.
func humanLock(mode string) string {
	switch mode {
	case "AccessExclusiveLock":
		return "ACCESS EXCLUSIVE"
	case "ExclusiveLock":
		return "EXCLUSIVE"
	case "ShareRowExclusiveLock":
		return "SHARE ROW EXCLUSIVE"
	case "ShareLock":
		return "SHARE"
	case "ShareUpdateExclusiveLock":
		return "SHARE UPDATE EXCLUSIVE"
	case "RowExclusiveLock":
		return "ROW EXCLUSIVE"
	case "RowShareLock":
		return "ROW SHARE"
	case "AccessShareLock":
		return "ACCESS SHARE"
	}
	return ""
}

// locationFor reports the migration location of a statement. The per-file line
// is not plumbed to the analyzer yet, so this stays nil; the evidence and title
// carry the actionable detail. (Location is populated when validate threads the
// source file, a follow-up.)
func locationFor(_ validate.Statement) *verdict.Location { return nil }

// shortTable drops the schema qualifier for a compact title ("public.orders" ->
// "orders"), matching the PRD §10 example title.
func shortTable(table string) string {
	if i := strings.LastIndexByte(table, '.'); i >= 0 {
		return table[i+1:]
	}
	return table
}

// humanCount renders a row count compactly (1200000 -> "1.2M").
func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// collapseSpaces normalizes runs of whitespace (incl. newlines) to single spaces.
func collapseSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// stripSQLComments removes -- line comments and /* */ block comments so a
// statement's leading keyword can be recognized even when the migration is
// documented with a comment header.
func stripSQLComments(sql string) string {
	var b strings.Builder
	runes := []rune(sql)
	for i, n := 0, len(runes); i < n; {
		switch {
		case runes[i] == '-' && i+1 < n && runes[i+1] == '-':
			for i < n && runes[i] != '\n' {
				i++
			}
		case runes[i] == '/' && i+1 < n && runes[i+1] == '*':
			i += 2
			for i < n && !(runes[i] == '*' && i+1 < n && runes[i+1] == '/') {
				i++
			}
			i += 2
		default:
			b.WriteRune(runes[i])
			i++
		}
	}
	return b.String()
}
