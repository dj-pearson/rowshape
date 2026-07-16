package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/verdict"
	"github.com/spf13/cobra"
)

// pullOptions holds the flags for `rowshape pull`.
type pullOptions struct {
	dsn     string
	out     string
	privacy string
	schemas []string
	iKnow   bool
}

// newPullCmd reads production shape read-only and emits a committable
// rowshape.yaml (RFC §13 emitter; PRD §8.1).
//
// In phase 1 this reads structure via the catalog (P1-T3). Fast-mode column
// profiling (P1-T4), privacy redaction (P1-T5), and the assembled emitter
// (P1-T6) layer on top.
func newPullCmd() *cobra.Command {
	opts := &pullOptions{out: "rowshape.yaml", privacy: "standard"}
	cmd := &cobra.Command{
		Use:   "pull [connection-url]",
		Short: "Read a database's shape (read-only) and emit rowshape.yaml",
		Long: "pull reads a database's structure and statistical shape through catalog\n" +
			"views only — never SELECT * on user tables — and writes a committable,\n" +
			"value-free rowshape.yaml. It requires a read-only role and refuses to run\n" +
			"as a superuser without --i-know.\n\n" +
			"The connection may be given as a URL argument or through the standard\n" +
			"libpq environment variables (PGHOST, PGPORT, PGUSER, PGDATABASE, ...).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.dsn = args[0]
			}
			return runPull(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.out, "out", "o", opts.out, "output path for the fixture")
	f.StringVar(&opts.privacy, "privacy", opts.privacy, "privacy level: strict | standard | permissive")
	f.StringSliceVar(&opts.schemas, "schema", nil, "restrict to these schemas (default: all non-system)")
	f.BoolVar(&opts.iKnow, "i-know", false, "override the refusal to run as a superuser")
	return cmd
}

// runPull connects, enforces the read-only posture, reads structure, and writes
// the fixture. Connection strings and credentials are never logged or written
// into the fixture (PRD §5): errors are sanitized, and meta records no host.
func runPull(ctx context.Context, opts *pullOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	conn, err := connect(ctx, opts.dsn)
	if err != nil {
		// Never surface the DSN — it may carry a password. Report only that the
		// connection failed.
		fmt.Fprintln(os.Stderr, "rowshape pull: could not connect to the database (check your connection settings)")
		return toolError()
	}
	defer conn.Close(ctx)

	if err := profile.CheckAccess(ctx, conn, opts.iKnow); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape pull: %v\n", err)
		return toolError()
	}

	f, err := profile.ReadStructure(ctx, conn, profile.Options{Schemas: opts.schemas})
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape pull: reading structure failed: %v\n", err)
		return toolError()
	}

	stampMeta(f, opts.privacy)
	if err := f.SetDigest(); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape pull: computing digest failed: %v\n", err)
		return toolError()
	}

	out, err := fixture.Marshal(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape pull: encoding fixture failed: %v\n", err)
		return toolError()
	}
	if err := os.WriteFile(opts.out, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape pull: writing %s failed: %v\n", opts.out, err)
		return toolError()
	}
	fmt.Fprintf(os.Stderr, "rowshape pull: wrote %s (%d tables)\n", opts.out, len(f.Tables))
	return nil
}

// connect opens a single connection. An empty dsn lets pgx fall back to the
// standard libpq environment variables.
func connect(ctx context.Context, dsn string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// stampMeta fills the meta fields the reader doesn't know: id, timestamps, the
// generator tag, the privacy level, and the fast profile mode. It deliberately
// records no host or connection detail (PRD §5, RFC §8.4).
func stampMeta(f *fixture.Fixture, privacy string) {
	now := time.Now().UTC().Format(time.RFC3339)
	if f.Meta.ID == "" {
		// A neutral, host-free label (PRD §5): the scan date, never the source.
		f.Meta.ID = "fixture@" + now[:10]
	}
	f.Meta.GeneratedAt = now
	f.Meta.Generator = "rowshape/" + version
	f.Meta.Privacy = privacy
	f.Meta.Profile.Mode = "fast"
	f.Meta.Profile.ScannedAt = now
}

// toolError returns the exit-3 tool-error outcome (PRD §10).
func toolError() error {
	return &ExitError{Code: verdict.ExitToolError}
}
