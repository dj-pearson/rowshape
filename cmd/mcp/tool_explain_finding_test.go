package mcp

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rowshape/rowshape/internal/findings"
)

// TestExplainFindingMatchesCatalog: explain_finding returns, for every shipped
// code, the SAME content the `rowshape explain <CODE>` CLI renders (both read the
// internal/findings catalog), and every code carries non-empty remediation (PRD
// §10 — a finding an agent can't act on is a bug).
func TestExplainFindingMatchesCatalog(t *testing.T) {
	cs := connectClient(t)
	for _, code := range findings.Codes() {
		want, _ := findings.Explain(code)
		res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "explain_finding",
			Arguments: map[string]any{"code": code},
		})
		if err != nil {
			t.Fatalf("call explain_finding %s: %v", code, err)
		}
		if res.IsError {
			t.Fatalf("explain_finding %s returned an error", code)
		}
		var got findings.Explanation
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("decode explanation for %s: %v", code, err)
		}
		if got.Code != want.Code || got.Title != want.Title || got.Remediation != want.Remediation {
			t.Errorf("explain_finding %s diverged from the catalog:\n got %+v\nwant %+v", code, got, want)
		}
		if got.Remediation == "" {
			t.Errorf("%s has no remediation", code)
		}
	}
}

// TestExplainFindingCaseInsensitive: a lowercased code still resolves.
func TestExplainFindingCaseInsensitive(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "explain_finding",
		Arguments: map[string]any{"code": "rs-lock-001"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Error("a lowercased code should resolve")
	}
}

// TestExplainFindingUnknownCode: an unknown code is a clean tool error, not a
// crash, and it names the known codes so the agent can recover.
func TestExplainFindingUnknownCode(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "explain_finding",
		Arguments: map[string]any{"code": "RS-NOPE-999"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Error("an unknown code must be a tool error")
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if text == "" {
		t.Error("the error should carry a message listing known codes")
	}
}
