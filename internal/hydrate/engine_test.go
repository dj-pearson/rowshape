package hydrate

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// oneTable builds a single-table fixture with the given columns and declared row
// count, for engine tests.
func oneTable(name string, rows int64, cols map[string]fixture.Column) *fixture.Fixture {
	return &fixture.Fixture{
		RowshapeFixture: fixture.FormatVersion,
		Meta:            fixture.Meta{Engine: fixture.Engine{Name: "postgres", Version: "16"}},
		Tables: map[string]fixture.Table{
			name: {Rows: fixture.Fact[int64]{Value: rows, Confidence: fixture.Exact}, Columns: cols},
		},
	}
}

func nullableText(nullFrac float64, distinct int64, format string) fixture.Column {
	return fixture.Column{
		Type:         "text",
		Nullable:     true,
		NullFraction: &fixture.Fact[float64]{Value: nullFrac, Confidence: fixture.Estimated},
		Distinct:     &fixture.Fact[int64]{Value: distinct, Confidence: fixture.Estimated},
		Format:       format,
	}
}

func column(rows int64) *fixture.Fixture {
	return oneTable("public.users", rows, map[string]fixture.Column{
		"id":    {Type: "bigint", Nullable: false, Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact, Via: "constraint"}, Generated: "identity"},
		"email": nullableText(0.1, 900, "email"),
	})
}

// TestDeterminism: the same seed reproduces identical output; a different seed
// produces different output (RFC §10).
func TestDeterminism(t *testing.T) {
	f := column(500)
	a, err := Generate(f, Options{Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := Generate(f, Options{Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same seed produced different output")
	}
	c, err := Generate(f, Options{Seed: 7})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if reflect.DeepEqual(a, c) {
		t.Errorf("different seeds produced identical output")
	}
}

// TestScalePrefixStability: increasing --scale only appends rows; the retained
// prefix of an independent column's values is byte-stable (RFC §10).
func TestScalePrefixStability(t *testing.T) {
	f := column(1000)
	small, err := Generate(f, Options{Seed: 42, Scale: 0.1}) // 100 rows
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	big, err := Generate(f, Options{Seed: 42, Scale: 0.5}) // 500 rows
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	st := small.Tables[0]
	bt := big.Tables[0]
	if len(bt.Rows) <= len(st.Rows) {
		t.Fatalf("expected more rows at larger scale: %d vs %d", len(bt.Rows), len(st.Rows))
	}
	for r := range st.Rows {
		if !reflect.DeepEqual(st.Rows[r], bt.Rows[r]) {
			t.Fatalf("prefix row %d changed with scale: %v vs %v", r, st.Rows[r], bt.Rows[r])
		}
	}
}

// TestNullFractionTolerance: the realized null fraction is within ±0.5% of the
// declared value (RFC §13).
func TestNullFractionTolerance(t *testing.T) {
	for _, p := range []float64{0.0, 0.03, 0.1, 0.5} {
		f := oneTable("public.t", 5000, map[string]fixture.Column{
			"c": nullableText(p, 1000, "free_text"),
		})
		res, err := Generate(f, Options{Seed: 42})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		gt := res.Tables[0]
		nulls := 0
		for _, row := range gt.Rows {
			if row[0] == nil {
				nulls++
			}
		}
		got := float64(nulls) / float64(len(gt.Rows))
		if diff := got - p; diff > 0.005 || diff < -0.005 {
			t.Errorf("null_fraction %.3f: realized %.4f, outside ±0.5%%", p, got)
		}
	}
}

// TestUniqueHonored: a column marked unique has all-distinct values (RFC §13).
func TestUniqueHonored(t *testing.T) {
	f := oneTable("public.t", 2000, map[string]fixture.Column{
		"email": {
			Type:     "text",
			Nullable: false,
			Unique:   &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact, Via: "constraint"},
			Format:   "email",
		},
	})
	res, err := Generate(f, Options{Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gt := res.Tables[0]
	seen := map[any]bool{}
	for _, row := range gt.Rows {
		if seen[row[0]] {
			t.Fatalf("duplicate value in unique column: %v", row[0])
		}
		seen[row[0]] = true
	}
	if len(seen) != len(gt.Rows) {
		t.Errorf("unique column has %d distinct of %d rows", len(seen), len(gt.Rows))
	}
}

// TestObviouslyFake: generated values are obviously synthetic — content realism
// is explicitly not attempted (RFC §13).
func TestObviouslyFake(t *testing.T) {
	f := oneTable("public.t", 50, map[string]fixture.Column{
		"email": {Type: "text", Nullable: false, Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact}, Format: "email"},
	})
	res, err := Generate(f, Options{Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, row := range res.Tables[0].Rows {
		s, _ := row[0].(string)
		if !strings.HasSuffix(s, "@example.invalid") || !strings.HasPrefix(s, "user_") {
			t.Errorf("email %q is not obviously fake (want user_N@example.invalid)", s)
		}
	}
}

// TestFanoutShape: a foreign key reproduces the fan-out DISTRIBUTION shape
// (p50/p95/max children per parent), not just the mean (RFC §6.6).
func TestFanoutShape(t *testing.T) {
	f := &fixture.Fixture{
		RowshapeFixture: fixture.FormatVersion,
		Meta:            fixture.Meta{Engine: fixture.Engine{Name: "postgres", Version: "16"}},
		Tables: map[string]fixture.Table{
			"public.users": {
				Rows: fixture.Fact[int64]{Value: 1000, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{
					"id": {Type: "bigint", Nullable: false, Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact}, Generated: "identity"},
				},
			},
			"public.orders": {
				Rows: fixture.Fact[int64]{Value: 8000, Confidence: fixture.Exact},
				Columns: map[string]fixture.Column{
					"id":      {Type: "bigint", Nullable: false, Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact}, Generated: "identity"},
					"user_id": {Type: "bigint", Nullable: false},
				},
				References: []fixture.Reference{
					{Column: "user_id", To: "public.users.id", OnDelete: "cascade",
						Fanout: &fixture.Fanout{Mean: 8, P50: 3, P95: 41, Max: 200, Confidence: fixture.Measured}},
				},
			},
		},
	}
	res, err := Generate(f, Options{Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Find orders and its user_id column.
	var orders GeneratedTable
	for _, t := range res.Tables {
		if t.Name == "public.orders" {
			orders = t
		}
	}
	fkCol := indexOf(orders.Columns, "user_id")
	perParent := map[int64]int64{}
	for _, row := range orders.Rows {
		pid := row[fkCol].(int64)
		perParent[pid]++
	}
	// Include parents with zero children (1000 parents, ids 1..1000).
	counts := make([]int64, 0, 1000)
	for pid := int64(1); pid <= 1000; pid++ {
		counts = append(counts, perParent[pid])
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i] < counts[j] })

	p50 := counts[len(counts)*50/100]
	p95 := counts[len(counts)*95/100]
	max := counts[len(counts)-1]
	t.Logf("fan-out: p50=%d p95=%d max=%d (target 3/41/200)", p50, p95, max)

	// The distribution must be heavy-tailed, not uniform: p95 far above p50 and a
	// long max. Exact quantiles need not match, but the SHAPE must.
	if !(p95 > p50*3) {
		t.Errorf("fan-out not heavy-tailed: p95=%d should be >> p50=%d", p95, p50)
	}
	if !(max > p95*2) {
		t.Errorf("fan-out lacks a long tail: max=%d should be >> p95=%d", max, p95)
	}
	// And the quantiles should be in the right ballpark of the target.
	if p95 < 20 || p95 > 80 {
		t.Errorf("p95=%d far from target 41", p95)
	}
	if max < 100 {
		t.Errorf("max=%d far below target 200", max)
	}
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

// TestForeignKeysPointAtRealParents: hydrate must not invent orphans.
//
// A real `pull` emits a range for every numeric column (RFC §6.2), so a real
// fixture's id column has one and numericInRange generates `min + (ordinal %
// span)` — for range {1, N} that is 1..N, not 0..N-1. parentIDValue used to
// return the ordinal, so children referenced 0..N-1 while parents were 1..N, and
// user_id=0 pointed at nothing.
//
// Both fixtures declare orphan_fraction {0, exact, via: constraint} — the FK is
// PROVEN to have no orphans. Hydrate inventing one means a migration adding
// `FOREIGN KEY (user_id) REFERENCES users(id)` fails on hydrated data and
// rowshape reports a FAIL it manufactured itself.
//
// The no-fan-out case is the one that pins it. With a fan-out fact the rescale in
// scaledTargetCounts can zero out the low quantiles, so parent ordinal 0 is never
// handed out and the off-by-one hides; the uniform spread (out[i] = i % parentN)
// always uses ordinal 0. An earlier version of this test only had the fan-out
// case and passed against the bug.
func TestForeignKeysPointAtRealParents(t *testing.T) {
	build := func(fo *fixture.Fanout) *fixture.Fixture {
		return &fixture.Fixture{
			Tables: map[string]fixture.Table{
				"public.users": {
					Rows: fixture.Fact[int64]{Value: 100},
					Columns: map[string]fixture.Column{
						// The shape a real pull emits: unique, and WITH a range.
						"id": {Type: "bigint", Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact},
							Range: &fixture.Range{Min: 1, Max: 100}},
					},
				},
				"public.orders": {
					Rows: fixture.Fact[int64]{Value: 400},
					Columns: map[string]fixture.Column{
						"id":      {Type: "bigint", Unique: &fixture.Fact[bool]{Value: true, Confidence: fixture.Exact}, Range: &fixture.Range{Min: 1, Max: 400}},
						"user_id": {Type: "bigint"},
					},
					References: []fixture.Reference{{
						Column: "user_id", To: "public.users.id",
						Fanout:         fo,
						OrphanFraction: &fixture.Fact[float64]{Value: 0, Confidence: fixture.Exact},
					}},
				},
			},
		}
	}

	cases := []struct {
		name string
		fo   *fixture.Fanout
	}{
		{"uniform spread (no fan-out fact)", nil},
		{"with a fan-out distribution", &fixture.Fanout{Mean: 4, P50: 2, P95: 8, Max: 40, Confidence: fixture.Measured}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Generate(build(tc.fo), Options{Seed: 42, Scale: 1})
			if err != nil {
				t.Fatal(err)
			}

			parentIDs := map[int64]bool{}
			var childFKs []int64
			for _, tb := range res.Tables {
				idx := map[string]int{}
				for i, c := range tb.Columns {
					idx[c] = i
				}
				for _, row := range tb.Rows {
					switch tb.Name {
					case "public.users":
						parentIDs[row[idx["id"]].(int64)] = true
					case "public.orders":
						childFKs = append(childFKs, row[idx["user_id"]].(int64))
					}
				}
			}
			if len(parentIDs) == 0 || len(childFKs) == 0 {
				t.Fatal("nothing generated; this case proves nothing")
			}

			var orphans []int64
			for _, fk := range childFKs {
				if !parentIDs[fk] {
					orphans = append(orphans, fk)
				}
			}
			if len(orphans) > 0 {
				t.Errorf("%d of %d foreign keys reference a parent that does not exist (e.g. %v) — the "+
					"fixture declares orphan_fraction 0 (exact), so hydrate invented the exact condition it "+
					"proves absent. `ADD FOREIGN KEY` would fail on this data and rowshape would report a "+
					"FAIL it manufactured.", len(orphans), len(childFKs), orphans[:minInt(3, len(orphans))])
			}
		})
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
