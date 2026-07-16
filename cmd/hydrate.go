package cmd

import (
	"fmt"
	"os"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
	"github.com/spf13/cobra"
)

// hydrateOptions holds the flags for `rowshape hydrate`.
type hydrateOptions struct {
	fixturePath string
	out         string
	seed        int64
	scale       float64
	maxRows     int64
}

// newHydrateCmd deterministically reconstructs a disposable database from a
// fixture (RFC §10, §13). In phase 1 this is the synthesis engine (P1-T7): it
// reads a fixture and emits deterministic INSERT SQL. Wiring the SQL into a
// disposable Postgres target lands in P1-T9.
func newHydrateCmd() *cobra.Command {
	opts := &hydrateOptions{fixturePath: "rowshape.yaml", out: "-", scale: 1.0}
	cmd := &cobra.Command{
		Use:   "hydrate [rowshape.yaml]",
		Short: "Reconstruct a deterministic disposable database from rowshape.yaml",
		Long: "hydrate synthesizes rows whose SHAPE matches production — row counts,\n" +
			"null fractions, cardinality, fan-out — with obviously-fake content. The\n" +
			"same fixture, seed, and engine version produce identical output on any\n" +
			"platform. In phase 1 it emits INSERT SQL; --seed makes it reproducible.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.fixturePath = args[0]
			}
			return runHydrate(opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.out, "out", "o", opts.out, "output path for the SQL ('-' for stdout)")
	f.Int64Var(&opts.seed, "seed", 0, "deterministic seed")
	f.Float64Var(&opts.scale, "scale", opts.scale, "fraction of declared rows to synthesize")
	f.Int64Var(&opts.maxRows, "max-rows", 0, "cap synthesized rows per table (0 = no cap)")
	return cmd
}

func runHydrate(opts *hydrateOptions) error {
	data, err := os.ReadFile(opts.fixturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: reading %s failed: %v\n", opts.fixturePath, err)
		return toolError()
	}
	f, err := fixture.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: %v\n", err)
		return toolError()
	}

	res, err := hydrate.Generate(f, hydrate.Options{Seed: opts.seed, Scale: opts.scale, MaxRows: opts.maxRows})
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: %v\n", err)
		return toolError()
	}

	w := os.Stdout
	if opts.out != "-" {
		file, err := os.Create(opts.out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rowshape hydrate: creating %s failed: %v\n", opts.out, err)
			return toolError()
		}
		defer file.Close()
		w = file
	}
	if err := hydrate.WriteSQL(w, res); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: writing SQL failed: %v\n", err)
		return toolError()
	}
	return nil
}
