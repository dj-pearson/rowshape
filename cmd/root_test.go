package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// TestREADMEListsEveryCommand: CR-T29. The README described every command as a
// "phase 0 stub that returns exit code 3" long after they were all implemented —
// the first thing a visitor reads, saying the tool does nothing. It also listed
// eight commands when the binary had ten.
//
// Doc drift is only catchable by a human noticing, so this makes the README's
// command list answerable to the actual command tree.
func TestREADMEListsEveryCommand(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(readme)

	// cobra adds `help` and `completion` to every binary; they are not rowshape's
	// surface and the README should not have to list them.
	builtin := map[string]bool{"help": true, "completion": true}

	var checked int
	for _, c := range NewRootCmd().Commands() {
		name := c.Name()
		if builtin[name] {
			continue
		}
		checked++
		if !strings.Contains(text, "`"+name+"`") {
			t.Errorf("README does not mention the %q command; a reader cannot discover it", name)
		}
	}
	if checked == 0 {
		t.Fatal("no commands found on the root command; this check would pass over nothing")
	}
	t.Logf("verified the README mentions all %d rowshape commands", checked)

	// The stale claim itself, pinned so it cannot come back.
	for _, stale := range []string{"phase 0 these are stubs", "stubs that return exit code 3"} {
		if strings.Contains(text, stale) {
			t.Errorf("README still carries the stale phase-0 stub claim: %q", stale)
		}
	}
}
