package target

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
)

// DDL renders CREATE SCHEMA / CREATE TABLE statements for a fixture, enough to
// receive hydrated rows. It creates columns with their types and structural
// nullability plus primary-key and unique constraints — the constraints the
// synthesis engine reliably satisfies (RFC §13).
//
// CHECK expressions and foreign keys are intentionally NOT emitted here: a CHECK
// can carry domain logic that obviously-fake values needn't satisfy, and a
// foreign key needs dependency-ordered loading. Reintroducing and validating
// those against hydrated data is the job of `validate` (phase 2), not of getting
// shape onto disk.
func DDL(f *fixture.Fixture) []string {
	var stmts []string

	// Create every referenced schema first (sorted for determinism).
	schemas := map[string]bool{}
	for name := range f.Tables {
		schemas[schemaOf(name)] = true
	}
	for _, s := range sortedStrings(schemas) {
		if s != "" && s != "public" {
			stmts = append(stmts, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(s))
		}
	}

	for _, name := range sortedKeys(f.Tables) {
		stmts = append(stmts, createTable(name, f.Tables[name]))
	}
	return stmts
}

// createTable renders one CREATE TABLE statement.
func createTable(name string, tbl fixture.Table) string {
	var lines []string
	for _, col := range sortedKeys(tbl.Columns) {
		c := tbl.Columns[col]
		line := fmt.Sprintf("  %s %s", quoteIdent(col), c.Type)
		if !c.Nullable {
			line += " NOT NULL"
		}
		lines = append(lines, line)
	}
	for _, con := range tbl.Constraints {
		switch con.Kind {
		case "primary_key":
			lines = append(lines, fmt.Sprintf("  PRIMARY KEY (%s)", quoteCols(con.Columns)))
		case "unique":
			lines = append(lines, fmt.Sprintf("  UNIQUE (%s)", quoteCols(con.Columns)))
		}
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n)", quoteTable(name), strings.Join(lines, ",\n"))
}

// quoteCols quotes a column list for a constraint definition.
func quoteCols(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(c)
	}
	return strings.Join(out, ", ")
}

// quoteTable quotes a schema.table identifier.
func quoteTable(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return quoteIdent(name[:i]) + "." + quoteIdent(name[i+1:])
	}
	return quoteIdent(name)
}

// schemaOf returns the schema part of a qualified name, or "" if unqualified.
func schemaOf(name string) string {
	if i := strings.Index(name, "."); i >= 0 {
		return name[:i]
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
