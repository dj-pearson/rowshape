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

## Related repos

- [`rowshape/fixture-spec`](https://github.com/rowshape/fixture-spec) — the Rowshape Fixture Spec (RFC-0001).
- [`rowshape/corpus`](https://github.com/rowshape/corpus) — `(migration, fixture, expected verdict)` triples.
