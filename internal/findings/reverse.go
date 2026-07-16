package findings

import (
	"fmt"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

func init() { validate.Register(rsReverse{}) }

// rsReverse detects reversibility hazards — migrations whose DOWN-migration would
// lose data or could not execute (PRD §10 RS-REVERSE namespace):
//
//   - RS-REVERSE-001: DROP COLUMN permanently loses the column's data; a rollback
//     can recreate the column but not its rows.
//   - RS-REVERSE-002: DROP TABLE permanently loses every row; a rollback cannot
//     restore them.
//   - RS-REVERSE-003: a narrowing column type change truncates values and cannot
//     be reversed without the original data.
//
// Each finding declares depends_on (the table's rows — what is lost) and carries
// mandatory remediation, and is confidence-capped like every other class.
type rsReverse struct{}

func (rsReverse) Analyze(f *fixture.Fixture, c *validate.Capture) []verdict.Finding {
	var out []verdict.Finding
	for _, st := range c.Statements {
		clean := collapseSpaces(stripSQLComments(st.SQL))
		upper := strings.ToUpper(clean)

		switch {
		case strings.HasPrefix(upper, "DROP TABLE"):
			out = append(out, dropTableFinding(f, clean, upper))
		case strings.HasPrefix(upper, "ALTER TABLE") && strings.Contains(upper, "DROP COLUMN"):
			if fnd, ok := dropColumnFinding(f, clean, upper); ok {
				out = append(out, fnd)
			}
		case strings.HasPrefix(upper, "ALTER TABLE") && (strings.Contains(upper, " TYPE ") || strings.Contains(upper, "SET DATA TYPE")):
			if fnd, ok := narrowTypeFinding(f, clean, upper); ok {
				out = append(out, fnd)
			}
		}
	}
	return out
}

func dropTableFinding(f *fixture.Fixture, clean, upper string) verdict.Finding {
	table := dropTableTarget(clean, upper)
	rows := f.Tables[table].Rows.Value
	return verdict.Finding{
		Code:        "RS-REVERSE-002",
		Severity:    verdict.SeverityWarn,
		Title:       fmt.Sprintf("DROP TABLE %s is irreversible: all %s rows are lost", shortTable(table), humanCount(rows)),
		Detail:      "Dropping a table permanently removes every row; a down-migration can recreate the table but not its data.",
		Evidence:    map[string]any{"rows": rows},
		DependsOn:   []string{table + ".rows"},
		Remediation: remediation("RS-REVERSE-002"),
		Explain:     "rowshape explain RS-REVERSE-002",
	}
}

func dropColumnFinding(f *fixture.Fixture, clean, upper string) (verdict.Finding, bool) {
	table := alterTableTarget(clean)
	col := identAfter(clean, upper, "DROP COLUMN")
	if table == "" || col == "" {
		return verdict.Finding{}, false
	}
	rows := f.Tables[table].Rows.Value
	return verdict.Finding{
		Code:        "RS-REVERSE-001",
		Severity:    verdict.SeverityWarn,
		Title:       fmt.Sprintf("DROP COLUMN %s.%s loses its data irreversibly", shortTable(table), col),
		Detail:      "Dropping a column permanently removes its values across all rows; a down-migration can recreate the column but not what it held.",
		Evidence:    map[string]any{"rows": rows},
		DependsOn:   []string{table + ".rows"},
		Remediation: remediation("RS-REVERSE-001"),
		Explain:     "rowshape explain RS-REVERSE-001",
	}, true
}

func narrowTypeFinding(f *fixture.Fixture, clean, upper string) (verdict.Finding, bool) {
	table := alterTableTarget(clean)
	col := columnBeforeTypeChange(clean, upper)
	newType := typeAfter(clean, upper)
	if table == "" || col == "" || newType == "" {
		return verdict.Finding{}, false
	}
	c, ok := f.Tables[table].Columns[col]
	if !ok || !isNarrowing(c.Type, newType) {
		return verdict.Finding{}, false
	}
	return verdict.Finding{
		Code:        "RS-REVERSE-003",
		Severity:    verdict.SeverityWarn,
		Title:       fmt.Sprintf("Narrowing %s.%s from %s to %s can truncate data irreversibly", shortTable(table), col, c.Type, newType),
		Detail:      "Narrowing a column's type can truncate or lose values, and cannot be reversed without the original data.",
		Evidence:    map[string]any{"from": c.Type, "to": newType},
		DependsOn:   []string{table + ".rows"},
		Remediation: remediation("RS-REVERSE-003"),
		Explain:     "rowshape explain RS-REVERSE-003",
	}, true
}

// dropTableTarget extracts the table from DROP TABLE [IF EXISTS] <table>.
func dropTableTarget(clean, upper string) string {
	fields := strings.Fields(clean)
	i := 2 // DROP TABLE
	if i < len(fields) && strings.EqualFold(fields[i], "IF") {
		i += 2 // IF EXISTS
	}
	if i < len(fields) {
		return strings.Trim(strings.TrimRight(fields[i], ";"), `"`)
	}
	return ""
}

// identAfter returns the identifier following a keyword (case-insensitive).
func identAfter(clean, upper, keyword string) string {
	i := strings.Index(upper, strings.ToUpper(keyword))
	if i < 0 {
		return ""
	}
	fields := strings.Fields(clean[i+len(keyword):])
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(strings.TrimRight(fields[0], ";,"), `"`)
}

// columnBeforeTypeChange returns the column of an ALTER COLUMN ... TYPE clause.
func columnBeforeTypeChange(clean, upper string) string {
	i := strings.Index(upper, " TYPE ")
	if i < 0 {
		if j := strings.Index(upper, "SET DATA TYPE"); j >= 0 {
			i = j
		} else {
			return ""
		}
	}
	fields := strings.Fields(clean[:i])
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[len(fields)-1], `"`)
}

// typeAfter returns the target type of a TYPE / SET DATA TYPE clause.
func typeAfter(clean, upper string) string {
	key := " TYPE "
	i := strings.Index(upper, key)
	if i < 0 {
		if j := strings.Index(upper, "SET DATA TYPE"); j >= 0 {
			i, key = j, "SET DATA TYPE"
		} else {
			return ""
		}
	}
	rest := strings.TrimSpace(clean[i+len(key):])
	// Cut at the next clause boundary (USING, ;, ,).
	for _, stop := range []string{" USING", ";", ","} {
		if k := strings.Index(strings.ToUpper(rest), strings.ToUpper(stop)); k >= 0 {
			rest = rest[:k]
		}
	}
	return strings.TrimSpace(rest)
}

// intRank orders integer types by width for narrowing detection.
var intRank = map[string]int{
	"smallint": 1, "int2": 1,
	"integer": 2, "int": 2, "int4": 2,
	"bigint": 3, "int8": 3,
}

// isNarrowing reports whether changing oldType to newType can lose data: an
// integer narrowing, an unbounded string to a length-limited one, or a
// numeric/float to an integer.
func isNarrowing(oldType, newType string) bool {
	o := strings.ToLower(strings.TrimSpace(baseSQLType(oldType)))
	nFull := strings.ToLower(strings.TrimSpace(newType))
	n := strings.ToLower(strings.TrimSpace(baseSQLType(newType)))

	if ro, ok := intRank[o]; ok {
		if rn, ok := intRank[n]; ok {
			return rn < ro
		}
	}
	if (o == "text" || o == "varchar" || o == "character varying" || o == "character" || o == "char") && strings.Contains(nFull, "(") {
		return true
	}
	if (o == "numeric" || o == "decimal" || o == "double precision" || o == "real") && (n == "integer" || n == "bigint" || n == "smallint" || n == "int") {
		return true
	}
	return false
}

// baseSQLType strips a type's length/precision modifier ("varchar(255)" -> "varchar").
func baseSQLType(t string) string {
	if i := strings.IndexByte(t, '('); i >= 0 {
		return strings.TrimSpace(t[:i])
	}
	return t
}
