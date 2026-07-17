# Transcript: the loop closing with no human turn

A lightly-edited capture of a single agent turn. The human speaks **once** (the
first line). Everything after is the agent reacting to `rowshape` verdicts
through the MCP tools wired in by `rowshape init --agent`. No human approval, no
"is this okay?", no hand-waving a warning.

---

**Human:** Add a NOT NULL `email` column to `users`.

**Agent:** Before I write the migration I'll check the production shape.

> 🛠 `describe_shape(tables: ["public.users"])`
> → `public.users`: **5,000,000 rows** (exact). Columns: `id` (bigint, pk),
>   `name` (text), `created_at` (timestamptz). No `email` column.

Five million rows — I have to be careful how I add a NOT NULL column. I'll start
with the direct version and validate it.

> writes `migrations/naive/001_add_email.sql`:
> ```sql
> ALTER TABLE public.users
>   ADD COLUMN email text NOT NULL DEFAULT (gen_random_uuid()::text || '@users.invalid');
> ```

> 🛠 `validate_migration(migrations: "migrations/naive")`
> → **WARN** · finding **RS-LOCK-001** · `ACCESS EXCLUSIVE` lock, table rewrite of
>   5,000,000 rows, duration bucket **outage**.

A WARN is not a pass, and this one is a full-table rewrite under an exclusive
lock — that is a write outage on 5M rows. I won't ship it. Let me get the fix.

> 🛠 `explain_finding(code: "RS-LOCK-001")`
> → Avoid the rewrite: add the column nullable, backfill in batches, then enforce
>   NOT NULL online (a validated `CHECK`), each step in its own transaction.

Rewriting as three online steps.

> writes `migrations/rewrite/001_add_email_nullable.sql`, `002_backfill_email.sql`,
> `003_enforce_not_null.sql` (add nullable → backfill with a `WHERE` → `ADD
> CONSTRAINT … CHECK (email IS NOT NULL) NOT VALID; COMMIT; VALIDATE CONSTRAINT`).

> 🛠 `validate_migration(migrations: "migrations/rewrite")`
> → **PASS** · no findings.

PASS. The rewrite adds the column, backfills it, and enforces NOT NULL with a
validated `CHECK` — no table rewrite, no long lock. Opening the PR with the
three-step migration.

---

The agent went naive → **WARN RS-LOCK-001** → rewrite → **PASS** without a human
turn. That is the whole bet (PRD §15): the loop, not the tool.
