package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/verdict"
	"github.com/spf13/cobra"
)

// verifyOptions holds the flags for `rowshape verify`.
type verifyOptions struct {
	against     string
	fixturePath string
}

// newVerifyCmd implements `rowshape verify --against <url>`: a read-only,
// post-deploy check that a live target's actual schema matches the intent
// declared in a fixture (PRD §8.1, §11). It is read-only BY DEFINITION — it only
// reads the target's structure (profile.ReadStructure, INV-BLAST-RADIUS-ZERO) —
// and reports drift, exiting non-zero when reality does not match intent.
func newVerifyCmd() *cobra.Command {
	opts := &verifyOptions{fixturePath: "rowshape.yaml"}
	cmd := &cobra.Command{
		Use:   "verify [rowshape.yaml]",
		Short: "Read-only check that a live target matches the intended schema (drift)",
		Long: "verify reads a live target's schema (read-only) and compares it to the\n" +
			"schema a fixture declares: tables, columns, nullability, and constraints.\n" +
			"It writes nothing. It exits 0 when reality matches intent, 1 on drift.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.fixturePath = args[0]
			}
			return runVerify(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.against, "against", "", "live database URL to verify (read-only)")
	_ = cmd.MarkFlagRequired("against")
	return cmd
}

func runVerify(opts *verifyOptions) error {
	ctx := context.Background()

	data, err := os.ReadFile(opts.fixturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape verify: reading %s failed: %v\n", opts.fixturePath, err)
		return toolError()
	}
	expected, err := fixture.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape verify: %v\n", err)
		return toolError()
	}

	actual, err := readLiveSchema(ctx, opts.against)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape verify: %v\n", err)
		return toolError()
	}

	drifts := compareSchema(expected, actual)
	writeVerify(os.Stdout, opts.against, drifts)
	if len(drifts) > 0 {
		return &ExitError{Code: verdict.ExitFail}
	}
	return nil
}

// drift is one mismatch between the intended fixture and the live target.
type drift struct {
	Object string
	Want   string
	Got    string
}

// compareSchema reports where the live target does not match the intended
// fixture: missing tables/columns, a NOT NULL that isn't enforced, a type
// mismatch, or a missing constraint. It is a comparison only — no writes.
func compareSchema(expected, actual *fixture.Fixture) []drift {
	var drifts []drift
	for _, tname := range sortedTableNames(expected) {
		et := expected.Tables[tname]
		at, ok := actual.Tables[tname]
		if !ok {
			drifts = append(drifts, drift{Object: tname, Want: "present", Got: "missing table"})
			continue
		}
		for _, cname := range sortedColNames(et.Columns) {
			ec := et.Columns[cname]
			ac, ok := at.Columns[cname]
			if !ok {
				drifts = append(drifts, drift{Object: tname + "." + cname, Want: "present", Got: "missing column"})
				continue
			}
			if normType(ec.Type) != normType(ac.Type) && ec.Type != "" {
				drifts = append(drifts, drift{Object: tname + "." + cname, Want: "type " + ec.Type, Got: "type " + ac.Type})
			}
			if !ec.Nullable && ac.Nullable {
				drifts = append(drifts, drift{Object: tname + "." + cname, Want: "NOT NULL", Got: "nullable"})
			}
		}
		for _, ec := range et.Constraints {
			if !hasConstraint(at, ec) {
				drifts = append(drifts, drift{Object: tname + " constraint " + ec.Name, Want: ec.Kind, Got: "missing"})
			}
		}
	}
	return drifts
}

func writeVerify(w io.Writer, against string, drifts []drift) {
	fmt.Fprintf(w, "verify against %s (read-only)\n\n", redactURL(against))
	if len(drifts) == 0 {
		fmt.Fprintln(w, "  [OK] reality matches intent")
		return
	}
	for _, d := range drifts {
		fmt.Fprintf(w, "  [DRIFT] %s: want %s, got %s\n", d.Object, d.Want, d.Got)
	}
	fmt.Fprintf(w, "\n%d drift(s) — reality does not match intent\n", len(drifts))
}

// hasConstraint reports whether the live table carries a constraint matching the
// expected one by name, or failing that by kind + column set.
func hasConstraint(at fixture.Table, ec fixture.Constraint) bool {
	for _, ac := range at.Constraints {
		if ac.Name == ec.Name {
			return true
		}
		if ac.Kind == ec.Kind && sameCols(ac.Columns, ec.Columns) && len(ec.Columns) > 0 {
			return true
		}
	}
	return false
}

func sameCols(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// normType normalizes common Postgres type spellings so equivalent types compare
// equal (e.g. int4 == integer, timestamptz == timestamp with time zone).
func normType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "int4", "int":
		return "integer"
	case "int8":
		return "bigint"
	case "int2":
		return "smallint"
	case "bool":
		return "boolean"
	case "timestamptz":
		return "timestamp with time zone"
	case "timestamp":
		return "timestamp without time zone"
	case "varchar":
		return "character varying"
	}
	return t
}

func sortedTableNames(f *fixture.Fixture) []string {
	out := make([]string, 0, len(f.Tables))
	for k := range f.Tables {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedColNames(m map[string]fixture.Column) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
