# `rowshape mcp` — the four tools

`rowshape mcp` is a subcommand of the same binary, not a separate artifact. It
serves the Model Context Protocol over stdio, built on the official Go SDK, so an
agent can reach rowshape **inside its own turn**: write a migration → validate →
read the failure → fix → re-validate, with no human turn in the middle.

```jsonc
// .mcp.json
{
  "mcpServers": {
    "rowshape": { "command": "rowshape", "args": ["mcp"] }
  }
}
```

`rowshape init --agent` writes this for you, along with the agent rule and a
pre-commit backstop.

## The tools

| Tool | What it's for |
| --- | --- |
| `describe_shape` | Hand the agent the production shape *before* it writes SQL. |
| `validate_migration` | The loop-closer: verdict + compact finding codes. |
| `explain_finding` | Remediation for a code, without a web search. |
| `plan_against` | What a migration would change on target X (read-only). |

Four. The set is closed, and a test asserts it.

### `describe_shape`

Returns the statistical facts an agent reasons over — row counts, null fractions,
cardinality and uniqueness *with their confidence*, fan-out distributions, format
classes — read from the committed fixture.

With **no table**, it returns a table index (names, row counts, column counts).
With a **table**, it returns that table's shape. It never returns a full fixture
body: the point of a fixture is reasoning over four kilobytes instead of forty
gigabytes, and a tool that dumps the whole thing gives that back.

Value-derived detail (raw ranges, histogram bounds, sample values) is summarized
to a flag — `has_range`, `has_histogram` — never re-surfaced. The fixture's own
privacy level still governs what exists to summarize.

### `validate_migration`

Runs the same analyzers and the same confidence capping as the CLI, and returns
the same `Verdict` struct (one struct, two marshalers — the agent and the human
get the same answer). Findings come back **compact**:

```json
{
  "verdict": "WARN",
  "exit_code": 2,
  "findings": [
    {
      "code": "RS-DATA-014",
      "severity": "warning",
      "title": "ADD CONSTRAINT UNIQUE on a column with unproven uniqueness",
      "confidence": "estimated",
      "explain": "rowshape explain RS-DATA-014"
    }
  ]
}
```

Codes are what an agent branches on. Remediation, detail, and evidence are *not*
inlined — `explain_finding` is the expansion path, paid only when the agent
actually hits the finding.

This is the fast, target-free path: static analysis against the committed fixture,
cheap enough to call every turn. The CLI's `rowshape validate` additionally
hydrates a disposable Postgres and applies the migration to catch runtime
failures. Both return the same struct.

**Capping holds end to end.** A finding resting on an `estimated` fact never
reports PASS through this tool — it reports WARN and names the command that
resolves it. A wrong PASS is unrecoverable; the MCP surface gets no exemption.

### `explain_finding`

Wraps `rowshape explain <CODE>`. Same content the CLI prints. An unknown code is
a clean tool error, not a crash.

### `plan_against`

Wraps `plan --against`. Read-only: it returns the diff a migration *would*
produce against a live target and applies nothing.

## Thin schemas, on purpose

Every tool a server advertises is paid for in **every** session, whether or not
the agent calls it — the client injects all four names, descriptions, and JSON
schemas into the context before the model has done anything.

So the advertised surface has a budget, and
[`schema_budget_test.go`](../cmd/mcp/schema_budget_test.go) holds the line:

| | Budget | Actual |
| --- | --- | --- |
| Per tool | 700 chars (~175 tokens) | 310–510 |
| All four (session tax) | 2400 chars (~600 tokens) | ~1734 (~433 tokens) |
| `describe_shape` default answer | 4096 chars | ~425 for a 2-table fixture |

The budget is in characters — a stable proxy that needs no tokenizer; for the
mixed English-plus-JSON in these schemas, tokens run roughly chars/4.

When a change trips the guard, the fix is almost never a bigger budget. It's
fewer parameters, a shorter description, or moving the detail behind
`explain_finding`. The discipline erodes one helpful field at a time, which is
exactly why it's a test and not a convention.

## Versioning

The server advertises, in its handshake instructions, the `rowshape_fixture`
format major it understands. A peer on a newer major should refuse rather than
best-effort — see [RFC-0001 §12](https://github.com/rowshape/fixture-spec).

## Related

- [MCPFold](https://github.com/dj-pearson/MCPFold) — one canonical MCP config,
  folded out to every client, loading only the tools each agent needs. rowshape
  is a flagship consumer of the same discipline: MCPFold cuts the tax across
  servers, rowshape keeps its own share of it small at the source.
- [`rowshape/fixture-spec`](https://github.com/rowshape/fixture-spec) — RFC-0001.
