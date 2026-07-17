---
title: Finding catalog
description: Every finding code rowshape can return, what it means, and how to fix it.
sidebar:
  order: 1
---

Every finding rowshape returns carries a permanent, namespaced code, and every
error-severity finding carries remediation. A finding an agent cannot act on is a
bug.

The codes are grouped by what they are about:

| Namespace | Concerns |
| --- | --- |
| `RS-LOCK` | Locks a migration takes, and for how long |
| `RS-DATA` | Existing data that contradicts the change |
| `RS-CONSTRAINT` | Constraints that cannot be added or validated |
| `RS-INDEX` | Index builds that fail or block |
| `RS-PERF` | Rewrites and scans that cost more than they look like they do |
| `RS-REVERSE` | Changes that cannot be safely reversed |

The full catalog — every code, its evidence, and its remediation — lands here.
You can also read any of it from the CLI with `rowshape explain <CODE>`, or from
an agent with the `explain_finding` MCP tool.
