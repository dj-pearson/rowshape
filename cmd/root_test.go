package cmd

import (
	"errors"
	"io"
	"testing"
)

// The root command sets SilenceErrors/SilenceUsage so that commands which
// already report their own failure (and return an ExitError carrying just the
// exit code) don't print the same thing twice. That shifts the duty of reporting
// everything ELSE onto main.
//
// When main didn't, the result was `rowshape pull --nope` exiting 3 with zero
// bytes on stdout and stderr: no message, no usage, nothing to act on. Silent for
// a human, and worse for an agent, which sees a bare exit 3 and cannot tell a
// typo'd flag from a dead database (PRD §10 — a failure you can't act on is a
// bug).
//
// These tests pin the contract that split makes: a usage error must come back as
// a PLAIN error for main to print, and never as an ExitError, which is the
// "already reported" signal.

// runRoot executes the command tree with args, discarding output the way the real
// binary does (cobra prints nothing; main does the reporting).
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCmd()
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
}

func TestUsageErrorsAreReportable(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"pull", "--definitely-not-a-flag"}},
		{"unknown subcommand", []string{"wat"}},
		{"unknown flag on validate", []string{"validate", "--nope"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := runRoot(t, c.args...)
			if err == nil {
				t.Fatalf("%v should be an error", c.args)
			}

			// An ExitError means "a message was already printed; just carry the
			// code". A usage error has printed nothing, so returning one here
			// would send it to main's silent path — the original bug.
			var ee *ExitError
			if errors.As(err, &ee) {
				t.Errorf("%v returned an ExitError (the 'already reported' signal), so main prints nothing "+
					"and the user gets a bare exit %d with no message", c.args, ee.Code)
			}

			if err.Error() == "" {
				t.Errorf("%v produced an error with no message — there is nothing for main to print", c.args)
			}
		})
	}
}

// TestHelpIsNotAnError: --help is a successful outcome, not a failure. If it came
// back as an error, main would print it to stderr and exit 3 — and every
// `rowshape --help` in a script would look like a broken tool.
func TestHelpIsNotAnError(t *testing.T) {
	if err := runRoot(t, "--help"); err != nil {
		t.Errorf("--help should succeed, got: %v", err)
	}
}
