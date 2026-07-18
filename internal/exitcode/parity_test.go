package exitcode_test

import (
	"testing"

	"github.com/rowshape/rowshape/internal/exitcode"
	"github.com/rowshape/rowshape/internal/toolerror"
	"github.com/rowshape/rowshape/internal/verdict"
)

// TestExitCodeParity is the point of the package (CR-T23): the exit codes had two
// independent definitions — verdict.ExitToolError and a bare `return 3` in
// toolerror.ExitCode(). Two literals for one contract value is how they
// eventually disagree, silently, in a way a consumer branching on exit status
// would feel and nothing would report.
//
// It lives in an _test package so it can import both consumers without either
// importing the other.
func TestExitCodeParity(t *testing.T) {
	// The wire values themselves are permanent (INV-VERDICT-STABLE, PRD §10).
	// Written as literals on purpose: deriving them from the constants under test
	// would only prove the file agrees with itself.
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"exitcode.Pass", exitcode.Pass, 0},
		{"exitcode.Fail", exitcode.Fail, 1},
		{"exitcode.WarnOnly", exitcode.WarnOnly, 2},
		{"exitcode.ToolError", exitcode.ToolError, 3},
		{"verdict.ExitPass", verdict.ExitPass, 0},
		{"verdict.ExitFail", verdict.ExitFail, 1},
		{"verdict.ExitWarnOnly", verdict.ExitWarnOnly, 2},
		{"verdict.ExitToolError", verdict.ExitToolError, 3},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d — this value is part of the public API", tc.name, tc.got, tc.want)
		}
	}

	// The two definitions that used to be independent must agree.
	if got := toolerror.New(toolerror.Internal, "m", "").ExitCode(); got != verdict.ExitToolError {
		t.Errorf("toolerror.ExitCode() = %d but verdict.ExitToolError = %d — the two have drifted, "+
			"which is exactly what CR-T23 removed the possibility of", got, verdict.ExitToolError)
	}
}

// TestResultExitCodeMappingUnchanged pins the verdict→exit mapping through the
// refactor, including the --warn-fail knob.
func TestResultExitCodeMappingUnchanged(t *testing.T) {
	cases := []struct {
		verdict           string
		normal, warnFails int
	}{
		{verdict.VerdictPass, 0, 0},
		{verdict.VerdictFail, 1, 1},
		{verdict.VerdictWarn, 2, 1},
	}
	for _, tc := range cases {
		r := verdict.Result{Verdict: tc.verdict}
		if got := r.ExitCode(false); got != tc.normal {
			t.Errorf("%s exit = %d, want %d", tc.verdict, got, tc.normal)
		}
		if got := r.ExitCode(true); got != tc.warnFails {
			t.Errorf("%s exit (warnFails) = %d, want %d", tc.verdict, got, tc.warnFails)
		}
	}
}
