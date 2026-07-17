package cmd

import (
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// tbl builds a table with the given columns (name -> nullable).
func tbl(cols map[string]bool) fixture.Table {
	t := fixture.Table{Columns: map[string]fixture.Column{}}
	for name, nullable := range cols {
		t.Columns[name] = fixture.Column{Type: "text", Nullable: nullable}
	}
	return t
}

func fx(tables map[string]fixture.Table) *fixture.Fixture {
	return &fixture.Fixture{Tables: tables}
}

// TestCompareSchemaDetectsUndeclaredColumn: "does reality match intent"
// (PRD §8.1) runs in BOTH directions.
//
// verify used to walk only the fixture, answering just "is everything I declared
// still there?". A column that exists in production and is absent from the
// fixture returned `[OK] reality matches intent`, exit 0 — verified against a
// live database before this test existed.
//
// That is the commonest drift there is: someone shipped a migration without
// re-pulling. And it is the most consequential, because validate hydrates FROM
// the fixture — so every later verdict reasons about a schema production no
// longer has, confidently and wrongly. verify is the one command meant to catch
// it.
func TestCompareSchemaDetectsUndeclaredColumn(t *testing.T) {
	expected := fx(map[string]fixture.Table{
		"public.users": tbl(map[string]bool{"id": false, "email": false}),
	})
	actual := fx(map[string]fixture.Table{
		"public.users": tbl(map[string]bool{"id": false, "email": false, "sneaky": true}),
	})

	drifts := compareSchema(expected, actual)
	if len(drifts) == 0 {
		t.Fatal("a column on the target that the fixture does not declare is drift — the fixture is stale, " +
			"and every validate run after it reasons about a schema production no longer has")
	}
	var found bool
	for _, d := range drifts {
		if strings.Contains(d.Object, "sneaky") {
			found = true
			if !strings.Contains(d.Got, "pull") {
				t.Errorf("the drift should say how to fix it (re-pull), got: %s", d.Got)
			}
		}
	}
	if !found {
		t.Errorf("drift should name the undeclared column, got: %+v", drifts)
	}
}

// TestCompareSchemaIgnoresUndeclaredTables: a fixture may legitimately cover a
// subset of the database (`pull --schema public`), so a table it never declared
// is not evidence of anything. Flagging those would make verify noisy enough to
// ignore, which costs more than the drift it would catch.
func TestCompareSchemaIgnoresUndeclaredTables(t *testing.T) {
	expected := fx(map[string]fixture.Table{
		"public.users": tbl(map[string]bool{"id": false}),
	})
	actual := fx(map[string]fixture.Table{
		"public.users":   tbl(map[string]bool{"id": false}),
		"other.audit":    tbl(map[string]bool{"id": false}),
		"reporting.snap": tbl(map[string]bool{"id": false}),
	})

	if drifts := compareSchema(expected, actual); len(drifts) != 0 {
		t.Errorf("tables outside the fixture's scope are not drift, got: %+v", drifts)
	}
}

// TestCompareSchemaClean: a target that matches must report nothing, or the
// signal is worthless.
func TestCompareSchemaClean(t *testing.T) {
	same := func() *fixture.Fixture {
		return fx(map[string]fixture.Table{
			"public.users": tbl(map[string]bool{"id": false, "email": true}),
		})
	}
	if drifts := compareSchema(same(), same()); len(drifts) != 0 {
		t.Errorf("identical schemas must report no drift, got: %+v", drifts)
	}
}

// TestCompareSchemaStillDetectsMissing: the original direction must keep working
// — the new check is additive.
func TestCompareSchemaStillDetectsMissing(t *testing.T) {
	expected := fx(map[string]fixture.Table{
		"public.users": tbl(map[string]bool{"id": false, "email": false}),
	})
	actual := fx(map[string]fixture.Table{
		"public.users": tbl(map[string]bool{"id": false}),
	})
	drifts := compareSchema(expected, actual)
	if len(drifts) != 1 || !strings.Contains(drifts[0].Got, "missing column") {
		t.Errorf("a declared column absent from the target is still drift, got: %+v", drifts)
	}
}
