package conformance

import (
	"fmt"
	"reflect"

	"github.com/rowshape/rowshape/internal/estimate"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
	"github.com/rowshape/rowshape/internal/verdict"
)

// CheckHydrator runs the hydrator MUSTs (RFC §13, §10) against a hydrator by
// hydrating f: null_fraction is honored within ±0.5%; a column declared `unique`
// is hydrated with distinct values; and generation is deterministic (the same
// fixture + seed produces byte-identical output). rows is the number of rows to
// synthesize (a larger count tightens the null_fraction tolerance).
func CheckHydrator(f *fixture.Fixture, seed, rows int64) []Violation {
	var vs []Violation

	opts := hydrate.Options{Seed: seed, Scale: 1.0, MaxRows: rows}
	r1, err := hydrate.Generate(f, opts)
	if err != nil {
		return []Violation{{"§13 hydrator", "hydrate", "generation failed: " + err.Error()}}
	}
	r2, err := hydrate.Generate(f, opts)
	if err != nil || !reflect.DeepEqual(r1, r2) {
		vs = append(vs, Violation{"§10 deterministic", "hydrate", "the same fixture + seed did not reproduce identical output"})
	}

	for _, gt := range r1.Tables {
		tbl := f.Tables[gt.Name]
		n := int64(len(gt.Rows))
		if n == 0 {
			continue
		}
		for ci, cname := range gt.Columns {
			col, ok := tbl.Columns[cname]
			if !ok {
				continue
			}
			if col.NullFraction != nil {
				got := nullFractionOf(gt.Rows, ci)
				if diff := abs(got - col.NullFraction.Value); diff > 0.005 {
					vs = append(vs, Violation{"§13 null_fraction ±0.5%", gt.Name + "." + cname, fmt.Sprintf("declared %.4f, hydrated %.4f (off by %.4f)", col.NullFraction.Value, got, diff)})
				}
			}
			if col.Unique != nil && col.Unique.Value {
				if dup := firstDuplicate(gt.Rows, ci); dup != nil {
					vs = append(vs, Violation{"§13 honor unique", gt.Name + "." + cname, fmt.Sprintf("column declared unique but hydrated a duplicate value (%v)", dup)})
				}
			}
		}
	}
	return vs
}

// CheckValidator runs the validator MUSTs (RFC §13, §7.4, §9.2) against
// rowshape's verdict engine: a finding resting on an estimated fact is capped to
// WARN (never PASS); the same finding on a proven fact may PASS; durations are
// reported only as the five known buckets; and a dependency's confidence is read
// from the fixture, never lowered by the finding (structurally).
func CheckValidator() []Violation {
	var vs []Violation

	estimated := mustFixture(`rowshape_fixture: "1"
meta: {id: c, engine: {name: postgres, version: "16"}}
tables:
  public.t:
    rows: {value: 1000000, confidence: exact}
    columns:
      c: {type: text, nullable: false, distinct: {value: 999000, confidence: estimated}}
`)
	proven := mustFixture(`rowshape_fixture: "1"
meta: {id: c, engine: {name: postgres, version: "16"}}
tables:
  public.t:
    rows: {value: 1000000, confidence: exact}
    columns:
      c: {type: text, nullable: false, unique: {value: true, confidence: exact, via: probe}}
`)

	// §7.4: a PASS resting on an unproven fact is capped to WARN.
	eng := verdict.NewEngine(estimated)
	got, _ := eng.Cap(verdict.VerdictPass, verdict.Finding{Code: "X", Severity: verdict.SeverityInfo, DependsOn: []string{"public.t.c.unique"}})
	if got != verdict.VerdictWarn {
		vs = append(vs, Violation{"§7.4 capping", "estimated fact", fmt.Sprintf("a finding on an unproven fact produced %s, MUST be WARN", got)})
	}
	// §7.4 boundary: a proven fact may PASS.
	if got, _ := verdict.NewEngine(proven).Cap(verdict.VerdictPass, verdict.Finding{Code: "X", Severity: verdict.SeverityInfo, DependsOn: []string{"public.t.c.unique"}}); got != verdict.VerdictPass {
		vs = append(vs, Violation{"§7.4 capping", "proven fact", fmt.Sprintf("a finding on a proven fact produced %s, expected PASS", got)})
	}

	// §9.2: durations are one of the five buckets, never a point estimate.
	known := map[string]bool{verdict.BucketInstant: true, verdict.BucketFast: true, verdict.BucketNoticeable: true, verdict.BucketSlow: true, verdict.BucketOutage: true}
	for _, ms := range []float64{0, 50, 500, 5000, 30000, 120000} {
		if b := estimate.Bucket(ms); !known[b] {
			vs = append(vs, Violation{"§9.2 duration buckets", "bucket", fmt.Sprintf("%v ms produced non-bucket %q", ms, b)})
		}
	}
	return vs
}

func nullFractionOf(rows [][]any, col int) float64 {
	nulls := 0
	for _, r := range rows {
		if col < len(r) && r[col] == nil {
			nulls++
		}
	}
	return float64(nulls) / float64(len(rows))
}

func firstDuplicate(rows [][]any, col int) any {
	seen := map[any]bool{}
	for _, r := range rows {
		if col >= len(r) || r[col] == nil {
			continue
		}
		v := r[col]
		if seen[v] {
			return v
		}
		seen[v] = true
	}
	return nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// mustFixture parses a fixture used by the validator conformance checks.
func mustFixture(yaml string) *fixture.Fixture {
	f, err := fixture.Parse([]byte(yaml))
	if err != nil {
		panic("conformance: bad built-in fixture: " + err.Error())
	}
	return f
}
