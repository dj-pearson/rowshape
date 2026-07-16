package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectClient boots the server and returns a connected in-process client session.
func connectClient(t *testing.T) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewServer()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// writeFixture writes a fixture to a temp file and returns its path.
func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rowshape.yaml")
	if err := os.WriteFile(path, []byte(`rowshape_fixture: "1"
meta:
  id: t
  engine: {name: postgres, version: "16"}
  privacy: standard
tables:
  public.users:
    rows: {value: 1200000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false, unique: {value: true, confidence: exact, via: constraint}}
      email: {type: text, nullable: false, distinct: {value: 1199950, confidence: measured, via: hll}, format: email}
      age: {type: integer, nullable: true, null_fraction: {value: 0.03, confidence: exact}, range: {min: 18, max: 97}}
  public.orders:
    rows: {value: 8000000, confidence: exact}
    columns:
      id: {type: bigint, nullable: false}
      user_id: {type: bigint, nullable: false}
    references:
      - {column: user_id, to: public.users.id, on_delete: cascade, fanout: {mean: 6.7, max: 900, confidence: measured}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// callDescribeShape calls the tool and returns (text, structured JSON).
func callDescribeShape(t *testing.T, cs *sdk.ClientSession, args map[string]any) (string, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "describe_shape", Arguments: args})
	if err != nil {
		t.Fatalf("call describe_shape: %v", err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	var structured map[string]any
	if res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			_ = json.Unmarshal(b, &structured)
		}
	}
	if res.IsError {
		t.Fatalf("describe_shape returned an error: %s", text)
	}
	return text, structured
}

// TestDescribeShapeNamedTable: a named table returns its shape with confidence on
// each fact (RFC §7), and no value-derived fields (range values are summarized to
// a has_range flag, honoring privacy — RFC §8).
func TestDescribeShapeNamedTable(t *testing.T) {
	cs := connectClient(t)
	fx := writeFixture(t)

	_, out := callDescribeShape(t, cs, map[string]any{"fixture": fx, "table": "public.users"})
	if out["table"] != "public.users" {
		t.Fatalf("expected shape of public.users, got %v", out["table"])
	}
	rows, _ := out["rows"].(map[string]any)
	if rows["confidence"] != "exact" {
		t.Errorf("rows fact must carry confidence, got %v", rows)
	}
	cols, _ := out["columns"].([]any)
	if len(cols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(cols))
	}
	// email carries distinct with confidence; age summarizes its range to a flag.
	var sawDistinctConf, sawRangeFlag bool
	for _, c := range cols {
		cm := c.(map[string]any)
		if cm["name"] == "email" {
			if d, ok := cm["distinct"].(map[string]any); ok && d["confidence"] == "measured" {
				sawDistinctConf = true
			}
		}
		if cm["name"] == "age" {
			if cm["has_range"] == true {
				sawRangeFlag = true
			}
			// The raw range values must NOT be in the shape (value-derived, RFC §8).
			if _, leaked := cm["range"]; leaked {
				t.Error("describe_shape leaked the raw range values")
			}
		}
	}
	if !sawDistinctConf {
		t.Error("email.distinct should carry its measured confidence")
	}
	if !sawRangeFlag {
		t.Error("age should summarize its range to has_range, not omit the signal entirely")
	}
}

// TestDescribeShapeNoTableIsIndexOnly: with no table the tool returns a table
// index (names + row counts), never the full fixture body — the whole point is a
// four-kilobyte reasoning surface (PRD §8.2, RFC §2).
func TestDescribeShapeNoTableIsIndexOnly(t *testing.T) {
	cs := connectClient(t)
	fx := writeFixture(t)

	_, out := callDescribeShape(t, cs, map[string]any{"fixture": fx})
	tables, _ := out["tables"].([]any)
	if len(tables) != 2 {
		t.Fatalf("index should list 2 tables, got %d", len(tables))
	}
	// The index must NOT include column detail — that is the "full fixture body"
	// it refuses to dump.
	for _, tb := range tables {
		tm := tb.(map[string]any)
		if _, leaked := tm["columns"].([]any); leaked {
			t.Error("the no-table index must not include per-column detail")
		}
		if tm["rows"] == nil {
			t.Error("index entry should carry a row count")
		}
	}
	if !strings.Contains(out["note"].(string), "table") {
		t.Errorf("index should hint how to drill in, got note %v", out["note"])
	}
}

// TestDescribeShapeUnknownTable: a missing table is a tool-level error, not a
// silent full dump.
func TestDescribeShapeUnknownTable(t *testing.T) {
	cs := connectClient(t)
	fx := writeFixture(t)

	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "describe_shape", Arguments: map[string]any{"fixture": fx, "table": "public.nope"}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Error("an unknown table should be a tool error, not a dump")
	}
}
