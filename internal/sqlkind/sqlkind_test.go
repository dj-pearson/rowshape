package sqlkind

import "testing"

// This is the single home for the transaction-control test that used to be
// duplicated in internal/plan and internal/validate (docs/TESTING-GAPS.md item
// 3b). Both packages now call IsTxControl, so this one table covers both.
func TestIsTxControl(t *testing.T) {
	yes := []string{
		"BEGIN", "begin", "  COMMIT  ", "COMMIT;", "ROLLBACK",
		"START TRANSACTION", "END", "SAVEPOINT sp1", "RELEASE sp1",
		"begin isolation level serializable",
		"\tROLLBACK\n",
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
		if !IsTxControl(s) {
			t.Errorf("IsTxControl(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsTxControl(s) {
			t.Errorf("IsTxControl(%q) = true, want false", s)
		}
	}
}
