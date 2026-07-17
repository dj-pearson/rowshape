package fixture

import (
	"fmt"
	"strings"
	"testing"
)

func sampleFixture() *Fixture {
	nf := &Fact[float64]{Value: 0.032, Confidence: Estimated, Via: "pg_stats"}
	uniq := &Fact[bool]{Value: true, Confidence: Exact, Via: "constraint"}
	return &Fixture{
		RowshapeFixture: FormatVersion,
		Meta: Meta{
			ID:          "prod@2026-07-14",
			GeneratedAt: "2026-07-14T09:12:44Z",
			Generator:   "rowshape/1.0.0",
			Engine:      Engine{Name: "postgres", Version: "16.3"},
			Privacy:     "standard",
			Source:      "sha256:41b0",
			Profile:     Profile{Mode: "fast", ScannedAt: "2026-07-14T09:12:44Z"},
		},
		Tables: map[string]Table{
			"public.users": {
				Rows: Fact[int64]{Value: 1200000, Confidence: Exact},
				Columns: map[string]Column{
					"id":    {Type: "bigint", Nullable: false, Unique: uniq},
					"email": {Type: "text", Nullable: true, NullFraction: nf, Format: "email"},
				},
			},
		},
	}
}

// TestEmitRoundTrips: emitted bytes parse back through the P1-T1 data model.
func TestEmitRoundTrips(t *testing.T) {
	f := sampleFixture()
	out, err := Emit(f)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse emitted fixture: %v\n%s", err, out)
	}
	if back.Meta.Engine.Version != "16.3" {
		t.Errorf("engine.version lost in round-trip: %+v", back.Meta.Engine)
	}
	if _, ok := back.Tables["public.users"]; !ok {
		t.Errorf("table lost in round-trip")
	}
	// The emitted profile block always carries an explicit escalated list.
	if back.Meta.Profile.Escalated == nil {
		t.Errorf("emitted profile must include an escalated list")
	}
}

// TestEmitRequiresEngineVersion: engine.version is mandatory (RFC §9.1).
func TestEmitRequiresEngineVersion(t *testing.T) {
	f := sampleFixture()
	f.Meta.Engine.Version = ""
	if _, err := Emit(f); err == nil {
		t.Errorf("Emit must refuse a fixture with no engine.version (RFC §9.1)")
	}

	f2 := sampleFixture()
	f2.Meta.Engine.Name = ""
	if _, err := Emit(f2); err == nil {
		t.Errorf("Emit must refuse a fixture with no engine.name")
	}
}

// TestEmitDigestMatchesFile: the stored meta.digest matches a fresh
// recomputation over the emitted file (RFC §11).
func TestEmitDigestMatchesFile(t *testing.T) {
	f := sampleFixture()
	out, err := Emit(f)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	ok, stored, recomputed, err := VerifyDigest(out)
	if err != nil {
		t.Fatalf("VerifyDigest: %v", err)
	}
	if !ok {
		t.Errorf("digest mismatch: stored %q, recomputed %q", stored, recomputed)
	}
	if !strings.HasPrefix(stored, DigestPrefix) {
		t.Errorf("stored digest missing prefix: %q", stored)
	}

	// Tampering with a fact but keeping the old digest is detected.
	tampered := strings.Replace(string(out), "value: 1200000", "value: 999", 1)
	ok, _, _, err = VerifyDigest([]byte(tampered))
	if err != nil {
		t.Fatalf("VerifyDigest(tampered): %v", err)
	}
	if ok {
		t.Errorf("VerifyDigest should reject a fixture whose data was edited without redigest")
	}
}

// TestEmitTwoSpaceIndent: the output is two-space indented for clean diffs.
func TestEmitTwoSpaceIndent(t *testing.T) {
	out, err := Emit(sampleFixture())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "\nmeta:\n  id:") {
		t.Errorf("expected two-space indent under meta:\n%s", text)
	}
	if strings.Contains(text, "\r") {
		t.Errorf("emit must use \\n line endings")
	}
}

// TestEmitSizeUnder100KB: a 200-table schema fits under 100KB (RFC §3.3).
func TestEmitSizeUnder100KB(t *testing.T) {
	f := &Fixture{
		RowshapeFixture: FormatVersion,
		Meta: Meta{
			ID:      "big@2026-07-14",
			Engine:  Engine{Name: "postgres", Version: "16.3"},
			Privacy: "standard",
			Profile: Profile{Mode: "fast"},
		},
		Tables: map[string]Table{},
	}
	// A realistic 200-table schema has a heavy tail of small join/lookup tables
	// and fewer wide tables. Column counts cycle through this distribution
	// (average ~6). Facts land at estimated (bare) as fast-mode profiling emits.
	colCounts := []int{2, 2, 2, 3, 3, 3, 4, 4, 5, 2}
	uniqInt := func() *Fact[int64] { return &Fact[int64]{Value: 100000, Confidence: Estimated} }
	nf := func() *Fact[float64] { return &Fact[float64]{Value: 0.01, Confidence: Estimated} }
	mn, mx := int64(3), int64(64)
	mean := 24.0
	pool := func(idx int) (string, Column) {
		switch idx {
		case 0:
			return "id", Column{Type: "bigint", Nullable: false, Distinct: uniqInt(), Unique: &Fact[bool]{Value: true, Confidence: Exact, Via: "constraint"}, Generated: "identity"}
		case 1:
			return "owner_id", Column{Type: "bigint", Nullable: false, Distinct: &Fact[int64]{Value: 5000, Confidence: Estimated}}
		case 2:
			return "created_at", Column{Type: "timestamp with time zone", Nullable: false, Distinct: uniqInt()}
		case 3:
			return "status", Column{Type: "text", Nullable: false, Distinct: &Fact[int64]{Value: 4, Confidence: Estimated}, Format: "enum_like", Length: &Length{Min: &mn, Max: &mx, Mean: &mean}}
		case 4:
			return "name", Column{Type: "text", Nullable: true, NullFraction: nf(), Distinct: uniqInt(), Format: "free_text", Length: &Length{Min: &mn, Max: &mx, Mean: &mean}}
		case 5:
			return "amount", Column{Type: "numeric(10,2)", Nullable: true, NullFraction: nf(), Distinct: uniqInt(), Range: &Range{Min: 0, Max: 9999, Mean: &mean}}
		default:
			// Most columns in a wide table are simple scalars (fks, ints, flags),
			// not richly-profiled text.
			return fmt.Sprintf("attr_%02d", idx), Column{Type: "integer", Nullable: false, Distinct: uniqInt()}
		}
	}
	for i := 0; i < 200; i++ {
		n := colCounts[i%len(colCounts)]
		cols := map[string]Column{}
		for c := 0; c < n; c++ {
			name, col := pool(c)
			cols[name] = col
		}
		tname := fmt.Sprintf("public.table_%03d", i)
		tbl := Table{
			Rows:    Fact[int64]{Value: 100000, Confidence: Estimated},
			Bytes:   8000000,
			Columns: cols,
			Constraints: []Constraint{
				{Name: fmt.Sprintf("table_%03d_pkey", i), Kind: "primary_key", Columns: []string{"id"}},
			},
		}
		// About half the tables carry an FK + supporting index.
		if i%3 == 0 && n > 1 {
			tbl.Indexes = []Index{{Name: fmt.Sprintf("table_%03d_owner_idx", i), Method: "btree", Columns: []string{"owner_id"}, Bytes: 400000}}
			tbl.References = []Reference{{Column: "owner_id", To: "public.users.id", OnDelete: "cascade"}}
		}
		f.Tables[tname] = tbl
	}
	out, err := Emit(f)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	const limit = 100 * 1024
	if len(out) >= limit {
		t.Errorf("200-table fixture is %d bytes, want < %d (RFC §3.3)", len(out), limit)
	}
	t.Logf("200-table fixture size: %d bytes", len(out))
}
