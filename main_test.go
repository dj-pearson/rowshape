package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The silent-failure regression, tested against the real binary.
//
// `rowshape pull --nope` used to exit 3 with zero bytes on stdout AND stderr: the
// root command silences cobra (SilenceErrors, so commands that report their own
// failure don't print twice) and main then dropped the error on the floor.
//
// This test builds and runs the actual binary because the bug lived in the SEAM
// between those two halves. A test that reaches into cmd and calls
// NewRootCmd().Execute() cannot see it — it exercises neither main's reporting
// nor the wiring in Execute(), and stays green while the shipped binary says
// nothing at all. The only thing that can fail here is the thing a user runs.

// buildBinary compiles rowshape once and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rowshape.test.exe")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("building the binary: %v\n%s", err, out)
	}
	return bin
}

// TestFailuresAlwaysSayWhy: every failing invocation must tell the user what went
// wrong. An exit code with no message is unactionable for a human, and for an
// agent it is indistinguishable from a dead database (PRD §10).
func TestFailuresAlwaysSayWhy(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)

	cases := []struct {
		name string
		args []string
		want string // a substring the message must contain
	}{
		{"unknown flag", []string{"pull", "--definitely-not-a-flag"}, "unknown flag"},
		{"unknown subcommand", []string{"wat"}, "unknown command"},
		{"unknown flag on validate", []string{"validate", "--nope"}, "unknown flag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(bin, c.args...)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			if code != 3 {
				t.Errorf("exit = %d, want 3 (tool error, PRD §10)", code)
			}

			combined := stdout.String() + stderr.String()
			if strings.TrimSpace(combined) == "" {
				t.Fatalf("`rowshape %s` failed with exit %d and printed NOTHING — "+
					"nothing for a human to read or an agent to act on",
					strings.Join(c.args, " "), code)
			}
			if !strings.Contains(combined, c.want) {
				t.Errorf("message should mention %q, got: %s", c.want, combined)
			}
		})
	}
}

// TestHelpSucceeds: --help is an outcome, not a failure. If it exited non-zero,
// every `rowshape --help` in a script or Dockerfile would look like a broken tool.
func TestHelpSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)

	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Errorf("--help should exit 0, got %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "rowshape") {
		t.Errorf("--help should describe the tool, got:\n%s", out)
	}
}
