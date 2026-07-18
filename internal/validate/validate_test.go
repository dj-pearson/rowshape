package validate

import (
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestSplitStatements: a SQL script splits on top-level semicolons only —
// semicolons inside strings, line comments, and dollar-quoted bodies do not
// split a statement.
func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"two", "ALTER TABLE a ADD c int; ALTER TABLE b ADD d int;", 2},
		{"trailing-none", "SELECT 1", 1},
		{"string-semicolon", "INSERT INTO t VALUES ('a;b'); SELECT 1;", 2},
		{"line-comment", "-- drop; this\nSELECT 1;", 1},
		{"dollar-quote", "CREATE FUNCTION f() RETURNS void AS $$ BEGIN raise notice 'x;y'; END; $$ LANGUAGE plpgsql; SELECT 1;", 2},
		{"block-comment", "/* a;b */ SELECT 1;", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitStatements(c.in)
			if len(got) != c.want {
				t.Errorf("SplitStatements(%q) = %d statements %v, want %d", c.in, len(got), got, c.want)
			}
		})
	}
}

// fakeAnalyzer emits one fixed finding, standing in for the P2-T8+ detectors.
type fakeAnalyzer struct{ f verdict.Finding }

func (a fakeAnalyzer) Analyze(*fixture.Fixture, *Capture) []verdict.Finding {
	return []verdict.Finding{a.f}
}

func estimatedFixture() *fixture.Fixture {
	return &fixture.Fixture{
		Meta: fixture.Meta{ID: "t", Digest: "sha256:abc"},
		Tables: map[string]fixture.Table{
			"public.users": {
				Rows: fixture.Fact[int64]{Value: 1_000_000, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{
					"email": {Type: "text", Distinct: &fixture.Fact[int64]{Value: 999_000, Confidence: fixture.Estimated}},
				},
			},
		},
	}
}

// TestBuildResultCaps: an info finding that wants to certify PASS but rests on an
// absent/estimated fact is capped to a resolving WARN (RFC §7.4). The overall
// verdict follows.
func TestBuildResultCaps(t *testing.T) {
	f := estimatedFixture()
	analyzer := fakeAnalyzer{verdict.Finding{
		Code:      "RS-DATA-014",
		Severity:  verdict.SeverityInfo,
		Title:     "ADD UNIQUE(email)",
		DependsOn: []string{"public.users.email.unique"}, // absent → weakest
	}}
	res := BuildResult(f, &Capture{Success: true}, []Analyzer{analyzer}, false)
	if res.Verdict != verdict.VerdictWarn {
		t.Errorf("verdict = %s, want WARN (capped)", res.Verdict)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected the capped WARN to surface, got %d findings", len(res.Findings))
	}
	if got := res.Findings[0]; got.Severity != verdict.SeverityWarn || got.Remediation == "" {
		t.Errorf("capped finding must be an actionable warn with a resolve command, got %+v", got)
	}
}

// TestBuildResultGroundTruth: against a provided live branch the evidence is
// exact, so the same finding is NOT capped — it certifies PASS (PRD §15).
func TestBuildResultGroundTruth(t *testing.T) {
	f := estimatedFixture()
	analyzer := fakeAnalyzer{verdict.Finding{
		Code:      "RS-DATA-014",
		Severity:  verdict.SeverityInfo,
		Title:     "ADD UNIQUE(email)",
		DependsOn: []string{"public.users.email.unique"},
	}}
	res := BuildResult(f, &Capture{Success: true}, []Analyzer{analyzer}, true)
	if res.Verdict != verdict.VerdictPass {
		t.Errorf("verdict = %s, want PASS (ground truth, capping cannot fire)", res.Verdict)
	}
	if len(res.Findings) != 0 {
		t.Errorf("a clean PASS certification should be the silent default, got %+v", res.Findings)
	}
}

// TestBuildResultEmptyIsPass: with no analyzers and a clean apply, the verdict is
// PASS — the orchestration returns a well-formed verdict before any detector
// exists (P2-T7 before P2-T8+).
func TestBuildResultEmptyIsPass(t *testing.T) {
	res := BuildResult(estimatedFixture(), &Capture{Success: true}, nil, false)
	if res.Verdict != verdict.VerdictPass || len(res.Findings) != 0 {
		t.Errorf("empty analyzers + clean apply = PASS with no findings, got %s / %d", res.Verdict, len(res.Findings))
	}
	if res.Rowshape != verdict.Rowshape {
		t.Errorf("result must carry the contract version tag")
	}
}

// TestBuildResultFailedApplyIsNeverPass: a migration that did not apply cleanly
// is floored to FAIL, never certified PASS — even with no analyzers.
func TestBuildResultFailedApplyIsNeverPass(t *testing.T) {
	cap := &Capture{Success: false, Statements: []Statement{{SQL: "ALTER ...", ErrCode: "23502", ErrMsg: "not null violation"}}}
	res := BuildResult(estimatedFixture(), cap, nil, false)
	if res.Verdict != verdict.VerdictFail {
		t.Errorf("a failed apply must floor to FAIL, got %s", res.Verdict)
	}
}

// TestConstraintViolationClass: a class-23 SQLSTATE is recognized as a constraint
// violation (the migration hitting real data), distinct from a tool error.
func TestConstraintViolationClass(t *testing.T) {
	if !(Statement{ErrCode: "23505"}).ConstraintViolation() {
		t.Error("23505 (unique_violation) must be a constraint violation")
	}
	if (Statement{ErrCode: "42P01"}).ConstraintViolation() {
		t.Error("42P01 (undefined_table) is not a constraint violation")
	}
}

// TestCheckHostRefusesEquivalentSpellings: a host is not a string.
//
// This is INV-BLAST-RADIUS-ZERO's last line, and it was bypassable. A fixture
// pulled from `localhost` and validated with `--ephemeral` against `127.0.0.1`
// went straight past the refusal and started creating a database on the very
// server the fixture came from. The old test only compared a host to itself and
// to an obviously different one, so every equivalent spelling went unchecked.
//
// Each case below is the SAME machine written two ways, and every one must refuse.
func TestCheckHostRefusesEquivalentSpellings(t *testing.T) {
	cases := []struct {
		name        string
		fixtureHost string // what pull recorded in meta.source
		targetHost  string // what validate was aimed at
		wantRefusal bool
	}{
		// The proven bypass, in both directions — a fixture may already exist
		// written either way, so neither direction may fail open.
		{"localhost fixture, 127.0.0.1 target", "localhost", "127.0.0.1", true},
		{"127.0.0.1 fixture, localhost target", "127.0.0.1", "localhost", true},
		{"localhost fixture, ::1 target", "localhost", "::1", true},
		{"::1 fixture, 127.0.0.1 target", "::1", "127.0.0.1", true},
		{"bracketed ipv6 target", "::1", "[::1]", true},
		// 127.0.0.0/8 is loopback in its entirety.
		{"127.0.0.53 is still this machine", "localhost", "127.0.0.53", true},

		// DNS is case-insensitive (RFC 4343).
		{"case differs", "DB.Internal", "db.internal", true},
		{"case differs, other way", "db.internal", "DB.INTERNAL", true},

		// An absolute FQDN names the same host as its relative form.
		{"trailing FQDN dot", "db.internal", "db.internal.", true},

		{"same host, same spelling", "prod.example.com", "prod.example.com", true},

		// Genuinely different hosts must still be allowed, or validate refuses to
		// do its job.
		{"different host", "prod.example.com", "staging.example.com", false},
		{"different host, loopback target", "prod.example.com", "localhost", false},
		// Not loopback: 128.x is a public range, and a substring match on "127."
		// must not creep into treating it as one.
		{"128.0.0.1 is not loopback", "localhost", "128.0.0.1", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := profile.HashSource(c.fixtureHost)
			err := CheckHost(source, c.targetHost)

			if c.wantRefusal && err == nil {
				t.Errorf("fixture pulled from %q, target %q: NOT refused — validate would touch the "+
					"server the fixture came from (INV-BLAST-RADIUS-ZERO)", c.fixtureHost, c.targetHost)
			}
			if !c.wantRefusal && err != nil {
				t.Errorf("fixture pulled from %q, target %q: refused a genuinely different host (%v)",
					c.fixtureHost, c.targetHost, err)
			}
		})
	}
}

// TestSplitStatementsInTracksLines: a finding's `location` (PRD §10) is only as
// good as the line the splitter reports.
//
// locationFor was a stub returning nil unconditionally, and Statement carried no
// origin at all, so `location` was never populated on any finding — while sitting
// in the documented verdict contract, in PRD §10's own example, in the human
// renderer ("at file:line"), and under P4-T2, whose entire job is turning it into
// a PR annotation at the offending line.
func TestSplitStatementsInTracksLines(t *testing.T) {
	sql := "-- a leading comment\n" + // 1
		"\n" + // 2
		"ALTER TABLE t ADD COLUMN a text;\n" + // 3
		"\n" + // 4
		"ALTER TABLE t ADD COLUMN b text;\n" // 5

	got := SplitStatementsIn("migrations/001.sql", sql)
	if len(got) != 2 {
		t.Fatalf("expected 2 statements, got %d: %+v", len(got), got)
	}
	// The first statement's text begins at its leading comment, on line 1.
	if got[0].Line != 1 {
		t.Errorf("first statement Line = %d, want 1", got[0].Line)
	}
	// The second begins on line 5 — blank lines between statements must not shift
	// it, which is what the trim-aware offset is for.
	if got[1].Line != 5 {
		t.Errorf("second statement Line = %d, want 5 — a wrong line annotates the wrong code", got[1].Line)
	}
	for i, l := range got {
		if l.File != "migrations/001.sql" {
			t.Errorf("statement %d lost its file: %q", i, l.File)
		}
	}

	// Inline SQL has no file, and must not invent one.
	inline := SplitStatementsIn("", "ALTER TABLE t ADD COLUMN c text;")
	if len(inline) != 1 || inline[0].File != "" {
		t.Errorf("inline SQL must carry no file, got %+v", inline)
	}

	// SplitStatements keeps its old shape for callers that only want the text.
	if plain := SplitStatements(sql); len(plain) != 2 {
		t.Errorf("SplitStatements should still return 2 statements, got %d", len(plain))
	}
}

// --- CR-T6: E'...' escape strings -------------------------------------------
//
// The splitter had no backslash handling at all, so a Postgres escape string
// closed early on `\'`: `SELECT E'it\'s; oops';` became TWO statements, the
// first a syntactically broken fragment. That fragment then went to
// capture.Apply, which produced a spurious FAIL on a perfectly valid migration.

func TestSplitStatementsEscapeStrings(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{
			// The reported defect.
			name: "escaped quote inside E-string",
			sql:  `SELECT E'it\'s; oops';`,
			want: []string{`SELECT E'it\'s; oops'`},
		},
		{
			// \ is an escaped BACKSLASH, so the quote that follows really does
			// close the literal. A naive "skip the char after every backslash"
			// gets this wrong in the other direction.
			name: "escaped backslash then a real close",
			// Two backslashes in the SQL: an escaped BACKSLASH, so the quote that
			// follows really does close the literal and the semicolon splits.
			// Built by concatenation because a doubled backslash in a literal is
			// easy to mangle in transit and this case is meaningless without
			// exactly two.
			sql:  `SELECT E'path` + `\` + `\` + `'; SELECT 2;`,
			want: []string{`SELECT E'path` + `\` + `\` + `'`, `SELECT 2`},
		},
		{
			name: "semicolon inside an E-string",
			sql:  `SELECT E'a;b';`,
			want: []string{`SELECT E'a;b'`},
		},
		{
			name: "lowercase e prefix",
			sql:  `SELECT e'it\'s; fine';`,
			want: []string{`SELECT e'it\'s; fine'`},
		},
		{
			// standard_conforming_strings: a backslash in an ORDINARY literal is
			// a plain character, so this closes at the quote. Treating backslash
			// as an escape everywhere would swallow it and merge the statements.
			name: "ordinary literal ending in a backslash",
			sql:  `SELECT 'C:\'; SELECT 2;`,
			want: []string{`SELECT 'C:\'`, `SELECT 2`},
		},
		{
			name: "doubled quote is not the end",
			sql:  `SELECT 'it''s; fine';`,
			want: []string{`SELECT 'it''s; fine'`},
		},
		{
			// The E must be a standalone token: here it is the tail of an
			// identifier, so the literal is an ordinary one.
			name: "trailing E of an identifier is not an escape prefix",
			sql:  `SELECT valueE'C:\'; SELECT 2;`,
			want: []string{`SELECT valueE'C:\'`, `SELECT 2`},
		},
		{
			name: "dollar quoting still works",
			sql:  `CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END $$ LANGUAGE plpgsql; SELECT 2;`,
			want: []string{`CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END $$ LANGUAGE plpgsql`, `SELECT 2`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitStatements(tc.sql)
			if len(got) != len(tc.want) {
				t.Fatalf("split into %d statements, want %d:\n got:  %q\n want: %q",
					len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("statement %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSplitStatementsInLinesAcrossEscapeString: the File/Line contract from
// P2-T7 is load-bearing for PR annotations (P4-T2), and an E-string spanning
// newlines is exactly where a scanner that mis-tracks the literal would drift.
func TestSplitStatementsInLinesAcrossEscapeString(t *testing.T) {
	// Built from raw-string pieces so the backslash inside the E-string is
	// unambiguous in the Go source as well as in the SQL.
	sql := "SELECT 1;\n\n" +
		`SELECT E'line one\'s` + "\n" +
		`line two; still inside';` + "\n\nSELECT 3;\n"
	got := SplitStatementsIn("m.sql", sql)
	if len(got) != 3 {
		t.Fatalf("expected 3 statements, got %d: %+v", len(got), got)
	}
	wantLines := []int{1, 3, 6}
	for i, w := range wantLines {
		if got[i].Line != w {
			t.Errorf("statement %d at line %d, want %d (SQL %q)", i, got[i].Line, w, got[i].SQL)
		}
	}
	if got[0].File != "m.sql" {
		t.Errorf("file = %q, want m.sql", got[0].File)
	}
}
