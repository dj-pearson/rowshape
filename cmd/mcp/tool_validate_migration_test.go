package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rowshape/rowshape/internal/verdict"
)

// callValidate calls validate_migration and returns the compact structured output.
func callValidate(t *testing.T, cs *sdk.ClientSession, fixture, migration string) (*sdk.CallToolResult, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "validate_migration",
		Arguments: map[string]any{"fixture": fixture, "migration": migration},
	})
	if err != nil {
		t.Fatalf("call validate_migration: %v", err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return res, out
}

func writeFile2(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestValidateMigrationCappingPreserved: a migration whose safety rests on an
// unproven fact never reports PASS — the tool honors confidence capping end to
// end (RFC §7.4). ADD UNIQUE against a column with no proven uniqueness → WARN.
func TestValidateMigrationCappingPreserved(t *testing.T) {
	cs := connectClient(t)
	dir := t.TempDir()
	fx := writeFile2(t, dir, "rowshape.yaml", `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 800000, confidence: exact}
    columns:
      email: {type: text, nullable: false, distinct: {value: 799000, confidence: estimated}}
`)
	// Inline SQL — the agent passes the migration it just wrote, unsaved.
	_, out := callValidate(t, cs, fx, "ALTER TABLE public.users ADD CONSTRAINT u UNIQUE (email);")

	if out["verdict"] != verdict.VerdictWarn {
		t.Fatalf("verdict = %v, want WARN (capping: uniqueness unproven, never PASS)", out["verdict"])
	}
	if ec, _ := out["exit_code"].(float64); int(ec) != verdict.ExitWarnOnly {
		t.Errorf("exit_code = %v, want %d (WARN-only)", out["exit_code"], verdict.ExitWarnOnly)
	}
	findings, _ := out["findings"].([]any)
	// Two distinct hazards on the same statement: the data cannot certify the
	// build (RS-DATA-014, capping) AND the build itself locks the table
	// (RS-INDEX-002). Both are WARN, so the verdict stays WARN.
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (RS-DATA-014 + RS-INDEX-002), got %d: %v", len(findings), findings)
	}
	var dataFinding map[string]any
	codes := map[string]bool{}
	for _, f := range findings {
		fm := f.(map[string]any)
		codes[fm["code"].(string)] = true
		if fm["code"] == "RS-DATA-014" {
			dataFinding = fm
		}
	}
	if !codes["RS-DATA-014"] || !codes["RS-INDEX-002"] {
		t.Fatalf("want both RS-DATA-014 and RS-INDEX-002, got %v", codes)
	}
	// Compact: findings carry a code and an explain path, NOT remediation prose.
	if _, hasRemediation := dataFinding["remediation"]; hasRemediation {
		t.Error("compact finding must not inline remediation prose")
	}
	if !strings.Contains(dataFinding["explain"].(string), "RS-DATA-014") {
		t.Errorf("finding should carry the explain_finding expansion path, got %v", dataFinding["explain"])
	}
}

// TestValidateMigrationPass: a safe migration reports PASS with no findings.
func TestValidateMigrationPass(t *testing.T) {
	cs := connectClient(t)
	dir := t.TempDir()
	fx := writeFile2(t, dir, "rowshape.yaml", `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.users:
    rows: {value: 100, confidence: exact}
    columns:
      email: {type: text, nullable: true, null_fraction: {value: 0.0, confidence: exact}}
`)
	// SET NOT NULL against a proven-zero null_fraction is safe.
	_, out := callValidate(t, cs, fx, "ALTER TABLE public.users ALTER COLUMN email SET NOT NULL;")
	if out["verdict"] != verdict.VerdictPass {
		t.Errorf("verdict = %v, want PASS", out["verdict"])
	}
}

// TestValidateMigrationLockFinding: a rewrite fires RS-LOCK with a duration
// bucket, returned compactly.
func TestValidateMigrationLockFinding(t *testing.T) {
	cs := connectClient(t)
	dir := t.TempDir()
	fx := writeFile2(t, dir, "rowshape.yaml", `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.orders:
    rows: {value: 5000000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
`)
	_, out := callValidate(t, cs, fx, "ALTER TABLE public.orders ADD COLUMN token uuid NOT NULL DEFAULT gen_random_uuid();")
	if out["verdict"] != verdict.VerdictWarn {
		t.Fatalf("verdict = %v, want WARN (RS-LOCK)", out["verdict"])
	}
	findings := out["findings"].([]any)
	f0 := findings[0].(map[string]any)
	if f0["code"] != "RS-LOCK-001" {
		t.Errorf("code = %v, want RS-LOCK-001", f0["code"])
	}
	if f0["bucket"] == nil || f0["bucket"] == "" {
		t.Errorf("RS-LOCK finding should carry a duration bucket, got %v", f0["bucket"])
	}
}

// TestValidateMigrationFromFile: the migration argument also accepts a path.
func TestValidateMigrationFromFile(t *testing.T) {
	cs := connectClient(t)
	dir := t.TempDir()
	fx := writeFile2(t, dir, "rowshape.yaml", `rowshape_fixture: "1"
meta: {id: t, engine: {name: postgres, version: "16"}}
tables:
  public.t: {rows: {value: 10, confidence: exact}, columns: {c: {type: text, nullable: true}}}
`)
	mig := writeFile2(t, dir, "m.sql", "ALTER TABLE public.t ADD COLUMN d int;")
	res, out := callValidate(t, cs, fx, mig)
	if res.IsError {
		t.Fatalf("unexpected error validating a migration file")
	}
	if out["verdict"] == nil {
		t.Error("expected a verdict from a migration file path")
	}
}
