package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// CR-T12. All three framework runners built their child environment with
// `append(os.Environ(), "DATABASE_URL="+dsn)`, leaving two entries for the same
// key when the invoking shell already exported one.
//
// That is NOT the wrong-DSN bug it appears to be: os/exec documents that with
// duplicate keys "only the last value in the slice for each duplicate key is
// used", and running it confirms the child receives rowshape's DSN. These tests
// pin the stronger property anyway — exactly one entry, so the guarantee is
// visible at the call site rather than inherited from a dedup rule in the
// standard library.

func databaseURLEntries(env []string) []string {
	var out []string
	for _, kv := range env {
		if strings.HasPrefix(kv, databaseURLVar+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func TestEnvWithDSNReplacesInheritedURL(t *testing.T) {
	t.Setenv(databaseURLVar, "postgres://stale-from-the-shell/prod")

	got := databaseURLEntries(envWithDSN("postgres://disposable/rowshape"))
	if len(got) != 1 {
		t.Fatalf("expected exactly one %s entry, got %d: %v", databaseURLVar, len(got), got)
	}
	if got[0] != databaseURLVar+"=postgres://disposable/rowshape" {
		t.Errorf("child would use %q, want rowshape's disposable target", got[0])
	}
	if strings.Contains(got[0], "prod") {
		t.Errorf("the inherited production DSN survived: %q", got[0])
	}
}

func TestEnvWithDSNWhenNoneInherited(t *testing.T) {
	t.Setenv(databaseURLVar, "")
	got := databaseURLEntries(envWithDSN("postgres://disposable/rowshape"))
	if len(got) != 1 {
		t.Fatalf("expected exactly one %s entry, got %d: %v", databaseURLVar, len(got), got)
	}
}

// TestEnvWithDSNKeepsOtherVars: replacing DATABASE_URL must not strip the rest
// of the environment — PATH in particular, or the runner binary is not found.
func TestEnvWithDSNKeepsOtherVars(t *testing.T) {
	t.Setenv("ROWSHAPE_ENV_CANARY", "kept")
	env := envWithDSN("postgres://disposable/rowshape")
	var canary bool
	for _, kv := range env {
		if kv == "ROWSHAPE_ENV_CANARY=kept" {
			canary = true
		}
	}
	if !canary {
		t.Error("unrelated environment variables must survive")
	}
}

// TestAllFrameworkRunnersUseTheHelper drives each runner's real ApplyCmd, so a
// future runner that hand-rolls the append is caught here rather than by review.
func TestAllFrameworkRunnersUseTheHelper(t *testing.T) {
	t.Setenv(databaseURLVar, "postgres://stale-from-the-shell/prod")
	const dsn = "postgres://disposable/rowshape"
	ctx := context.Background()
	root := t.TempDir()

	cmds := map[string]*exec.Cmd{
		"alembic": (&alembicRunner{root: root}).ApplyCmd(ctx, dsn),
		"prisma":  (&prismaRunner{root: root}).ApplyCmd(ctx, dsn),
		"drizzle": (&drizzleRunner{root: root}).ApplyCmd(ctx, dsn),
	}
	for name, cmd := range cmds {
		t.Run(name, func(t *testing.T) {
			got := databaseURLEntries(cmd.Env)
			if len(got) != 1 {
				t.Fatalf("expected exactly one %s entry, got %d: %v", databaseURLVar, len(got), got)
			}
			if !strings.Contains(got[0], "disposable") {
				t.Errorf("child would use %q, want the disposable target", got[0])
			}
			if strings.Contains(got[0], "prod") {
				t.Errorf("the inherited production DSN survived: %q", got[0])
			}
		})
	}
}
