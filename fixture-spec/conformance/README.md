# Rowshape Fixture Spec — Conformance Suite

The strategic value of the fixture format is that **anyone can emit it** (PRD §3,
§16). This directory is what makes the spec a position rather than an aspiration:
an executable conformance suite and a published, machine-readable JSON Schema that
a third party can run against their own emitter, hydrator, or validator.

In the published layout this is the `rowshape/fixture-spec` repository; here it
lives under `fixture-spec/` in the monorepo.

## What it checks (RFC-0001 §13)

**Emitter MUSTs** (`CheckEmitter`, static, over any parsed fixture):

- a known format version (`rowshape_fixture: "1"`, RFC §12);
- **never `range` on a text or bytea column** — its min/max are real values (§6.1);
- **`unique` is exact or absent** — a sample can never establish uniqueness (§7.2);
- every fact carries a valid confidence (§6.1);
- the canonical digest is stable across repeated computation (§11).

**Hydrator MUSTs** (`CheckHydrator`, by hydrating a fixture):

- honors `null_fraction` within ±0.5%;
- honors `unique` (a unique column hydrates distinct values);
- is deterministic — same fixture + seed → byte-identical output (§10).

**Validator MUSTs** (`CheckValidator`, over the verdict engine):

- caps a verdict by the minimum confidence of the facts it rests on — a finding
  on an unproven fact yields WARN, never PASS (§7.4);
- reports durations only as the five buckets `instant/fast/noticeable/slow/outage`
  (§9.2);
- reads a dependency's confidence from the fixture, never from the finding, so it
  cannot be lowered to reach a stronger verdict (structural).

## Running it

```
go test ./fixture-spec/...
```

The reference fixtures live in `fixtures/valid/` (must pass) and
`fixtures/invalid/` (must be rejected — a `range` on text, and uniqueness claimed
from a sample). A suite that cannot reject the invalid fixtures proves nothing.

## The JSON Schema

`../schema/rowshape.schema.json` is a JSON Schema (draft 2020-12) for
`rowshape.yaml` format version `"1"`. It validates every reference and corpus
fixture and rejects a fixture claiming `unique` at less than `exact` confidence.
Validate any fixture with a standard tool, e.g.:

```
pip install check-jsonschema
check-jsonschema --schemafile schema/rowshape.schema.json path/to/rowshape.yaml
```

CI (`.github/workflows/conformance.yml`) runs the Go suite and the schema
validation on every push.

## For third parties

Point `CheckEmitter` at a fixture your tool produced; hydrate a reference fixture
with your hydrator and pass it to `CheckHydrator`; or validate your `rowshape.yaml`
against the JSON Schema. The MUSTs are the contract — meet them and your tool
interoperates with every rowshape consumer, including the agents that read the
format before writing SQL.
