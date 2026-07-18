package validate

import "testing"

// isTxControl and classifyIndexBuild are DB-free, load-bearing helpers that sat
// at 0% direct coverage — reached only incidentally through the live-DB apply
// path. isTxControl underpins RS-CONSTRAINT-001's same-transaction detection and
// classifyIndexBuild decides the CONCURRENTLY branch. docs/TESTING-GAPS.md item 3.

func TestIsTxControl(t *testing.T) {
	yes := []string{
		"BEGIN", "begin", "  COMMIT  ", "COMMIT;", "ROLLBACK",
		"START TRANSACTION", "END", "SAVEPOINT sp1", "RELEASE sp1",
		"begin isolation level serializable",
	}
	no := []string{
		"", "SELECT 1",
		"BEGINNING",             // must not prefix-match a real identifier
		"COMMITTED",             // ditto
		"ENDPOINT",              // ditto
		"CREATE TABLE beginner", // the keyword appears but not as a statement verb
		"ALTER TABLE t ADD COLUMN c int",
	}
	for _, s := range yes {
		if !isTxControl(s) {
			t.Errorf("isTxControl(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isTxControl(s) {
			t.Errorf("isTxControl(%q) = true, want false", s)
		}
	}
}

func TestClassifyIndexBuild(t *testing.T) {
	cases := []struct {
		sql              string
		isIndex, concurr bool
	}{
		{"CREATE INDEX i ON t (c)", true, false},
		{"CREATE UNIQUE INDEX i ON t (c)", true, false},
		{"CREATE INDEX CONCURRENTLY i ON t (c)", true, true},
		{"create unique index concurrently i on t (c)", true, true},
		{"REINDEX INDEX i", false, false},
		{"CREATE TABLE index_stats (id int)", false, false}, // "INDEX" only in a name
		{"ALTER TABLE t ADD COLUMN c int", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		gotIdx, gotConc := classifyIndexBuild(tc.sql)
		if gotIdx != tc.isIndex || gotConc != tc.concurr {
			t.Errorf("classifyIndexBuild(%q) = (%v,%v), want (%v,%v)",
				tc.sql, gotIdx, gotConc, tc.isIndex, tc.concurr)
		}
	}
}

// NOTE: isTxControl is duplicated verbatim in internal/plan/plan.go:163 with no
// guard tying the two copies together. A cross-package test can't live here
// (internal/plan imports internal/validate, so importing plan back would cycle);
// the real fix is to de-duplicate the helper into a shared low-level package.
// Tracked in docs/TESTING-GAPS.md. Plan's copy is exercised directly by
// internal/plan's TestItemsSkipsTxControlAndBlanks.
