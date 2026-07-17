package target

import (
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// realPullFixture builds a fixture shaped the way a real `rowshape pull` emits
// one — which is the whole point of this file.
//
// Postgres backs a PRIMARY KEY / UNIQUE constraint with an implicit index named
// after the constraint. A conformant pull records BOTH (RFC §6.4 constraints,
// §6.5 indexes) because both genuinely exist in the catalog. Every hand-written
// test fixture in this repo lists constraints WITHOUT their backing indexes, so
// none of them could catch a duplicate-index bug — while every real schema with a
// primary key hits it on the first `validate`.
func realPullFixture() *fixture.Fixture {
	return &fixture.Fixture{
		Tables: map[string]fixture.Table{
			"public.orders": {
				Rows: fixture.Fact[int64]{Value: 20000},
				Columns: map[string]fixture.Column{
					"id":     {Type: "bigint"},
					"email":  {Type: "text"},
					"status": {Type: "text", Nullable: true},
				},
				Constraints: []fixture.Constraint{
					{Name: "orders_pkey", Kind: "primary_key", Columns: []string{"id"}},
					{Name: "orders_email_key", Kind: "unique", Columns: []string{"email"}},
				},
				Indexes: []fixture.Index{
					// The implicit indexes Postgres created for the two constraints
					// above. Same names — they are the same objects.
					{Name: "orders_pkey", Method: "btree", Columns: []string{"id"}, Unique: true},
					{Name: "orders_email_key", Method: "btree", Columns: []string{"email"}, Unique: true},
					// A genuinely secondary index, which MUST still be created.
					{Name: "orders_status_idx", Method: "btree", Columns: []string{"status"}},
				},
			},
		},
	}
}

// TestDDLDoesNotDuplicateConstraintBackedIndexes: CREATE TABLE already emits the
// PRIMARY KEY and UNIQUE constraints, and Postgres creates their indexes with it.
// Emitting those indexes again is a hard failure:
//
//	ERROR: relation "orders_pkey" already exists (SQLSTATE 42P07)
//
// which fails the DDL and takes `rowshape validate` down with it, on any real
// schema.
func TestDDLDoesNotDuplicateConstraintBackedIndexes(t *testing.T) {
	stmts := DDL(realPullFixture())
	all := strings.Join(stmts, ";\n")

	for _, name := range []string{"orders_pkey", "orders_email_key"} {
		if n := strings.Count(all, `INDEX "`+name+`"`); n != 0 {
			t.Errorf("DDL creates index %q explicitly (%d time(s)), but CREATE TABLE's constraint already builds it — "+
				"Postgres fails with 'relation \"%s\" already exists'.\n%s", name, n, name, all)
		}
	}

	// The constraints themselves must still be declared — dropping them to dodge
	// the duplicate would silently lose the uniqueness the fixture recorded.
	if !strings.Contains(all, "PRIMARY KEY") {
		t.Errorf("the primary key must still be declared:\n%s", all)
	}
	if !strings.Contains(all, "UNIQUE (") {
		t.Errorf("the unique constraint must still be declared:\n%s", all)
	}

	// A genuinely secondary index is still created — that is what the loop is for.
	if !strings.Contains(all, `INDEX "orders_status_idx"`) {
		t.Errorf("secondary indexes must still be created:\n%s", all)
	}
}

// TestDDLKeepsSecondaryIndexWithConstraintLikeName: only names that actually
// belong to a constraint on THIS table are skipped. An index merely named like
// one is still a real index and must be built.
func TestDDLKeepsSecondaryIndexWithConstraintLikeName(t *testing.T) {
	f := &fixture.Fixture{
		Tables: map[string]fixture.Table{
			"public.t": {
				Rows:    fixture.Fact[int64]{Value: 1},
				Columns: map[string]fixture.Column{"a": {Type: "integer"}},
				// No constraints at all.
				Indexes: []fixture.Index{{Name: "t_pkey", Method: "btree", Columns: []string{"a"}}},
			},
		},
	}
	all := strings.Join(DDL(f), ";\n")
	if !strings.Contains(all, `INDEX "t_pkey"`) {
		t.Errorf("an index with no matching constraint must still be created:\n%s", all)
	}
}
