---
title: Finding catalog
description: Every finding code rowshape can return, what it means, and how to fix it.
---

Every finding rowshape returns carries a permanent, namespaced code, and every
error-severity finding carries remediation. A finding an agent cannot act on is a
bug.

You can read any of this from the CLI with `rowshape explain <CODE>`, or from an
agent with the `explain_finding` MCP tool. These pages are generated from that
same catalog, so they never drift from what the tool returns.

## RS-CONSTRAINT — Constraints that cannot be added or validated

- [`RS-CONSTRAINT-001`](./rs-constraint-001/) — NOT VALID constraint validated in the same transaction
- [`RS-CONSTRAINT-010`](./rs-constraint-010/) — CHECK constraint conflicts with existing data

## RS-DATA — Existing data that contradicts the change

- [`RS-DATA-001`](./rs-data-001/) — SET NOT NULL against existing NULLs
- [`RS-DATA-014`](./rs-data-014/) — ADD UNIQUE without proven uniqueness
- [`RS-DATA-020`](./rs-data-020/) — FOREIGN KEY validated against pre-existing orphans

## RS-INDEX — Index builds that fail or block

- [`RS-INDEX-001`](./rs-index-001/) — Non-concurrent CREATE INDEX blocks writes
- [`RS-INDEX-002`](./rs-index-002/) — ADD PRIMARY KEY or UNIQUE builds an index under ACCESS EXCLUSIVE
- [`RS-INDEX-010`](./rs-index-010/) — CREATE UNIQUE INDEX without proven uniqueness
- [`RS-INDEX-020`](./rs-index-020/) — Non-concurrent REINDEX rebuilds under lock

## RS-LOCK — Locks a migration takes, and for how long

- [`RS-LOCK-001`](./rs-lock-001/) — ACCESS EXCLUSIVE lock for a full table rewrite

## RS-PERF — Rewrites and scans that cost more than they look like they do

- [`RS-PERF-001`](./rs-perf-001/) — DELETE cascades through a long-tailed fan-out
- [`RS-PERF-002`](./rs-perf-002/) — Unqualified UPDATE/DELETE touches every row

## RS-REVERSE — Changes that cannot be safely reversed

- [`RS-REVERSE-001`](./rs-reverse-001/) — DROP COLUMN loses its data irreversibly
- [`RS-REVERSE-002`](./rs-reverse-002/) — DROP TABLE loses every row irreversibly
- [`RS-REVERSE-003`](./rs-reverse-003/) — Narrowing a column type can truncate data irreversibly

