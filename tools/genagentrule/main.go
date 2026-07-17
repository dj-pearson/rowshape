// Command genagentrule writes the docs page that shows the agent rule
// (`rowshape init --agent` writes this into AGENTS.md / CLAUDE.md), sourced from
// internal/agentrule so the published text can never drift from what the binary
// actually writes (P4-T5 criterion 3). Run from the repo root:
//
//	go run ./tools/genagentrule
//
// The generated page is committed; gen_test.go fails if it is stale.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/rowshape/rowshape/internal/agentrule"
)

// rulePage is the Starlight page path, relative to the repo root.
const rulePage = "docs-site/src/content/docs/agent/rule.md"

func main() {
	if err := os.WriteFile(rulePage, []byte(renderRulePage()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genagentrule:", err)
		os.Exit(1)
	}
}

// renderRulePage wraps the embedded rule text in a docs page. The rule body is
// the exact agentrule.Text (what the managed block contains), shown in a fenced
// block so it renders as-is rather than as live Markdown headings.
func renderRulePage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "title: The agent rule\n")
	fmt.Fprintf(&b, "description: The exact text rowshape init --agent writes into AGENTS.md / CLAUDE.md.\n")
	fmt.Fprintf(&b, "sidebar:\n  order: 3\n")
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "`rowshape init --agent` writes the rule below into your `AGENTS.md` (and\n")
	fmt.Fprintf(&b, "`CLAUDE.md`), inside a managed block marked `rowshape:begin v%d` … `rowshape:end`.\n", agentrule.Version)
	fmt.Fprintf(&b, "Everything outside that block is left byte-for-byte untouched, and a re-run\n")
	fmt.Fprintf(&b, "replaces the block in place — so an improvement to the rule reaches repos that\n")
	fmt.Fprintf(&b, "ran `init --agent` months ago. It is a versioned product artifact (currently\n")
	fmt.Fprintf(&b, "**v%d**), iterated against real agent sessions like a prompt.\n\n", agentrule.Version)

	fmt.Fprintf(&b, "This is the current text, verbatim:\n\n")
	fmt.Fprintf(&b, "````markdown\n%s\n````\n", strings.TrimSpace(agentrule.Text))
	return b.String()
}
