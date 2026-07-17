---
title: Privacy
description: What a fixture contains, what it does not, and how to check for yourself.
sidebar:
  order: 1
---

A fixture contains no rows. It contains statistics about rows.

That distinction is the product, so it gets stated precisely rather than
generously — the exact claim, its limits, and the fields that are derived from
real values, land here verbatim from the spec.

Until then, the two things worth knowing:

- `rowshape inspect --leaks` enumerates every field in a fixture that is derived
  from row values, with its source column and privacy level. Your security team
  will find those fields anyway; pointing at them first is the difference between
  a documented tradeoff and an undisclosed leak.
- `strict` privacy is supported and emitters never default to `permissive`.
