// Package hydrate is rowshape's own deterministic synthesis engine (PRD §16 —
// not gofakeit). Given a fixture and a seed it reconstructs a disposable
// database's worth of rows whose SHAPE matches production — row counts, null
// fractions, cardinality, fan-out — while its CONTENT is obviously fake
// (RFC §13). The same fixture, seed, and engine version produce identical output
// on any platform (RFC §10, INV-DETERMINISM).
package hydrate

import (
	"crypto/sha256"
	"encoding/binary"
)

// EngineVersion participates in the determinism contract (RFC §10): changing how
// values are generated is a breaking change to the hydrator, not to the fixture,
// and is recorded by bumping this.
const EngineVersion = "1"

// rng is a small, fully self-contained splitmix64 generator. Using our own
// algorithm (rather than math/rand, whose stream is not contractually stable
// across Go versions) keeps determinism under our control and identical across
// platforms.
type rng struct {
	state uint64
}

// next advances the splitmix64 state and returns the next 64-bit value.
func (r *rng) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// float64 returns a deterministic value in [0, 1).
func (r *rng) float64() float64 {
	// Top 53 bits give a uniformly-distributed double in [0, 1).
	return float64(r.next()>>11) / float64(uint64(1)<<53)
}

// intn returns a deterministic value in [0, n). It returns 0 for n <= 0.
func (r *rng) intn(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return int64(r.next() % uint64(n))
}

// cellRNG returns the generator for a single cell, seeded by
// hash(seed, table, column, row_ordinal) (RFC §10). Because a cell's stream
// depends only on its own coordinates, adding a column never reshuffles another
// column's values, and increasing --scale only appends rows — the retained
// prefix is byte-stable.
func cellRNG(seed int64, table, column string, ordinal int64) *rng {
	return &rng{state: cellSeed(seed, table, column, ordinal)}
}

// cellSeed derives the 64-bit per-cell seed. SHA-256 over an unambiguously
// framed encoding of the inputs is portable and collision-resistant; length
// prefixes prevent (table, column) boundary ambiguity.
func cellSeed(seed int64, table, column string, ordinal int64) uint64 {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(seed))
	h.Write(buf[:])
	writeFramed(h, table)
	writeFramed(h, column)
	binary.BigEndian.PutUint64(buf[:], uint64(ordinal))
	h.Write(buf[:])
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// writeFramed writes a length-prefixed string so concatenations are unambiguous.
func writeFramed(h interface{ Write([]byte) (int, error) }, s string) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
	h.Write(buf[:])
	h.Write([]byte(s))
}
