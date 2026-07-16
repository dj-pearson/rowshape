// Package verdict defines the public verdict contract for rowshape.
//
// The verdict contract (fields, finding codes, exit codes) is the public API
// (INV-VERDICT-STABLE, PRD §10). This package is one of the two package
// boundaries — together with internal/fixture — that the phase-5 cloud API
// imports UNCHANGED so that verdict shape and exit-code semantics have exactly
// one implementation (PRD §9).
//
// NOTE: This is the phase-0 scaffold. The full implementation — two marshalers
// (JSON + human), confidence capping, and the DSSE/in-toto predicate shape —
// lands in P2-T3 / P2-T4. Field names here match the PRD §10 example so later
// phases extend rather than rename.
package verdict

// Exit codes are part of the public API and are permanent (INV-VERDICT-STABLE,
// PRD §10). Every command maps its outcome onto exactly these.
const (
	ExitPass      = 0 // PASS
	ExitFail      = 1 // FAIL
	ExitWarnOnly  = 2 // WARN-only
	ExitToolError = 3 // tool error (could not produce a verdict)
)

// Result is the top-level verdict value. One struct, rendered by two marshalers
// (INV-VERDICT-SHAPE); human output is always a rendering of this struct, never
// a separate code path.
type Result struct {
	Rowshape   string     `json:"rowshape"`    // format/contract version tag
	Verdict    string     `json:"verdict"`     // PASS | FAIL | WARN
	Fixture    FixtureRef `json:"fixture"`     // subject of the verdict
	DurationMs int64      `json:"duration_ms"` // wall time of the run
	Findings   []Finding  `json:"findings"`    // zero or more findings
}

// FixtureRef identifies the fixture a verdict was computed against.
type FixtureRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Finding is a single result. Finding codes are permanent and namespaced
// (RS-LOCK, RS-DATA, RS-CONSTRAINT, RS-INDEX, RS-PERF, RS-REVERSE). remediation
// is mandatory on every error-severity finding (INV-VERDICT-STABLE).
type Finding struct {
	Code        string   `json:"code"`
	Severity    string   `json:"severity"` // error | warn | info
	Title       string   `json:"title"`
	Location    string   `json:"location,omitempty"`
	Detail      string   `json:"detail,omitempty"`
	Evidence    any      `json:"evidence,omitempty"`
	Estimate    any      `json:"estimate,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"` // fixture facts this rests on (capping, INV-CONFIDENCE-CAPPING)
	Confidence  string   `json:"confidence,omitempty"` // min confidence across depends_on
	Remediation string   `json:"remediation,omitempty"`
	Explain     string   `json:"explain,omitempty"`
}
