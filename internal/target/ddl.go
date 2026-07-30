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

	// Create every referenced schema first (sorted for determinism). A user-defined
	// type can live in a schema no table does, so the type names contribute here
	// too — otherwise CREATE TYPE would target a schema that does not exist yet.
	schemas := map[string]bool{}
	for name := range f.Tables {
		schemas[schemaOf(name)] = true
	}
	for name := range f.Types {
		schemas[schemaOf(name)] = true
	}
	// An extension can be installed into a schema no table lives in, and CREATE
	// EXTENSION ... WITH SCHEMA requires that schema to exist already.
	for _, e := range f.Extensions {
		schemas[e.Schema] = true
	}
	for _, s := range sortedStrings(schemas) {
		if s != "" && s != "public" {
			stmts = append(stmts, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(s))
		}
	}

	// Then the extensions, before the types — because an extension IS how some types
	// arrive. A `citext` column named a type a fresh disposable database has never
	// heard of, so the whole DDL failed with `type "citext" does not exist` and
	// `validate` could not run at all on any schema using an extension type: citext
	// on an email column, hstore, ltree, postgis geometry, pgvector's `vector`.
	//
	// IF NOT EXISTS, and no version: the fixture's claim is that the schema NEEDS
	// citext, not that it needs a particular citext. Pinning would make a fixture
	// refuse to hydrate on a server shipping a different one, for nothing — the
	// disposable database only has to provide the type.
	for _, name := range sortedKeys(f.Extensions) {
		stmts = append(stmts, createExtension(name, f.Extensions[name]))
	}

	// Then the user-defined types, before any table that carries one as a column
	// type (RFC §6.7). A column typed `z.mood` names something a fresh disposable
	// database has never heard of, so without these the whole DDL failed with
	// `type "z.mood" does not exist` and no enum- or domain-using schema could be
	// hydrated at all.
	for _, name := range sortedKeys(f.Types) {
		if stmt := createType(name, f.Types[name]); stmt != "" {
			stmts = append(stmts, stmt)
		}
	}

	for _, name := range sortedKeys(f.Tables) {
		stmts = append(stmts, createTable(name, f.Tables[name]))
	}
	return stmts
}

// SecondaryIndexes renders the CREATE INDEX statements for a fixture's secondary
// indexes, separately from DDL because they are BEST-EFFORT.
//
// The distinction is about blast radius. A table must exist or nothing works, but
// an index key can be an arbitrary expression — `lower(email)`, a call to an
// immutable user-defined function, a custom operator class — and the fixture records
// the expression's TEXT without any of the functions or operator classes it may
// depend on. Emitting such an index inside the one big DDL transaction would abort
// the entire load for a schema that is otherwise perfectly hydratable.
//
// So the caller runs each of these in a savepoint and reports the ones that fail,
// rather than either dropping them silently (which is what used to happen, losing
// production uniqueness constraints) or letting one exotic expression break
// hydration outright.
func SecondaryIndexes(f *fixture.Fixture) []string {
	var stmts []string

	// Recreating them at all is what lets a migration reindex or build against the
	// indexes production has. On hydrated (small) data the builds are cheap; the
	// fixture-recorded bytes/bloat drive extrapolation, not the real build.
	//
	// SECONDARY is the operative word. Postgres backs every PRIMARY KEY and UNIQUE
	// constraint with an implicit index NAMED AFTER THE CONSTRAINT, and a conformant
	// `pull` records both the constraint (§6.4) and that index (§6.5) — they are both
	// really there. createTable already emits the constraint, which recreates its
	// index, so emitting the index again is a duplicate:
	//
	//	ERROR: relation "orders_pkey" already exists (SQLSTATE 42P07)
	//
	// Every hand-written test fixture lists constraints without their backing
	// indexes, so only a fixture from a real `pull` ever triggers this — that is,
	// every real schema with a primary key.
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

// createIndex renders a secondary index, including the expression and partial
// forms. Both used to be skipped, which quietly removed uniqueness constraints
// production enforces — see the Keys and Partial handling below.
func createIndex(table string, ix fixture.Index) string {
	if ix.Name == "" {
		return ""
	}

	// Keys first: they are the only description of an EXPRESSION index. An index on
	// lower(email) has no column key, so keying off `columns` alone rendered it
	// unbuildable and it was dropped — taking a uniqueness constraint production
	// enforces with it, which is how a migration that violates that constraint could
	// PASS. Keys come from pg_get_indexdef and are already valid SQL (including any
	// DESC or operator class), so they are emitted verbatim, exactly as the recorded
	// partial predicate and domain CHECK are.
	keys := ix.Keys
	if len(keys) == 0 {
		if len(ix.Columns) == 0 {
			return ""
		}
		keys = quotedCols(ix.Columns)
	}

	unique := ""
	if ix.Unique {
		unique = "UNIQUE "
	}
	using := ""
	if ix.Method != "" && !strings.EqualFold(ix.Method, "btree") {
		using = " USING " + ix.Method
	}

	stmt := fmt.Sprintf("CREATE %sINDEX %s ON %s%s (%s)",
		unique, quoteIdent(ix.Name), quoteTable(table), using, strings.Join(keys, ", "))

	// INCLUDE payload, which must stay OUT of the key list above. Folding it in is
	// what turned `UNIQUE (customer_id, created_at) INCLUDE (total)` into a
	// three-column unique index — strictly weaker than what production enforces, so
	// the disposable database accepted rows the source database rejects.
	if len(ix.Include) > 0 {
		stmt += " INCLUDE (" + strings.Join(quotedCols(ix.Include), ", ") + ")"
	}

	// A PARTIAL index used to be skipped entirely. That is the dangerous one to drop:
	// a partial UNIQUE index is the standard soft-delete uniqueness pattern
	// (`UNIQUE (email) WHERE deleted_at IS NULL`), and without it the disposable
	// database does not enforce what production does.
	//
	// "opaque" is the placeholder privacy:strict leaves behind (§6.4). It is not a
	// predicate, and inventing one would change what the index enforces, so the index
	// is skipped rather than built with the wrong scope.
	if ix.Partial != "" {
		if ix.Partial == "opaque" {
			return ""
		}
		stmt += " WHERE " + ix.Partial
	}
	return stmt
}

// createExtension renders CREATE EXTENSION for one required extension (RFC §6.8).
//
// WITH SCHEMA only when the fixture recorded one: a type name in a column can be
// schema-qualified (`ext.citext`), and installing the extension somewhere else
// would leave that name unresolvable. Where the source did not record a schema,
// the server's default is the honest choice — inventing `public` would be a guess
// that silently disagrees with a search_path this code does not control.
func createExtension(name string, e fixture.Extension) string {
	stmt := "CREATE EXTENSION IF NOT EXISTS " + quoteIdent(name)
	if e.Schema != "" {
		stmt += " WITH SCHEMA " + quoteIdent(e.Schema)
	}
	return stmt
}

// createType renders CREATE TYPE / CREATE DOMAIN for one user-defined type
// (RFC §6.7). An unrecognized kind returns "" so an extension to the vocabulary
// degrades to the pre-existing behavior — the DDL names the missing type and fails
// there — rather than emitting a statement built from a guess.
func createType(name string, t fixture.UserType) string {
	switch t.Kind {
	case "enum":
		// EffectiveLabels, not t.Labels: under privacy:strict the vocabulary is
		// withheld and only the count survives, and the placeholders it invents must
		// be the SAME ones the synthesis engine draws from — a single disagreement
		// would make every insert of the missing label fail as not a member of the
		// type. Hence one shared implementation on the model rather than a copy here.
		labels := t.EffectiveLabels()
		if len(labels) == 0 {
			return "" // an enum with neither labels nor a count cannot be created
		}
		quoted := make([]string, len(labels))
		for i, l := range labels {
			quoted[i] = quoteLiteral(l)
		}
		return fmt.Sprintf("CREATE TYPE %s AS ENUM (%s)", quoteTable(name), strings.Join(quoted, ", "))
	case "domain":
		if t.Base == "" {
			return ""
		}
		stmt := fmt.Sprintf("CREATE DOMAIN %s AS %s", quoteTable(name), t.Base)
		if t.NotNull {
			stmt += " NOT NULL"
		}
		// An opaque CHECK (privacy:strict) is deliberately NOT reconstructed: the
		// expression is unknown, and inventing one would constrain hydrated data in a
		// way production may not. The domain is created without it, which is the
		// weaker but honest reading of what the fixture actually says.
		if t.Check != "" && t.Check != "opaque" {
			stmt += " CHECK " + t.Check
		}
		return stmt
	}
	return ""
}

// quoteLiteral renders a SQL string literal, doubling embedded quotes. Enum labels
// are arbitrary text from the source catalog, so a label containing an apostrophe
// must not be able to break out of the statement.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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

// quotedCols quotes each column separately, for callers that assemble the list
// themselves (an index whose keys may mix plain columns and expressions).
func quotedCols(cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(c)
	}
	return out
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
