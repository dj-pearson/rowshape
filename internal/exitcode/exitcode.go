// Package exitcode holds rowshape's process exit codes, which are part of the
// public API and are permanent (INV-VERDICT-STABLE, PRD §10).
//
// It exists because the codes had two independent definitions: verdict.ExitToolError
// = 3, and a bare `return 3` in toolerror.ExitCode(). Two literals for one
// contract value is how they eventually disagree, and the disagreement would be
// silent — a consumer branching on exit status would simply start doing the
// wrong thing.
//
// Why a THIRD package rather than one importing the other: internal/toolerror is
// deliberately a leaf (its own comment notes it avoids importing `strings` for a
// single Builder), so it should not take a dependency on internal/verdict; and
// verdict has no business depending on toolerror, which is the not-a-verdict
// case. Neither direction is natural, so the shared value gets its own home. No
// import cycle existed before this change and none is introduced.
//
// These are exit codes only. None of them is a JSON field, so nothing here can
// alter an emitted verdict or tool-error document (INV-DSSE-SHAPE).
package exitcode

const (
	// Pass: the migration is safe against production-shaped data.
	Pass = 0
	// Fail: a finding proved the migration unsafe.
	Fail = 1
	// WarnOnly: findings exist but none is an error. Configurable to fail via
	// the --warn-fail knob (see verdict.Result.ExitCode).
	WarnOnly = 2
	// ToolError: rowshape could not produce a verdict at all. Distinct from a
	// verdict on purpose — an agent must never read "the tool could not run" as
	// "the migration is unsafe" (PRD §10, §17.2).
	ToolError = 3
)
