#!/usr/bin/env bash
# rowshape GitHub Action — run `rowshape validate` and map its verdict onto a CI
# job outcome.
#
# This is a THIN WRAPPER over the released binary (P0-T4), not a reimplementation
# (PRD §13). It adds no finding logic and reveals no new facts: everything comes
# from `rowshape validate`, which renders the SAME Verdict struct as the CLI and
# the MCP server — one struct, two marshalers (PRD §10, INV-VERDICT-SHAPE). This
# script only (1) translates Action inputs to `validate` flags and (2) maps the
# process exit code onto a CI gate:
#
#   0 PASS        -> job passes
#   1 FAIL        -> job fails
#   2 WARN-only   -> job passes by default; fails when warn-as-fail is set
#   3 tool error  -> job fails (could not produce a verdict)
#
# The "configurable WARN" knob (PRD §10) is honored in one place: when
# warn-as-fail is true we pass `--validate --warn-fail`, so `validate` itself
# returns 1 for a WARN-only verdict; when it is false a raw exit 2 is remapped to
# 0 here so a WARN informs review without blocking the merge.
set -u

BIN="${ROWSHAPE_BIN:-${INPUT_BINARY:-rowshape}}"

args=(validate)
[ -n "${INPUT_FIXTURE:-}" ] && args+=("${INPUT_FIXTURE}")
[ -n "${INPUT_MIGRATIONS:-}" ] && args+=(--migrations "${INPUT_MIGRATIONS}")
[ -n "${INPUT_TARGET:-}" ] && args+=(--target "${INPUT_TARGET}")
[ -n "${INPUT_EPHEMERAL:-}" ] && args+=(--ephemeral "${INPUT_EPHEMERAL}")
[ -n "${INPUT_RUNNER:-}" ] && args+=(--runner "${INPUT_RUNNER}")
[ -n "${INPUT_SEED:-}" ] && args+=(--seed "${INPUT_SEED}")
[ -n "${INPUT_SCALE:-}" ] && args+=(--scale "${INPUT_SCALE}")
[ "${INPUT_WARN_AS_FAIL:-false}" = "true" ] && args+=(--warn-fail)

# INPUT_ARGS is an optional space-separated passthrough of extra `validate` flags.
if [ -n "${INPUT_ARGS:-}" ]; then
  # shellcheck disable=SC2206  # intentional word-splitting of extra flags
  extra=(${INPUT_ARGS})
  args+=("${extra[@]}")
fi

json="${INPUT_JSON:-true}"
out="${ROWSHAPE_VERDICT_JSON:-rowshape-verdict.json}"

# --json puts the machine-readable Verdict (or a tool-error payload) on stdout.
# Capture it to a file so a downstream step (P4-T2) can render PR annotations
# from the same struct, then echo it into the job log.
if [ "$json" = "true" ]; then
  args+=(--json)
  "$BIN" "${args[@]}" >"$out"
  code=$?
  cat "$out"
else
  "$BIN" "${args[@]}"
  code=$?
fi

# Extract the verdict string for the step output. The JSON is pretty-printed, so
# "verdict": "FAIL" sits on its own line; a tool error has no verdict field.
verdict=""
if [ "$json" = "true" ] && [ -f "$out" ]; then
  verdict=$(sed -n 's/.*"verdict"[[:space:]]*:[[:space:]]*"\([A-Za-z]*\)".*/\1/p' "$out" | head -n1)
fi

# Map the validate exit code onto the CI gate. Only a WARN-only (2) is softened,
# and only because warn-as-fail=false means "do not block on WARN"; FAIL (1) and
# tool error (3) always fail the job.
job=$code
if [ "$code" -eq 2 ]; then
  job=0
fi

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  {
    echo "verdict=${verdict}"
    echo "exit-code=${code}"
    echo "verdict-json=${out}"
  } >>"$GITHUB_OUTPUT"
fi

echo "rowshape: verdict=${verdict:-<none>} (validate exit ${code}; job exit ${job})" >&2
exit "$job"
