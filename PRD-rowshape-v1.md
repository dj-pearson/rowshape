# PRD — Rowshape

**Migration validation for a world where the migration author might not be human.**

`rowshape.com` · Status: Draft v0.6 · Owner: Dan Pearson / Pearson Media LLC
Stack: Go · License intent: MIT core, commercial cloud

---

## 1. Problem

Every team has shipped a migration that passed CI and broke prod. The reason is
always the same: CI tested against an empty database, and prod has ten million
rows with a shape nobody documented.

The tooling that exists solves the wrong half of the problem:

| Tool | What it does | What it doesn't do |
|---|---|---|
| Flyway / Liquibase / Alembic / Prisma | Runs migrations in order | Tells you if the migration is *safe* |
| Atlas / Squawk / migra | Static schema diffing and linting | Executes against realistic data |
| DBeaver / TablePlus | Human GUI inspection | Anything automatable |
| Neon / PlanetScale branching | Realistic branch to test on | Vendor-locked, and still needs you to know what to look for |

Nobody owns the middle: **execute the proposed change against production-shaped
data, in a disposable environment, and return a machine-readable verdict.**

That gap has always been annoying. It became urgent about eighteen months ago,
when the person writing the migration stopped being a person.

## 2. Why now — the wedge

Claude Code, Cursor, and every agentic coding tool are now writing migrations.
They are *good* at the SQL and *blind* to the consequences. An agent asked to
"add a NOT NULL email column to users" will confidently emit DDL that locks the
table for nine minutes and fails on 400k legacy rows with a null email. It has
no feedback loop. It cannot know.

Today the loop closes when a human reviews the PR, or when prod falls over.

Rowshape closes it inside the agent's own turn:

```
agent writes migration → rowshape validate → structured verdict →
agent reads the failure → agent fixes it → validate again → PASS → open PR
```

Same shape as `tsc` or `pytest` in an agent loop — except no equivalent exists
for schema changes. Whoever becomes the default verification step in that loop
owns the category.

**Positioning:** the type-checker for database migrations, built so a human and
an agent get the same answer through the same contract.

Note what this is *not* claiming. Static linters that read your `.sql` and warn
about lock modes already exist and are good (pgfence, Squawk, Atlas). Rowshape's
claim is narrower and harder: it executes the change against your data's actual
shape, and it knows how confident it is. Everything downstream follows from those
two things and nothing else.

## 3. Why the name

The moat is not the validator — anyone can shell out to `psql` and time a lock.
The moat is the **fixture**: a portable, committable description of what your
production data actually looks like. Nulls, cardinality, distributions, the ugly
edge cases. The shape of your rows.

The name says the moat out loud. `rowshape validate` reads like a tool that's
been around for a decade. It also isn't the word "schema," which leaves room to
move down into data-level checks later without a rename.

Ship the fixture format as an open spec (§6). The tool is the on-ramp; the spec
is the position.

## 4. Non-goals (v1)

- Not a migration runner. It validates *your* Flyway/Alembic/Prisma/raw-SQL
  migrations. Bring your own tool. Being the neutral validator is worth more
  than being the 40th runner.
- Not an ORM, not a schema DSL, not a query builder.
- Not a prod-data mirroring service. We take shapes and distributions, not rows
  (§6).
- Not a GUI in v1.
- Postgres first. MySQL second. SQLite third. Nothing else is on the map.

## 5. Users

**Dana — staff engineer.** Owns the migration in the PR. Wants CI to fail loudly
before review, not after deploy. Runs `rowshape validate` locally, reads
human-formatted output.

**The agent — Claude Code or similar.** Wrote the migration thirty seconds ago.
Needs `--json`, deterministic exit codes, and error text that names the exact
constraint and the exact remediation. Has no eyes and no patience for ASCII
tables.

**Priya — platform lead.** Doesn't write migrations. Needs an audit trail of what
ran where, and needs the fixture pipeline to never touch a real customer email.
She is the cloud buyer.

## 6. The fixture pipeline (the hard part, and the moat)

`rowshape pull --from $PROD_URL`

1. Read schema via catalog queries. Read-only role, no `SELECT *` on user tables.
2. Profile each column: type, nullability, distinct count, min/max, length
   distribution, format class (email-like, uuid-like, json-like, enum-like).
3. Emit `rowshape.yaml` — a spec, not a dump. Human-readable, diffable,
   committable, safe to paste into a GitHub issue.
4. `rowshape hydrate` synthesizes N rows locally matching that profile, into a
   throwaway container. Deterministic given a seed.

Why this and not "snapshot anonymized prod rows": anonymized dumps are a
compliance liability, they're gigabytes, they can't live in git, and they leak
via join keys anyway. A distribution spec is a few KB, reviewable by Priya's
security team in an afternoon, and — critically — an agent can *read* it. It can
see `email: 3.2% null, 400k rows, unique` and reason about that before writing a
line of SQL. That is a capability a dump structurally cannot offer.

The format is called the **Rowshape Fixture Spec**. Own repo, own version,
own RFC. Other tools should be able to emit it.

## 7. Stack

**Go.** Decided against TypeScript deliberately, on long-term rather than
v1-velocity grounds:

- **Single static binary, no runtime.** `pull` asks for a production database
  credential. A CLI dragging 300 transitive npm dependencies is a hard sell to
  exactly the buyer §11 is written for. "One binary, no supply chain" is most of
  a security review.
- **Native dialect of the category.** Atlas, pgroll, Bytebase, golang-migrate,
  pg_flo are all Go. That's where the drive-by contributors and the credibility
  live.
- **`hydrate` is CPU-bound.** Synthesizing millions of profile-matched rows is
  the one genuinely compute-heavy path, and it only gets heavier at scale.
- **The npx objection is void.** esbuild and turbo are Go and ship via npm; biome
  is Rust and does the same. A thin wrapper package fetches the platform binary
  on install, so `npx rowshape validate` works identically — *and* you get brew,
  scoop, `go install`, and curl.
- **The MCP SDK objection is stale.** The official Go SDK is past v1.0.0 with a
  formal no-breaking-changes guarantee and full spec coverage.

The asymmetry that settles it: TypeScript's advantages decay after month one;
Go's compound as the project grows.

Key deps, kept deliberately few: `pgx` (Postgres), `cobra` or stdlib `flag`
(CLI), `modelcontextprotocol/go-sdk` (MCP), `testcontainers-go` (disposable
targets). No ORM. No fake-data library (§14.1).

**Distribution:** goreleaser → GitHub Releases (darwin/linux/windows, amd64 +
arm64), Homebrew tap, `go install`, npm wrapper package, and a `FROM scratch`
Docker image for CI.

## 8. Surfaces

### 8.1 CLI (OSS core)

```
rowshape init                    # detect stack, scaffold config
rowshape pull                    # prod → rowshape.yaml
rowshape hydrate                 # rowshape.yaml → local container
rowshape validate [--json]       # the main event
rowshape explain <CODE>          # docs for a finding, agent-readable
rowshape plan --against prod     # dry-run diff vs a live target
rowshape verify --against prod   # post-deploy: does reality match intent
```

`validate` is what runs in CI and in the agent loop. It:

- hydrates a disposable Postgres (docker, `pg_tmp`, or a provided URL)
- applies the current migration set through the detected runner
- captures: success/failure, per-statement wall time, lock mode and duration,
  rows affected, constraint violations, index build behavior
- returns the verdict

Runner detection shells out to the user's own tool (`alembic`, `prisma`,
`dbmate`, or plain `psql -f`). Rowshape orchestrates; it does not reimplement.

### 8.2 MCP server (the wedge)

`rowshape mcp` — a subcommand of the same binary, not a separate artifact. Built
on the official Go SDK. Exposes four tools:

- `describe_shape` — hand the agent the shape *before* it writes SQL
- `validate_migration` — the loop-closer
- `explain_finding` — remediation without a web search
- `plan_against` — what would change on target X

You shipped MCPFold, so you know the failure mode: four fat tool schemas in every
session costs tokens and earns resentment. Keep schemas brutally thin, return
findings as compact codes with `explain_finding` as the expansion path, and never
return a full fixture unless asked for a specific table.

Rowshape's MCP server should be the showcase for MCPFold-shaped discipline, and
MCPFold gets a flagship consumer. Cross-link them in both READMEs.

### 8.3 Agent distribution (the part that actually closes the loop)

An MCP server the agent never calls is decoration. Nothing in §8.2 makes Claude
Code *reach for* rowshape unprompted, and agents don't invoke tools they haven't
been told about. Getting into the agent's context is a distinct product surface
from exposing the tools, and it's closer to the wedge than the server itself.

`rowshape init --agent` writes three things:

1. **MCP config** for the detected client (`.mcp.json`, or the client's own path).
2. **An agent rule** into `AGENTS.md` / `CLAUDE.md` / `.cursor/rules`: before
   writing a migration, call `describe_shape`; before opening a PR, call
   `validate_migration`; never hand-wave a FAIL.
3. **A pre-commit hook** running `rowshape validate` — the backstop for when the
   agent ignores the rule, which it sometimes will.

The rule text is a product artifact, not a README snippet. It gets iterated on
against real agent sessions the way a prompt does, and it ships versioned in the
repo so improvements reach existing users on upgrade.

### 8.4 Cloud (commercial)

The CLI is free forever and complete on its own — a solo dev never hits a wall.
Cloud sells what only exists when there is more than one of you:

- **Fixture registry** — hosted, versioned, access-controlled fixtures. Dana
  pulls `prod@2026-07-14` without ever holding a prod credential. This is the
  killer feature; it's also what gets `$PROD_URL` off her laptop.
- **Drift detection** — scheduled `verify` against every environment; alert when
  prod diverges from what the migration history claims.
- **Audit trail** — every validate, who, when, which fixture, what verdict.
  Immutable. This is what Priya writes the check for.
- **Approval gates** — a FAIL blocks the deploy; a WARN requires a named human to
  override, and the override is recorded.
- **Agent attestation** — the differentiated one. When an agent validated a
  migration, record which agent, which model, which fixture version, what verdict
  at the moment the PR opened. As teams let agents touch schemas, "prove the
  machine checked it against real shape before merge" becomes an audit
  requirement. Nobody sells this yet.

  **Constraint worth knowing before you sell it:** self-reported identity is
  worthless. A CLI flag saying `--agent=claude-code` is trivially spoofed, and an
  auditor will say so in the first meeting. Attestation only becomes evidence when
  bound to a workload identity — CI OIDC (GitHub Actions' token, or the
  equivalent), signing the verdict + fixture digest server-side. That makes
  attestation a **CI product, not a laptop product**. Laptop validation stays
  useful and stays unattested; don't blur the two.

Pricing shape: free CLI; ~$29/dev/mo or ~$99/connected-database/mo for registry +
audit; enterprise for SSO/on-prem. **Never gate `validate`.** The moment validate
is gated, the agent loop breaks and the wedge dies.

**License: MIT, chosen rather than defaulted to.** Atlas and pgfence can absorb
the validator under any license a serious competitor cares to work around; you
cannot out-license a funded team. The moat is being the default in the agent
loop, and every restriction taxes exactly that. AGPL would get rowshape
blanket-banned by the corporate legal departments that gate your cloud buyer.
MIT the CLI and the spec; keep the registry, audit trail, and attestation
service closed. Revisit only if someone actually ships a hosted rowshape.

## 9. Cloud architecture (phase 5 — recorded now, built later)

None of this gets built until §13 says so. Recorded because two pieces constrain
v1.

**Marketing + docs:** Astro Starlight on Cloudflare Pages. TypeScript. Fits the
existing Pages standard, purpose-built for docs + landing, ships zero JS by
default. The Go decision was about the binary, not the website — there's no
tension here.

**API: Go, on Coolify, alongside self-hosted Supabase.** Not Deno edge functions,
for one specific reason: RFC §11's canonical form must have exactly one
implementation. If the CLI digests canonical YAML in Go and the server recomputes
it in TypeScript, the two will eventually disagree on float formatting or key
ordering, attestations will stop verifying, and the cause will be invisible. The
API imports the CLI's own `fixture` and `verdict` packages. Parsing, digesting,
and verdict structs are correct by construction or not at all.

**Datastore: self-hosted Supabase.** Postgres for metadata and audit records,
Storage for fixture blobs, GoTrue for auth. Nothing exotic.

**Why a single VPS is defensible here** — and it wouldn't be for most SaaS:

- `validate` never calls the cloud (§8.4). The critical path never touches this
  infra.
- Fixtures are committed to git. The registry is a convenience, not a dependency.
  If it's down, nobody's CI breaks.
- Fixtures are value-free by design (RFC §8). A breach exposes statistics, not
  customer data. The whole format lowers the stakes of hosting it.

**Drift detection must be customer-side.** Scheduled `verify` means either the
customer runs a runner in their own infra that posts signed results, or *we hold
their production credentials*. The second makes us a breach target and kills the
deal with the exact buyer we're chasing. Customer-side also shrinks the backend to
"receive and store signed JSON."

**Attestation: don't be the root of trust.** "Immutable audit trail hosted on a
solo founder's VPS" is not an infra problem, it's a sales problem, and uptime
doesn't fix it. Instead, sign attestations and let the artifact carry its own
proof — verifiable by the customer, offline, with zero trust in us. §9.1.

Billing: Stripe.

### 9.1 Attestation and Sigstore

**Decide in v1 (cheap, retrofit is not):** the verdict is shaped as a signable
in-toto/DSSE predicate from day one.

- `subject` = the fixture digest plus digests of the migration files validated.
- `predicateType` = a URI we own, e.g. `https://rowshape.com/attestation/v1`.
- `predicate` = the verdict body already specced in §10, including
  `depends_on` and `confidence`.

Cost is field naming and hashing the migration files — near zero today, ugly to
retrofit once agents parse the format.

**Decide in phase 5 (not now):** where signatures go. This is genuinely not free,
and the tension is worth recording before it surprises us:

Public Rekor makes immutability a property of the artifact rather than a claim
about our server — which is the whole point. But it's a *public transparency log*,
and keyless signing binds a Fulcio cert to the CI workload identity. Publishing an
attestation for a private repo therefore publishes the repo identity and the
timing. For an OSS project that's free marketing. For an enterprise on a private
monorepo, it's an unacceptable leak, and they'll catch it.

So the likely landing point is two modes: public Rekor for OSS repos (free tier,
verifiable by anyone, good story), and detached signatures held in the registry —
or a private log — for enterprise. Don't resolve it now. Just don't ship a verdict
shape that can't be signed.

## 10. The verdict contract

This is the API. Everything else can change; this can't, once agents depend on it.

```json
{
  "rowshape": "1",
  "verdict": "FAIL",
  "fixture": { "id": "prod@2026-07-14", "digest": "sha256:..." },
  "duration_ms": 8420,
  "findings": [
    {
      "code": "RS-LOCK-001",
      "severity": "error",
      "title": "ACCESS EXCLUSIVE lock on users, estimated 10-60s",
      "location": { "file": "migrations/0042_add_email.sql", "line": 3 },
      "detail": "ALTER TABLE users ADD COLUMN email text NOT NULL DEFAULT '' rewrites 1.2M rows.",
      "evidence": {
        "lock_mode": "ACCESS EXCLUSIVE",
        "rows_rewritten": 1200000
      },
      "estimate": {
        "bucket": "slow",
        "model": "linear",
        "basis_rows": 12000,
        "basis_ms": 91,
        "declared_rows": 1200000
      },
      "depends_on": ["public.users.rows"],
      "confidence": "exact",
      "remediation": "Split into: ADD COLUMN nullable; backfill in batches; SET NOT NULL via a validated CHECK constraint.",
      "explain": "rowshape explain RS-LOCK-001"
    }
  ]
}
```

Rules:

- Exit codes: `0` PASS, `1` FAIL, `2` WARN-only (configurable to fail), `3` tool error.
- Finding codes are permanent, namespaced by class: `RS-LOCK`, `RS-DATA`,
  `RS-CONSTRAINT`, `RS-INDEX`, `RS-PERF`, `RS-REVERSE`.
- `remediation` is mandatory on every error. A finding an agent can't act on is a bug.
- **Every finding declares `depends_on` and carries a `confidence`** — the minimum
  across the fixture facts it relied on. Per RFC-0001 §7.4, a finding resting on
  `estimated` facts cannot produce PASS; it produces WARN naming the command that
  would resolve it. This is what makes a cheap `fast` fixture safe to default:
  it declines to certify rather than certifying wrongly.
- **Durations are buckets, never point estimates.** `instant` / `fast` /
  `noticeable` / `slow` / `outage`, with the extrapolation basis attached.
  "9.2 seconds" invites someone to time it and blog about the gap; "10–60s,
  extrapolated from 12k rows on a linear model" is a claim we can defend.
- Human output is a *rendering* of this JSON, never a separate code path. In Go
  terms: one `Verdict` struct, two marshalers. The MCP server, the CLI, and the
  GitHub Action all render the same struct.

## 11. Safety model

**The primary safety property is not data safety — it's verdict honesty.**

A tool that misses a problem is disappointing. A tool that certifies a broken
migration as PASS is worse than no tool, because it replaced a cautious human
with a confident machine. Everything below is secondary to RFC-0001 §7.4: a
verdict is capped by the confidence of the facts it relied on, and uniqueness is
never inferred from a sample.

- `pull` requires a read-only role; refuse to run as superuser without `--i-know`.
- **The README claim must be narrow and true:** a fixture contains no rows from
  your database; it contains statistics computed from them; at `--privacy
  standard` some of those reveal the extremes of numeric and date columns; at
  `--privacy strict` none do. The broader claim ("no production values leave") is
  false and will be caught (RFC-0001 §8.1).
- `rowshape inspect --leaks` enumerates every value-derived field in a fixture.
  Ship it in v1. Priya's team finds these fields either way; pointing at them
  first is the difference between a documented tradeoff and an undisclosed leak.
- `validate` never touches a non-disposable database. Hard-refuse if the target
  URL matches the fixture's source host.
- `verify` is read-only by definition.
- Deliberately no `rowshape apply` in v1. Applying is your runner's job. Blast
  radius stays at zero, and the security review stays trivial — which is exactly
  what gets this installed in a regulated shop.
- Ship SBOM and cosign signatures from the first release. Nearly free in
  goreleaser, and it's half the reason to be in Go at all.

## 12. The corpus (second repo, first asset)

How do you know `RS-LOCK-001` is *correct*? Right now: vibes. That's untenable for
a tool whose entire value proposition is that its verdicts can be trusted.

`rowshape/corpus` is a set of `(migration, fixture, expected verdict)` triples,
executable, covering the documented ways Postgres migrations break: the volatile
default that rewrites, the `SET NOT NULL` that full-scans, the unique index that
can't build, the `VALIDATE` that trips on pre-existing orphans, the cascade delete
that finds the fan-out tail, the `NOT VALID` constraint validated in the same
transaction.

Two things make this non-optional:

- **It's the regression suite for the one thing that must never be wrong.** RFC
  §7.4's capping rules are untested assertions until something exercises them.
  Write the corpus entries for capping *before* the findings they cap.
- **Findings are version-conditional, and that multiplies the matrix.** PG11+
  fast-paths `ADD COLUMN ... DEFAULT` only when the default can live in the
  catalog — a volatile default still rewrites, and `SET NOT NULL` still
  full-scans. A finding that's right on 16 is wrong on 10. The fixture already
  carries `engine.version`; the finding rules must consume it, and the corpus must
  run against every supported major.

The upside is that this is an asset, not a tax. "Here are the documented ways
Postgres migrations break, with executable reproductions" is a repo people star on
its own merits, cite in blog posts, and contribute to — including people who never
install rowshape. It's the artifact that makes the project credible rather than
clever, and it's the cheapest marketing in the plan.

## 13. v1 scope

**In:** Postgres (11–17, version-conditional findings). `pull` (fast + targeted
modes, auto-escalation, client-side HLL) / `hydrate`. Confidence model and verdict
capping. `inspect --leaks`. `validate` with RS-LOCK, RS-DATA, RS-CONSTRAINT,
RS-INDEX findings. JSON verdict + human renderer. MCP server as a subcommand.
`init --agent` (MCP config + agent rule + pre-commit hook). Corpus repo with
capping coverage across the version matrix. GitHub Action. Docker-based
disposable target. Runner detection for Alembic, Prisma, Drizzle, raw SQL
directories. Signed binaries on five platforms. Verdicts emitted in DSSE-signable
predicate shape (§9.1) — no signing infrastructure, just a format that won't need
retrofitting.

**Out:** MySQL, SQLite, cloud, GUI, query-plan regression, `apply`, approval
gates, Flyway/Liquibase (JVM ecosystem, different buyer, later).

**v1 is done when:** a Claude Code session, given a repo and a fixture, can be
told "add a NOT NULL email column to users," produce a naive migration, watch it
FAIL with RS-LOCK-001, rewrite it as a three-step backfill, and reach PASS — with
no human turn in between.

That demo is the launch. Record it before you write the README.

## 14. Sequencing

| Phase | Weeks | Ships |
|---|---|---|
| 0 | 1 | Claim `rowshape.com`, GitHub org, npm name, Go module path. Fixture Spec RFC public. Repo + goreleaser skeleton |
| 1 | 2–5 | `pull` / `hydrate` for Postgres, deterministic, own synthesis engine |
| 1b | 6 | Confidence model: HLL, uniqueness probes, auto-escalation, `inspect --leaks` |
| 2 | 7–10 | Corpus + capping tests **first**, then `validate` + verdict contract + RS-LOCK / RS-DATA |
| 3 | 11 | MCP subcommand + `init --agent` + the agent rule |
| 4 | 12 | GitHub Action, docs site, the agent-loop demo |
| 5 | 13+ | Findings breadth, version matrix depth, MySQL, then cloud only if pull-through is real |

Roughly three weeks longer than the original TypeScript sketch: one for the Go
ramp and release pipeline, one for the confidence engine, one for the corpus. All
three are paid once, and the last two are what separate a tool people trust with
production from a toy.

Note the ordering inside phase 2: the corpus and the capping tests land before the
findings they test. Reversing that means writing the findings, demoing them, and
then discovering which ones were wrong on PG 11.

Cloud does not start until the CLI has organic users. Without a few hundred stars
and unsolicited issues, the cloud has no distribution and shouldn't exist.

### 14.1 Kill criteria

Written down now, while it's cheap to be honest:

- **Week 6:** if `pull` + `hydrate` don't produce a database that reproduces a
  known pathology from the corpus, the fixture premise is wrong. Stop and
  reconsider before building `validate` on top of it.
- **Week 12:** if the agent-loop demo doesn't work end-to-end without a human
  turn, the wedge isn't real. Rowshape becomes a nice linter in a market that has
  two already — reassess whether that's worth continuing.
- **Week 20:** if there are zero unsolicited issues or PRs from people you didn't
  tell about it, that's information, not a reason to push harder.

The dominant failure mode here isn't a competitor. It's project fourteen
absorbing evenings for a year on momentum. Deciding these thresholds now is worth
more than any feature in this document.

## 15. Risks

- **pgfence is the closest competitor, not Atlas.** It shipped a Postgres
  migration safety CLI doing lock-mode analysis, risk scoring, and safe rewrite
  recipes — output nearly field-for-field identical to RS-LOCK, down to the
  expand/backfill/contract remediation. It has editor tooling and a GitHub Action,
  it's free and open-source today, and its governance tier is an exploratory
  design-partner program. That is this document's cloud model, aimed at this
  document's buyer, already in market.

  Read it precisely: **pgfence is static.** It analyzes a `.sql` file and never
  touches your data, so it cannot know `email` has 400k nulls or that `user_id`
  fan-out has a 12,902-row tail. The differentiation survives — but it is narrower
  and sharper than an earlier draft of this document claimed. Findings and
  remediation are now table stakes. The only defensible ground is the fixture, the
  confidence model, and the agent contract. Build those first and do not spend a
  week polishing finding text that pgfence already wrote.

- **Atlas ships this.** Ariga is funded, already does linting plus a registry, and
  is also Go — no moat in the language. They're static-analysis-first and
  human-first. Move fast, publish the spec early, stay the neutral layer that
  works with everyone's runner — including theirs.

- **Database branching is more mainstream than convenient.** Neon, Xata, and Vela
  all push copy-on-write branches, and 2026 practitioner writeups call branching
  the most under-used safety tool of the year — it materializes a real production
  copy in seconds. "Teams won't bother" is not the argument and never was. The
  argument is: a branch requires the vendor, requires prod access, can't be
  committed to git, and **can't be read by an agent**. A fixture is four kilobytes
  of YAML that an agent reasons over before writing SQL; a branch is a connection
  string it can only flail against. Position as complementary —
  `--target $NEON_BRANCH_URL` should work on day one, and validating against a
  branch should upgrade the relevant facts to `exact`.

- **Fixture fidelity is a tarpit.** Synthetic data will never fully reproduce prod
  pathology. Say so in the docs. A fixture catching 80% of lock and constraint
  failures is worth shipping; chasing 100% kills the project.
- **Go ramp on a solo maintainer.** The real one. The dominant failure mode for
  rowshape isn't Atlas, it's project fourteen losing steam. If week 3 is a slog
  against unfamiliar tooling rather than against the problem, that's the signal —
  reassess honestly instead of sinking cost.
- **A single wrong PASS is unrecoverable.** Reputation in this category is
  asymmetric: missing a problem is forgivable, certifying a broken migration is
  not. The confidence model (RFC-0001 §7) is the mitigation, and the pressure to
  weaken it will come from wanting a green demo, not from an attacker. Write the
  capping tests before the findings.
- **The bet is the loop, not the tool.** If agents don't get routinely trusted
  with schema work, this is just a nice linter. That bet looks good right now, but
  be honest that it's a bet.

## 16. Decisions carried into RFC-0001

- **Synthesis engine: own, not `gofakeit`.** Its distributions are decorative —
  it'll happily generate 100% unique emails when the fixture says 3.2% null and
  12% duplicated, which is exactly the pathology that breaks migrations. Build
  only the profile classes v1 needs. Do not build a general-purpose fake-data
  library.
- **Fixture Spec gets its own repo and RFC from day one.** A spec others can emit
  is worth more than a tool.
- **Sampling resolved via the confidence model,** not by finding a better
  estimator. `fast` mode stays cheap; auto-escalation to a full pass fires only on
  columns where `n_distinct/rows > 0.95` with no unique constraint — the exact set
  where a wrong answer costs an outage. Everything else declines to certify.

## 17. Open questions

1. Capture `pg_stat_statements` in v1 fixtures without acting on it, or leave it
   out entirely? (Leaning: capture, don't act.)
2. Disposable target: `testcontainers-go` (needs a Docker daemon, awkward in some
   CI and on a cold agent machine) or embedded/`pg_tmp` (fewer prerequisites, more
   fragile)? This decides how frictionless the agent loop feels on first run.
3. Auto-escalation cost ceiling (RFC-0001 §14.5). A `fast` pull that quietly
   full-scans a 400M-row table is a bad surprise. Leaning: soft cap with a WARN
   naming what was skipped.
