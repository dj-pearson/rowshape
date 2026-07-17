package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
	"github.com/rowshape/rowshape/internal/target"
	"github.com/spf13/cobra"
)

// hydrateOptions holds the flags for `rowshape hydrate`.
type hydrateOptions struct {
	fixturePath string
	out         string
	target      string
	ephemeral   string
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
	f.StringVar(&opts.target, "target", "", "hydrate into this database URL instead of emitting SQL")
	f.StringVar(&opts.ephemeral, "ephemeral", "", "admin URL: create a disposable database, hydrate into it, then drop it")
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

	genOpts := hydrate.Options{Seed: opts.seed, Scale: opts.scale, MaxRows: opts.maxRows}

	// If a live target is requested, load rows into it instead of emitting SQL.
	if opts.target != "" || opts.ephemeral != "" {
		return loadIntoTarget(f, genOpts, opts)
	}

	res, err := hydrate.Generate(f, genOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: %v\n", err)
		return toolError()
	}

	if opts.out == "-" {
		if err := hydrate.WriteSQL(os.Stdout, res); err != nil {
			fmt.Fprintf(os.Stderr, "rowshape hydrate: writing SQL failed: %v\n", err)
			return toolError()
		}
		return nil
	}

	file, err := os.Create(opts.out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: creating %s failed: %v\n", opts.out, err)
		return toolError()
	}
	if err := hydrate.WriteSQL(file, res); err != nil {
		_ = file.Close()
		fmt.Fprintf(os.Stderr, "rowshape hydrate: writing SQL failed: %v\n", err)
		return toolError()
	}
	// Close is checked, not deferred. This is a write path: a Close error is a
	// failed flush — a full disk, a network filesystem — which means the SQL on
	// disk is truncated. Deferring it would drop that error and report success
	// over a corrupt file, and hydrate's entire promise is that its output is
	// byte-reproducible for a given fixture and seed.
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: closing %s failed (the file may be incomplete): %v\n", opts.out, err)
		return toolError()
	}
	return nil
}

// loadIntoTarget hydrates directly into a live database: a user-provided one
// (--target) or a disposable ephemeral database created and dropped for the run
// (--ephemeral). Connection strings and credentials are never logged.
func loadIntoTarget(f *fixture.Fixture, genOpts hydrate.Options, opts *hydrateOptions) error {
	ctx := context.Background()

	var t target.Target
	if opts.target != "" {
		t = target.NewProvided(opts.target)
	} else {
		eph, err := target.NewEphemeral(ctx, opts.ephemeral)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rowshape hydrate: could not create a disposable database (check the admin connection)")
			return toolError()
		}
		// A disposable target is always torn down, even on failure.
		defer func() { _ = eph.Close(ctx) }()
		t = eph
	}

	report, err := target.Load(ctx, t, f, genOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape hydrate: load failed: %v\n", err)
		return toolError()
	}
	var total int64
	for _, n := range report.Tables {
		total += n
	}
	fmt.Fprintf(os.Stderr, "rowshape hydrate: loaded %d rows across %d tables\n", total, len(report.Tables))
	return nil
}
