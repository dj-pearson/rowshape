package fixture

import (
	"crypto/sha256"
	"encoding/hex"
)

// DigestPrefix is the algorithm prefix on every fixture digest (RFC §11).
const DigestPrefix = "sha256:"

// Digest computes meta.digest: SHA-256 over the canonical form (RFC §11),
// returned as "sha256:" + lowercase hex. Two fixtures with the same digest are
// interchangeable; the digest is what a verdict cites and what an attestation
// binds to.
func Digest(f *Fixture) (string, error) {
	canon, err := Canonical(f)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return DigestPrefix + hex.EncodeToString(sum[:]), nil
}

// SetDigest computes the fixture's digest and stores it in meta.digest. Because
// meta.digest is excluded from the canonical form (RFC §11), setting it does not
// change the digest, so SetDigest is idempotent.
func (f *Fixture) SetDigest() error {
	d, err := Digest(f)
	if err != nil {
		return err
	}
	f.Meta.Digest = d
	return nil
}
