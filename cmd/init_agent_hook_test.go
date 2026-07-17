package cmd

import (
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// gitRepo makes dir look like a git repo so the native-hook path engages.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	mkdir(t, filepath.Join(dir, ".git"))
}

// TestInitAgentInstallsNativeHook: a plain git repo gets .git/hooks/pre-commit
// running `rowshape validate` (PRD §8.3 item 3).
func TestInitAgentInstallsNativeHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	gitRepo(t, dir)

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}

	path := filepath.Join(dir, ".git", "hooks", "pre-commit")
	hook := readFile(t, path)
	if !strings.Contains(hook, "rowshape validate") {
		t.Errorf("the hook must run `rowshape validate`, got:\n%s", hook)
	}
	if !strings.HasPrefix(hook, "#!/bin/sh") {
		t.Errorf("the hook needs a shebang, got:\n%s", hook)
	}
	// Reversibility is an acceptance criterion, and the place a user looks for it
	// is the file itself — not the docs, at the moment it is blocking them.
	if !strings.Contains(hook, "rm .git/hooks/pre-commit") {
		t.Error("the hook must document its own uninstall")
	}
	if !strings.Contains(hook, "--no-verify") {
		t.Error("the hook must document how to bypass it once")
	}

	// A git hook without the executable bit is silently never run — the most
	// expensive way to have a backstop. (Windows has no POSIX mode bits; git for
	// Windows runs the hook regardless.)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("the hook is not executable (%v) — git would silently never run it", info.Mode().Perm())
		}
	}
}

// TestHookBlocksOnFailOnly is the behavioral core: the hook must block a FAIL and
// must NOT block on a tool error.
//
// It runs the real script against a stub `rowshape` that exits with a chosen code,
// so this asserts what git will actually do — not what the script looks like.
func TestHookBlocksOnFailOnly(t *testing.T) {
	sh := posixShell(t)

	cases := []struct {
		name          string
		rowshapeExit  int
		wantHookExit  int
		wantBlocked   bool
		wantInMessage string
	}{
		{"PASS lets the commit through", 0, 0, false, ""},
		{"WARN-only does not block", 2, 0, false, ""},
		{"FAIL blocks the commit", 1, 1, true, "FAIL"},
		{"tool error does not block", 3, 0, false, "NOT blocking"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()

			// A stub rowshape on PATH that exits with the code under test.
			bin := filepath.Join(dir, "bin")
			mkdir(t, bin)
			// The stub announces itself so the assertions below can tell "rowshape
			// ran and returned this code" from "the hook never got that far".
			stub := "#!/bin/sh\necho RAN_VALIDATE\nexit " + string(rune('0'+c.rowshapeExit)) + "\n"
			if err := os.WriteFile(filepath.Join(bin, "rowshape"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			// A stub git that reports a staged migration, so the hook proceeds.
			gitStub := "#!/bin/sh\necho migrations/001_init.sql\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitStub), 0o755); err != nil {
				t.Fatal(err)
			}

			hookPath := filepath.Join(dir, "pre-commit")
			if err := os.WriteFile(hookPath, []byte(nativeHookScript("rawsql")), 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := osexec.Command(sh, hookPath)
			cmd.Env = append(os.Environ(), hookPATH(t, bin))
			out, err := cmd.CombinedOutput()

			// The hook must actually have REACHED rowshape. Without this, a hook
			// that bailed out early (a missing grep, a pattern that never matches)
			// passes every "lets the commit through" case vacuously — green, and
			// blind to the FAIL it exists to catch.
			if !strings.Contains(string(out), "RAN_VALIDATE") {
				t.Fatalf("the hook never invoked rowshape — this case proves nothing:\n%s", out)
			}

			got := 0
			if ee, ok := err.(*osexec.ExitError); ok {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("running the hook: %v\n%s", err, out)
			}

			if got != c.wantHookExit {
				t.Errorf("rowshape exit %d -> hook exit %d, want %d (blocked=%v)\n%s",
					c.rowshapeExit, got, c.wantHookExit, c.wantBlocked, out)
			}
			if c.wantInMessage != "" && !strings.Contains(string(out), c.wantInMessage) {
				t.Errorf("hook output should mention %q, got:\n%s", c.wantInMessage, out)
			}
		})
	}
}

// TestHookSkipsWhenNoMigrationStaged: validate hydrates a database. Paying that on
// a README typo is how a hook earns its deletion — and a deleted hook catches
// nothing, forever.
func TestHookSkipsWhenNoMigrationStaged(t *testing.T) {
	sh := posixShell(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	mkdir(t, bin)

	// rowshape must NOT be reached: if it is, this test fails loudly.
	if err := os.WriteFile(filepath.Join(bin, "rowshape"), []byte("#!/bin/sh\necho RAN_VALIDATE\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Staged files: only a README.
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho README.md\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	hookPath := filepath.Join(dir, "pre-commit")
	if err := os.WriteFile(hookPath, []byte(nativeHookScript("rawsql")), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := osexec.Command(sh, hookPath)
	cmd.Env = append(os.Environ(), hookPATH(t, bin))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("a commit touching no migration must not be blocked, got %v:\n%s", err, out)
	}
	if strings.Contains(string(out), "RAN_VALIDATE") {
		t.Errorf("the hook hydrated a database for a commit with no migration staged:\n%s", out)
	}

	// Prove the gate is what skipped, not a broken harness: the same hook with a
	// migration staged DOES reach rowshape. Without this the assertion above is
	// satisfied by any hook that never runs anything.
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho migrations/001_init.sql\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd = osexec.Command(sh, hookPath)
	cmd.Env = append(os.Environ(), hookPATH(t, bin))
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "RAN_VALIDATE") {
		t.Fatalf("control case failed: the hook does not run rowshape even when a migration IS staged, so the skip above proves nothing:\n%s", out)
	}
}

// TestHookSkipsWhenRowshapeMissing: a teammate who has not installed rowshape must
// still be able to commit. The hook is a backstop, not a gate on tooling.
func TestHookSkipsWhenRowshapeMissing(t *testing.T) {
	sh := posixShell(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	mkdir(t, bin)
	// git present, rowshape absent.
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho migrations/001_init.sql\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	hookPath := filepath.Join(dir, "pre-commit")
	if err := os.WriteFile(hookPath, []byte(nativeHookScript("rawsql")), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := osexec.Command(sh, hookPath)
	env := hookPATH(t, bin)
	// This case only means anything if rowshape is genuinely unreachable here.
	if _, err := osexec.LookPath("rowshape"); err == nil {
		t.Skip("rowshape is installed on this machine's PATH; cannot stage its absence")
	}
	cmd.Env = append(os.Environ(), env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("a missing rowshape must not block the commit, got %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "not on PATH") {
		t.Errorf("the hook should say why it skipped, got:\n%s", out)
	}
}

// TestInitAgentUsesFrameworkWhenPresent: a repo already running the pre-commit
// framework gets an entry in ITS config, not a native hook alongside it. Two hooks
// would run validate twice and fight the tool the repo already chose — and the
// framework config is committed, so the whole team gets the backstop.
func TestInitAgentUsesFrameworkWhenPresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	gitRepo(t, dir)
	existing := `# Our hooks. Please keep sorted.
repos:
  - repo: https://github.com/psf/black
    rev: 24.1.0
    hooks:
      - id: black
`
	if err := os.WriteFile(filepath.Join(dir, preCommitConfig), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}

	got := readFile(t, filepath.Join(dir, preCommitConfig))
	if !strings.Contains(got, hookID) || !strings.Contains(got, "rowshape validate") {
		t.Errorf("the framework config should carry the rowshape hook, got:\n%s", got)
	}
	if !strings.Contains(got, "id: black") || !strings.Contains(got, "psf/black") {
		t.Errorf("init --agent dropped the user's existing hooks:\n%s", got)
	}
	// yaml.Node round-trip: their comment survives. This file is committed and
	// reviewed; churning it is how the tool loses trust.
	if !strings.Contains(got, "# Our hooks. Please keep sorted.") {
		t.Errorf("the user's comment was destroyed:\n%s", got)
	}
	// No native hook alongside it.
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "pre-commit")); !os.IsNotExist(err) {
		t.Error("a native hook was installed alongside the framework — validate would run twice")
	}
}

// TestInitAgentHookIsIdempotent: --agent re-runs forever. Neither path may
// duplicate the hook or rewrite a file that is already correct.
func TestInitAgentHookIsIdempotent(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CLAUDECODE", "")
		gitRepo(t, dir)
		path := filepath.Join(dir, ".git", "hooks", "pre-commit")

		if err := runInitAgent(dir, false, io.Discard); err != nil {
			t.Fatalf("first: %v", err)
		}
		first := readFile(t, path)

		var out strings.Builder
		if err := runInitAgent(dir, false, &out); err != nil {
			t.Fatalf("second: %v", err)
		}
		if readFile(t, path) != first {
			t.Error("re-running rewrote the hook")
		}
		if !strings.Contains(out.String(), "already runs") {
			t.Errorf("a no-op re-run should say so, got: %s", out.String())
		}
	})

	t.Run("framework", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CLAUDECODE", "")
		gitRepo(t, dir)
		if err := os.WriteFile(filepath.Join(dir, preCommitConfig), []byte("repos: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := runInitAgent(dir, false, io.Discard); err != nil {
			t.Fatalf("first: %v", err)
		}
		first := readFile(t, filepath.Join(dir, preCommitConfig))

		if err := runInitAgent(dir, false, io.Discard); err != nil {
			t.Fatalf("second: %v", err)
		}
		second := readFile(t, filepath.Join(dir, preCommitConfig))

		if first != second {
			t.Errorf("re-running rewrote the config:\n--- first ---\n%s\n--- second ---\n%s", first, second)
		}
		if n := strings.Count(second, "id: "+hookID); n != 1 {
			t.Errorf("expected exactly 1 rowshape hook after two runs, got %d:\n%s", n, second)
		}
	})
}

// TestInitAgentUpdatesStaleFrameworkHook: an entry from an older rowshape is
// corrected in place, wherever the user moved it in their list — not appended
// beside, which would run validate twice.
func TestInitAgentUpdatesStaleFrameworkHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	gitRepo(t, dir)
	stale := `repos:
  - repo: local
    hooks:
      - id: ` + hookID + `
        name: rowshape
        entry: rowshape check --old-flag
        language: system
  - repo: https://github.com/psf/black
    rev: 24.1.0
    hooks:
      - id: black
`
	if err := os.WriteFile(filepath.Join(dir, preCommitConfig), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInitAgent(dir, false, io.Discard); err != nil {
		t.Fatalf("init --agent: %v", err)
	}
	got := readFile(t, filepath.Join(dir, preCommitConfig))

	if strings.Contains(got, "--old-flag") {
		t.Errorf("the stale entry was not corrected:\n%s", got)
	}
	if n := strings.Count(got, "id: "+hookID); n != 1 {
		t.Errorf("expected exactly 1 rowshape hook, got %d — an upgrade must replace, not append:\n%s", n, got)
	}
	if !strings.Contains(got, "id: black") {
		t.Errorf("correcting our entry dropped a neighbor:\n%s", got)
	}
}

// TestInitAgentRefusesForeignHook: someone else's pre-commit hook is someone
// else's whole workflow. Replacing it to install a backstop is a worse outcome
// than not installing one.
func TestInitAgentRefusesForeignHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")
	gitRepo(t, dir)
	mkdir(t, filepath.Join(dir, ".git", "hooks"))
	foreign := "#!/bin/sh\n# our carefully tuned hook\nmake lint\n"
	path := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(path, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err := runInitAgent(dir, false, &out)
	if err == nil {
		t.Fatal("init --agent should fail rather than silently replace a hook it did not write")
	}
	if readFile(t, path) != foreign {
		t.Errorf("the user's hook was modified:\n%s", readFile(t, path))
	}
	if !strings.Contains(out.String(), "rowshape validate") {
		t.Errorf("a refusal must say what to add by hand, got: %s", out.String())
	}
}

// TestInitAgentSkipsHookOutsideGitRepo: no .git, nothing to hook. That is not an
// error — the MCP config and the rule are still worth writing.
func TestInitAgentSkipsHookOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "")

	var out strings.Builder
	if err := runInitAgent(dir, false, &out); err != nil {
		t.Fatalf("a non-git directory must not be a failure: %v", err)
	}
	if !strings.Contains(out.String(), "not a git repo") {
		t.Errorf("init should say why it skipped the hook, got: %s", out.String())
	}
	// The other two writes still happened.
	hasRule(t, readFile(t, filepath.Join(dir, "AGENTS.md")), "AGENTS.md")
}

// TestHookMigrationPatternPerRunner: the pattern decides whether the hook fires at
// all. A pattern that misses the repo's layout is a backstop that never runs.
func TestHookMigrationPatternPerRunner(t *testing.T) {
	cases := []struct {
		runner string
		match  string
		reject string
	}{
		{"alembic", "alembic/versions/a1b2_add_col.py", "src/app.py"},
		{"prisma", "prisma/migrations/20240101_init/migration.sql", "src/index.ts"},
		{"drizzle", "drizzle/0001_init.sql", "src/schema.ts"},
		{"rawsql", "migrations/001_init.sql", "README.md"},
		{"", "migrations/001_init.sql", "README.md"}, // undetected runner still fires
	}
	for _, c := range cases {
		name := c.runner
		if name == "" {
			name = "undetected"
		}
		t.Run(name, func(t *testing.T) {
			re := regexpMustCompile(t, migrationPattern(c.runner))
			if !re.MatchString(c.match) {
				t.Errorf("%s pattern %q should match %q — a hook that never fires is no hook", name, re, c.match)
			}
			if re.MatchString(c.reject) {
				t.Errorf("%s pattern %q should not match %q — it would hydrate a database for nothing", name, re, c.reject)
			}
		})
	}
}

// regexpMustCompile compiles a pattern, failing the test rather than panicking.
func regexpMustCompile(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("pattern %q does not compile: %v", pattern, err)
	}
	return re
}

// posixShell locates a shell that can execute the hook.
//
// The hook is a /bin/sh script; the tests below assert what git will actually do
// with it. Gating those on GOOS would skip them on Windows even though Git for
// Windows ships the very sh that runs them there — and skipping the tests that
// prove "blocks on FAIL, does not block on a tool error" is skipping the whole
// point of the hook. Gate on the shell being reachable instead of on the OS.
func posixShell(t *testing.T) string {
	t.Helper()
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on PATH; the hook is a /bin/sh script")
	}
	return sh
}

// hookPATH builds the PATH the hook runs under: the stub bin first, so the test's
// fake rowshape/git win, plus the directory holding the shell's own utilities so
// `grep` resolves.
//
// Pointing PATH at the stub dir alone looks tidier and silently breaks the tests:
// grep goes missing, the "did a migration change?" gate finds nothing, and the
// hook exits 0 before ever reaching rowshape — so every "lets the commit through"
// case passes for the wrong reason.
func hookPATH(t *testing.T, bin string) string {
	t.Helper()
	dirs := []string{bin}
	for _, util := range []string{"grep"} {
		p, err := osexec.LookPath(util)
		if err != nil {
			t.Skipf("the hook needs %s on PATH: %v", util, err)
		}
		dirs = append(dirs, filepath.Dir(p))
	}
	return "PATH=" + strings.Join(dirs, string(os.PathListSeparator))
}
