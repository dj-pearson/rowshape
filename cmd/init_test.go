package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitDetectsRunners: init detects the migration runner from the repo layout
// (alembic.ini, prisma/schema.prisma, a drizzle config, or a SQL migrations
// directory) and records it in the config (PRD §8.1, §8).
func TestInitDetectsRunners(t *testing.T) {
	cases := []struct {
		name   string
		marker string // file to create, relative to the repo root
		want   string
	}{
		{"alembic", "alembic.ini", "alembic"},
		{"prisma", filepath.Join("prisma", "schema.prisma"), "prisma"},
		{"drizzle", "drizzle.config.ts", "drizzle"},
		{"rawsql", filepath.Join("migrations", "001_init.sql"), "rawsql"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMarker(t, filepath.Join(dir, c.marker))

			if err := runInit(dir, false); err != nil {
				t.Fatalf("init: %v", err)
			}
			cfg := readConfig(t, dir)
			if !strings.Contains(cfg, `runner = "`+c.want+`"`) {
				t.Errorf("config should record runner %q, got:\n%s", c.want, cfg)
			}
			if !strings.Contains(cfg, `engine = "postgres"`) {
				t.Errorf("config should record the engine, got:\n%s", cfg)
			}
		})
	}
}

// TestInitUnknownRunner: with no recognizable layout, init still writes a config,
// naming the runner as not-detected for the user to fill in — it never fails to
// scaffold.
func TestInitUnknownRunner(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := readConfig(t, dir)
	if !strings.Contains(cfg, `runner = "unknown"`) {
		t.Errorf("config should record an unknown runner placeholder, got:\n%s", cfg)
	}
}

// TestInitIdempotent: re-running init leaves the user's config edits untouched —
// no clobber — and does not error.
func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, filepath.Join(dir, "alembic.ini"))

	if err := runInit(dir, false); err != nil {
		t.Fatalf("first init: %v", err)
	}
	// Simulate a user edit.
	path := filepath.Join(dir, configFile)
	edited := readConfig(t, dir) + "\n# my custom note\nprivacy = \"strict\"\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-run without --force: must not clobber, must not error.
	if err := runInit(dir, false); err != nil {
		t.Fatalf("re-run must not error, got: %v", err)
	}
	if got := readConfig(t, dir); got != edited {
		t.Errorf("re-run clobbered the user's config:\n--- want ---\n%s\n--- got ---\n%s", edited, got)
	}

	// --force regenerates it (discarding the edit).
	if err := runInit(dir, true); err != nil {
		t.Fatalf("forced re-run: %v", err)
	}
	if got := readConfig(t, dir); strings.Contains(got, "my custom note") {
		t.Error("--force should regenerate the config, but the old edit remained")
	}
}

// TestInitMakesNoDBConnection: init works with no database reachable and no
// connection env — detection is offline (PRD §8.1). A bogus PG* environment must
// not matter, because init never connects.
func TestInitMakesNoDBConnection(t *testing.T) {
	t.Setenv("PGHOST", "203.0.113.1") // TEST-NET-3, unroutable
	t.Setenv("PGPORT", "1")
	t.Setenv("PGCONNECT_TIMEOUT", "1")
	dir := t.TempDir()
	writeMarker(t, filepath.Join(dir, "alembic.ini"))

	if err := runInit(dir, false); err != nil {
		t.Fatalf("init must succeed offline, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, configFile)); err != nil {
		t.Errorf("init should have written the config offline: %v", err)
	}
}

func writeMarker(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readConfig(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(b)
}
