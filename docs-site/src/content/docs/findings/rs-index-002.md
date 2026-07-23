---
title: 'RS-INDEX-002 — ADD PRIMARY KEY builds a unique index under ACCESS EXCLUSIVE'
description: 'Adding a PRIMARY KEY over existing data scans the column for NULLs and builds a unique index while holding an ACCESS EXCLUSIVE lock — no reads or writes proceed for the whole O(n log n) build.'
---

**Namespace:** `RS-INDEX` · **Code:** `RS-INDEX-002`

Adding a PRIMARY KEY over existing data scans the column for NULLs and builds a unique index while holding an ACCESS EXCLUSIVE lock — no reads or writes proceed for the whole O(n log n) build. On a large table that is a full outage, not just a write block.

## Remediation

Build the index first without the exclusive lock, then adopt it: CREATE UNIQUE INDEX CONCURRENTLY on the column(s), ensure the column is already NOT NULL (add a validated CHECK (col IS NOT NULL) if needed), then ALTER TABLE ... ADD PRIMARY KEY USING INDEX <name>, which attaches the prebuilt index and holds the exclusive lock only briefly.

## References

- RFC §6.5
- RFC §9.1
- PRD §10

---

This page is generated from the same catalog `rowshape explain RS-INDEX-002` reads, so the remediation here is byte-identical to the one a verdict carries — they cannot drift. An agent can read it with the `explain_finding` MCP tool.
