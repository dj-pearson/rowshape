// Package mcp runs rowshape as a Model Context Protocol server over stdio, so an
// agent can reach rowshape's tools inside its own turn — the wedge surface of
// PRD §8.2. It is a subcommand of the single rowshape binary, not a separate
// artifact, built on the official Go SDK (modelcontextprotocol/go-sdk, past
// v1.0.0 with a no-breaking-changes guarantee, PRD §7).
//
// This file is the server scaffold (P3-T2): it registers exactly the four tools
// named in PRD §8.2 with brutally thin schemas (fat tool schemas cost tokens in
// every session — the MCPFold discipline) and advertises the fixture format
// major version it understands (RFC §12). The tool BEHAVIOR is filled in by
// P3-T3..T6; here the handlers are scaffolds.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rowshape/rowshape/internal/fixture"
)

// serverName / serverVersion identify this server to a client in the handshake.
const (
	serverName    = "rowshape"
	serverVersion = "0.1.0"
)

// ToolNames are exactly the four tools the server exposes (PRD §8.2). The set is
// closed: the server registers these and no others.
var ToolNames = []string{"describe_shape", "validate_migration", "explain_finding", "plan_against"}

// instructions advertise, to a connecting client, the fixture format major this
// build understands — so a peer on a newer major knows to refuse rather than
// best-effort (RFC §12).
var instructions = fmt.Sprintf(
	"rowshape MCP server. Tools: describe_shape (the production shape before you write SQL), "+
		"validate_migration (the loop-closer), explain_finding (remediation, no web search), "+
		"plan_against (what a migration would change on a target). "+
		"This build understands rowshape_fixture format major version %q (RFC-0001 §12).",
	fixture.FormatVersion,
)

// describeShapeInput asks for the shape of a fixture, optionally one table.
// Schemas are kept minimal on purpose (PRD §8.2).
type describeShapeInput struct {
	Fixture string `json:"fixture" jsonschema:"path to the rowshape.yaml fixture"`
	Table   string `json:"table,omitempty" jsonschema:"optional qualified table name to restrict the shape to"`
}

// validateMigrationInput validates a migration against a fixture.
type validateMigrationInput struct {
	Fixture   string `json:"fixture" jsonschema:"path to the rowshape.yaml fixture"`
	Migration string `json:"migration" jsonschema:"path to the migration .sql file or directory"`
}

// explainFindingInput expands a finding code.
type explainFindingInput struct {
	Code string `json:"code" jsonschema:"a finding code, e.g. RS-LOCK-001"`
}

// planAgainstInput dry-runs a migration diff against a live target.
type planAgainstInput struct {
	Migration string `json:"migration" jsonschema:"path to the migration .sql file or directory"`
	Target    string `json:"target" jsonschema:"a live database URL to diff against (read-only)"`
}

// NewServer builds the MCP server with the four tools registered. The handlers
// are scaffolds until P3-T3..T6 fill them in.
func NewServer() *sdk.Server {
	s := sdk.NewServer(
		&sdk.Implementation{Name: serverName, Version: serverVersion, Title: "rowshape"},
		&sdk.ServerOptions{Instructions: instructions},
	)

	sdk.AddTool(s, &sdk.Tool{
		Name:        "describe_shape",
		Description: "Return the production shape (row counts, null fractions, cardinality, fan-out) an agent should read BEFORE writing a migration. Never returns a full fixture unless a specific table is asked for.",
	}, handleDescribeShape)

	sdk.AddTool(s, &sdk.Tool{
		Name:        "validate_migration",
		Description: "Validate a migration against production-shaped data and return the verdict — the loop-closer. Findings come back as compact codes; expand them with explain_finding.",
	}, scaffold[validateMigrationInput]("validate_migration", "P3-T4"))

	sdk.AddTool(s, &sdk.Tool{
		Name:        "explain_finding",
		Description: "Return the documentation and remediation for a finding code — remediation without a web search.",
	}, scaffold[explainFindingInput]("explain_finding", "P3-T5"))

	sdk.AddTool(s, &sdk.Tool{
		Name:        "plan_against",
		Description: "Dry-run diff: what a migration would change on a live target (read-only, applies nothing).",
	}, scaffold[planAgainstInput]("plan_against", "P3-T6"))

	return s
}

// Serve runs the server over stdio until the context is cancelled or the client
// disconnects. This is what `rowshape mcp` invokes.
func Serve(ctx context.Context) error {
	return NewServer().Run(ctx, &sdk.StdioTransport{})
}

// scaffold returns a placeholder handler for a tool whose behavior lands in a
// later task. It responds with a structured, honest "not yet implemented" rather
// than a fabricated result, so the tool is discoverable now and wired later.
func scaffold[In any](tool, task string) sdk.ToolHandlerFor[In, any] {
	return func(_ context.Context, _ *sdk.CallToolRequest, _ In) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{
				Text: fmt.Sprintf("rowshape: the %q tool is registered but its handler is not yet implemented (lands in %s).", tool, task),
			}},
		}, nil, nil
	}
}
