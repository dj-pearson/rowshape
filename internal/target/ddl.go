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
	// Recreate the fixture's secondary indexes so a migration that reindexes or
	// depends on them has them present. On hydrated (small) data these are cheap;
	// their fixture-recorded bytes/bloat drive extrapolation, not the real build.
	//
	// SECONDARY is the operative word. Postgres backs every PRIMARY KEY and UNIQUE
	// constraint with an implicit index NAMED AFTER THE CONSTRAINT, and a
	// conformant `pull` records both the constraint (§6.4) and that index (§6.5) —
	// they are both really there. createTable above already emits the constraint,
	// which recreates its index, so emitting the index again is a duplicate:
	//
	//	ERROR: relation "orders_pkey" already exists (SQLSTATE 42P07)
	//
	// which fails the whole DDL and takes `validate` with it. Every hand-written
	// test fixture lists constraints without their backing indexes, so only a
	// fixture from a real `pull` ever triggers this — that is, every real schema
	// with a primary key.
	for _, name := range sortedKeys(f.Tables) {
		backed := constraintBackedIndexes(f.Tables[name])
		for _, ix := range f.Tables[name].Indexes {
			if backed[ix.Name] {
				continue
			}
			if stmt := createIndex(name, ix); stmt != "" {
				stmts = append(stmts, stmt)
			}
		}
	}
	return stmts
}

// constraintBackedIndexes returns the index names that a PRIMARY KEY or UNIQUE
// constraint on this table already creates.
//
// Postgres names the implicit index after the constraint that owns it, which is
// what makes name matching exact rather than a guess: `orders_pkey` the
// constraint and `orders_pkey` the index are the same object viewed twice.
func constraintBackedIndexes(tbl fixture.Table) map[string]bool {
	backed := make(map[string]bool)
	for _, con := range tbl.Constraints {
		switch con.Kind {
		case "primary_key", "unique":
			if con.Name != "" {
				backed[con.Name] = true
			}
		}
	}
	return backed
}

// createIndex renders a secondary index. Partial and expression indexes are
// skipped (their predicates/expressions may reference domain logic hydrated data
// needn't satisfy); a plain column index is enough for a migration to reindex or
// build against.
func createIndex(table string, ix fixture.Index) string {
	if ix.Name == "" || len(ix.Columns) == 0 || ix.Partial != "" {
		return ""
	}
	unique := ""
	if ix.Unique {
		unique = "UNIQUE "
	}
	using := ""
	if ix.Method != "" && !strings.EqualFold(ix.Method, "btree") {
		using = " USING " + ix.Method
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s%s (%s)", unique, quoteIdent(ix.Name), quoteTable(table), using, quoteCols(ix.Columns))
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
