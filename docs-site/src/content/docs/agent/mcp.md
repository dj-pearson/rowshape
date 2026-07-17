---
title: MCP tools
description: The four thin MCP tools rowshape mcp serves, and how an agent uses them.
sidebar:
  order: 2
---

`rowshape mcp` serves four deliberately thin tools from the same static binary.
They are built to the [MCPFold](https://github.com/dj-pearson/MCPFold) discipline:
small schemas that don't tax the context window, findings returned as compact
codes you expand on demand rather than as fat payloads. rowshape's server is that
discipline's flagship consumer.

## The four tools

### `describe_shape`

Returns the production shape — row counts, null fractions, cardinality, fan-out —
that an agent should read **before** writing a migration. It never returns a full
fixture unless a specific table is asked for. The shape decides the answer: a
`SET NOT NULL` on a 3%-null column fails on contact; a cascade delete on a
long-tailed foreign key is an outage, not a cleanup.

### `validate_migration`

The loop-closer. Validates a migration against production-shaped data and returns
the verdict (`PASS` / `WARN` / `FAIL`). Findings come back as compact codes
(`RS-LOCK-001`, `RS-DATA-014`, …) — expand them with `explain_finding`.

### `explain_finding`

Returns the documentation and remediation for a finding code — the fix, without a
web search. It reads the same catalog the analyzers cite and the [finding
catalog](../../findings/) is generated from, so the remediation never drifts.

### `plan_against`

A read-only dry-run: what a migration *would* change on a live target. It applies
nothing.

## How the agent uses them

The four compose into the loop the [agent rule](./rule/) tells the agent to run:

```text
describe_shape        →  read the shape before writing SQL
   ↓ (write migration)
validate_migration    →  PASS? ship it.
   ↓ WARN / FAIL
explain_finding CODE  →  get the fix
   ↓ (rewrite)
validate_migration    →  PASS.
```

A WARN is not a pass and a FAIL is not an opinion — the rule is explicit about
both, which is what keeps the agent from hand-waving a finding into a merged PR.
The exit codes and the `Verdict` struct are identical across the CLI, the MCP
tools, and the [GitHub Action](../../install/), so a human reviewer and the agent
are looking at the same answer.
