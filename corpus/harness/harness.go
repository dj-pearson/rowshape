// Package harness loads and runs the rowshape corpus: executable
// (migration, fixture, expected verdict) triples covering the documented ways a
// Postgres migration breaks (PRD §12). It is a runnable asset, not prose — each
// case is a directory under corpus/cases/ holding migration.sql, fixture.yaml,
// and expected.json.
//
// The harness consumes a fixture + migration and (once `validate` lands in
// P2-T7, via the Validator hook) asserts the expected verdict. Until then it
// asserts every triple is well-formed and the corpus covers every named
// pathology — the corpus is written and proven BEFORE the findings it tests
// (PRD §14 ordering).
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
)

// KnownCodes are the permanent, namespaced finding codes (INV-VERDICT-STABLE).
var KnownCodes = map[string]bool{
	"RS-LOCK": true, "RS-DATA": true, "RS-CONSTRAINT": true,
	"RS-INDEX": true, "RS-PERF": true, "RS-REVERSE": true,
}

// KnownVerdicts are the three verdict values (PRD §10).
var KnownVerdicts = map[string]bool{"PASS": true, "WARN": true, "FAIL": true}

// ExpectedFinding is one finding the corpus expects a validator to produce.
type ExpectedFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
}

// Expected is the expected verdict for a case.
type Expected struct {
	Description string            `json:"description"`
	Verdict     string            `json:"verdict"`
	Findings    []ExpectedFinding `json:"findings"`
}

// Case is one corpus triple.
type Case struct {
	Name      string
	Dir       string
	Migration string
	Fixture   *fixture.Fixture
	Expected  Expected
}

// PGMajor returns the Postgres major version the harness runs against, from
// ROWSHAPE_PG_VERSION (default 16). This is the knob the CI matrix drives in
// P2-T13 to run the whole corpus against PG 11–17.
func PGMajor() string {
	if v := os.Getenv("ROWSHAPE_PG_VERSION"); v != "" {
		return v
	}
	return "16"
}

// LoadCases reads and parses every triple under <root>/cases. root is the corpus
// directory. A malformed triple is an error — the corpus itself must be valid.
func LoadCases(root string) ([]Case, error) {
	casesDir := filepath.Join(root, "cases")
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		return nil, fmt.Errorf("read cases dir: %w", err)
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := loadCase(filepath.Join(casesDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("case %s: %w", e.Name(), err)
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases, nil
}

func loadCase(dir string) (Case, error) {
	c := Case{Name: filepath.Base(dir), Dir: dir}

	mig, err := os.ReadFile(filepath.Join(dir, "migration.sql"))
	if err != nil {
		return c, fmt.Errorf("read migration.sql: %w", err)
	}
	c.Migration = string(mig)

	fx, err := os.ReadFile(filepath.Join(dir, "fixture.yaml"))
	if err != nil {
		return c, fmt.Errorf("read fixture.yaml: %w", err)
	}
	parsed, err := fixture.Parse(fx)
	if err != nil {
		return c, fmt.Errorf("parse fixture.yaml: %w", err)
	}
	c.Fixture = parsed

	exp, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		return c, fmt.Errorf("read expected.json: %w", err)
	}
	if err := json.Unmarshal(exp, &c.Expected); err != nil {
		return c, fmt.Errorf("parse expected.json: %w", err)
	}
	return c, nil
}

// Validate checks a triple is well-formed: a non-empty migration, a fixture that
// declares an engine version (required for extrapolation, RFC §9.1), and an
// expected verdict using only known verdicts and finding codes.
func (c Case) Validate() error {
	if strings.TrimSpace(c.Migration) == "" {
		return fmt.Errorf("empty migration.sql")
	}
	if c.Fixture == nil || c.Fixture.Meta.Engine.Version == "" {
		return fmt.Errorf("fixture is missing meta.engine.version")
	}
	if !KnownVerdicts[c.Expected.Verdict] {
		return fmt.Errorf("unknown expected verdict %q", c.Expected.Verdict)
	}
	for _, f := range c.Expected.Findings {
		if !KnownCodes[f.Code] {
			return fmt.Errorf("unknown finding code %q", f.Code)
		}
	}
	return nil
}

// Validator runs a migration against a fixture and returns the verdict and the
// finding codes it produced. `validate` implements this in P2-T7; until then the
// harness runs only the well-formedness and coverage checks.
type Validator interface {
	Validate(c Case) (verdict string, codes []string, err error)
}
