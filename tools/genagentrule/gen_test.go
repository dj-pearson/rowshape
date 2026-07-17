package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/agentrule"
)

func committedRulePage(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", rulePage)
}

// TestRulePageUpToDate: the committed rule page is byte-identical to a fresh
// render. If the rule (internal/agentrule/rule.md) changes, run
// `go run ./tools/genagentrule`.
func TestRulePageUpToDate(t *testing.T) {
	got, err := os.ReadFile(committedRulePage(t))
	if err != nil {
		t.Fatalf("read committed rule page: %v — run `go run ./tools/genagentrule`", err)
	}
	if string(got) != renderRulePage() {
		t.Errorf("agent/rule.md is stale — run `go run ./tools/genagentrule`")
	}
}

// TestRulePageShowsRule: the page contains the verbatim rule text the binary
// writes (P4-T5 criterion 3), so a reader sees exactly what lands in AGENTS.md.
func TestRulePageShowsRule(t *testing.T) {
	body, err := os.ReadFile(committedRulePage(t))
	if err != nil {
		t.Fatal(err)
	}
	rule := strings.TrimSpace(agentrule.Text)
	if !strings.Contains(string(body), rule) {
		t.Errorf("rule page does not contain the current agent rule verbatim")
	}
	// Sanity: the rule's load-bearing lines are present.
	for _, want := range []string{"describe_shape", "validate_migration", "Never hand-wave a FAIL", "A WARN is not a pass"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("rule page missing %q", want)
		}
	}
}
