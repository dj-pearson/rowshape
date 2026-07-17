package fixture

import "strings"

// ResolveTable maps a table name as written in a migration onto the key this
// fixture actually uses, and reports whether it found one.
//
// RFC §5 keys `tables` by QUALIFIED name — `public.users`. Migrations are not
// written that way. `ALTER TABLE users ...` is ordinary SQL, resolved by Postgres
// through search_path, and it is what people write. Looking that name up directly
// misses, and a miss is not harmless: the analyzer reads the zero value, so a
// 50M-row table reports rows=0 and its rewrite is estimated as `instant` instead
// of `outage` — the exact answer rowshape exists to prevent, delivered
// confidently.
//
// Resolution is a lookup, not a guess, and never assumes a default schema:
//
//   - an exact key match wins outright;
//   - otherwise an unqualified name matches on the part after the last dot, and
//     ONLY when exactly one table in the fixture matches.
//
// Ambiguity means unresolved. If a fixture holds both `public.users` and
// `tenant.users`, nothing here can know which one `users` meant — that depends on
// the search_path the migration runs under — so it declines. The caller then has
// no facts, which caps the finding to WARN (an unresolvable dependency reads as
// `absent`) rather than answering from the wrong table.
//
// The single-match case is also what actually happens during validate: the
// migration is applied to a database hydrated FROM THIS FIXTURE, so the only
// tables that exist are these. If just one is named `users`, that is the one the
// statement will hit.
func (f *Fixture) ResolveTable(name string) (string, bool) {
	if f == nil || name == "" || len(f.Tables) == 0 {
		return name, false
	}
	if _, ok := f.Tables[name]; ok {
		return name, true
	}
	// Only an unqualified name is ambiguous enough to search for. A qualified
	// name that missed names a table this fixture does not have.
	if strings.Contains(name, ".") {
		return name, false
	}

	var found string
	for key := range f.Tables {
		if unqualified(key) != name {
			continue
		}
		if found != "" {
			// Same table name in two schemas: which one `users` means depends on
			// the search_path, which the fixture does not record. Decline.
			return name, false
		}
		found = key
	}
	if found == "" {
		return name, false
	}
	return found, true
}

// unqualified returns the part of a table key after the last dot.
func unqualified(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[i+1:]
	}
	return key
}
