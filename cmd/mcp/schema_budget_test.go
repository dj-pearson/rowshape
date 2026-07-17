package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The MCPFold discipline, enforced (PRD §8.2).
//
// Every tool a server advertises is paid for in EVERY session, whether or not the
// agent calls it: the client injects the name, description, and JSON schema of all
// four tools into the context before the model has done anything. Four fat schemas
// cost tokens and earn resentment. So the advertised surface gets a budget, and a
// test holds the line — otherwise it erodes one helpful field at a time.
//
// The budget is in characters, a stable proxy that needs no tokenizer. For the
// mixed English-plus-JSON these schemas contain, tokens run roughly chars/4, so the
// per-tool ceiling below is ~200 tokens and the whole four-tool surface is ~600 —
// a fixed, knowable session tax.
//
// Headroom is deliberate but finite. If a change trips these, the fix is almost
// never a bigger budget: it is fewer parameters, a shorter description, or moving
// the detail behind explain_finding.
const (
	// perToolBudget caps one tool's full advertised surface (name + description +
	// input schema + output schema, as the client receives it).
	perToolBudget = 700

	// sessionBudget caps the sum across all four tools — what every session pays
	// up front for having rowshape connected at all.
	sessionBudget = 2400

	// payloadBudget caps the default, no-arguments-beyond-the-fixture answer from
	// describe_shape. A tool that dumps a fixture body defeats the point of a
	// fixture (RFC §2: reason over four kilobytes, not forty gigabytes).
	payloadBudget = 4096
)

// TestToolSchemaBudget asserts each advertised tool schema stays inside the budget,
// and reports the real cost of each so the number is visible when it moves.
func TestToolSchemaBudget(t *testing.T) {
	cs := connectClient(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != len(ToolNames) {
		t.Fatalf("advertising %d tools, want exactly %d (PRD §8.2)", len(res.Tools), len(ToolNames))
	}

	total := 0
	for _, tool := range res.Tools {
		// Marshal the tool exactly as the client receives it: this is the wire cost.
		b, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal tool %s: %v", tool.Name, err)
		}
		size := len(b)
		total += size

		t.Logf("%-18s %4d chars (~%d tokens) of %d budget", tool.Name, size, size/4, perToolBudget)

		if size > perToolBudget {
			t.Errorf("tool %s advertises %d chars, over the %d budget by %d.\n"+
				"Do not raise the budget: cut a parameter, shorten the description, or move the detail behind explain_finding (PRD §8.2).\n"+
				"schema: %s", tool.Name, size, perToolBudget, size-perToolBudget, b)
		}

		// A tool with no description is not thin, it is broken — an agent cannot
		// choose a tool it cannot read.
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %s advertises no description; thin is not the same as absent", tool.Name)
		}
	}

	t.Logf("session cost: %d chars (~%d tokens) of %d budget", total, total/4, sessionBudget)
	if total > sessionBudget {
		t.Errorf("the four tools advertise %d chars total, over the %d session budget by %d — "+
			"this is paid in every session whether or not a tool is called (PRD §8.2)", total, sessionBudget, total-sessionBudget)
	}
}

// TestNoToolDumpsFixtureByDefault: describe_shape without a table target returns an
// index, not a fixture body — detail requires asking for a specific table
// (PRD §8.2). The index-only *shape* is asserted in tool_describe_shape_test.go;
// this guards its SIZE, which is the property that erodes silently.
func TestNoToolDumpsFixtureByDefault(t *testing.T) {
	cs := connectClient(t)
	fx := writeFixture(t)

	_, out := callDescribeShape(t, cs, map[string]any{"fixture": fx})
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	t.Logf("describe_shape index payload: %d chars for %d tables", len(b), len(out["tables"].([]any)))
	if len(b) > payloadBudget {
		t.Errorf("the default describe_shape answer is %d chars, over the %d payload budget — "+
			"it should be an index, not a fixture body (RFC §2)", len(b), payloadBudget)
	}
}

// TestValidateReturnsCompactCodes: findings come back as codes an agent branches
// on, with explain_finding as the ONLY expansion path. Remediation prose inline
// would be paid on every finding of every validate call, which is exactly the
// failure mode the compact-code contract exists to avoid (PRD §8.2).
func TestValidateReturnsCompactCodes(t *testing.T) {
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
	_, out := callValidate(t, cs, fx, "ALTER TABLE public.users ADD CONSTRAINT u UNIQUE (email);")

	findings, _ := out["findings"].([]any)
	if len(findings) == 0 {
		t.Fatal("expected at least one finding to inspect")
	}
	for _, raw := range findings {
		f := raw.(map[string]any)

		// The expansion path must be named, not inlined.
		explain, _ := f["explain"].(string)
		if !strings.Contains(explain, "explain") {
			t.Errorf("finding %v should name its expansion path (`rowshape explain <CODE>`), got %q", f["code"], explain)
		}

		// The fat fields belong behind explain_finding, never in the loop-closer's
		// per-finding payload.
		for _, fat := range []string{"remediation", "detail", "evidence", "explanation"} {
			if v, present := f[fat]; present {
				t.Errorf("finding %v inlines %q (%v) — that expands via explain_finding (PRD §8.2)", f["code"], fat, v)
			}
		}
	}
}
