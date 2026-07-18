package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The four MCP tools had good happy-path coverage but almost no assertions that
// bad input produces an ERROR result (IsError) rather than a malformed success.
// An agent branches on IsError; a tool that silently returns an empty success on
// a missing fixture would send it down the wrong path. docs/TESTING-GAPS.md item 7.
//
// These run offline: every error branch asserted here is reachable without a live
// database, including plan_against's (its argument checks run before it connects).

// callTool invokes a tool and returns the result and any concatenated text. It
// does NOT fail on IsError — asserting IsError is the point of these tests.
func callTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s (transport/protocol error, not a tool error): %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	return res, text
}

func mustError(t *testing.T, res *sdk.CallToolResult, text, wantSubstr string) {
	t.Helper()
	if !res.IsError {
		t.Errorf("expected an error result, got success: %s", text)
		return
	}
	if wantSubstr != "" && !strings.Contains(text, wantSubstr) {
		t.Errorf("error text %q should mention %q", text, wantSubstr)
	}
}

// malformedFixture writes an unparseable fixture and returns its path.
func malformedFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(p, []byte("{{{ not a fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDescribeShapeErrorPaths(t *testing.T) {
	cs := connectClient(t)
	t.Run("empty fixture arg hits the handler guard", func(t *testing.T) {
		res, text := callTool(t, cs, "describe_shape", map[string]any{"fixture": ""})
		mustError(t, res, text, "no fixture given")
	})
	t.Run("omitted required field is rejected by the schema", func(t *testing.T) {
		res, text := callTool(t, cs, "describe_shape", map[string]any{})
		mustError(t, res, text, "required")
	})
	t.Run("unreadable path", func(t *testing.T) {
		res, text := callTool(t, cs, "describe_shape", map[string]any{"fixture": "/no/such/rowshape.yaml"})
		mustError(t, res, text, "reading fixture")
	})
	t.Run("malformed fixture", func(t *testing.T) {
		res, text := callTool(t, cs, "describe_shape", map[string]any{"fixture": malformedFixture(t)})
		mustError(t, res, text, "")
	})
}

func TestValidateMigrationErrorPaths(t *testing.T) {
	cs := connectClient(t)
	fx := writeFixture(t)
	t.Run("empty fixture", func(t *testing.T) {
		res, text := callTool(t, cs, "validate_migration", map[string]any{"fixture": "", "migration": "SELECT 1;"})
		mustError(t, res, text, "no fixture given")
	})
	t.Run("empty migration", func(t *testing.T) {
		res, text := callTool(t, cs, "validate_migration", map[string]any{"fixture": fx, "migration": ""})
		mustError(t, res, text, "no migration given")
	})
	t.Run("whitespace-only migration yields no statements", func(t *testing.T) {
		// A comment-only migration is NOT empty — the splitter keeps a leading
		// comment as part of a statement, so it parses to one no-op statement and
		// PASSes. Only genuinely blank input reaches the "no statements" branch.
		res, text := callTool(t, cs, "validate_migration", map[string]any{"fixture": fx, "migration": "   \n\t  \n"})
		mustError(t, res, text, "no SQL statements found")
	})
	t.Run("malformed fixture", func(t *testing.T) {
		res, text := callTool(t, cs, "validate_migration", map[string]any{"fixture": malformedFixture(t), "migration": "SELECT 1;"})
		mustError(t, res, text, "")
	})
}

func TestExplainFindingErrorPaths(t *testing.T) {
	cs := connectClient(t)
	t.Run("empty code", func(t *testing.T) {
		res, text := callTool(t, cs, "explain_finding", map[string]any{"code": ""})
		mustError(t, res, text, "unknown finding code")
	})
	t.Run("whitespace code", func(t *testing.T) {
		res, text := callTool(t, cs, "explain_finding", map[string]any{"code": "   "})
		mustError(t, res, text, "unknown finding code")
	})
	t.Run("nonexistent code", func(t *testing.T) {
		res, text := callTool(t, cs, "explain_finding", map[string]any{"code": "RS-NOPE-999"})
		mustError(t, res, text, "unknown finding code")
	})
}

func TestPlanAgainstErrorPaths(t *testing.T) {
	cs := connectClient(t)
	t.Run("empty target", func(t *testing.T) {
		res, text := callTool(t, cs, "plan_against", map[string]any{"target": "", "migration": "SELECT 1;"})
		mustError(t, res, text, "no target given")
	})
	t.Run("target set but empty migration", func(t *testing.T) {
		res, text := callTool(t, cs, "plan_against", map[string]any{"target": "postgres://u@127.0.0.1:1/db", "migration": ""})
		mustError(t, res, text, "no migration given")
	})
	t.Run("unreachable target does not leak credentials", func(t *testing.T) {
		const secret = "leakpw_mcp"
		res, text := callTool(t, cs, "plan_against", map[string]any{
			"target":    "postgres://u:" + secret + "@127.0.0.1:1/db?connect_timeout=1",
			"migration": "ALTER TABLE t ADD COLUMN c int;",
		})
		mustError(t, res, text, "")
		if strings.Contains(text, secret) {
			t.Errorf("plan_against leaked the target password into its error: %s", text)
		}
	})
}
