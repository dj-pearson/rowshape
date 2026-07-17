package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The third of the three things `init --agent` writes (PRD §8.3): a pre-commit
// hook running `rowshape validate` — the backstop for when the agent ignores the
// rule, which it sometimes will.
//
// The rule (P3-T9) is instruction; instructions get skipped. This is the thing
// that doesn't depend on the agent having read anything, so the guarantee isn't
// resting solely on good behavior.
//
// A backstop has one failure mode that matters, and it isn't missing a bad
// migration: it's being annoying enough to get deleted. A hook that hydrates a
// database on a README typo, or blocks every commit because Docker isn't running,
// gets removed within a week — and then it catches nothing, forever. So the hook
// runs only when a migration is actually staged, and it distinguishes a FAIL (a
// reproduction — block) from a tool error (Docker down — say so, don't block).

// hookID is the pre-commit framework hook id, and the marker we recognize our own
// native hook by.
const hookID = "rowshape-validate"

// hookVersion tracks the hook body, so `init --agent` can update one it wrote
// earlier — the same upgrade path the agent rule has (P3-T9).
const hookVersion = 1

// nativeHookPath is where git looks for the hook. Note it is under .git/ and is
// therefore NOT committable — which is exactly why the pre-commit framework path
// below is preferred when a repo has one: that config IS committed, so the whole
// team gets the backstop instead of only the person who ran init.
var nativeHookPath = filepath.Join(".git", "hooks", "pre-commit")

// preCommitConfig is the pre-commit framework's config file.
const preCommitConfig = ".pre-commit-config.yaml"

// migrationPattern returns the regex matching migration files for a runner.
//
// This is what keeps the hook tolerable: `rowshape validate` hydrates a
// disposable Postgres, which is seconds at best. Paying that on every commit —
// including the ones that only touch a README — is how a hook earns its deletion.
func migrationPattern(runner string) string {
	switch runner {
	case "alembic":
		return `(^|/)versions/.*\.py$`
	case "prisma":
		return `(^|/)prisma/migrations/`
	case "drizzle":
		return `(^|/)drizzle/.*\.sql$`
	case "rawsql":
		return `(^|/)(migrations|db/migrations|database/migrations|sql)/.*\.sql$`
	default:
		// Runner not detected: match the common conventions rather than nothing.
		// A hook that never fires is indistinguishable from no hook.
		return `(^|/)(migrations|versions)/`
	}
}

// nativeHookScript renders the git hook.
//
// The exit-code handling is the interesting part, and it is a deliberate reading
// of the verdict contract (PRD §10): 0 PASS and 2 WARN-only let the commit
// through; 1 FAIL blocks; anything else (3, tool error) does NOT block.
//
// That last one is the judgment call. A tool error is not a verdict — it means
// rowshape couldn't answer, usually because Docker isn't running. Blocking every
// commit on a developer's machine because a daemon is down is exactly the
// behavior that gets the hook deleted or `--no-verify` aliased permanently, and
// then the backstop is gone for the FAILs too. A commit is not a deploy, and CI
// still gates the merge — so it warns loudly and gets out of the way.
func nativeHookScript(runner string) string {
	return `#!/bin/sh
# rowshape:begin v` + fmt.Sprint(hookVersion) + ` — managed by ` + "`rowshape init --agent`" + `; edits are overwritten on upgrade.
#
# The backstop (PRD 8.3). The agent rule tells the agent to validate before
# opening a PR. It sometimes won't. This catches that.
#
# Uninstall:   rm ` + filepath.ToSlash(nativeHookPath) + `
# Skip once:   git commit --no-verify

# Only when a migration is actually staged: validate hydrates a database, and a
# README typo should not pay for that.
staged=$(git diff --cached --name-only --diff-filter=ACM | grep -E '` + migrationPattern(runner) + `')
if [ -z "$staged" ]; then
  exit 0
fi

if ! command -v rowshape >/dev/null 2>&1; then
  echo "rowshape: not on PATH — skipping migration validation." >&2
  exit 0
fi

rowshape validate
status=$?

case "$status" in
  0) exit 0 ;;
  2)
    # WARN-only: rowshape could not prove something. It printed what and how to
    # resolve it. Not a blocker here — use ` + "`rowshape validate --warn-fail`" + ` in CI
    # if you want WARN to gate a merge.
    exit 0 ;;
  1)
    echo "" >&2
    echo "rowshape: FAIL — this migration broke against production-shaped data." >&2
    echo "rowshape: that is a reproduction, not an opinion. Fix it and re-commit," >&2
    echo "rowshape: or 'git commit --no-verify' if you know why it is safe." >&2
    exit 1 ;;
  *)
    # Tool error (exit 3): rowshape could not answer — usually Docker is not
    # running. Not a verdict, so it does not block your commit. CI still gates
    # the merge.
    echo "" >&2
    echo "rowshape: could not validate (exit $status) — NOT blocking this commit." >&2
    echo "rowshape: run 'rowshape validate' to see why." >&2
    exit 0 ;;
esac
# rowshape:end
`
}

// hookTarget reports how this repo gets its backstop.
type hookMode int

const (
	hookNone      hookMode = iota // not a git repo — nothing to hook
	hookFramework                 // .pre-commit-config.yaml exists: add an entry to it
	hookNative                    // plain git repo: write .git/hooks/pre-commit
)

// detectHookMode decides where the hook goes.
//
// The pre-commit framework wins when present: its config is committed, so the
// whole team gets the backstop, and it is already the thing running on their
// commits. Installing a native hook alongside it would run validate twice and
// fight the tool the repo already chose.
func detectHookMode(dir string) hookMode {
	if _, err := os.Stat(filepath.Join(dir, preCommitConfig)); err == nil {
		return hookFramework
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return hookNone
	}
	return hookNative
}

// writeNativeHook installs .git/hooks/pre-commit.
//
// It will not clobber a hook it did not write. Someone else's pre-commit hook is
// someone else's whole workflow, and silently replacing it to install a backstop
// is a worse outcome than not installing one.
func writeNativeHook(dir, runner string) (writeStatus, error) {
	path := filepath.Join(dir, nativeHookPath)
	want := nativeHookScript(runner)

	if data, err := os.ReadFile(path); err == nil {
		existing := string(data)
		if !strings.Contains(existing, "rowshape:begin") {
			return 0, fmt.Errorf("%s already exists and was not written by rowshape; "+
				"leaving it alone. Add this line to it by hand:\n\n    rowshape validate", filepath.ToSlash(nativeHookPath))
		}
		if existing == want {
			return statusUnchanged, nil
		}
		if err := writeExecutable(path, want); err != nil {
			return 0, err
		}
		return statusUpdated, nil
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("reading %s: %w", filepath.ToSlash(nativeHookPath), err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("creating %s: %w", filepath.Dir(nativeHookPath), err)
	}
	if err := writeExecutable(path, want); err != nil {
		return 0, err
	}
	return statusCreated, nil
}

// writeExecutable writes the hook with the executable bit — a git hook without it
// is silently never run, which is the most expensive way to have a backstop.
func writeExecutable(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// frameworkHookYAML is the entry added to .pre-commit-config.yaml.
//
// `repo: local` with `language: system` runs the rowshape already on the
// developer's PATH. The alternative — pointing at a remote hook repo — would make
// this depend on published tags, and would reinstall a second copy of a binary the
// user has already installed.
func frameworkHookYAML(runner string) string {
	return `repo: local
hooks:
  - id: ` + hookID + `
    name: rowshape validate
    entry: rowshape validate
    language: system
    files: '` + migrationPattern(runner) + `'
    pass_filenames: false
`
}

// writeFrameworkHook adds (or updates) the rowshape hook in .pre-commit-config.yaml.
//
// This edits a file the user owns and commits, so it goes through yaml.Node rather
// than a marshal round-trip: Node preserves their comments, key order, and
// formatting, and only the bytes we actually change move.
func writeFrameworkHook(dir, runner string) (writeStatus, error) {
	path := filepath.Join(dir, preCommitConfig)

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", preCommitConfig, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("%s is not valid YAML (%v); add this hook by hand:\n\n%s", preCommitConfig, err, frameworkHookYAML(runner))
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return 0, fmt.Errorf("%s has no top-level mapping; add this hook by hand:\n\n%s", preCommitConfig, frameworkHookYAML(runner))
	}
	root := doc.Content[0]

	repos := mappingValue(root, "repos")
	if repos == nil || repos.Kind != yaml.SequenceNode {
		return 0, fmt.Errorf("%s has no `repos:` list; add this hook by hand:\n\n%s", preCommitConfig, frameworkHookYAML(runner))
	}

	var want yaml.Node
	if err := yaml.Unmarshal([]byte(frameworkHookYAML(runner)), &want); err != nil {
		return 0, fmt.Errorf("building the hook entry: %w", err)
	}
	wantRepo := want.Content[0]

	if existing := findHookRepo(repos); existing != nil {
		if nodesEqual(existing, wantRepo) {
			return statusUnchanged, nil
		}
		*existing = *wantRepo
	} else {
		repos.Content = append(repos.Content, wantRepo)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return 0, fmt.Errorf("encoding %s: %w", preCommitConfig, err)
	}
	if err := enc.Close(); err != nil {
		return 0, fmt.Errorf("encoding %s: %w", preCommitConfig, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return 0, fmt.Errorf("writing %s: %w", preCommitConfig, err)
	}
	return statusUpdated, nil
}

// mappingValue returns the value node for key in a mapping, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// findHookRepo locates the repo entry holding our hook id, wherever the user has
// moved it in their list. Matching on the id — not on position, and not on the
// whole entry — is what lets an upgrade correct a stale entry in place instead of
// appending a duplicate hook that runs validate twice.
func findHookRepo(repos *yaml.Node) *yaml.Node {
	for _, repo := range repos.Content {
		hooks := mappingValue(repo, "hooks")
		if hooks == nil || hooks.Kind != yaml.SequenceNode {
			continue
		}
		for _, h := range hooks.Content {
			if id := mappingValue(h, "id"); id != nil && id.Value == hookID {
				return repo
			}
		}
	}
	return nil
}

// nodesEqual compares two YAML nodes by their rendered form — the only comparison
// that means "the file would not change".
func nodesEqual(a, b *yaml.Node) bool {
	ab, err1 := yaml.Marshal(a)
	bb, err2 := yaml.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

// writeHook installs the backstop wherever this repo takes it.
func writeHook(dir string) (writeStatus, string, error) {
	runner := detectStack(dir).Runner
	switch detectHookMode(dir) {
	case hookFramework:
		st, err := writeFrameworkHook(dir, runner)
		return st, preCommitConfig, err
	case hookNative:
		st, err := writeNativeHook(dir, runner)
		return st, filepath.ToSlash(nativeHookPath), err
	default:
		return statusUnchanged, "", nil // not a git repo
	}
}
