package hydrate

import "github.com/rowshape/rowshape/internal/fixture"

// This file makes RFC §9's separation explicit and hard to get wrong: a fixture
// DECLARES production rows (e.g. 1.2M) which drive extrapolation of lock and
// rewrite cost, while hydrate MATERIALIZES a scaled subset (e.g. 12k) which
// tests correctness. The two counts serve different purposes and MUST NOT be
// conflated (RFC §9, INV-DURATIONS-BUCKETS): a lock finding reports "≈9s, 1.2M
// rows rewritten" having physically rewritten 12k.

// RowPlan records, for one table, the strict separation between declared
// production rows and hydrated (materialized) rows.
type RowPlan struct {
	Table string
	// Declared is the production row count the fixture asserts. It is NEVER
	// scaled and is what a validator extrapolates from (RFC §9).
	Declared int64
	// Hydrated is the number of rows hydrate actually materializes at the chosen
	// scale. It is what correctness is tested against, and nothing else.
	Hydrated int64
}

// PlanRows computes the per-table row plan for a fixture at a given scale,
// without generating any data. Tables are returned in sorted order.
func PlanRows(f *fixture.Fixture, opts Options) []RowPlan {
	scale := opts.Scale
	if scale <= 0 {
		scale = 1.0
	}
	var plans []RowPlan
	for _, name := range sortedKeys(f.Tables) {
		declared := f.Tables[name].Rows.Value
		plans = append(plans, RowPlan{
			Table:    name,
			Declared: declared,
			Hydrated: hydratedRowCount(declared, scale, opts.MaxRows),
		})
	}
	return plans
}

// Materialized returns the number of rows actually generated for a table — the
// hydrated count, never the declared count.
func (g GeneratedTable) Materialized() int64 {
	return int64(len(g.Rows))
}

// Plan returns the row plan for a generated result: each table's declared
// production rows alongside the rows actually materialized. Extrapolation code
// MUST read Declared; correctness checks read Hydrated (via Materialized).
func (r *Result) Plan() []RowPlan {
	plans := make([]RowPlan, 0, len(r.Tables))
	for _, t := range r.Tables {
		plans = append(plans, RowPlan{
			Table:    t.Name,
			Declared: t.DeclaredRows,
			Hydrated: t.Materialized(),
		})
	}
	return plans
}
