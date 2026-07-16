package fixture

import "sort"

// This file implements the self-audit behind `rowshape inspect --leaks`
// (RFC §8.3): it enumerates every field in a fixture that is DERIVED FROM ROW
// VALUES, with its source location and the privacy level at which it appears. It
// is a trust feature, not a compliance checkbox — a security team finds these
// fields anyway, and pointing at them first is the difference between a
// documented tradeoff and an undisclosed leak (PRD §11).

// Leak is one field derived from production row values (RFC §8.1).
type Leak struct {
	// Location is where the field lives: schema.table.column for a column-derived
	// field, or schema.table (constraint <name>) for a CHECK expression.
	Location string
	// Field names the kind of value-derived data (range, histogram, values,
	// frequencies, check_expression, length.max).
	Field string
	// Privacy is the weakest privacy level at which this field is still present —
	// the level you would drop below to remove it (RFC §8.2). "strict" means the
	// field survives even the strictest level.
	Privacy string
	// Detail is a short human explanation of what it reveals.
	Detail string
}

// Leaks returns every value-derived field in the fixture, in a deterministic
// order (by location, then field). The list is exhaustive: if a field made it
// into the fixture and it came from row values, it is here (RFC §8.3).
func Leaks(f *Fixture) []Leak {
	var leaks []Leak
	for _, table := range sortedTableNames(f) {
		tbl := f.Tables[table]

		for _, colName := range sortedColumnNames(tbl) {
			col := tbl.Columns[colName]
			loc := table + "." + colName

			// Numeric/temporal range min/max are real extreme values (RFC §8.1).
			if col.Range != nil && (col.Range.Min != nil || col.Range.Max != nil) {
				leaks = append(leaks, Leak{loc, "range", "standard", "min/max are real extreme values from the column"})
			}
			// Histogram bounds are, literally, real values (RFC §8.1).
			if col.Histogram != nil && len(col.Histogram.Bounds) > 0 {
				leaks = append(leaks, Leak{loc, "histogram", "standard", "bucket bounds are real values from the column"})
			}
			// A materialized value set reveals the values themselves (RFC §8.1).
			if len(col.Values) > 0 {
				leaks = append(leaks, Leak{loc, "values", "permissive", "the value set is published verbatim"})
			}
			if len(col.Frequencies) > 0 {
				leaks = append(leaks, Leak{loc, "frequencies", "permissive", "per-value frequencies are published"})
			}
			// length.max on free_text is weakly identifying in a small table
			// (RFC §8.1) and survives even strict.
			if col.Format == "free_text" && col.Length != nil && col.Length.Max != nil {
				leaks = append(leaks, Leak{loc, "length.max", "strict", "max length of free text is weakly identifying in a small table"})
			}
		}

		// A verbatim CHECK expression can carry literal values from your domain
		// (RFC §6.4 / §8.1). Under strict it is replaced by "opaque".
		for _, c := range tbl.Constraints {
			if c.Kind == "check" && c.Expression != "" && c.Expression != "opaque" {
				leaks = append(leaks, Leak{table + " (constraint " + c.Name + ")", "check_expression", "standard", "the CHECK body may contain domain literals"})
			}
		}
	}

	sort.SliceStable(leaks, func(i, j int) bool {
		if leaks[i].Location != leaks[j].Location {
			return leaks[i].Location < leaks[j].Location
		}
		return leaks[i].Field < leaks[j].Field
	})
	return leaks
}

func sortedTableNames(f *Fixture) []string {
	out := make([]string, 0, len(f.Tables))
	for k := range f.Tables {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedColumnNames(t Table) []string {
	out := make([]string, 0, len(t.Columns))
	for k := range t.Columns {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
