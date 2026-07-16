// Package hll is a client-side HyperLogLog for estimating a column's distinct
// count without retaining any values (RFC §7.3). The emitter streams a column
// with a server-side cursor, hashes each value into the sketch, and discards it:
// bounded memory (a fixed 16KB of registers), ~1% error, and — crucially — NO
// server extension. postgresql-hll is unavailable on most managed Postgres, so
// depending on it would gate the feature behind exactly the infrastructure that
// most needs it.
package hll

import (
	"math"
	"math/bits"
)

// Precision is the HLL precision p (RFC §14.4 leaning): 2^14 registers, ~16KB of
// state, and a relative standard error of 1.04/sqrt(2^p) ≈ 0.8%.
const Precision = 14

const (
	registers = 1 << Precision     // m = 16384
	maxRank   = 64 - Precision + 1 // largest rank a register can hold (51)
)

// Sketch is a HyperLogLog sketch. Its size is fixed regardless of how many
// values are added, so memory never grows with cardinality.
type Sketch struct {
	reg [registers]uint8
}

// New returns an empty sketch.
func New() *Sketch { return &Sketch{} }

// Add folds one value's hash into the sketch. The value itself is never stored —
// only the running per-register maximum rank (RFC §7.3, INV-NO-ROWS).
func (s *Sketch) Add(value []byte) {
	s.AddHash(hash64(value))
}

// AddString is a convenience for text values.
func (s *Sketch) AddString(v string) { s.AddHash(hash64([]byte(v))) }

// AddHash folds a precomputed 64-bit hash into the sketch. The top Precision bits
// select the register; the rank is the position of the leftmost set bit in the
// remaining bits.
func (s *Sketch) AddHash(x uint64) {
	idx := x >> (64 - Precision)
	rest := x << Precision // shift the register bits out; low bits become zero
	rank := uint8(bits.LeadingZeros64(rest)) + 1
	if rank > maxRank {
		rank = maxRank
	}
	if rank > s.reg[idx] {
		s.reg[idx] = rank
	}
}

// Count returns the estimated number of distinct values. It uses the standard
// HLL estimator with linear-counting correction for small cardinalities; the
// 64-bit hash makes the large-range correction unnecessary.
func (s *Sketch) Count() uint64 {
	m := float64(registers)
	sum := 0.0
	zeros := 0
	for _, r := range s.reg {
		sum += 1.0 / float64(uint64(1)<<r)
		if r == 0 {
			zeros++
		}
	}
	est := alpha() * m * m / sum

	// Linear counting is more accurate when many registers are still empty.
	if est <= 2.5*m && zeros > 0 {
		est = m * math.Log(m/float64(zeros))
	}
	return uint64(est + 0.5)
}

// RelativeError returns the sketch's relative standard error (RFC §7.4 publishes
// this in distinct.error). It depends only on the precision.
func RelativeError() float64 {
	return 1.04 / math.Sqrt(float64(registers))
}

// SizeBytes is the sketch's fixed memory footprint.
func SizeBytes() int { return registers }

// alpha is the HLL bias-correction constant for this register count.
func alpha() float64 {
	switch registers {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/float64(registers))
	}
}

// hash64 is a well-distributed 64-bit hash: FNV-1a for the byte fold, then a
// splitmix64 finalizer for strong avalanche (HLL accuracy depends on the hash
// behaving like a random oracle).
func hash64(b []byte) uint64 {
	var h uint64 = 14695981039346656037
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return h
}
