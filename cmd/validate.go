package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
	"github.com/rowshape/rowshape/internal/runner"
	"github.com/rowshape/rowshape/internal/target"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
	"github.com/spf13/cobra"
)

// validateOptions holds the flags for `rowshape validate`.
type validateOptions struct {
	fixturePath string
	migrations  string // a .sql file or a project/migrations directory
	target      string // a provided live target (e.g. a Neon branch URL)
	ephemeral   string // admin URL: create a disposable database for the run
	runnerKind  string // override runner auto-detection
	asJSON      bool
	warnFail    bool // make a WARN-only verdict exit non-zero
	calibrate   bool // fit the cost curve at two scales, upgrading estimates to measured
	seed        int64
	scale       float64
	maxRows     int64
}

// newValidateCmd applies a proposed migration against a hydrated disposable
// target (or a provided live branch) and returns a verdict. It is free forever
// and never calls the cloud (INV-NEVER-GATE-VALIDATE): this command imports no
// network client. Its blast radius is zero (INV-BLAST-RADIUS-ZERO): there is no
// `apply`, and it hard-refuses a target whose host matches the fixture's source.
func newValidateCmd() *cobra.Command {
	opts := &validateOptions{fixturePath: "rowshape.yaml", migrations: "migrations", scale: 1.0}
	cmd := &cobra.Command{
		Use:   "validate [rowshape.yaml]",
		Short: "Validate a migration against production-shaped data; return a verdict",
		Long: "validate hydrates a disposable Postgres from the fixture, applies the\n" +
			"migration set through your own runner, captures what happened (locks,\n" +
			"durations, rows, constraint violations, index builds), and returns a\n" +
			"verdict. Against a provided live branch (--target) the facts are ground\n" +
			"truth. validate never touches the fixture's source database.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.fixturePath = args[0]
			}
			return runValidate(opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.migrations, "migrations", "m", opts.migrations, "migration .sql file or directory")
	f.StringVar(&opts.target, "target", "", "validate against this live database URL (its data is ground truth)")
	f.StringVar(&opts.ephemeral, "ephemeral", "", "admin URL: create a disposable database, hydrate into it, then drop it")
	f.StringVar(&opts.runnerKind, "runner", "", "override runner detection (alembic|prisma|drizzle|rawsql)")
	f.BoolVar(&opts.asJSON, "json", false, "emit the machine-readable verdict as JSON")
	f.BoolVar(&opts.warnFail, "warn-fail", false, "exit non-zero on a WARN-only verdict")
	f.BoolVar(&opts.calibrate, "calibrate", false, "hydrate at two scales and fit the cost curve, upgrading duration estimates to measured (slower)")
	f.Int64Var(&opts.seed, "seed", 0, "deterministic hydration seed")
	f.Float64Var(&opts.scale, "scale", opts.scale, "fraction of declared rows to hydrate")
	f.Int64Var(&opts.maxRows, "max-rows", 0, "cap hydrated rows per table (0 = no cap)")
	return cmd
}

func runValidate(opts *validateOptions) error {
	ctx := context.Background()

	data, err := os.ReadFile(opts.fixturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape validate: reading %s failed: %v\n", opts.fixturePath, err)
		return toolError()
	}
	f, err := fixture.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape validate: %v\n", err)
		return toolError()
	}

	// Resolve the target and enforce the host-match refusal BEFORE touching it.
	groundTruth := opts.target != ""
	adminOrTarget := opts.target
	if adminOrTarget == "" {
		adminOrTarget = opts.ephemeral
	}
	if adminOrTarget == "" {
		fmt.Fprintln(os.Stderr, "rowshape validate: provide a disposable target (--ephemeral <admin-url>) or a live target (--target <url>)")
		return toolError()
	}
	if host := hostOf(adminOrTarget); host != "" {
		if err := validate.CheckHost(f.Meta.Source, host); err != nil {
			fmt.Fprintf(os.Stderr, "rowshape validate: %v\n", err)
			return toolError()
		}
	}

	// Prepare the target and capture. A provided live branch is used as-is
	// (ground truth); a disposable database is hydrated from the fixture.
	var cap *validate.Capture
	if groundTruth {
		if opts.calibrate {
			fmt.Fprintln(os.Stderr, "rowshape validate: --calibrate applies only to a disposable target; a provided branch's data is already ground truth")
			return toolError()
		}
		cap, err = applyAndCapture(ctx, target.NewProvided(opts.target), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rowshape validate: %v\n", err)
			return toolError()
		}
	} else {
		cap, err = hydrateApplyEphemeral(ctx, f, opts, opts.scale)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rowshape validate: %v\n", err)
			return toolError()
		}
		// --calibrate fits the cost curve to a SECOND run at half scale, upgrading
		// duration estimates from `estimated` to `measured` (RFC §9.2). Slower and
		// honest — the option for the one migration you're genuinely nervous about.
		if opts.calibrate {
			cap2, err := hydrateApplyEphemeral(ctx, f, opts, opts.scale/2)
			if err != nil {
				fmt.Fprintf(os.Stderr, "rowshape validate: calibration run failed: %v\n", err)
				return toolError()
			}
			cap.Calibration = &validate.Calibration{
				TableRows:    cap2.TableRows,
				StatementMs2: statementDurations(cap2),
			}
		}
	}

	result := validate.BuildResult(f, cap, validate.Registered(), groundTruth)
	if err := result.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape validate: produced verdict is malformed: %v\n", err)
		return toolError()
	}
	if fs := cap.FailedStatement(); fs != nil {
		fmt.Fprintf(os.Stderr, "rowshape validate: migration did not apply cleanly: %s (%s)\n", fs.ErrMsg, fs.ErrCode)
	}

	if err := emitResult(os.Stdout, result, opts.asJSON); err != nil {
		return toolError()
	}
	if code := result.ExitCode(opts.warnFail); code != verdict.ExitPass {
		return &ExitError{Code: code}
	}
	return nil
}

// hydrateApplyEphemeral creates a disposable database, hydrates the fixture at
// the given scale, applies the migration, and returns the capture (with hydrated
// row counts). The database is torn down before returning.
func hydrateApplyEphemeral(ctx context.Context, f *fixture.Fixture, opts *validateOptions, scale float64) (*validate.Capture, error) {
	eph, err := target.NewEphemeral(ctx, opts.ephemeral)
	if err != nil {
		return nil, fmt.Errorf("could not create a disposable database (check the admin connection): %w", err)
	}
	defer func() { _ = eph.Close(ctx) }()

	report, err := target.Load(ctx, eph, f, hydrate.Options{Seed: opts.seed, Scale: scale, MaxRows: opts.maxRows})
	if err != nil {
		return nil, fmt.Errorf("hydration failed: %w", err)
	}
	cap, err := applyAndCapture(ctx, eph, opts)
	if err != nil {
		return nil, err
	}
	cap.TableRows = report.Tables
	return cap, nil
}

// statementDurations extracts the per-statement wall times of a capture, aligned
// by index with the primary run's statements (both apply the same migration).
func statementDurations(c *validate.Capture) []int64 {
	ms := make([]int64, len(c.Statements))
	for i, st := range c.Statements {
		ms[i] = st.DurationMs
	}
	return ms
}

// applyAndCapture applies the migration set to the target and captures the six
// signal classes. A raw-SQL migration (a .sql file, or a directory the raw-SQL
// runner recognizes) is executed statement-by-statement over a connection for
// full per-statement capture — the primary path and the corpus format. A
// detected framework runner is reported as not-yet-captured: its per-statement
// introspection lands with the framework finding rules.
func applyAndCapture(ctx context.Context, t target.Target, opts *validateOptions) (*validate.Capture, error) {
	if isSQLFile(opts.migrations) {
		stmts, err := readSQLFile(opts.migrations)
		if err != nil {
			return nil, err
		}
		return applyStatements(ctx, t, stmts)
	}

	r, err := detectValidateRunner(opts)
	if err != nil {
		return nil, err
	}
	raw, ok := r.(interface{ Files() []string })
	if r.Kind() != runner.RawSQL || !ok {
		return nil, fmt.Errorf("capturing %s migrations is not yet supported; point --migrations at a raw-SQL file or directory", r.Kind())
	}
	var stmts []string
	for _, name := range raw.Files() {
		s, err := readSQLFile(filepath.Join(opts.migrations, name))
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s...)
	}
	return applyStatements(ctx, t, stmts)
}

// applyStatements connects to the target and captures each statement.
func applyStatements(ctx context.Context, t target.Target, stmts []string) (*validate.Capture, error) {
	conn, err := t.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to target: %w", err)
	}
	defer conn.Close(ctx)
	return validate.Apply(ctx, conn, stmts), nil
}

func detectValidateRunner(opts *validateOptions) (runner.Runner, error) {
	if opts.runnerKind != "" {
		return runner.ForKind(opts.migrations, runner.Kind(opts.runnerKind))
	}
	return runner.Detect(opts.migrations)
}

// emitResult writes the verdict: JSON (the machine contract, PRD §10) or the
// human rendering of the SAME struct (INV-VERDICT-SHAPE).
func emitResult(w io.Writer, r verdict.Result, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	r.WriteHuman(w)
	return nil
}

// hostOf extracts the host from a Postgres connection string, or "" if it cannot
// be parsed (in which case there is no host to compare and the caller proceeds).
func hostOf(dsn string) string {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return ""
	}
	return cfg.Host
}

func isSQLFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && filepath.Ext(path) == ".sql"
}

// readSQLFile reads a .sql file and splits it into statements.
func readSQLFile(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return validate.SplitStatements(string(b)), nil
}
