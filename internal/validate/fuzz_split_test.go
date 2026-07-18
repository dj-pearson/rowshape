package validate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The SQL splitter is the single front door for every migration: a hand-written
// rune scanner over quotes, comments, escape strings, and dollar-quoting. It has
// good example-based tests but no fuzzing, and the repo has zero fuzz targets
// overall (docs/TESTING-GAPS.md, backlog item 2). A mis-split turns one valid
// statement into a broken fragment that reaches capture.Apply and yields a
// spurious verdict, so the invariants below are the ones a wrong split violates.
//
// Run with:  go test ./internal/validate/ -run x -fuzz FuzzSplitStatements
func FuzzSplitStatements(f *testing.F) {
	seeds := []string{
		"",
		";",
		";;;",
		"SELECT 1",
		"SELECT 1; SELECT 2",
		"SELECT ';' ; SELECT 2",
		"-- a comment\nSELECT 1",
		"/* block ; comment */ SELECT 1",
		"SELECT E'a\\';b'; SELECT 2",
		"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END $$ LANGUAGE plpgsql",
		"DO $tag$ SELECT 1; $tag$;",
		// deliberately malformed / unterminated — must not panic:
		"/* unterminated",
		"SELECT 'unterminated",
		"DO $t$ unterminated",
		"SELECT E'\\",
		"$$ nested $x$ tags $$",
		"ünïcödé; SELECT 1", // multibyte identifiers
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, sql string) {
		// Invalid UTF-8 is out of scope: SQL scripts are text, and the scanner
		// converts to []rune up front. Skip so the fuzzer spends its budget on
		// structural edge cases, not encoding noise.
		if !utf8.ValidString(sql) {
			t.Skip()
		}

		// INVARIANT 1: never panics on any input. (The whole point of the target.)
		parts := SplitStatements(sql)

		for _, p := range parts {
			// INVARIANT 2: no emitted statement is blank. A blank statement would
			// reach the apply loop and either error or no-op spuriously; the
			// splitter is supposed to drop them.
			if strings.TrimSpace(p) == "" {
				t.Fatalf("SplitStatements(%q) emitted a blank statement %q", sql, p)
			}
		}

		// INVARIANT 3: the line-tracking variant agrees with the flat one on the
		// statement text, and reports plausible (1-based, non-decreasing) lines.
		// SplitStatementsIn is what the CLI uses to point at the failing line, so
		// a divergence between the two is a real bug.
		located := SplitStatementsIn("fuzz.sql", sql)
		if len(located) != len(parts) {
			t.Fatalf("SplitStatements gave %d stmts but SplitStatementsIn gave %d for %q",
				len(parts), len(located), sql)
		}
		lines := strings.Count(sql, "\n") + 1
		prev := 0
		for i, loc := range located {
			if loc.SQL != parts[i] {
				t.Fatalf("stmt %d text mismatch: flat %q vs located %q", i, parts[i], loc.SQL)
			}
			if loc.Line < 1 || loc.Line > lines {
				t.Fatalf("stmt %d line %d out of range [1,%d] for %q", i, loc.Line, lines, sql)
			}
			if loc.Line < prev {
				t.Fatalf("stmt %d line %d went backwards from %d", i, loc.Line, prev)
			}
			prev = loc.Line
		}
	})
}
