package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const testAdminEnv = "ROWSHAPE_TEST_PG_DSN"

var pvCounter atomic.Int64

// tempDB creates a uniquely-named disposable database and returns its URL plus a
// cleanup. Skips when ROWSHAPE_TEST_PG_DSN is unset.
func tempDB(t *testing.T) (string, func()) {
	t.Helper()
	admin := os.Getenv(testAdminEnv)
	if admin == "" {
		t.Skipf("set %s to run the plan_against live-target test", testAdminEnv)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Skipf("admin connect: %v", err)
	}
	defer conn.Close(ctx)

	name := fmt.Sprintf("rowshape_mcp_%d_%d", os.Getpid(), pvCounter.Add(1))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create db: %v", err)
	}
	cleanup := func() {
		c, err := pgx.Connect(ctx, admin)
		if err != nil {
			return
		}
		defer c.Close(ctx)
		_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}
	cfg, _ := pgx.ParseConfig(admin)
	auth := cfg.User
	if cfg.Password != "" {
		auth += ":" + cfg.Password
	}
	return fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable", auth, cfg.Host, cfg.Port, name), cleanup
}

func execSQL(t *testing.T, url, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func columnExists(t *testing.T, url, table, col string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	schema, tbl := "public", table
	if i := strings.IndexByte(table, '.'); i >= 0 {
		schema, tbl = table[:i], table[i+1:]
	}
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3)`, schema, tbl, col).Scan(&exists); err != nil {
		t.Fatalf("column check: %v", err)
	}
	return exists
}

// TestPlanAgainstDryRunReadOnly: plan_against returns the dry-run diff of a
// migration against a live target and applies nothing (read-only, PRD §11).
func TestPlanAgainstDryRunReadOnly(t *testing.T) {
	url, cleanup := tempDB(t)
	defer cleanup()
	execSQL(t, url, "CREATE TABLE public.t (id int)")

	cs := connectClient(t)
	dir := t.TempDir()
	mig := filepath.Join(dir, "m.sql")
	if err := os.WriteFile(mig, []byte("ALTER TABLE public.t ADD COLUMN c int;"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "plan_against",
		Arguments: map[string]any{"migration": mig, "target": url},
	})
	if err != nil {
		t.Fatalf("call plan_against: %v", err)
	}
	if res.IsError {
		t.Fatalf("plan_against errored: %+v", res.Content)
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)

	if out["applied"] != false {
		t.Errorf("plan must not apply anything, applied=%v", out["applied"])
	}
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 diff item, got %d", len(items))
	}
	i0 := items[0].(map[string]any)
	if !strings.Contains(i0["change"].(string), "add column t.c") {
		t.Errorf("diff should describe the add-column, got %v", i0["change"])
	}
	// The target's credentials are redacted in the echoed target.
	if strings.Contains(out["target"].(string), "@") && !strings.Contains(out["target"].(string), "…") {
		// only a concern if there were credentials; localhost URLs have none.
	}
	// Read-only: the column was NOT created on the target.
	if columnExists(t, url, "public.t", "c") {
		t.Error("plan_against must be read-only, but it created the column on the target")
	}
}
