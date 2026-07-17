package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

const testAdminEnv = "ROWSHAPE_TEST_PG_DSN"

// TestValidateRefusesSourceHost: validate hard-refuses a target whose host
// matches the fixture's source host, BEFORE it connects (INV-BLAST-RADIUS-ZERO,
// PRD §11). It is exercised through runValidate, which computes the refusal from
// the fixture's meta.source and the target URL's host.
func TestValidateRefusesSourceHost(t *testing.T) {
	const prodHost = "prod-db.internal.example.com"
	dir := t.TempDir()
	fx := filepath.Join(dir, "rowshape.yaml")
	// meta.source is the salted hash of the production host (RFC §8.4).
	writeFile(t, fx, `rowshape_fixture: "1"
meta:
  id: t
  engine: {name: postgres, version: "16"}
  source: `+profile.HashSource(prodHost)+`
tables:
  public.users:
    rows: {value: 10, confidence: exact}
    columns:
      email: {type: text, nullable: true}
`)
	mig := filepath.Join(dir, "m.sql")
	writeFile(t, mig, "ALTER TABLE public.users ALTER COLUMN email SET NOT NULL;")

	// The ephemeral admin URL points at the SAME host the fixture was pulled from.
	opts := &validateOptions{
		fixturePath: fx,
		migrations:  mig,
		ephemeral:   "postgres://admin:secret@" + prodHost + ":5432/postgres",
		scale:       1.0,
	}
	_, stderr := captureOutput(t, func() error { return runValidate(opts) })

	if !strings.Contains(stderr, "refusing to run against the fixture's source host") {
		t.Errorf("expected a host-match refusal on stderr, got:\n%s", stderr)
	}
}

// TestCheckHostRefusal pins the refusal predicate directly (RFC §8.4, PRD §11):
// the target host is hashed with the same salt as meta.source and compared.
func TestCheckHostRefusal(t *testing.T) {
	source := profile.HashSource("prod.example.com")
	if err := validate.CheckHost(source, "prod.example.com"); err == nil {
		t.Error("must refuse when the target host matches the fixture source")
	}
	if err := validate.CheckHost(source, "localhost"); err != nil {
		t.Errorf("must allow a different host, got %v", err)
	}
	if err := validate.CheckHost("", "localhost"); err != nil {
		t.Errorf("no source host means nothing to collide with, got %v", err)
	}
}

// TestValidateNoCloudEgress asserts structurally that the validate code path
// imports no network/cloud client — validate never calls the cloud
// (INV-NEVER-GATE-VALIDATE). Scanning imports is a deterministic proof that no
// egress can happen, stronger than observing one run.
func TestValidateNoCloudEgress(t *testing.T) {
	forbidden := []string{"net/http", "net/rpc", "cloud", "aws", "gcp", "grpc", "google.golang.org"}
	files := []string{"validate.go"}
	// Include the whole internal/validate package.
	pkg := filepath.Join("..", "internal", "validate")
	entries, err := os.ReadDir(pkg)
	if err != nil {
		t.Fatalf("read internal/validate: %v", err)
	}
	paths := append([]string(nil), files...)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			paths = append(paths, filepath.Join(pkg, e.Name()))
		}
	}
	fset := token.NewFileSet()
	for _, p := range paths {
		af, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		for _, imp := range af.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.Contains(path, bad) {
					t.Errorf("%s imports %q — validate must make no network/cloud call (INV-NEVER-GATE-VALIDATE)", p, path)
				}
			}
		}
	}
}

// TestEmitResultTwoRenderings: --json and the default human output are two
// renderings of the SAME struct (INV-VERDICT-SHAPE). The JSON round-trips to an
// equal Result; the human text surfaces the verdict.
func TestEmitResultTwoRenderings(t *testing.T) {
	r := verdict.Result{
		Rowshape: verdict.Rowshape,
		Verdict:  verdict.VerdictWarn,
		Fixture:  verdict.FixtureRef{ID: "prod@2026-07-14", Digest: "sha256:abc"},
		Findings: []verdict.Finding{{Code: "RS-DATA-014", Severity: verdict.SeverityWarn, Title: "cannot confirm uniqueness", Remediation: "rowshape pull --exact public.users.email"}},
	}

	var jsonBuf bytes.Buffer
	if err := emitResult(&jsonBuf, r, true); err != nil {
		t.Fatalf("emit json: %v", err)
	}
	var back verdict.Result
	if err := decodeJSON(jsonBuf.Bytes(), &back); err != nil {
		t.Fatalf("json did not round-trip: %v", err)
	}
	if back.Verdict != r.Verdict || len(back.Findings) != 1 || back.Findings[0].Code != "RS-DATA-014" {
		t.Errorf("json rendering diverged from the struct: %+v", back)
	}

	var humanBuf bytes.Buffer
	if err := emitResult(&humanBuf, r, false); err != nil {
		t.Fatalf("emit human: %v", err)
	}
	human := humanBuf.String()
	for _, want := range []string{"WARN", "RS-DATA-014", "rowshape pull --exact public.users.email"} {
		if !strings.Contains(human, want) {
			t.Errorf("human rendering missing %q:\n%s", want, human)
		}
	}
}

// TestValidateEndToEnd runs the full pipeline against a real Postgres when
// ROWSHAPE_TEST_PG_DSN is set: hydrate a disposable database from a corpus
// fixture, apply the migration, capture, and emit a well-formed verdict. With no
// analyzers registered yet (P2-T8+), a cleanly-applying safe migration is PASS.
func TestValidateEndToEnd(t *testing.T) {
	admin := os.Getenv(testAdminEnv)
	if admin == "" {
		t.Skipf("set %s to a Postgres admin connection to run the end-to-end validate", testAdminEnv)
	}
	// Sanity: the admin connection actually works.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Skipf("admin connection unusable: %v", err)
	}
	_ = conn.Close(ctx)

	opts := &validateOptions{
		fixturePath: filepath.Join("..", "corpus", "cases", "capping-exact-not-null", "fixture.yaml"),
		migrations:  filepath.Join("..", "corpus", "cases", "capping-exact-not-null", "migration.sql"),
		ephemeral:   admin,
		scale:       1.0,
		asJSON:      true,
	}
	stdout, stderr := captureOutput(t, func() error { return runValidate(opts) })

	if !strings.Contains(stdout, `"verdict"`) || !strings.Contains(stdout, `"rowshape"`) {
		t.Errorf("expected a JSON verdict on stdout, got:\n%s\nstderr:\n%s", stdout, stderr)
	}
	var res verdict.Result
	if err := decodeJSON([]byte(stdout), &res); err != nil {
		t.Fatalf("verdict is not valid JSON: %v\n%s", err, stdout)
	}
	// A SET NOT NULL against exact-0%% nulls applies cleanly; no analyzers yet → PASS.
	if res.Verdict != verdict.VerdictPass {
		t.Errorf("verdict = %s, want PASS (clean apply, no analyzers). stderr:\n%s", res.Verdict, stderr)
	}
}

// --- test helpers ---

func decodeJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureOutput redirects os.Stdout/os.Stderr around fn and returns what it
// wrote to each. runValidate writes directly to the process streams.
func captureOutput(t *testing.T, fn func() error) (stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	done := make(chan struct{})
	var outBuf, errBuf bytes.Buffer
	go func() {
		_, _ = outBuf.ReadFrom(rOut)
		_, _ = errBuf.ReadFrom(rErr)
		close(done)
	}()

	err := fn()
	var ee *ExitError
	if err != nil && !errors.As(err, &ee) {
		// Non-exit errors are unexpected from runValidate (it maps to ExitError).
		t.Logf("runValidate returned non-exit error: %v", err)
	}

	_ = wOut.Close()
	_ = wErr.Close()
	<-done
	os.Stdout, os.Stderr = origOut, origErr
	return outBuf.String(), errBuf.String()
}
