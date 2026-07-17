// Command rowshape is the type-checker for database migrations.
//
// A single static binary: CLI + MCP server. main's only job is to run the
// command tree and map its outcome onto the stable exit-code contract
// (0 PASS / 1 FAIL / 2 WARN-only / 3 tool error, PRD §10).
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/rowshape/rowshape/cmd"
	"github.com/rowshape/rowshape/internal/verdict"
)

func main() {
	err := cmd.Execute()
	if err == nil {
		return
	}

	// An ExitError means the command already told the user what happened and is
	// only carrying its exit code up (PRD §10). Printing here would say it twice.
	var ee *cmd.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.Code)
	}

	// Everything else is unclassified — most often a flag or argument error from
	// cobra, which the root command silences (SilenceErrors) so that commands
	// which report their own failures don't double-print. Silencing cobra without
	// printing here is what made `rowshape pull --nope` exit 3 with no output at
	// all: no message, no usage, nothing to act on. That is a bad CLI for a human
	// and a broken contract for an agent, which gets a bare exit 3 and cannot tell
	// a typo from a dead database (PRD §10: a failure an agent can't act on is a
	// bug).
	fmt.Fprintf(os.Stderr, "rowshape: %v\n", err)
	os.Exit(verdict.ExitToolError)
}
