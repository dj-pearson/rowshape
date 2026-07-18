package fixture

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden files instead of comparing against them:
//
//	go test ./internal/fixture/... -update
var update = flag.Bool("update", false, "regenerate golden files")

// loadExample parses testdata/example.yaml, the RFC §5/§6 example fixture.
func loadExample(t *testing.T) *Fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "example.yaml"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}
	return f
}

// TestCanonicalGolden pins the exact canonical bytes (RFC §11). Regenerate with
// -update after an intentional format change.
func TestCanonicalGolden(t *testing.T) {
	f := loadExample(t)
	got, err := Canonical(f)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	goldenPath := filepath.Join("testdata", "example.canonical.golden")
	if *update {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("canonical form does not match golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestDigestGolden pins the digest of the example fixture (RFC §11).
func TestDigestGolden(t *testing.T) {
	f := loadExample(t)
	got, err := Digest(f)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	goldenPath := filepath.Join("testdata", "example.digest.golden")
	if *update {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write digest golden: %v", err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read digest golden (run with -update first): %v", err)
	}
	if got != string(want) {
		t.Errorf("digest = %q, want %q", got, string(want))
	}
	if !strings.HasPrefix(got, DigestPrefix) {
		t.Errorf("digest missing %q prefix: %q", DigestPrefix, got)
	}
}

// TestCanonicalDeterministic: two canonicalizations of an equal fixture are
// byte-identical, regardless of Go map iteration order (INV-DETERMINISM). Parsing
// the same document twice yields independently-built maps; their canonical forms
// must match every time.
// The tests in this file are the enforcement of INV-ONE-CANONICAL-FORM (RFC §11,
// PRD §9): the canonical form and digest have exactly ONE implementation, and it
// is this one. The phase-5 cloud API imports this package rather than
// recomputing canonicalization, because a second implementation drifts and
// attestations then stop verifying with no visible cause.
//
// The invariant is named here deliberately. A CR-loop audit grepped every
// INV- id across the test tree to find promises nothing checks, and this
// invariant read as unenforced purely because no test mentioned it by name —
// traceability that only exists by convention is traceability nobody can audit.
func TestCanonicalDeterministic(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "example.yaml"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var last []byte
	for i := 0; i < 50; i++ {
		f, err := Parse(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		got, err := Canonical(f)
		if err != nil {
			t.Fatalf("Canonical: %v", err)
		}
		if last != nil && !bytes.Equal(got, last) {
			t.Fatalf("canonical form not deterministic across runs (iteration %d)", i)
		}
		last = got
	}
}

// TestCanonicalProperties asserts the structural rules of RFC §11 on the output.
func TestCanonicalProperties(t *testing.T) {
	f := loadExample(t)
	got, err := Canonical(f)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	text := string(got)

	// "\n" line endings only — no CR.
	if strings.Contains(text, "\r") {
		t.Errorf("canonical form contains a carriage return")
	}
	// No YAML anchors or aliases.
	if strings.Contains(text, " &") || strings.Contains(text, " *") {
		t.Errorf("canonical form appears to contain an anchor or alias:\n%s", text)
	}
	// meta.digest and meta.generated_at are excluded from the canonical form.
	if strings.Contains(text, "digest:") {
		t.Errorf("canonical form must exclude meta.digest:\n%s", text)
	}
	if strings.Contains(text, "generated_at:") {
		t.Errorf("canonical form must exclude meta.generated_at:\n%s", text)
	}
	// Two-space indent: meta's children sit at exactly two spaces. The document
	// begins with "meta:" (top-level keys are sorted), so check from the start.
	if !strings.HasPrefix(text, "meta:\n  ") {
		t.Errorf("expected two-space indent under meta:\n%s", text)
	}
	// Top-level keys are lexicographically sorted: meta before rowshape_fixture
	// before tables. Only top-level keys sit in column 0 ("\nkey:" or start).
	iMeta := topLevelKeyIndex(text, "meta")
	iRF := topLevelKeyIndex(text, "rowshape_fixture")
	iTables := topLevelKeyIndex(text, "tables")
	if !(iMeta >= 0 && iMeta < iRF && iRF < iTables) {
		t.Errorf("top-level keys not lexicographically sorted (meta<rowshape_fixture<tables): meta=%d rf=%d tables=%d\n%s", iMeta, iRF, iTables, text)
	}
}

// topLevelKeyIndex returns the byte offset of a top-level (column-0) key, or -1.
func topLevelKeyIndex(text, key string) int {
	if strings.HasPrefix(text, key+":") {
		return 0
	}
	if i := strings.Index(text, "\n"+key+":"); i >= 0 {
		return i
	}
	return -1
}

// TestFormatFloat6 checks the 6-significant-figure rule (RFC §11).
func TestFormatFloat6(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.032, "0.032"},
		{24.1, "24.1"},
		{82.3, "82.3"},
		{1.0 / 3.0, "0.333333"},
		{0.123456789, "0.123457"},
		{1234567.0, "1.23457e+06"},
	}
	for _, tc := range cases {
		if got := formatFloat6(tc.in); got != tc.want {
			t.Errorf("formatFloat6(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDigestExcludesVolatileFields: changing meta.digest or meta.generated_at
// must not change the digest (RFC §11 excludes both).
func TestDigestExcludesVolatileFields(t *testing.T) {
	f := loadExample(t)
	base, err := Digest(f)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}

	f.Meta.GeneratedAt = "1999-01-01T00:00:00Z"
	f.Meta.Digest = "sha256:completely-different"
	after, err := Digest(f)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if base != after {
		t.Errorf("digest changed when only excluded fields changed: %q vs %q", base, after)
	}

	// Changing a real fact DOES change the digest.
	users := f.Tables["public.users"]
	users.Rows.Value = 999
	f.Tables["public.users"] = users
	changed, err := Digest(f)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if changed == base {
		t.Errorf("digest unchanged after a real fact changed")
	}
}

// TestSetDigestIdempotent: SetDigest is idempotent because meta.digest is
// excluded from the hashed form (RFC §11).
func TestSetDigestIdempotent(t *testing.T) {
	f := loadExample(t)
	if err := f.SetDigest(); err != nil {
		t.Fatalf("SetDigest: %v", err)
	}
	first := f.Meta.Digest
	if err := f.SetDigest(); err != nil {
		t.Fatalf("SetDigest: %v", err)
	}
	if f.Meta.Digest != first {
		t.Errorf("SetDigest not idempotent: %q then %q", first, f.Meta.Digest)
	}
}
