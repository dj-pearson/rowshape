package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/agentrule"
)

// readFile reads a rule target written by init --agent.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// hasRule asserts the managed block landed with the current version and the rule's
// substance (not just the markers).
func hasRule(t *testing.T, content, where string) {
	t.Helper()
	if !strings.Contains(content, "rowshape:begin v") {
		t.Fatalf("%s carries no managed rule block:\n%s", where, content)
	}
	if !strings.Contains(content, "rowshape:end") {
		t.Errorf("%s has an unterminated managed block:\n%s", where, content)
	}
	for _, want := range []string{"describe_shape", "validate_migration", "FAIL"} {
		if !strings.Contains(content, want) {
			t.Errorf("%s should instruct the agent about %q (PRD §8.3)", where, want)
		}
	}
}

// TestInitAgentWritesRuleToDetectedTargets: the rule lands in the conventions the
// repo already keeps (PRD §8.3 item 2).
func TestInitAgentWritesRuleToDetectedTargets(t *testing.T) {
	cases := []struct {
		name   string
		marker string // what the repo already has
		isDir  bool
		want   string // where the rule must land
	}{
		{"agents.md", "AGENTS.md", false, "AGENTS.md"},
		{"claude.md", "CLAUDE.md", false, "CLAUDE.md"},
		{"cursor", ".cursor", true, filepath.Join(".cursor", "rules", "rowshape.mdc")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDECODE", "")
			if c.isDir {
				mkdir(t, filepath.Join(dir, c.marker))
			} else {
				writeMarker(t, filepath.Join(dir, c.marker))
			}

			if err := runInitAgent(dir, false, io.Discard); err != nil {
				t.Fatalf("init --agent: %v", err)
			}
			hasRule(t, readFile(t, filepath.Join(dir, c.want)), c.want)
		})
	}
}

// TestInitAgentCursorRuleHasFrontmatter: an .mdc without `alwaysApply` is a rule
// Cursor loads only when it feels like it — indistinguishable from not writing one.
func TestInitAgentCursorRuleHasFrontmatter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	mkdir(t, filepath.Join(dir, ".cursor"))

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	content := readFile(t, filepath.Join(dir, ".cursor", "rules", "rowshape.mdc"))
	if !strings.HasPrefix(content, "---\n") || !strings.Contains(content, "alwaysApply: true") {
		t.Errorf("a Cursor rule needs frontmatter to apply at all, got:\n%s", content)
	}
}

// TestInitAgentFallsBackToAgentsMD: a repo keeping none of the conventions still
// gets the rule. It has to land somewhere or --agent has done nothing, and
// AGENTS.md is the choice that isn't tied to one vendor.
func TestInitAgentFallsBackToAgentsMD(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	hasRule(t, readFile(t, filepath.Join(dir, "AGENTS.md")), "AGENTS.md")

	// It writes to the convention in use, it does not litter.
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("init --agent created CLAUDE.md in a repo that does not use it")
	}
}

// TestInitAgentUpgradesRuleInPlace is the story's reason to exist. The rule is a
// product artifact iterated like a prompt, so an improvement found in month four
// must reach a repo that ran init in month one — replacing the old block IN PLACE,
// not appending a second rule that contradicts the first.
func TestInitAgentUpgradesRuleInPlace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	path := filepath.Join(dir, "AGENTS.md")

	// A repo wired by an older rowshape: v0 rule, with the user's own guidance
	// above and below it.
	stale := `# Contributing

Run the tests before you push.

<!-- rowshape:begin v0 — managed by ` + "`rowshape init --agent`" + ` -->

## Database migrations

Old advice that we have since learned is wrong.

<!-- rowshape:end -->

## House style

Tabs, not spaces. This paragraph is ours.
`
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	got := readFile(t, path)

	// The old rule is gone, replaced — not accumulated.
	if strings.Contains(got, "Old advice") {
		t.Error("the outdated rule text survived the upgrade")
	}
	if n := strings.Count(got, "rowshape:begin"); n != 1 {
		t.Errorf("expected exactly 1 managed block after upgrade, got %d — an upgrade must replace, not append:\n%s", n, got)
	}
	if !strings.Contains(got, "rowshape:begin v1") {
		t.Errorf("the block should now carry the current version, got:\n%s", got)
	}
	hasRule(t, got, "AGENTS.md")

	// The user's content, above AND below, is untouched. rowshape is a guest in
	// this file.
	for _, own := range []string{"# Contributing", "Run the tests before you push.", "## House style", "Tabs, not spaces. This paragraph is ours."} {
		if !strings.Contains(got, own) {
			t.Errorf("the upgrade clobbered the user's own content: %q is gone from:\n%s", own, got)
		}
	}
	if !strings.HasPrefix(got, "# Contributing") {
		t.Error("content above the block must stay above it")
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "This paragraph is ours.") {
		t.Error("content below the block must stay below it")
	}
}

// TestInitAgentRuleIsIdempotent: --agent re-runs on every upgrade, forever. A
// current rule means the file is not touched at all.
func TestInitAgentRuleIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	path := filepath.Join(dir, "AGENTS.md")

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("first init --agent: %v", err)
	}
	first := readFile(t, path)

	var out strings.Builder
	if err := runInitAgent(dir, false, &out); err != nil {
		t.Fatalf("second init --agent: %v", err)
	}
	second := readFile(t, path)

	if first != second {
		t.Errorf("re-running rewrote the rule file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if strings.Count(second, "rowshape:begin") != 1 {
		t.Errorf("re-running duplicated the rule block:\n%s", second)
	}
	if !strings.Contains(out.String(), "already carries agent rule") {
		t.Errorf("a no-op re-run should say so, got: %s", out.String())
	}
}

// TestInitAgentAppendsRuleToExistingFile: an existing AGENTS.md with no rowshape
// block keeps everything it had; the rule is appended, not substituted for it.
func TestInitAgentAppendsRuleToExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	path := filepath.Join(dir, "AGENTS.md")
	existing := "# Agent guidance\n\nBe careful with the billing code.\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	got := readFile(t, path)

	if !strings.HasPrefix(got, existing) {
		t.Errorf("appending the rule disturbed the file's existing content:\n%s", got)
	}
	hasRule(t, got, "AGENTS.md")
}

// TestInitWithoutAgentWritesNoRule: --agent is opt-in; plain init touches nothing
// an agent reads.
func TestInitWithoutAgentWritesNoRule(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, filepath.Join(dir, "AGENTS.md"))

	if err := runInit(dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "AGENTS.md")), "rowshape:begin") {
		t.Error("plain `init` should not write the agent rule; --agent is opt-in")
	}
}

// TestRuleBlockRoundTrip covers the block machinery directly, including the
// malformed-marker case an integration test would not reach: a hand-mangled
// version marker must be repaired, not duplicated.
func TestRuleBlockRoundTrip(t *testing.T) {
	t.Run("insert into empty", func(t *testing.T) {
		out, changed := agentrule.Upsert("")
		if !changed || !strings.Contains(out, "rowshape:begin v1") {
			t.Errorf("empty content should get the block, got: %q", out)
		}
	})

	t.Run("current block is a no-op", func(t *testing.T) {
		once, _ := agentrule.Upsert("")
		twice, changed := agentrule.Upsert(once)
		if changed || once != twice {
			t.Error("re-upserting a current block must not change anything")
		}
	})

	t.Run("malformed version marker is repaired", func(t *testing.T) {
		mangled := "before\n\n<!-- rowshape:begin vNOPE -->\nold\n<!-- rowshape:end -->\n\nafter\n"
		out, changed := agentrule.Upsert(mangled)
		if !changed {
			t.Fatal("a malformed block should be repaired")
		}
		if strings.Count(out, "rowshape:begin") != 1 {
			t.Errorf("repair must replace, not append:\n%s", out)
		}
		if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
			t.Errorf("repair clobbered surrounding content:\n%s", out)
		}
	})

	t.Run("version is reported", func(t *testing.T) {
		block, _ := agentrule.Upsert("")
		_, _, v, found := agentrule.FindBlock(block)
		if !found || v != agentrule.Version {
			t.Errorf("FindBlock reported v%d found=%v, want v%d", v, found, agentrule.Version)
		}
	})
}

// TestRuleStaysInBudget: the rule is in the agent's context on every turn of every
// session — the same tax the tool schemas pay (PRD §8.2). It is prose, so it will
// attract "just one more sentence" forever; this is the line that says no.
//
// 2000 chars is ~500 tokens: a deliberate ceiling on what the rule may cost per
// turn, not a measurement of what it happens to cost today. The rule is meant to
// be iterated against real sessions, so a budget set snugly against the current
// text would fire on every honest edit and teach everyone to raise it — which is
// how a budget stops meaning anything.
func TestRuleStaysInBudget(t *testing.T) {
	const budget = 2000 // chars, ~500 tokens

	size := len(agentrule.Block())
	t.Logf("agent rule v%d: %d chars (~%d tokens) of %d budget", agentrule.Version, size, size/4, budget)
	if size > budget {
		t.Errorf("the agent rule is %d chars, over the %d budget by %d — it is read on every turn; "+
			"cut a sentence rather than raising the budget", size, budget, size-budget)
	}
}
