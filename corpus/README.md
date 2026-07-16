# rowshape corpus

An executable corpus of the documented ways a PostgreSQL migration breaks. Each
case is a **triple** — a migration, a rowshape fixture describing the production
data's shape, and the verdict rowshape is expected to return:

```
cases/<pathology>/
  migration.sql    # the proposed schema change
  fixture.yaml     # production shape (rowshape fixture, no row values)
  expected.json    # { verdict: PASS|WARN|FAIL, findings: [{code, severity}] }
```

This is a runnable harness, not prose. The corpus is written and proven **before**
the findings that consume it, so a finding can never be demoed on PG 16 and then
discovered wrong on PG 11 (PRD §14 ordering).

## Pathologies covered

| Case | What breaks |
|------|-------------|
| `volatile_default_rewrite` | `ADD COLUMN … DEFAULT gen_random_uuid()` rewrites every row under ACCESS EXCLUSIVE |
| `set_not_null_fullscan` | `SET NOT NULL` fails on the existing NULL rows (nullable-but-not-0%-null) |
| `unique_index_cant_build` | `CREATE UNIQUE INDEX` on a column proven non-unique |
| `validate_orphans` | `VALIDATE CONSTRAINT` trips on pre-existing FK orphans |
| `cascade_delete_fanout` | a cascade delete through a long-tailed fan-out becomes an outage |
| `not_valid_validated_same_tx` | `NOT VALID` then `VALIDATE` in one transaction defeats the split |

## Running

```
go test ./corpus/harness/...
```

The harness loads every triple, asserts it is well-formed, and checks the corpus
covers every documented pathology. Once `validate` lands (P2-T7) it also runs each
migration against its fixture and compares the verdict to `expected.json`.

The harness is parameterized by Postgres major version via `ROWSHAPE_PG_VERSION`
(default 16); CI drives it across PG 11–17 (P2-T13).

> This directory is the seed of the standalone `rowshape/corpus` repository
> (PRD §12); the Go module path is unaffected by where it eventually lives.
