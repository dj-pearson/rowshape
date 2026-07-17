package hydrate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rowshape/rowshape/internal/fixture"
)

// Options controls a hydration run.
type Options struct {
	// Seed drives all generation; the same seed reproduces the same output
	// (RFC §10).
	Seed int64
	// Scale is the fraction of declared rows to synthesize (RFC §9). 1.0 means
	// the full declared count; 0.01 means 1%. Values <= 0 default to 1.0.
	Scale float64
	// MaxRows caps the synthesized rows per table (0 = no cap), a safety valve so
	// a huge declared count can't try to generate billions of rows.
	MaxRows int64
}

// Result is the generated data for a whole fixture.
type Result struct {
	Tables []GeneratedTable
}

// GeneratedTable is one table's synthesized rows in column order.
type GeneratedTable struct {
	Name         string
	Columns      []string
	Rows         [][]any
	DeclaredRows int64 // production rows the fixture declares (RFC §9, for P1-T8)
}

// Generate synthesizes rows for every table in the fixture (RFC §13). Tables and
// columns are processed in sorted order so generation never depends on Go map
// iteration order (RFC §10).
func Generate(f *fixture.Fixture, opts Options) (*Result, error) {
	if f == nil {
		return nil, fmt.Errorf("hydrate: nil fixture")
	}
	scale := opts.Scale
	if scale <= 0 {
		scale = 1.0
	}

	tableNames := sortedKeys(f.Tables)
	res := &Result{}
	// A map of table -> hydrated row count, needed so a foreign key knows how many
	// parent rows exist.
	rowCounts := map[string]int64{}
	for _, name := range tableNames {
		rowCounts[name] = hydratedRowCount(f.Tables[name].Rows.Value, scale, opts.MaxRows)
	}

	for _, name := range tableNames {
		tbl := f.Tables[name]
		gt, err := generateTable(f, name, tbl, opts.Seed, rowCounts)
		if err != nil {
			return nil, fmt.Errorf("hydrate %s: %w", name, err)
		}
		res.Tables = append(res.Tables, gt)
	}
	return res, nil
}

// hydratedRowCount converts a declared count and scale into a row count of at
// least 1 (so every table gets exercised), capped by MaxRows.
func hydratedRowCount(declared int64, scale float64, maxRows int64) int64 {
	n := int64(float64(declared) * scale)
	if n < 1 {
		n = 1
	}
	if maxRows > 0 && n > maxRows {
		n = maxRows
	}
	return n
}

// generateTable synthesizes one table.
func generateTable(f *fixture.Fixture, name string, tbl fixture.Table, seed int64, rowCounts map[string]int64) (GeneratedTable, error) {
	n := rowCounts[name]
	colNames := sortedKeys(tbl.Columns)

	gt := GeneratedTable{
		Name:         name,
		Columns:      colNames,
		Rows:         make([][]any, n),
		DeclaredRows: tbl.Rows.Value,
	}
	for i := range gt.Rows {
		gt.Rows[i] = make([]any, len(colNames))
	}

	// Precompute foreign-key assignments per column so fan-out shape is honored.
	fkAssign := map[string][]int64{}
	fkRefs := map[string]fixture.Reference{}
	for _, ref := range tbl.References {
		parentRows := rowCounts[parentTable(ref.To)]
		fkAssign[ref.Column] = assignForeignKeys(seed, name, ref, n, parentRows, tbl.Rows.Value)
		fkRefs[ref.Column] = ref
	}

	for ci, col := range colNames {
		c := tbl.Columns[col]
		fk, isFK := fkAssign[col]
		for ord := int64(0); ord < n; ord++ {
			var v any
			switch {
			case isFK:
				// fk[ord] is the assigned parent ordinal; map it to the id value
				// the parent's identity column actually generated for that ordinal.
				v = parentIDValue(f, fkRefs[col], fk[ord], rowCounts[parentTable(fkRefs[col].To)])
			default:
				v = generateValue(seed, name, col, c, ord)
			}
			gt.Rows[ord][ci] = v
		}
	}
	return gt, nil
}

// generateValue synthesizes one cell. Nulls are placed by a deterministic
// low-discrepancy quota so the null fraction is honored within a row or two
// (RFC §13, ±0.5%) and a cell's null-ness never changes as --scale grows.
func generateValue(seed int64, table, column string, c fixture.Column, ord int64) any {
	if c.Nullable && c.NullFraction != nil && isNullAt(ord, c.NullFraction.Value) {
		return nil
	}
	r := cellRNG(seed, table, column, ord)

	// A unique column derives its value from the ordinal so values never collide
	// (RFC §13 honor `unique`). A non-unique column draws a bucket in [0, distinct)
	// so only about `distinct` distinct values appear.
	unique := c.Unique != nil && c.Unique.Value

	// A skewed numeric column carries a histogram; sample from it so hydrate
	// reproduces the skew, not just the mean/range (RFC §6.2). Each equi-depth
	// bucket holds equal rows, so picking a bucket uniformly and a value within it
	// recreates the original density.
	if !unique && c.Histogram != nil && categorize(c.Type) == "numeric" {
		if v, ok := sampleHistogram(c.Histogram, r); ok {
			return v
		}
	}

	var n int64
	if unique {
		n = ord
	} else if d := distinctOf(c); d > 0 {
		n = r.intn(d)
	} else {
		n = r.intn(1 << 20)
	}
	return fakeValue(c, n, r)
}

// sampleHistogram draws an integer from an equi-depth histogram: a uniformly
// chosen bucket, then a uniform value within that bucket's bounds. This
// reproduces the column's skew — dense value regions have many narrow buckets
// and so receive proportionally many rows.
func sampleHistogram(h *fixture.Histogram, r *rng) (any, bool) {
	if h == nil || len(h.Bounds) < 2 {
		return nil, false
	}
	b := int(r.intn(int64(len(h.Bounds) - 1)))
	lo, lok := toFloat(h.Bounds[b])
	hi, hok := toFloat(h.Bounds[b+1])
	if !lok || !hok {
		return nil, false
	}
	if hi < lo {
		lo, hi = hi, lo
	}
	span := hi - lo
	v := lo + r.float64()*span
	return int64(v), true
}

// toFloat best-effort converts a histogram bound to a float64.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

// fakeValue produces an obviously-fake value of the right shape (RFC §13): the
// hydrator reproduces shape, never content. n selects which value (the ordinal
// for unique columns, a bucket otherwise).
func fakeValue(c fixture.Column, n int64, r *rng) any {
	switch c.Format {
	case "email":
		return fmt.Sprintf("user_%05d@example.invalid", n)
	case "uuid":
		return fakeUUID(n)
	case "url":
		return fmt.Sprintf("https://example.invalid/%d", n)
	case "hostname":
		return fmt.Sprintf("host-%d.example.invalid", n)
	case "slug":
		return fmt.Sprintf("slug-%d", n)
	case "ipv4":
		return fmt.Sprintf("192.0.2.%d", n%256)
	case "numeric_string":
		return fmt.Sprintf("%d", n)
	case "enum_like":
		if len(c.Values) > 0 {
			return c.Values[n%int64(len(c.Values))]
		}
		return fmt.Sprintf("value_%d", n)
	case "free_text":
		return fmt.Sprintf("sample text %d", n)
	case "jsonb_shape", "json":
		return "{}"
	}

	// No format hint: fall back to the type category.
	switch categorize(c.Type) {
	case "numeric":
		return numericInRange(c, n)
	case "temporal":
		return temporalInRange(c, n)
	case "bool":
		return n%2 == 0
	case "bytea":
		return []byte(fmt.Sprintf("\\x%08x", n))
	case "uuid":
		return fakeUUID(n)
	default:
		return fmt.Sprintf("val_%d", n)
	}
}

// isNullAt reports whether ordinal ord is null for a target fraction p, using a
// deterministic low-discrepancy sequence: the count of nulls up to ord tracks
// p*ord to within one, so the realized fraction is within 1/N of p and ord's
// null-ness is independent of the total row count (scale-stable).
func isNullAt(ord int64, p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	prev := int64(float64(ord) * p)
	cur := int64(float64(ord+1) * p)
	return cur > prev
}

// distinctOf returns the column's distinct estimate, or 0 if unknown.
func distinctOf(c fixture.Column) int64 {
	if c.Distinct == nil {
		return 0
	}
	return c.Distinct.Value
}

// numericInRange returns an integer within the column's range (or a plain fake
// number if no range is known).
func numericInRange(c fixture.Column, n int64) any {
	lo, hi, ok := numericBounds(c)
	if !ok {
		return n
	}
	span := hi - lo + 1
	if span <= 0 {
		return lo
	}
	return lo + (n % span)
}

// numericBounds extracts integer min/max from a numeric range, if present.
func numericBounds(c fixture.Column) (lo, hi int64, ok bool) {
	if c.Range == nil {
		return 0, 0, false
	}
	l, lok := toInt64(c.Range.Min)
	h, hok := toInt64(c.Range.Max)
	if !lok || !hok || h < l {
		return 0, 0, false
	}
	return l, h, true
}

// temporalInRange returns a timestamp within the column's range, or a fixed fake
// epoch-based time if no range is known. It returns a time.Time so it encodes
// cleanly for both SQL literals and the binary COPY protocol.
func temporalInRange(c fixture.Column, n int64) any {
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if c.Range != nil {
		if lo, ok := toTime(c.Range.Min); ok {
			if hi, ok := toTime(c.Range.Max); ok && !hi.Before(lo) {
				span := hi.Sub(lo)
				if span <= 0 {
					return lo.UTC()
				}
				off := time.Duration(n) % span
				return lo.Add(off).UTC()
			}
			return lo.UTC()
		}
	}
	return base.Add(time.Duration(n) * time.Hour).UTC()
}

// fakeUUID renders a deterministic, obviously-synthetic UUID encoding n.
func fakeUUID(n int64) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

// parentIDValue maps a parent ordinal to the id value the parent table actually
// generated for it, by running the same generator over the same ordinal.
//
// It used to just return the ordinal, on the reasoning that "a unique numeric id
// with no range is generated as its ordinal". The caveat was the bug: a real
// `pull` emits a range for every numeric column (RFC §6.2), so a real fixture's
// id column has one, and numericInRange produces `min + (ordinal % span)` — not
// the ordinal. For `users.id` with range {min: 1, max: 5000}, ordinal 0 is id 1.
//
// Returning the ordinal therefore pointed every child one parent short: user_id
// ran 0..parentN-1 while users.id ran 1..parentN, so user_id=0 referenced a
// parent that does not exist. That is an ORPHAN, in a fixture whose
// orphan_fraction is {value: 0, confidence: exact, via: constraint} — hydrate
// inventing the exact condition the fixture proves absent. A migration adding
// `FOREIGN KEY (user_id) REFERENCES users(id)` then fails on hydrated data, and
// rowshape reports a FAIL it manufactured itself.
//
// Deriving the value from the parent column instead of assuming its shape keeps
// the two in step whatever the range is — and if the fixture carries no facts for
// the parent column, generation falls back to the ordinal, which is what the id
// would be in that case anyway.
func parentIDValue(f *fixture.Fixture, ref fixture.Reference, parentOrdinal, parentN int64) int64 {
	col, ok := parentIDColumn(f, ref)
	if !ok {
		return parentOrdinal
	}
	// An ordinal at or beyond parentN is a deliberate orphan (assignForeignKeys
	// hands these out to honour orphan_fraction). It must be an id NO parent has,
	// and "beyond the largest one generated" is the only choice that holds
	// whatever the column's range is: min + (ordinal % span) wraps, so picking an
	// unused ordinal is not enough when the span is narrower than the parent
	// count — the wrap would land on a real parent and the orphan would quietly
	// become valid.
	if parentOrdinal >= parentN && parentN > 0 {
		return maxParentID(col, parentN) + 1 + (parentOrdinal - parentN)
	}
	if v, ok := numericInRange(col, parentOrdinal).(int64); ok {
		return v
	}
	return parentOrdinal
}

// maxParentID is the largest id the parent table generated across ordinals
// [0, parentN).
func maxParentID(col fixture.Column, parentN int64) int64 {
	var max int64
	for ord := int64(0); ord < parentN; ord++ {
		v, ok := numericInRange(col, ord).(int64)
		if !ok {
			return parentN // no numeric range: ids are the ordinals themselves
		}
		if ord == 0 || v > max {
			max = v
		}
		// The values cycle with period span, so one lap is enough.
		if lo, hi, ok := numericBounds(col); ok && ord >= hi-lo {
			break
		}
	}
	return max
}

// parentIDColumn resolves ref.To ("public.users.id") to the referenced column's
// profile.
func parentIDColumn(f *fixture.Fixture, ref fixture.Reference) (fixture.Column, bool) {
	if f == nil {
		return fixture.Column{}, false
	}
	i := strings.LastIndex(ref.To, ".")
	if i < 0 {
		return fixture.Column{}, false
	}
	tbl, ok := f.Tables[ref.To[:i]]
	if !ok {
		return fixture.Column{}, false
	}
	col, ok := tbl.Columns[ref.To[i+1:]]
	return col, ok
}

// parentTable extracts schema.table from a reference target schema.table.column.
func parentTable(to string) string {
	i := strings.LastIndex(to, ".")
	if i < 0 {
		return to
	}
	return to[:i]
}

// sortedKeys returns a map's keys in lexicographic order — the canonicalization
// that makes generation independent of map iteration order (RFC §10).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// toInt64 best-effort converts a range bound to int64.
func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		return int64(x), true
	default:
		return 0, false
	}
}

// toTime best-effort converts a range bound to a time.Time.
func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// categorize maps a Postgres type name onto a coarse generation category.
func categorize(typ string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch {
	case t == "bytea":
		return "bytea"
	case t == "json" || t == "jsonb":
		return "json"
	case t == "uuid":
		return "uuid"
	case t == "boolean":
		return "bool"
	case strings.HasPrefix(t, "timestamp") || t == "date" || strings.HasPrefix(t, "time"):
		return "temporal"
	case t == "text" || strings.Contains(t, "char") || strings.Contains(t, "varying") || t == "citext" || t == "name":
		return "text"
	case t == "smallint" || t == "integer" || t == "bigint" || t == "real" ||
		t == "double precision" || strings.HasPrefix(t, "numeric") ||
		strings.HasPrefix(t, "decimal") || t == "money" || strings.HasSuffix(t, "serial"):
		return "numeric"
	default:
		return "text"
	}
}
