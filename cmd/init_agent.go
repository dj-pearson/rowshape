package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/rowshape/rowshape/internal/agentrule"
)

// `init --agent` is the part that actually closes the loop (PRD §8.3).
//
// Serving the four tools is not the same product surface as getting an agent to
// reach for them, and the second one is closer to the wedge than the first.
// `--agent` writes three things:
//
//  1. MCP config for the detected client        (this story, P3-T8)
//  2. A versioned agent rule                    (P3-T9)
//  3. A pre-commit hook running rowshape validate (P3-T10)
//
// Each is additive and idempotent, so `init --agent` is safe to re-run — which it
// will be, on every upgrade, forever.

// runInitAgent scaffolds the base config and then wires the repo for agents. It
// reports what it wrote to w so the user can see exactly which files changed —
// this command edits files the user commits, so it does not get to be quiet.
func runInitAgent(dir string, force bool, w io.Writer) error {
	if err := runInit(dir, force); err != nil {
		return err
	}

	failed := false

	// 1. MCP config — tell the client the tools exist.
	for _, c := range detectMCPClients(dir) {
		status, err := writeMCPConfig(dir, c)
		if err != nil {
			fmt.Fprintf(w, "rowshape init: %s: %v\n", c.Name, err)
			failed = true
			continue
		}
		switch status {
		case statusCreated:
			fmt.Fprintf(w, "rowshape init: wrote %s (%s) — registered `rowshape mcp`\n", c.Path, c.Name)
		case statusUpdated:
			fmt.Fprintf(w, "rowshape init: updated %s (%s) — registered `rowshape mcp`\n", c.Path, c.Name)
		case statusUnchanged:
			fmt.Fprintf(w, "rowshape init: %s (%s) already registers `rowshape mcp`\n", c.Path, c.Name)
		}
	}

	// 2. The agent rule — make the agent actually reach for them.
	for _, t := range detectRuleTargets(dir) {
		status, err := writeRule(dir, t)
		if err != nil {
			fmt.Fprintf(w, "rowshape init: %s: %v\n", t.Name, err)
			failed = true
			continue
		}
		switch status {
		case statusCreated:
			fmt.Fprintf(w, "rowshape init: wrote %s — agent rule v%d\n", t.Path, agentrule.Version)
		case statusUpdated:
			fmt.Fprintf(w, "rowshape init: updated %s — agent rule v%d\n", t.Path, agentrule.Version)
		case statusUnchanged:
			fmt.Fprintf(w, "rowshape init: %s already carries agent rule v%d\n", t.Path, agentrule.Version)
		}
	}

	// 3. The pre-commit hook — the backstop for when the agent ignores the rule.
	switch status, where, err := writeHook(dir); {
	case err != nil:
		fmt.Fprintf(w, "rowshape init: %v\n", err)
		failed = true
	case where == "":
		fmt.Fprintf(w, "rowshape init: not a git repo — skipped the pre-commit hook\n")
	case status == statusUnchanged:
		fmt.Fprintf(w, "rowshape init: %s already runs `rowshape validate`\n", where)
	default:
		verb := "wrote"
		if status == statusUpdated {
			verb = "updated"
		}
		fmt.Fprintf(w, "rowshape init: %s %s — `rowshape validate` runs before each commit that touches a migration\n", verb, where)
	}

	if failed {
		// One unwritable file is a tool error: the user asked to be wired up and we
		// did not fully deliver. Say so with exit code 3 rather than reporting
		// success and leaving them to discover it when the agent never calls a tool.
		return toolError()
	}

	fmt.Fprintf(w, "rowshape init: restart your agent to pick up the new server, then ask it to call describe_shape.\n")
	return nil
}

// newInitAgentRunner returns the RunE body for `init`, dispatching on --agent.
func newInitAgentRunner(agent *bool, force *bool) func() error {
	return func() error {
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "rowshape init: %v\n", err)
			return toolError()
		}
		if *agent {
			return runInitAgent(dir, *force, os.Stderr)
		}
		return runInit(dir, *force)
	}
}
