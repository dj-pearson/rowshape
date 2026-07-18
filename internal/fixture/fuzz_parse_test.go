package fixture

import (
	"strings"
	"testing"
)

// Parse is the single door for every committed fixture — it drives verdicts,
// hydration, and the MCP tool output. It had no direct malformed-input test and
// the repo had no fuzz targets. A panic or a hang here is reachable from any
// untrusted rowshape.yaml. docs/TESTING-GAPS.md items 2 and 6.
//
// Run with:  go test ./internal/fixture/ -run x -fuzz FuzzParseFixture
func FuzzParseFixture(f *testing.F) {
	seeds := []string{
		"",
		"rowshape_fixture: \"1\"\n",
		"rowshape_fixture: \"1\"\nmeta: {id: t, engine: {name: postgres, version: \"16\"}}\ntables: {}\n",
		`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 10, confidence: exact}
    columns:
      email: {type: text, nullable: true, distinct: 5}
`,
		// bare-scalar fact shorthand
		"rowshape_fixture: \"1\"\nmeta: {engine: {name: postgres, version: \"16\"}}\ntables:\n  t:\n    rows: 5\n",
		// adversarial / malformed — must not panic:
		"{{{ not yaml",
		"rowshape_fixture: \"1\"\ntables: [not, a, map]",
		"rowshape_fixture: \"1\"\ntables:\n  t:\n    rows: {value: notanint}",
		"rowshape_fixture: \"9999\"\n", // unknown major
		strings.Repeat("a: &a [", 20),  // nesting / alias-ish
		"\x00\x01\x02",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// INVARIANT 1: Parse never panics, whatever the bytes are.
		fx, err := Parse(data)
		if err != nil {
			return // a clean error is a fine outcome
		}
		if fx == nil {
			t.Fatal("Parse returned nil fixture and nil error")
		}

		// INVARIANT 2: Digest never panics on anything Parse accepted.
		if _, err := Digest(fx); err != nil {
			return
		}

		// Emit normalizes (it fills defaults like profile.mode), so a minimal
		// hand-written fixture and its emitted form legitimately differ. What
		// must hold is on the EMITTED form:
		out1, err := Emit(fx)
		if err != nil {
			return
		}

		// INVARIANT 3: Emit always produces a fixture that passes its own digest
		// check. If ParseVerified rejects Emit's own output, the integrity gate a
		// wrong PASS depends on is broken.
		f1, err := ParseVerified(out1)
		if err != nil {
			t.Fatalf("ParseVerified rejected Emit's own output: %v\nemitted:\n%s", err, out1)
		}

		// INVARIANT 4: Emit is idempotent on an already-emitted fixture — the
		// normal form is a fixed point, so a fixture cannot keep changing meaning
		// each time it passes through the tool.
		d1, err := Digest(f1)
		if err != nil {
			t.Fatalf("Digest of emitted fixture failed: %v", err)
		}
		out2, err := Emit(f1)
		if err != nil {
			t.Fatalf("re-emitting a normalized fixture failed: %v", err)
		}
		f2, err := Parse(out2)
		if err != nil {
			t.Fatalf("re-parsing a normalized fixture failed: %v", err)
		}
		d2, err := Digest(f2)
		if err != nil {
			t.Fatalf("Digest of twice-emitted fixture failed: %v", err)
		}
		if d1 != d2 {
			t.Fatalf("Emit is not idempotent:\n  once  %s\n  twice %s\nemitted:\n%s", d1, d2, out2)
		}
	})
}

// TestParseMalformedInputs pins the error surface of Parse for the malformed
// shapes fuzzing is most likely to hit — none may panic; each either errors
// cleanly or yields a sane, weakest-reading value.
func TestParseMalformedInputs(t *testing.T) {
	t.Run("syntactically broken YAML errors", func(t *testing.T) {
		if _, err := Parse([]byte("{{{ not yaml")); err == nil {
			t.Error("broken YAML should be a parse error, not silent success")
		}
	})

	t.Run("wrong scalar type in a fact errors", func(t *testing.T) {
		_, err := Parse([]byte(`rowshape_fixture: "1"
meta: {engine: {name: postgres, version: "16"}}
tables:
  t:
    rows: {value: not_a_number}
`))
		if err == nil {
			t.Error("a non-numeric int fact value should error, not decode to zero silently")
		}
	})

	t.Run("bare scalar fact takes the weakest confidence, never a stronger one", func(t *testing.T) {
		f, err := Parse([]byte(`rowshape_fixture: "1"
meta: {engine: {name: postgres, version: "16"}}
tables:
  t:
    rows: 5
`))
		if err != nil {
			t.Fatalf("bare-scalar shorthand should parse: %v", err)
		}
		got := f.Tables["t"].Rows
		if got.Value != 5 {
			t.Errorf("rows value = %d, want 5", got.Value)
		}
		if got.Confidence != Estimated {
			t.Errorf("bare scalar confidence = %q, want %q (the weakest reading — INV-CONFIDENCE-CAPPING)", got.Confidence, Estimated)
		}
	})

	t.Run("duplicate mapping keys do not panic", func(t *testing.T) {
		// yaml.v3 rejects duplicate keys; the contract we care about is only that
		// Parse returns (not panics) either way.
		_, _ = Parse([]byte(`rowshape_fixture: "1"
meta: {engine: {name: postgres, version: "16"}}
tables:
  t:
    rows: 1
    rows: 2
`))
	})

	t.Run("unknown format major is refused", func(t *testing.T) {
		if _, err := Parse([]byte(`rowshape_fixture: "9999"` + "\n")); err == nil {
			t.Error("an unknown fixture format major should be refused (RFC §12)")
		}
	})
}
