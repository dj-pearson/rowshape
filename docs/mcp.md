# `rowshape mcp` — the four tools

`rowshape mcp` is a subcommand of the same binary, not a separate artifact. It
serves the Model Context Protocol over stdio, built on the official Go SDK, so an
agent can reach rowshape **inside its own turn**: write a migration → validate →
read the failure → fix → re-validate, with no human turn in the middle.

```jsonc
// .mcp.json
{
  "mcpServers": {
    "rowshape": { "command": "rowshape", "args": ["mcp"] }
  }
}
```

`rowshape init --agent` writes this for you, along with the agent rule and a
pre-commit backstop.

## `rowshape init --agent`

An MCP server the agent never calls is decoration. `--agent` wires it in: it
registers `rowshape mcp` in the config of every client it detects in the repo.

| Client | Config it writes | Root key |
| --- | --- | --- |
| Claude Code | `.mcp.json` | `mcpServers` |
| Cursor | `.cursor/mcp.json` | `mcpServers` |
| VS Code | `.vscode/mcp.json` | `servers` (+ `"type": "stdio"`) |

The formats have quietly diverged, which is why that table exists — a config in
the wrong shape is silently ignored by the client, and looks exactly like
rowshape not working.

A repo worked in by both Cursor and Claude Code gets both. A repo with no markers
at all gets `.mcp.json`, the de-facto standard. Only repo-local, committable paths
are written — never machine-global config, which is a different consent surface
and isn't committable anyway.

The registered command is the bare name `rowshape`, resolved from `PATH`, not the
absolute path of the binary that ran `init`. These files get committed; an
absolute path from your machine is broken for everyone else on the team.

**Re-running is safe.** The merge replaces only the `rowshape` key and leaves
every other server and top-level field intact, so `--agent` is safe on a repo that
already has five servers configured. When the entry is already correct, the file
isn't touched at all.

Two things worth knowing:

- **The first write reformats the file.** Merging goes through a JSON round-trip,
  so an existing config comes back re-indented with keys sorted. No data is lost
  and it happens once — the output is stable, so later runs are byte-identical
  no-ops — but expect the first `--agent` diff to touch lines you didn't add.
- **A config that isn't valid JSON is refused, not rewritten.** If your
  `.vscode/mcp.json` has comments (JSONC), rowshape will not touch it — it exits
  3 and prints the entry to paste. Clobbering a file we can't parse would destroy
  your other servers.

Zed isn't supported yet: it keys servers under `context_servers` with a different
entry shape, in a JSONC settings file whose format has moved across versions. A
half-right entry there is worse than an honest omission.

### The agent rule

Registering the server tells the client the tools *exist*. It doesn't make an
agent reach for them — agents don't invoke tools they haven't been told about. So
`--agent` also writes a rule: before writing a migration call `describe_shape`,
before opening a PR call `validate_migration`, never hand-wave a FAIL, and a WARN
is not a pass.

It goes into the conventions your repo already keeps — `AGENTS.md`, `CLAUDE.md`,
`.cursor/rules/rowshape.mdc` — and a repo that keeps none of them gets `AGENTS.md`,
the one that isn't tied to a single vendor.

The rule is a **product artifact**, not a doc snippet. It gets iterated against
real agent sessions the way a prompt does, which is why it ships versioned and
embedded in the binary ([`internal/agentrule/rule.md`](../internal/agentrule/rule.md)):
an improvement found in month four has to reach the repo that ran `init` in month
one. A rule you paste from a README can never do that.

That's what the markers are for:

```markdown
<!-- rowshape:begin v2 — managed by `rowshape init --agent`. Edits here are overwritten... -->
...the rule...
<!-- rowshape:end -->
```

Re-running `--agent` finds the block *at any version* and replaces it in place —
so an upgrade never leaves you with two rules that contradict each other.
Everything outside the block comes back byte-for-byte: your file, your
conventions, rowshape is a guest in it. Put your own guidance outside the markers
and it survives every upgrade; edit inside them and the next `--agent` wins.

The rule is budgeted too, at ~500 tokens — it's read on every turn, and prose
attracts "just one more sentence" forever.

### The pre-commit hook

The rule is an instruction, and instructions get skipped. The hook is the backstop
that doesn't depend on the agent having read anything.

If your repo uses the [pre-commit framework](https://pre-commit.com)
(`.pre-commit-config.yaml`), rowshape adds a `repo: local` entry to it — that file
is committed, so the whole team gets the backstop. Otherwise it installs
`.git/hooks/pre-commit`, which is local to you.

It will not touch a `.git/hooks/pre-commit` it didn't write. Your hook is your
workflow; replacing it to install a backstop is worse than not installing one.

**What it does:**

| `rowshape validate` | Hook | Why |
| --- | --- | --- |
| `0` PASS | allows | — |
| `2` WARN-only | allows | WARN is surfaced, not a gate. Use `--warn-fail` in CI. |
| `1` FAIL | **blocks** | Not an opinion: an execution that broke, or a measured fact that says it will. |
| `3` tool error | allows, loudly | Not a verdict — see below. |

It only runs when a **migration is actually staged**, matched per detected runner
(`versions/*.py` for Alembic, `prisma/migrations/`, `drizzle/*.sql`, `migrations/*.sql`).
`validate` hydrates a database; making a README typo pay for that is how a hook
earns its deletion.

**On exit 3.** A tool error means rowshape *couldn't answer* — usually Docker
isn't running. That's not a verdict, so it doesn't block. Blocking every commit on
a developer's machine because a daemon is down is exactly what gets the hook
deleted or `--no-verify` aliased forever, and then the backstop is gone for the
FAILs too. A commit isn't a deploy, and CI still gates the merge.

**Uninstall** — the hook documents this in its own header, where you'll be looking
when it blocks you:

```sh
rm .git/hooks/pre-commit        # native hook
git commit --no-verify          # bypass once
```

For the framework, delete the `rowshape-validate` entry from
`.pre-commit-config.yaml`.

## The tools

| Tool | What it's for |
| --- | --- |
| `describe_shape` | Hand the agent the production shape *before* it writes SQL. |
| `validate_migration` | The loop-closer: verdict + compact finding codes. |
| `explain_finding` | Remediation for a code, without a web search. |
| `plan_against` | What a migration would change on target X (read-only). |

Four. The set is closed, and a test asserts it.

### `describe_shape`

Returns the statistical facts an agent reasons over — row counts, null fractions,
cardinality and uniqueness *with their confidence*, fan-out distributions, format
classes — read from the committed fixture.

With **no table**, it returns a table index (names, row counts, column counts).
With a **table**, it returns that table's shape. It never returns a full fixture
body: the point of a fixture is reasoning over four kilobytes instead of forty
gigabytes, and a tool that dumps the whole thing gives that back.

Value-derived detail (raw ranges, histogram bounds, sample values) is summarized
to a flag — `has_range`, `has_histogram` — never re-surfaced. The fixture's own
privacy level still governs what exists to summarize.

### `validate_migration`

Runs the same analyzers and the same confidence capping as the CLI, and returns
the same `Verdict` struct (one struct, two marshalers — the agent and the human
get the same answer). Findings come back **compact**:

```json
{
  "verdict": "WARN",
  "exit_code": 2,
  "findings": [
    {
      "code": "RS-DATA-014",
      "severity": "warning",
      "title": "ADD CONSTRAINT UNIQUE on a column with unproven uniqueness",
      "confidence": "estimated",
      "explain": "rowshape explain RS-DATA-014"
    }
  ]
}
```

Codes are what an agent branches on. Remediation, detail, and evidence are *not*
inlined — `explain_finding` is the expansion path, paid only when the agent
actually hits the finding.

This is the fast, target-free path: static analysis against the committed fixture,
cheap enough to call every turn. The CLI's `rowshape validate` additionally
hydrates a disposable Postgres and applies the migration to catch runtime
failures. Both return the same struct.

**Capping holds end to end.** A finding resting on an `estimated` fact never
reports PASS through this tool — it reports WARN and names the command that
resolves it. A wrong PASS is unrecoverable; the MCP surface gets no exemption.

### `explain_finding`

Wraps `rowshape explain <CODE>`. Same content the CLI prints. An unknown code is
a clean tool error, not a crash.

### `plan_against`

Wraps `plan --against`. Read-only: it returns the diff a migration *would*
produce against a live target and applies nothing.

## Thin schemas, on purpose

Every tool a server advertises is paid for in **every** session, whether or not
the agent calls it — the client injects all four names, descriptions, and JSON
schemas into the context before the model has done anything.

So the advertised surface has a budget, and
[`schema_budget_test.go`](../cmd/mcp/schema_budget_test.go) holds the line:

| | Budget | Actual |
| --- | --- | --- |
| Per tool | 700 chars (~175 tokens) | 310–510 |
| All four (session tax) | 2400 chars (~600 tokens) | ~1734 (~433 tokens) |
| `describe_shape` default answer | 4096 chars | ~425 for a 2-table fixture |

The budget is in characters — a stable proxy that needs no tokenizer; for the
mixed English-plus-JSON in these schemas, tokens run roughly chars/4.

When a change trips the guard, the fix is almost never a bigger budget. It's
fewer parameters, a shorter description, or moving the detail behind
`explain_finding`. The discipline erodes one helpful field at a time, which is
exactly why it's a test and not a convention.

## Versioning

The server advertises, in its handshake instructions, the `rowshape_fixture`
format major it understands. A peer on a newer major should refuse rather than
best-effort — see [RFC-0001 §12](https://github.com/rowshape/fixture-spec).

## Related

- [MCPFold](https://github.com/dj-pearson/MCPFold) — one canonical MCP config,
  folded out to every client, loading only the tools each agent needs. rowshape
  is a flagship consumer of the same discipline: MCPFold cuts the tax across
  servers, rowshape keeps its own share of it small at the source.
- [`rowshape/fixture-spec`](https://github.com/rowshape/fixture-spec) — RFC-0001.
