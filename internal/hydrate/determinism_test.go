package hydrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"math/rand"
	"runtime"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
)

// CR-T14. INV-DETERMINISM promises the same fixture, seed and engine version
// produce byte-identical output on ANY platform. Three expressions on the
// synthesis path had the shape `x + y*z`, which the Go spec explicitly permits
// an implementation to fuse into a single FMA with one rounding instead of two —
// and the compiler DOES fuse it on arm64/ppc64/s390x while amd64 does not.
//
// The promise was therefore architecture-dependent, with no test that could
// notice. These two tests attack it from both ends: one proves the hazard is
// real for the values hydrate actually produces, the other pins the output.

// goldenDigest is the SHA-256 of the SQL emitted for determinismFixture at
// seed 42. It must be identical on every architecture. If this changes, either
// the synthesis engine changed deliberately (regenerate it, and say why in the
// commit) or something non-deterministic crept in (do NOT regenerate it).
const goldenDigest = "f44c84f504af15fdd0b0fa3c0bdd27119bd29f2253d148faed02b64d6cff3d64"

func determinismFixture(t *testing.T) *fixture.Fixture {
	t.Helper()
	// A histogram-driven column is the one that routes through the fused
	// expression, and wide bounds are where fusion actually changes the int64.
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: determinism, engine: {name: postgres, version: "16"}}
tables:
  public.events:
    rows: {value: 500, confidence: exact}
    columns:
      id: {type: bigint, nullable: false, unique: {value: true, confidence: exact, via: constraint}}
      amount: {type: bigint, nullable: false, histogram: {buckets: 4, bounds: [-37757646228629, 100, 79242707612419, 412345678901234, 987654321098765]}}
      note: {type: text, nullable: true, null_fraction: {value: 0.1, confidence: exact, via: scan}}
`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func hydrateDigest(t *testing.T, f *fixture.Fixture) string {
	t.Helper()
	res, err := Generate(f, Options{Seed: 42, Scale: 1.0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteSQL(&buf, res); err != nil {
		t.Fatalf("write sql: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// TestHydrateOutputDigestIsStable pins the synthesized SQL. Run on more than one
// GOARCH (see .github/workflows/ci.yml), it is what actually enforces
// INV-DETERMINISM across architectures rather than asserting it.
func TestHydrateOutputDigestIsStable(t *testing.T) {
	f := determinismFixture(t)
	got := hydrateDigest(t, f)

	// Same input twice in one process must agree, or the golden is meaningless.
	if again := hydrateDigest(t, determinismFixture(t)); again != got {
		t.Fatalf("hydrate is not deterministic within a single process: %s vs %s", got, again)
	}

	if goldenDigest == "PLACEHOLDER" {
		t.Fatalf("golden digest not set; on %s/%s the digest is %s",
			runtime.GOOS, runtime.GOARCH, got)
	}
	if got != goldenDigest {
		t.Errorf("hydrate digest on %s/%s = %s, want %s.\n"+
			"Either the synthesis engine changed deliberately (regenerate the golden and say why), "+
			"or output has become platform-dependent (do NOT regenerate it) — see D-011.",
			runtime.GOOS, runtime.GOARCH, got, goldenDigest)
	}
}

// TestFMAFusionWouldChangeSynthesizedValues proves the hazard is real for the
// values hydrate actually produces, which is what justifies the float64()
// conversions on the synthesis path.
//
// It cannot ask the compiler to fuse on demand, so it simulates a fusing backend
// with math.FMA(r, span, lo) — exactly the instruction arm64 emits for
// `lo + r*span` — and compares against the explicitly-rounded form the engine
// now uses. If this test ever reports ZERO divergence, the conversions could be
// dropped; while it reports divergence, removing them makes output
// architecture-dependent.
func TestFMAFusionWouldChangeSynthesizedValues(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 200000
	var floatDiff, intDiff int
	for i := 0; i < n; i++ {
		lo := (rng.Float64() - 0.5) * math.Pow(10, float64(rng.Intn(16)))
		span := rng.Float64() * math.Pow(10, float64(rng.Intn(16)))
		r := rng.Float64()

		unfused := lo + float64(r*span) // what the engine computes now
		fused := math.FMA(r, span, lo)  // what a fusing backend may compute
		if unfused != fused {
			floatDiff++
		}
		if int64(unfused) != int64(fused) {
			intDiff++ // the value that would actually reach the SQL
		}
	}
	t.Logf("fusing would change %d/%d floats (%.2f%%) and %d/%d emitted int64 values (%.4f%%)",
		floatDiff, n, 100*float64(floatDiff)/n, intDiff, n, 100*float64(intDiff)/n)

	if intDiff == 0 {
		t.Errorf("expected FMA fusion to change at least one emitted value; if this is genuinely 0, "+
			"the float64() conversions in sampleHistogram/lerp/bodyQuantile are unnecessary and this "+
			"test should be revisited (checked %d samples)", n)
	}
}

// TestLerpIsFusionStable pins the helper directly: the explicitly-rounded form
// must equal the two-rounding result, not the fused one.
func TestLerpIsFusionStable(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 100000; i++ {
		a := (rng.Float64() - 0.5) * 1e12
		b := (rng.Float64() - 0.5) * 1e12
		tt := rng.Float64()
		if got, want := lerp(a, b, tt), a+float64((b-a)*tt); got != want {
			t.Fatalf("lerp(%v,%v,%v) = %v, want the explicitly-rounded %v", a, b, tt, got, want)
		}
	}
}
