# rowshape

**The type-checker for database migrations — a human and an agent get the same answer through the same contract.**

Execute a proposed schema change against production-shaped data in a disposable
environment and return a machine-readable verdict.

- **Module path:** `github.com/rowshape/rowshape` (see [docs/DECISIONS.md](docs/DECISIONS.md))
- **License:** CLI + spec MIT.
- **Status:** early build-out. See [`prd.json`](prd.json) for the build loop and current progress.

## Build

```sh
go build ./...        # builds the single `rowshape` binary
go vet ./...
```

## Commands

`rowshape` exposes: `init`, `pull`, `hydrate`, `validate`, `explain`, `plan`,
`verify`, `mcp`. In phase 0 these are stubs that return exit code 3
(tool error). Behavior is filled in phase by phase per `prd.json`.

Exit codes are part of the public contract: `0` PASS · `1` FAIL · `2` WARN-only
· `3` tool error.

## For agents

`rowshape mcp` serves four tools over the Model Context Protocol — `describe_shape`,
`validate_migration`, `explain_finding`, `plan_against` — from this same binary, so
an agent can write a migration, validate it, read the failure, and fix it inside
its own turn. `rowshape init --agent` wires it up. See [docs/mcp.md](docs/mcp.md).

The schemas are deliberately thin. Every advertised tool is paid for in every
session whether or not it's called, so findings return as compact codes with
`explain_finding` as the only expansion path, no tool dumps a full fixture, and a
[budget test](cmd/mcp/schema_budget_test.go) fails the build if the four-tool
surface creeps past ~600 tokens.

## Related repos

- [`rowshape/fixture-spec`](https://github.com/rowshape/fixture-spec) — the Rowshape Fixture Spec (RFC-0001).
- [`rowshape/corpus`](https://github.com/rowshape/corpus) — `(migration, fixture, expected verdict)` triples.
- [MCPFold](https://github.com/dj-pearson/MCPFold) — one canonical MCP config, folded
  out to every client, loading only the tools each agent needs. rowshape's MCP server
  is built to the same discipline; MCPFold cuts the context-window tax across servers,
  rowshape keeps its own share of it small at the source.
