---
title: The agent rule
description: The exact text rowshape init --agent writes into AGENTS.md / CLAUDE.md.
sidebar:
  order: 3
---

`rowshape init --agent` writes the rule below into your `AGENTS.md` (and
`CLAUDE.md`), inside a managed block marked `rowshape:begin v2` … `rowshape:end`.
Everything outside that block is left byte-for-byte untouched, and a re-run
replaces the block in place — so an improvement to the rule reaches repos that
ran `init --agent` months ago. It is a versioned product artifact (currently
**v2**), iterated against real agent sessions like a prompt.

This is the current text, verbatim:

````markdown
## Database migrations

This repo has `rowshape` wired in as an MCP server. Migrations here get checked
against production *shape* — real row counts, null fractions, cardinality,
fan-out — not against a dev database with twelve rows in it. Most migrations that
break production pass every test on a small dataset first.

**Before writing a migration**, call `describe_shape` for the tables you're
touching. The shape decides the answer: `SET NOT NULL` on a column with a 3% null
fraction fails on contact, and a cascade delete on a foreign key whose fan-out has
a long tail is an outage rather than a cleanup. Guessing costs more than asking.

**Before opening a PR**, call `validate_migration`. It returns a verdict and
compact finding codes (`RS-LOCK-001`, `RS-DATA-014`, …). Call `explain_finding`
with a code to get the fix.

**Never hand-wave a FAIL.** A FAIL is not an opinion. Either the migration was
executed against production-shaped data and broke, or a fact measured from
production says it will. Fix it and re-validate. Do not explain it away in the PR
description, do not disable the check, and do not ask a human to accept it.

**A WARN is not a pass.** WARN means the verdict rests on a fact rowshape could
not prove — usually a statistic it could only estimate. The finding names the
command that resolves it. Run that command; do not assume the optimistic reading.

Both tools need the committed fixture (`rowshape.yaml`). If there isn't one, say
so and ask — creating it means reading a real database, which is a human's call.
````
