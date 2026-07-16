// Command rowshape is the type-checker for database migrations.
//
// A single static binary: CLI + MCP server. main's only job is to run the
// command tree and map its outcome onto the stable exit-code contract
// (0 PASS / 1 FAIL / 2 WARN-only / 3 tool error, PRD §10).
package main

import (
	"errors"
	"os"

	"github.com/rowshape/rowshape/cmd"
	"github.com/rowshape/rowshape/internal/verdict"
)

func main() {
	err := cmd.Execute()
	if err == nil {
		return
	}
	var ee *cmd.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.Code)
	}
	// Any unclassified error is a tool error.
	os.Exit(verdict.ExitToolError)
}
