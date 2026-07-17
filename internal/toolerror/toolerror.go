// Package toolerror defines the exit-code-3 tool-error contract: the operational
// failures that are NOT a verdict. An agent must never confuse "the tool couldn't
// run" with "the migration is unsafe" (PRD §10, §17.2). A tool error is one
// struct with two renderings — machine-readable JSON and a human rendering of the
// same shape — clearly distinct from a verdict: it carries `"error":"tool_error"`
// where a verdict carries `"verdict"`, and it always exits 3.
package toolerror

import (
	"encoding/json"
	"fmt"
	"io"
)

// Kind is a literal that marks the payload as a tool error, never a verdict, so a
// consumer branching on the JSON can tell them apart without heuristics.
const Kind = "tool_error"

// Category is the machine-readable class of an operational failure. An agent
// branches on it to decide what to do — retry the environment, fix the fixture,
// install the runner — none of which is "the migration is unsafe".
type Category string

const (
	// TargetUnavailable: no disposable target could be created (no Docker daemon,
	// pg_tmp unavailable, admin connection unusable) — PRD §17.2.
	TargetUnavailable Category = "target_unavailable"
	// ConnectFailed: a target was named but could not be connected to.
	ConnectFailed Category = "connect_failed"
	// RunnerNotFound: the migration runner could not be detected or invoked.
	RunnerNotFound Category = "runner_not_found"
	// FixtureParse: the fixture could not be read or parsed.
	FixtureParse Category = "fixture_parse"
	// UnknownVersion: the fixture declares a format major this build does not
	// understand; it MUST refuse rather than best-effort (RFC §12).
	UnknownVersion Category = "unknown_version"
	// BadUsage: the invocation itself is invalid (missing a required target).
	BadUsage Category = "bad_usage"
	// Internal: an unexpected failure that is still not a verdict.
	Internal Category = "internal"
)

// Contract is the version tag on every tool-error payload, matching the verdict's
// "rowshape" tag so both live under one stable contract.
const Contract = "1"

// ToolError is an operational failure (exit 3), not a verdict. It implements
// error, so it flows up the call stack like any error, and carries a structured,
// agent-readable reason.
type ToolError struct {
	Rowshape string   `json:"rowshape"` // contract version tag (== Contract)
	Error_   string   `json:"error"`    // always Kind ("tool_error") — never a verdict
	Category Category `json:"category"`
	Message  string   `json:"message"`        // human-readable reason
	Hint     string   `json:"hint,omitempty"` // how to resolve it, if actionable
}

// New builds a tool error of a category with a message and optional hint.
func New(cat Category, message, hint string) *ToolError {
	return &ToolError{Rowshape: Contract, Error_: Kind, Category: cat, Message: message, Hint: hint}
}

// Error implements the error interface.
func (e *ToolError) Error() string { return "tool error (" + string(e.Category) + "): " + e.Message }

// ExitCode is always 3: a tool error is never PASS/FAIL/WARN (PRD §10,
// INV-VERDICT-STABLE).
func (e *ToolError) ExitCode() int { return 3 }

// WriteJSON writes the machine-readable payload — the form an agent parses and
// branches on.
func (e *ToolError) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

// WriteHuman writes a rendering of the SAME struct for a person — no separate
// code path, mirroring the verdict's one-struct/two-marshalers rule.
func (e *ToolError) WriteHuman(w io.Writer) {
	fmt.Fprintf(w, "tool error [%s]: %s\n", e.Category, e.Message)
	if e.Hint != "" {
		fmt.Fprintf(w, "  hint: %s\n", e.Hint)
	}
}

// Human returns the human rendering as a string.
func (e *ToolError) Human() string {
	var b stringsBuilder
	e.WriteHuman(&b)
	return b.String()
}

// stringsBuilder is a tiny io.Writer over a byte buffer (avoids importing strings
// only for Builder in this leaf package).
type stringsBuilder struct{ b []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *stringsBuilder) String() string              { return string(s.b) }
