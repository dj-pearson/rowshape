#!/usr/bin/env bash
# verify-all.sh — run EVERY verification surface this repo has, in one command.
#
# Why this exists (CR-loop): `go test ./...` returns green while covering only
# the Go code. Four other surfaces verify things nothing in Go touches:
#
#   * docs-site `npm run verify`  — the site builds, no broken internal links,
#     and every page stays inside the 32 KiB JS budget (the amended §9 criterion,
#     D-009). Findings pages are GENERATED from internal/findings/registry.go, so
#     a Go change can break a page a Go test never renders.
#   * npm/ wrapper tests          — platform/arch naming for the published package.
#   * goreleaser check            — the release config stays valid (P0-T4).
#   * workflow YAML parse         — a malformed workflow fails only when CI runs it.
#
# In CI these live in separate, PATH-FILTERED workflows, so no single run proves
# the whole repo is sound and a local developer has no way to ask.
#
# THE RULE THIS SCRIPT FOLLOWS: a skipped check is reported loudly and changes
# the summary line. Silence must never look like success — that is the failure
# mode the code-review phase kept finding (a guard naming a guarantee it does not
# check; a mutation that matched nothing; a skipped subtest reading as a pass).
set -uo pipefail
cd "$(dirname "$0")/.."

pass=0 fail=0 skip=0
results=()

run() { # run <name> <command...>
  local name="$1"; shift
  printf '\n=== %s ===\n' "$name"
  if "$@"; then
    results+=("PASS  $name"); pass=$((pass + 1))
  else
    results+=("FAIL  $name"); fail=$((fail + 1))
  fi
}

skipped() { # skipped <name> <why>
  printf '\n=== %s ===\n  SKIPPED: %s\n' "$1" "$2"
  results+=("SKIP  $1 — $2"); skip=$((skip + 1))
}

have() { command -v "$1" >/dev/null 2>&1; }

# --- Go -----------------------------------------------------------------------
run "gofmt"  bash -c '[ -z "$(gofmt -l .)" ] || { gofmt -l .; false; }'
run "go vet" go vet ./...

if have golangci-lint; then
  run "golangci-lint" golangci-lint run
else
  skipped "golangci-lint" "not installed (CI pins v2.12.2)"
fi

# The Postgres-backed suites SKIP silently without a DSN, which is exactly the
# shape of green that hides missing coverage — so say so.
if [ -n "${ROWSHAPE_TEST_PG_DSN:-}" ]; then
  run "go test (with Postgres)" go test ./...
else
  skipped "go test (Postgres-backed suites)" \
    "ROWSHAPE_TEST_PG_DSN unset — corpus/profile/target/action/demo will SKIP and still print ok. See README for the Docker-free initdb recipe."
  run "go test (Go only, DB suites skipping)" go test ./...
fi

# --- Non-Go surfaces ----------------------------------------------------------
if have node && [ -d docs-site/node_modules ]; then
  run "docs-site build + JS budget" bash -c 'cd docs-site && npm run verify'
elif have node; then
  skipped "docs-site build + JS budget" "docs-site/node_modules missing — run 'npm ci' in docs-site/"
else
  skipped "docs-site build + JS budget" "node not installed"
fi

if have node; then
  run "npm wrapper tests" bash -c 'cd npm && node --test naming.test.js'
else
  skipped "npm wrapper tests" "node not installed"
fi

if have goreleaser; then
  run "goreleaser check" goreleaser check
else
  skipped "goreleaser check" "goreleaser not installed"
fi

if have python; then
  run "workflow YAML parses" python -c '
import yaml, glob, io, sys
bad = 0
for f in sorted(glob.glob(".github/workflows/*.yml")):
    try:
        yaml.safe_load(io.open(f, encoding="utf-8"))
        print("  ok  " + f)
    except Exception as e:
        print("  FAIL " + f + ": " + str(e)); bad += 1
sys.exit(1 if bad else 0)'
else
  skipped "workflow YAML parses" "python not installed"
fi

# --- Summary ------------------------------------------------------------------
printf '\n================ verify-all summary ================\n'
for r in "${results[@]}"; do printf '  %s\n' "$r"; done
printf '  ----------------------------------------------\n'
printf '  %d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skip"

if [ "$fail" -gt 0 ]; then
  printf '  RESULT: FAILED\n'; exit 1
fi
if [ "$skip" -gt 0 ]; then
  # Deliberately NOT exit 0: a partial run must not be mistaken for a full one.
  printf '  RESULT: PASSED, BUT INCOMPLETE (%d checks skipped above)\n' "$skip"; exit 2
fi
printf '  RESULT: PASSED (everything verified)\n'
