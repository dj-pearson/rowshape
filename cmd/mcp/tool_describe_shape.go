package mcp

import (
	"context"
	"fmt"
	"os"
	"sort"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rowshape/rowshape/internal/fixture"
)

// describe_shape hands an agent the production SHAPE before it writes SQL
// (PRD §8.2). It returns the statistical facts the agent reasons over — row
// counts, null fractions, cardinality/uniqueness with confidence, fan-out,
// format classes — but NOT the value-derived detail (ranges, histograms, sample
// values). That keeps the payload four kilobytes, not forty gigabytes (RFC §2),
// and never re-surfaces production values beyond the committed fixture.
//
// With no table named it returns a table index (names + row counts + column
// counts), never the full fixture body.

// factI is a compact integer fact: value + the confidence it is known at (RFC §7).
type factI struct {
	Value      int64  `json:"value"`
	Confidence string `json:"confidence"`
}

// factF is a compact float fact (fractions).
type factF struct {
	Value      float64 `json:"value"`
	Confidence string  `json:"confidence"`
}

// factB is a compact boolean fact (uniqueness).
type factB struct {
	Value      bool   `json:"value"`
	Confidence string `json:"confidence"`
}

// tableBrief is one row of the no-table index.
type tableBrief struct {
	Table   string `json:"table"`
	Rows    factI  `json:"rows"`
	Columns int    `json:"columns"`
}

// shapeIndex is the compact answer when no table is requested.
type shapeIndex struct {
	Fixture string       `json:"fixture"`
	Engine  string       `json:"engine"`
	Privacy string       `json:"privacy,omitempty"`
	Tables  []tableBrief `json:"tables"`
	Note    string       `json:"note"`
}

// columnShape is a column's statistical shape — no value-derived fields.
type columnShape struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Nullable     bool   `json:"nullable"`
	NullFraction *factF `json:"null_fraction,omitempty"`
	Distinct     *factI `json:"distinct,omitempty"`
	Unique       *factB `json:"unique,omitempty"`
	Format       string `json:"format,omitempty"`
	HasRange     bool   `json:"has_range,omitempty"`     // value-derived detail exists (not shown)
	HasHistogram bool   `json:"has_histogram,omitempty"` // value-derived detail exists (not shown)
}

// refShape is a foreign key's fan-out shape.
type refShape struct {
	Column         string  `json:"column"`
	To             string  `json:"to"`
	OnDelete       string  `json:"on_delete,omitempty"`
	Fanout         *fanout `json:"fanout,omitempty"`
	OrphanFraction *factF  `json:"orphan_fraction,omitempty"`
}

type fanout struct {
	Mean       float64 `json:"mean"`
	P50        float64 `json:"p50,omitempty"`
	P95        float64 `json:"p95,omitempty"`
	Max        float64 `json:"max,omitempty"`
	Confidence string  `json:"confidence,omitempty"`
}

// tableShape is the compact answer for a named table.
type tableShape struct {
	Table       string        `json:"table"`
	Rows        factI         `json:"rows"`
	Columns     []columnShape `json:"columns"`
	References  []refShape    `json:"references,omitempty"`
	Constraints []string      `json:"constraints,omitempty"`
}

// handleDescribeShape implements the describe_shape tool.
func handleDescribeShape(_ context.Context, _ *sdk.CallToolRequest, in describeShapeInput) (*sdk.CallToolResult, any, error) {
	f, err := loadFixture(in.Fixture)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}

	if in.Table == "" {
		out := buildIndex(in.Fixture, f)
		return textResult(fmt.Sprintf("%d tables in %s; call describe_shape with a table to see its shape.", len(out.Tables), in.Fixture)), out, nil
	}

	tbl, ok := f.Tables[in.Table]
	if !ok {
		return errorResult(fmt.Sprintf("no table %q in the fixture; call describe_shape with no table for the index", in.Table)), nil, nil
	}
	out := buildTableShape(in.Table, tbl)
	return textResult(fmt.Sprintf("shape of %s (%d rows, %s): %d columns.", in.Table, tbl.Rows.Value, tbl.Rows.Confidence, len(out.Columns))), out, nil
}

// buildIndex returns the table index — never the full fixture body (PRD §8.2).
func buildIndex(path string, f *fixture.Fixture) shapeIndex {
	idx := shapeIndex{
		Fixture: path,
		Engine:  f.Meta.Engine.Name + " " + f.Meta.Engine.Version,
		Privacy: f.Meta.Privacy,
		Note:    "index only — call describe_shape with a `table` for its columns and fan-out.",
	}
	for _, name := range sortedKeys(f.Tables) {
		t := f.Tables[name]
		idx.Tables = append(idx.Tables, tableBrief{
			Table:   name,
			Rows:    factI{Value: t.Rows.Value, Confidence: confOf(t.Rows.Confidence)},
			Columns: len(t.Columns),
		})
	}
	return idx
}

// buildTableShape returns one table's statistical shape (no value-derived fields).
func buildTableShape(name string, t fixture.Table) tableShape {
	out := tableShape{
		Table: name,
		Rows:  factI{Value: t.Rows.Value, Confidence: confOf(t.Rows.Confidence)},
	}
	for _, cn := range sortedKeys(t.Columns) {
		c := t.Columns[cn]
		cs := columnShape{
			Name:         cn,
			Type:         c.Type,
			Nullable:     c.Nullable,
			Format:       c.Format,
			HasRange:     c.Range != nil,
			HasHistogram: c.Histogram != nil,
		}
		if c.NullFraction != nil {
			cs.NullFraction = &factF{Value: c.NullFraction.Value, Confidence: confOf(c.NullFraction.Confidence)}
		}
		if c.Distinct != nil {
			cs.Distinct = &factI{Value: c.Distinct.Value, Confidence: confOf(c.Distinct.Confidence)}
		}
		if c.Unique != nil {
			cs.Unique = &factB{Value: c.Unique.Value, Confidence: confOf(c.Unique.Confidence)}
		}
		out.Columns = append(out.Columns, cs)
	}
	for _, ref := range t.References {
		rs := refShape{Column: ref.Column, To: ref.To, OnDelete: ref.OnDelete}
		if ref.Fanout != nil {
			rs.Fanout = &fanout{Mean: ref.Fanout.Mean, P50: ref.Fanout.P50, P95: ref.Fanout.P95, Max: ref.Fanout.Max, Confidence: confOf(ref.Fanout.Confidence)}
		}
		if ref.OrphanFraction != nil {
			rs.OrphanFraction = &factF{Value: ref.OrphanFraction.Value, Confidence: confOf(ref.OrphanFraction.Confidence)}
		}
		out.References = append(out.References, rs)
	}
	for _, con := range t.Constraints {
		out.Constraints = append(out.Constraints, con.Kind+" "+con.Name)
	}
	return out
}

// loadFixture reads and parses a committed fixture from disk.
func loadFixture(path string) (*fixture.Fixture, error) {
	if path == "" {
		return nil, fmt.Errorf("no fixture given; pass the path to a committed rowshape.yaml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading fixture %s failed", path)
	}
	return fixture.ParseVerified(data)
}

// confOf renders a confidence, defaulting an absent one to "estimated" (the
// weakest reading, matching the bare-scalar shorthand — RFC §6.1).
func confOf(c fixture.Confidence) string {
	if c == "" {
		return string(fixture.Estimated)
	}
	return string(c)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// textResult wraps a human-readable summary in a tool result.
func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// errorResult is a tool-level error result (not a transport error).
func errorResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}
