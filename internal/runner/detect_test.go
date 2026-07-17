package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates a file (and parent dirs) with trivial content, for building
// project-shape fixtures in a temp dir.
func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// argv joins a command's arguments for substring assertions.
func argv(args []string) string { return strings.Join(args, " ") }

// hasEnv reports whether env contains KEY=value for key.
func hasEnv(env []string, key string) (string, bool) {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return strings.TrimPrefix(e, key+"="), true
		}
	}
	return "", false
}

// TestDetectByToolSignature: each supported runner is auto-detected from its
// signature file, and shells out to that tool's own binary (PRD §8.1, §13).
func TestDetectByToolSignature(t *testing.T) {
	const dsn = "postgres://u@localhost:5433/disposable"
	cases := []struct {
		name    string
		sig     string // signature file to create, relative to project root
		want    Kind
		wantArg string // substring the apply command must contain
		wantBin string // the external binary rowshape shells out to
	}{
		{"alembic", "alembic.ini", Alembic, "upgrade head", "alembic"},
		{"prisma", filepath.Join("prisma", "schema.prisma"), Prisma, "migrate deploy", "prisma"},
		{"drizzle", "drizzle.config.ts", Drizzle, "migrate", "drizzle-kit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, c.sig))

			r, err := Detect(dir)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if r.Kind() != c.want {
				t.Fatalf("kind = %s, want %s", r.Kind(), c.want)
			}
			cmd := r.ApplyCmd(context.Background(), dsn)
			if cmd.Args[0] != c.wantBin {
				t.Errorf("shells out to %q, want the external tool %q (orchestrate, don't reimplement)", cmd.Args[0], c.wantBin)
			}
			if !strings.Contains(argv(cmd.Args), c.wantArg) {
				t.Errorf("apply command %q missing %q", argv(cmd.Args), c.wantArg)
			}
			if got, ok := hasEnv(cmd.Env, "DATABASE_URL"); !ok || got != dsn {
				t.Errorf("DATABASE_URL = %q (present=%v), want the target dsn %q", got, ok, dsn)
			}
		})
	}
}

// TestDetectRawSQL: a bare directory of .sql files is detected as raw SQL and
// applied with psql -f, in filename order, ON_ERROR_STOP so a failure halts.
func TestDetectRawSQL(t *testing.T) {
	const dsn = "postgres://u@localhost:5433/disposable"
	dir := t.TempDir()
	// Deliberately out of order on disk to prove they are sorted.
	write(t, filepath.Join(dir, "migrations", "002_add_email.sql"))
	write(t, filepath.Join(dir, "migrations", "001_init.sql"))
	write(t, filepath.Join(dir, "migrations", "README.md")) // ignored

	r, err := Detect(dir)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if r.Kind() != RawSQL {
		t.Fatalf("kind = %s, want rawsql", r.Kind())
	}
	raw := r.(*rawSQLRunner)
	if got := raw.Files(); len(got) != 2 || got[0] != "001_init.sql" || got[1] != "002_add_email.sql" {
		t.Fatalf("files = %v, want sorted [001_init.sql 002_add_email.sql]", got)
	}
	cmd := r.ApplyCmd(context.Background(), dsn)
	if cmd.Args[0] != "psql" {
		t.Errorf("raw SQL must shell out to psql, got %q", cmd.Args[0])
	}
	a := argv(cmd.Args)
	if !strings.Contains(a, "ON_ERROR_STOP=1") {
		t.Errorf("psql invocation %q must set ON_ERROR_STOP=1", a)
	}
	if !strings.Contains(a, "-d "+dsn) {
		t.Errorf("psql invocation %q must target the dsn", a)
	}
	// Applied in order: 001 before 002.
	if i, j := strings.Index(a, "001_init.sql"), strings.Index(a, "002_add_email.sql"); i < 0 || j < 0 || i > j {
		t.Errorf("psql must apply files in order, got %q", a)
	}
}

// TestFrameworkWinsOverRawSQL: a project that uses a framework AND happens to
// keep .sql files is detected as the framework, not raw SQL (detection order).
func TestFrameworkWinsOverRawSQL(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "alembic.ini"))
	write(t, filepath.Join(dir, "migrations", "001_init.sql"))

	r, err := Detect(dir)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if r.Kind() != Alembic {
		t.Errorf("kind = %s, want alembic (framework beats raw-SQL fallback)", r.Kind())
	}
}

// TestDetectNoneIsActionable: an unrecognized project errors, naming the
// supported runners rather than guessing.
func TestDetectNoneIsActionable(t *testing.T) {
	_, err := Detect(t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no runner is detected")
	}
	for _, want := range []string{"Alembic", "Prisma", "Drizzle", "--runner"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("detect error %q should mention %q", err, want)
		}
	}
}

// TestForKindOverride: detection is explicitly overridable. ForKind selects the
// named runner even against a project whose signature points elsewhere, and a
// raw-SQL override still requires .sql files to exist.
func TestForKindOverride(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "alembic.ini")) // would auto-detect as alembic

	// Override to prisma despite the alembic signature.
	r, err := ForKind(dir, Prisma)
	if err != nil {
		t.Fatalf("ForKind prisma: %v", err)
	}
	if r.Kind() != Prisma {
		t.Errorf("override kind = %s, want prisma", r.Kind())
	}

	// Raw-SQL override with no .sql files errors loudly.
	if _, err := ForKind(dir, RawSQL); err == nil {
		t.Error("raw-SQL override with no .sql files should error")
	}

	// Unknown kind errors.
	if _, err := ForKind(dir, Kind("flyway")); err == nil {
		t.Error("unknown runner kind should error")
	}
}
