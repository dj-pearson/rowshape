package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
)

var pvCounter atomic.Int64

// tempDB creates a uniquely-named disposable database on the test server and
// returns its URL plus a cleanup. Skips when ROWSHAPE_TEST_PG_DSN is unset.
func tempDB(t *testing.T) (string, func()) {
	t.Helper()
	admin := os.Getenv(testAdminEnv)
	if admin == "" {
		t.Skipf("set %s to run plan/verify live-target tests", testAdminEnv)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Skipf("admin connect: %v", err)
	}
	defer conn.Close(ctx)

	name := fmt.Sprintf("rowshape_pv_%d_%d", os.Getpid(), pvCounter.Add(1))
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
	return dbURL(t, admin, name), cleanup
}

func dbURL(t *testing.T, admin, name string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	auth := cfg.User
	if cfg.Password != "" {
		auth += ":" + cfg.Password
	}
	return fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable", auth, cfg.Host, cfg.Port, name)
}

func exec(t *testing.T, url, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect %s: %v", redactURL(url), err)
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
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3)`, schema, tbl, col).Scan(&exists)
	if err != nil {
		t.Fatalf("column check: %v", err)
	}
	return exists
}

// TestMarkExactUpgradesFacts: reading a PROVIDED live target upgrades the facts
// to `exact` — the data is ground truth, not a sample (PRD §15).
func TestMarkExactUpgradesFacts(t *testing.T) {
	f, err := fixture.Parse([]byte(`rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 1000, confidence: estimated}
    columns:
      email: {type: text, nullable: false, distinct: {value: 990, confidence: measured}, null_fraction: {value: 0.0, confidence: estimated}}
    references:
      - {column: org_id, to: public.orgs.id, on_delete: cascade, fanout: {mean: 3, max: 50, confidence: measured}, orphan_fraction: {value: 0.0, confidence: estimated}}
`))
	if err != nil {
		t.Fatal(err)
	}
	validate.MarkExact(f)
	tbl := f.Tables["public.users"]
	if tbl.Rows.Confidence != fixture.Exact {
		t.Errorf("rows confidence = %s, want exact", tbl.Rows.Confidence)
	}
	col := tbl.Columns["email"]
	if col.Distinct.Confidence != fixture.Exact || col.NullFraction.Confidence != fixture.Exact {
		t.Errorf("column facts not upgraded: distinct=%s null_fraction=%s", col.Distinct.Confidence, col.NullFraction.Confidence)
	}
	if tbl.References[0].OrphanFraction.Confidence != fixture.Exact || tbl.References[0].Fanout.Confidence != fixture.Exact {
		t.Error("reference facts not upgraded to exact")
	}
}

// TestPlanDryRunAppliesNothing: plan reports the diff of a migration against the
// live schema but applies nothing (read-only) — the new column stays absent.
func TestPlanDryRunAppliesNothing(t *testing.T) {
	url, cleanup := tempDB(t)
	defer cleanup()
	exec(t, url, "CREATE TABLE public.t (id int)")

	dir := t.TempDir()
	mig := filepath.Join(dir, "m.sql")
	writeFile(t, mig, "ALTER TABLE public.t ADD COLUMN c int;")

	stdout, stderr := captureOutput(t, func() error {
		return runPlan(&planOptions{against: url, migrations: mig})
	})
	if !strings.Contains(stdout, "add column t.c") {
		t.Errorf("plan output should describe the add-column diff, got:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if columnExists(t, url, "public.t", "c") {
		t.Error("plan must apply nothing, but column c was added to the live target")
	}
}

// TestVerifyDriftReadOnly: verify reports drift when the live target is missing
// an expected column, exits non-zero, and writes nothing.
func TestVerifyDriftReadOnly(t *testing.T) {
	url, cleanup := tempDB(t)
	defer cleanup()
	exec(t, url, "CREATE TABLE public.users (id bigint NOT NULL, email text NOT NULL)")

	dir := t.TempDir()
	fx := filepath.Join(dir, "rowshape.yaml")
	// Intent declares a `phone` column the live target does not have.
	writeFile(t, fx, `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 0, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
      email: {type: text, nullable: false}
      phone: {type: text, nullable: false}
`)
	stdout, _ := captureOutput(t, func() error {
		return runVerify(&verifyOptions{against: url, fixturePath: fx})
	})
	if !strings.Contains(stdout, "users.phone") || !strings.Contains(stdout, "DRIFT") {
		t.Errorf("verify should report the missing phone column as drift, got:\n%s", stdout)
	}
	// Read-only: verify must not have created the missing column.
	if columnExists(t, url, "public.users", "phone") {
		t.Error("verify must be read-only, but it created the phone column")
	}
}

// TestVerifyMatch: verify exits cleanly when reality matches intent.
func TestVerifyMatch(t *testing.T) {
	url, cleanup := tempDB(t)
	defer cleanup()
	exec(t, url, "CREATE TABLE public.users (id bigint NOT NULL, email text NOT NULL)")

	dir := t.TempDir()
	fx := filepath.Join(dir, "rowshape.yaml")
	writeFile(t, fx, `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 0, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
      email: {type: text, nullable: false}
`)
	stdout, stderr := captureOutput(t, func() error {
		return runVerify(&verifyOptions{against: url, fixturePath: fx})
	})
	if !strings.Contains(stdout, "matches intent") {
		t.Errorf("verify should report a match, got:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
