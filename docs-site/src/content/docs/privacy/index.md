---
title: Privacy
description: What a fixture contains, what it does not, and how to check for yourself.
sidebar:
  order: 1
---

A fixture contains no rows. It contains statistics about rows. That distinction
is the whole product, so it is stated precisely rather than generously.

## The claim, narrow and true

A fixture contains no rows from your database; it contains statistics computed from them; at `--privacy standard` some of those reveal the extremes of numeric and date columns; at `--privacy strict` none do.

That is the entire claim. The broader claim — that **no production values leave
your database** — is false, and rowshape will not make it. Some fields in a
fixture are derived from real values, and at `standard` a few of them expose an
extreme (a minimum, a maximum, a most-common value). If that is more than your
data can tolerate, use `strict`, which emits none of it.

## What each privacy level reveals

| | `strict` | `standard` (default) | `permissive` |
| --- | --- | --- | --- |
| Structure, types, nullability, constraints, indexes | ✅ | ✅ | ✅ |
| Row counts, null fractions, distinct estimates | ✅ | ✅ | ✅ |
| Fan-out distribution, orphan fractions | ✅ | ✅ | ✅ |
| Numeric / temporal **range** (min, max) | ❌ | ✅ | ✅ |
| Histograms (bucket bounds are real values) | ❌ | ✅ | ✅ |
| Verbatim `CHECK` expressions | ❌ (become `opaque`) | ✅ | ✅ |
| Value sets / frequencies | ❌ | ❌ | only when a value is common¹ |

¹ `permissive` materializes a value set for a column **only** when it has at most
50 distinct values **and** every value occurs at least *k* times (default
*k* = 20), so no rare or identifying value is ever emitted. `permissive` is never
the default, and text and bytea columns never receive a range or min/max — only a
length. Uniqueness is never inferred from a sample.

## Check it yourself

You do not have to take the table's word for it:

```sh
rowshape inspect --leaks rowshape.yaml
```

`inspect --leaks` enumerates **every** field in a fixture that is derived from row
values, with its source column and the privacy level that emitted it. Your
security team will find those fields anyway; pointing at them first is the
difference between a documented tradeoff and an undisclosed leak. Ship it, read
it, and decide your privacy level from what it shows — not from a promise.

## Why this is safe to commit

- `meta.source` is a salted, per-fixture hash of the host — never the hostname.
- A fixture is value-free by design, so it is meant to be committed to git and
  reviewed in a pull request like any other file.
- `validate` never calls the cloud and never touches a non-disposable database;
  it hard-refuses a target whose host matches the fixture's source host.
