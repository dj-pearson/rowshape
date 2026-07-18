// Package sqlkind classifies raw SQL statements by kind, without a parser.
//
// It exists to hold logic that more than one package needs to agree on. The
// transaction-control test lived, verbatim, in both internal/plan and
// internal/validate — two copies with no guard tying them together, so a fix to
// one could silently diverge from the other. Neither package can import the
// other (internal/plan already imports internal/validate), so the shared home is
// a third, lower package that depends on neither.
package sqlkind

import "strings"

// txKeywords are the leading verbs of a transaction-control statement.
var txKeywords = []string{
	"BEGIN", "COMMIT", "ROLLBACK", "START TRANSACTION", "END", "SAVEPOINT", "RELEASE",
}

// IsTxControl reports whether sql is transaction control (BEGIN, COMMIT,
// ROLLBACK, START TRANSACTION, END, SAVEPOINT, RELEASE) rather than DDL/DML.
//
// It matches on the leading verb only, so an identifier that merely starts with
// a keyword ("BEGINNING", "COMMITTED", a table named "beginner") is not mistaken
// for transaction control. Surrounding whitespace is ignored.
func IsTxControl(sql string) bool {
	u := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range txKeywords {
		if u == kw || strings.HasPrefix(u, kw+" ") || strings.HasPrefix(u, kw+";") {
			return true
		}
	}
	return false
}
