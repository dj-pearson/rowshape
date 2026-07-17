# Rowshape — Decisions Log

This file is the durable record of cross-cutting decisions referenced by `prd.json`.
It is the source of truth for the canonical Go module path (P0-T1) and the
resolution status of the `open_decisions` in `prd.json`.

---

## D-001 — Canonical Go module path

**Decision:** `github.com/rowshape/rowshape`

- This is the fixed import root for the OSS CLI + MCP server binary.
- Every internal package imports under this path (e.g.
  `github.com/rowshape/rowshape/internal/fixture`,
  `github.com/rowshape/rowshape/internal/verdict`).
- The phase-5 cloud API (PRD §9) imports the CLI's `fixture` and `verdict`
  packages from this same path — there must be exactly ONE implementation of
  canonical form + digesting (INV-ONE-CANONICAL-FORM).
- `go.mod` MUST declare exactly this module path. (P0-T1 verification:
  `grep` module path in `go.mod` matches this file.)

Rationale: PRD §14 Phase 0, §7 (distribution / single static binary).

> Status note: the repository currently lives at `github.com/dj-pearson/rowshape`.
> The Go module path is decided independently of where the git remote points and
> will not change when the code moves under the `rowshape` GitHub org. Imports are
> stable from day one.

---

## D-002 — Namespace reservations (P0-T1)

These are external ops actions. Each must be confirmed by the owner
(Dan Pearson / Pearson Media LLC) and the confirmation captured here.

| Namespace | Target | Status | Confirmation |
|-----------|--------|--------|--------------|
| Domain | `rowshape.com` | ☐ pending | _capture registrar confirmation_ |
| GitHub org | `rowshape` | ☐ pending | _create org; move repos under it_ |
| npm package | `rowshape` | ☐ pending | _placeholder publish of the npx wrapper package name_ |
| GitHub repo | `rowshape/fixture-spec` | ☐ pending | _create public repo; RFC-0001 + README + LICENSE (P0-T2)_ |
| GitHub repo | `rowshape/homebrew-tap` | ☐ pending | _tap the goreleaser cask targets (P0-T4)_ |
| Go module path | `github.com/rowshape/rowshape` | ☑ decided | D-001 above |

Until the three external reservations are confirmed, **P0-T1 stays `blocked`**
in `prd.json` (loop rule: never fake-pass; never weaken acceptance criteria).
The Go module path — the only part that blocks downstream code — is settled, so
code tasks (P0-T3+) can proceed.

---

## D-003 — Open decisions (mirrors `prd.json.open_decisions`)

Tracked here so resolutions are recorded where code can cite them.

| ID | Question (short) | Leaning | Resolve by |
|----|------------------|---------|-----------|
| OQ-PGSS | Capture `pg_stat_statements` in v1 fixtures? | Capture, don't act (schema-additive) | phase-1 (P1-T1) |
| OQ-TARGET | Disposable target: testcontainers-go vs embedded/pg_tmp? | Unresolved; ship testcontainers-go default, keep swap point clean | phase-1 (P1-T9) |
| OQ-ESCALATION-CEILING | Cost ceiling for auto-escalation? | Soft cap + WARN naming what was skipped | phase-1b (P1b-T4) |
| OQ-HLL-PRECISION | HLL precision 14 vs 16? | 14 (~1.6% err, 16KB) — measured only needs to beat estimated | phase-1b (P1b-T1) |
| OQ-PARTITIONS | Partitioned tables: parent-only or per-partition? | Parent declares `partitions: {count, strategy, skew}` | phase-1 (P1-T12) |
| OQ-BLOAT | Does `bloat_estimate` belong in a portable spec? | Unresolved; revisit at MySQL | phase-5 (P5-T4) |
| OQ-CORRELATION | Multi-column correlation block now or defer? | Defer to v2 | v2 (out of v1 scope) |

---

## D-005 — Disposable hydrate target (OQ-TARGET, P1-T9)

**Decision:** A single `internal/target.Target` interface abstracts the database
`hydrate` loads into, so the disposable mechanism can be swapped without touching
the synthesis engine. Three implementations ship:

- **`Ephemeral`** (default disposable target) — creates a throwaway database on a
  reachable Postgres server and drops it on teardown. Dependency-light: it needs
  only a libpq connection, no Docker daemon and no container SDK.
- **`Provided`** — hydrate loads into a user-supplied `--target` URL; teardown
  only closes connections, never drops the database.
- **`Container`** — a Docker-based disposable target (throwaway `postgres`
  container) for full OS-level isolation, invoked through the `docker` CLI.

**Why not testcontainers-go as the default (RFC §17.2 / OQ-TARGET):**
`testcontainers-go` pulls in the Docker client SDK and dozens of transitive
dependencies, which conflicts with **INV-SUPPLY-CHAIN** ("single static binary,
deps kept deliberately few"). The `Target` interface keeps testcontainers-go — or
`pg_tmp` / an embedded Postgres — a clean swap-in behind the same contract, so the
open question stays genuinely open while the shipped default stays dependency-light.
The ephemeral-database default provides the same observable behaviour the story
requires: a disposable Postgres is spun up and torn down after use.

---

## D-004 — Cloud traction gate (P5-T7)

Cloud (registry, audit, drift, attestation, billing) does NOT start until the
CLI shows organic pull-through (PRD §14 / §14.1 week-20 kill criterion).

**Gate criteria (must be written down and measurable):**

- [ ] GitHub stars ≥ a few hundred (organic, not solicited)
- [ ] ≥ 1 unsolicited issue or PR from someone not personally told about the project

A negative gate result is recorded as an explicit **stop / reassess** decision —
not silently ignored, not a reason to push harder on momentum alone. Cloud tasks
P5-T8..P5-T14 stay `blocked` until this gate is explicitly marked passed here.

---

## D-006 — Version-conditional extrapolation boundary is PG 11, not 11↔16 (P2-T5)

**Context:** P2-T5 acceptance criterion 4 asks for "a version-conditional case
[that] differs correctly between PG 11 and PG 16." Per RFC §9.1 the operation
whose cost is version-conditional is `ADD COLUMN ... DEFAULT`: PG **11+**
fast-paths a *non-volatile* default into the catalog (O(1), instant) instead of
rewriting the table; a *volatile* default rewrites on every version, and
`SET NOT NULL` full-scans on every version.

**Decision:** The catalog fast-path landed in PG 11, so PG 11 and PG 16 behave
**identically** for this operation — correctly, not by omission. The genuine
divergence is at the **10 → 11** boundary: PG 10 rewrites the whole table (a
heavy, user-visible bucket) while PG 11 and PG 16 are a catalog-only instant.

`internal/estimate` therefore asserts version-conditional divergence across the
**real** boundary (PG 10 vs 11 vs 16 in `TestVersionConditionalDivergence`),
including that PG 11 == PG 16. Fabricating a false 11-vs-16 difference — or
modeling the PG 12 `SET NOT NULL`/`CHECK` scan-skip, which the P2-T5 description
explicitly excludes ("SET NOT NULL still full-scans") — would contradict the
spec, so neither was done (loop rule: spec wins; never fake a pass).

The corpus PG-version matrix (P2-T13, `ROWSHAPE_PG_VERSION` 11–17) is where
version-conditioned corpus *runs* live; the model's version-conditionality is
proven here in the estimate unit tests named by P2-T5's verification.

## D-007 — Corpus version matrix extended to PG 10 to exercise the real boundary (P5-T3)

**Context:** P5-T3 asks for corpus cases that "cover version-divergent behaviors
… across PG 11-17" and cost models that "produce version-correct buckets on each
major." But D-006 established that within PG **11–17** the operations rowshape
models do **not** diverge: the non-volatile-`DEFAULT` catalog fast-path landed in
PG 11, so PG 11 == PG 17 for `ADD COLUMN … DEFAULT`; a volatile default rewrites
on every version; `SET NOT NULL` full-scans on every version; a bare (non-`CHECK`
-assisted) `SET NOT NULL` gains nothing from the PG 12 optimization. Fabricating a
false 11-vs-17 divergence would contradict the spec (loop rule: spec wins; never
fake a pass).

**Decision:** The genuine version boundary is **10 → 11**, so the version matrix
is extended down to PG **10** (`.github/workflows/corpus.yml` now runs 10–17). A
single new corpus case, `version-add-column-default`, carries the same migration
across that boundary: `ADD COLUMN … DEFAULT '<const>'` is a catalog-only instant
(PASS) on PG 11+ but a full-table `ACCESS EXCLUSIVE` rewrite (RS-LOCK WARN) on PG
10. This is the RFC §9.1 point made executable — the older databases most likely
to hold scary migrations are exactly where the model must not be confidently
wrong.

To express "right on one major, wrong on another" the corpus format gains an
optional per-major override: `expected.json` may carry a `version_verdicts` map
(keyed by the major the matrix drives, e.g. `"10"`) that overrides the default
verdict/findings for that major only. `Expected.ForMajor(major)` resolves it, and
`TestCorpusVerdicts` compares against the resolved expectation for the major under
test. Every other case keeps a single verdict that holds across the whole matrix.

The model's per-major verdict is also proven **offline** (no live server) in
`internal/findings/version_matrix_test.go`, which drives the case through
`BuildResult` for PG 10–17 and asserts WARN on 10, PASS on 11–17. Deepening the
model further (e.g. modeling the PG 12 `CHECK`-assisted `SET NOT NULL` scan-skip)
would require a new duration finding for a *different* migration shape (a
pre-existing validated `CHECK (col IS NOT NULL)`); it is deliberately left for a
follow-up rather than bolted onto this boundary case.

---

## D-008 — Release + deploy infrastructure gate (P0-T4, P0-T5, P4-T1, P4-T3)

All buildable work for the release pipeline, the GitHub Action, and the docs site
is **complete and locally verified**; the remaining steps are owner-only and
cannot be performed from the build environment. They gate a large set of stories
from flipping to `passes: true` (the loop rule: never fake-pass).

**One-time owner setup (order matters):**

1. Reserve the namespaces + create the repos in D-002 (org must exist first).
2. Add repository/organization **secrets** for the release + deploy workflows:
   - `NPM_TOKEN` — publish the npm wrapper (`.github/workflows/release.yml`).
   - `HOMEBREW_TAP_GITHUB_TOKEN` — push the cask to `rowshape/homebrew-tap`
     (without it the release SUCCEEDS but the tap silently does not update).
   - cosign keyless signing uses CI OIDC (`id-token: write` — already in the
     workflow; no secret, but requires the workflow to run under the org).
   - `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` — deploy the docs site
     (`.github/workflows/docs-deploy.yml`); create the Cloudflare Pages project.
3. Push the first release tag: `git tag v0.1.0 && git push --tags` → the release
   workflow builds all 5 platform/arch archives, SBOM + cosign, the Homebrew cask,
   the ghcr image, and publishes npm.

**What unblocks once the org exists + a tag is published:**
`P0-T4`, `P0-T5` become verifiable; `P4-T1` (the Action fetches a released binary)
and the DB-backed e2e (`test/action`, `test/demo`) run for real; `P4-T3` docs
deploy once Cloudflare secrets exist. Until then these stay `blocked`.

---

## D-009 — Two PRD wording amendments pending owner sign-off (P4-T3, P4-T6, P4-T7)

Two acceptance criteria are, as literally written, **impossible against the
correct implementation**. They were NOT reinterpreted or relaxed unilaterally
(loop rule: never weaken an acceptance criterion to make a task pass); they are
recorded here for an owner decision, each with a recommendation. Deciding them is
what lets the affected docs/demo stories flip to `passes: true`.

| ID | Criterion (as written) | Why it can't hold | Recommendation |
|----|------------------------|-------------------|----------------|
| **§9 zero-JS** (P4-T3) | "ships zero client JS (Starlight default)" | Starlight emits 8.6–11.1 KiB/page of progressive-enhancement JS; there is no supported zero-JS mode. | Amend to "**no framework runtime; client JS under a per-page budget enforced in CI**" — already implemented (`check-build.mjs`, 32 KiB/page). |
| **§13 FAIL RS-LOCK-001** (P4-T6, P4-T7) | naive migration "→ FAIL RS-LOCK-001" | `RS-LOCK-001` is `WARN` **by design** (a rewrite is an availability problem, not data corruption; capping only lowers severity). FAIL is reserved for RS-DATA integrity findings. | Amend to "**rejected on RS-LOCK-001 (WARN gated to fail)**". The demo gates the WARN via `--warn-fail` + the agent rule, so the loop still rejects the naive migration. |

Corollary already handled in the demo (no decision needed, recorded for context):
the three-step rewrite enforces NOT NULL via a **validated `CHECK`**, not a final
`SET NOT NULL`, because rowshape correctly cannot certify `SET NOT NULL` on a
column absent from the fixture (it caps to WARN). This is the tool being more
honest than the PRD shorthand; the validated CHECK is the equivalent it can prove.
