// Package fixture defines the rowshape fixture data model, canonical form, and
// digest.
//
// This is one of the two package boundaries — together with internal/verdict —
// reserved so the phase-5 cloud API can import it UNCHANGED. Canonical form and
// digesting MUST have exactly ONE implementation, in Go, shared by CLI and API
// (INV-ONE-CANONICAL-FORM, PRD §9, RFC §11).
//
// NOTE: This is the phase-0 scaffold. The full RFC §5/§6 data model lands in
// P1-T1, canonical form + digest in P1-T2. Only the format major version is
// pinned here so imports are stable from day one.
package fixture

// FormatVersion is the declared major version of the Rowshape Fixture Spec
// (RFC-0001). A fixture whose rowshape_fixture major differs from this is
// refused (RFC §12).
const FormatVersion = "1"
