package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

func loadFixture(t *testing.T, path string) *fixture.Fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, err := fixture.Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

func fixturesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths
}

// TestValidFixturesAreConformant: every reference fixture in fixtures/valid
// passes the emitter MUSTs — this is what rowshape's own `pull` output must look
// like (RFC §13).
func TestValidFixturesAreConformant(t *testing.T) {
	paths := fixturesIn(t, "fixtures/valid")
	if len(paths) == 0 {
		t.Fatal("no valid reference fixtures")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			if vs := CheckEmitter(loadFixture(t, p)); len(vs) != 0 {
				for _, v := range vs {
					t.Errorf("unexpected violation: %s", v)
				}
			}
		})
	}
}

// TestNonConformantFixturesAreRejected: the suite REJECTS deliberately broken
// fixtures — a range on a text column (§6.1) and uniqueness inferred from a
// sample (§7.2). A suite that cannot reject these proves nothing.
func TestNonConformantFixturesAreRejected(t *testing.T) {
	cases := []struct {
		file     string
		wantRule string
	}{
		{"fixtures/invalid/range-on-text.yaml", "§6.1 no range on text"},
		{"fixtures/invalid/unique-from-sample.yaml", "§7.2 uniqueness never from a sample"},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.file), func(t *testing.T) {
			vs := CheckEmitter(loadFixture(t, c.file))
			if len(vs) == 0 {
				t.Fatalf("expected a conformance violation, got none")
			}
			found := false
			for _, v := range vs {
				if v.Rule == c.wantRule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected a %q violation, got %v", c.wantRule, vs)
			}
		})
	}
}

// TestRowshapeHydratorIsConformant: rowshape's own hydrator honors null_fraction
// within ±0.5%, keeps a unique column unique, and is deterministic (RFC §13, §10).
func TestRowshapeHydratorIsConformant(t *testing.T) {
	f := loadFixture(t, "fixtures/valid/hydratable.yaml")
	if vs := CheckHydrator(f, 42, 8000); len(vs) != 0 {
		for _, v := range vs {
			t.Errorf("hydrator violation: %s", v)
		}
	}
}

// TestRowshapeValidatorIsConformant: rowshape's verdict engine caps per §7.4 and
// reports durations as buckets per §9.2.
func TestRowshapeValidatorIsConformant(t *testing.T) {
	if vs := CheckValidator(); len(vs) != 0 {
		for _, v := range vs {
			t.Errorf("validator violation: %s", v)
		}
	}
}

// TestSchemaIsPublished: the JSON Schema asset exists, is valid JSON, pins the
// format version, and stays consistent with the fixture format constants so it
// cannot silently rot (a full JSON-Schema validation of the reference fixtures
// runs in CI via a standard validator; see fixture-spec/conformance/README.md).
func TestSchemaIsPublished(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "schema", "rowshape.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"$schema"`, `"$id"`, `"rowshape_fixture"`, `"const": "` + fixture.FormatVersion + `"`, string(fixture.Exact), string(fixture.Measured), string(fixture.Estimated), string(fixture.Declared)} {
		if !strings.Contains(s, want) {
			t.Errorf("published schema is missing %q", want)
		}
	}
}
