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
