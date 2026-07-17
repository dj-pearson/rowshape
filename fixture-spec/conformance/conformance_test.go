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

// TestCheckEmitterYAMLIsTheThirdPartyEntryPoint: the suite's stated purpose is
// that a third party can hold their own emitter to the spec (PRD §3, §16 — the
// strategic value of the format is that anyone can emit it).
//
// Every exported check took *fixture.Fixture, a type in rowshape's internal
// tree. Go forbids importing .../internal/... across module boundaries, so no
// outside module could name the argument, let alone construct one: the whole
// surface was uncallable from anywhere but this repository, while the package
// doc said otherwise. An emitter has bytes. This asserts bytes are enough.
func TestCheckEmitterYAMLIsTheThirdPartyEntryPoint(t *testing.T) {
	// Conformant bytes -> no violations. A third party emits this and learns
	// their output is conformant, without importing anything internal.
	good, err := os.ReadFile(filepath.Join("fixtures", "valid", "basic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := CheckEmitterYAML(good)
	if err != nil {
		t.Fatalf("a valid reference fixture must parse: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("valid fixture reported violations: %v", vs)
	}

	// Non-conformant bytes -> the violation, named. `range` on a text column is
	// an RFC §6.1 MUST NOT: min/max would be real production values.
	bad, err := os.ReadFile(filepath.Join("fixtures", "invalid", "range-on-text.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	vs, err = CheckEmitterYAML(bad)
	if err != nil {
		t.Fatalf("the fixture parses; it is the CONTENT that violates: %v", err)
	}
	if len(vs) == 0 {
		t.Error("range on a text column must be reported (RFC §6.1)")
	}

	// Unparseable bytes are an error, not a verdict: we cannot say whether an
	// emitter conforms when we cannot read what it emitted.
	if _, err := CheckEmitterYAML([]byte("{{{ not yaml")); err == nil {
		t.Error("unreadable bytes must error rather than report zero violations — " +
			"silence would read as 'conformant'")
	}
}
