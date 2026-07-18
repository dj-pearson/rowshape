package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
	"github.com/rowshape/rowshape/internal/runner"
	"github.com/rowshape/rowshape/internal/target"
	"github.com/rowshape/rowshape/internal/toolerror"
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
		return emitToolError(opts.asJSON, toolerror.New(toolerror.FixtureParse, fmt.Sprintf("reading %s failed: %v", opts.fixturePath, err), "check the fixture path"))
	}
	f, err := fixture.ParseVerified(data)
	if err != nil {
		return emitToolError(opts.asJSON, fixtureParseError(err))
	}

	// Resolve the target and enforce the host-match refusal BEFORE touching it.
	groundTruth := opts.target != ""
	adminOrTarget := opts.target
	if adminOrTarget == "" {
		adminOrTarget = opts.ephemeral
	}
	if adminOrTarget == "" {
		return emitToolError(opts.asJSON, toolerror.New(toolerror.BadUsage, "no target given", "provide a disposable target (--ephemeral <admin-url>) or a live target (--target <url>)"))
	}
	if host := hostOf(adminOrTarget); host != "" {
		if err := validate.CheckHost(f.Meta.Source, host); err != nil {
			return emitToolError(opts.asJSON, toolerror.New(toolerror.BadUsage, err.Error(), "point --target/--ephemeral at a disposable or non-production host"))
		}
	}

	// Prepare the target and capture. A provided live branch is used as-is
	// (ground truth); a disposable database is hydrated from the fixture.
	var cap *validate.Capture
	if groundTruth {
		if opts.calibrate {
			return emitToolError(opts.asJSON, toolerror.New(toolerror.BadUsage, "--calibrate applies only to a disposable target", "a provided branch's data is already ground truth"))
		}
		cap, err = applyAndCapture(ctx, target.NewProvided(opts.target), opts)
		if err != nil {
			return emitToolError(opts.asJSON, asToolError(err))
		}
	} else {
		cap, err = hydrateApplyEphemeral(ctx, f, opts, opts.scale)
		if err != nil {
			return emitToolError(opts.asJSON, asToolError(err))
		}
		// --calibrate fits the cost curve to a SECOND run at half scale, upgrading
		// duration estimates from `estimated` to `measured` (RFC §9.2). Slower and
		// honest — the option for the one migration you're genuinely nervous about.
		if opts.calibrate {
			cap2, err := hydrateApplyEphemeral(ctx, f, opts, opts.scale/2)
			if err != nil {
				return emitToolError(opts.asJSON, asToolError(err))
			}
			cap.Calibration = &validate.Calibration{
				TableRows:    cap2.TableRows,
				StatementMs2: statementDurations(cap2),
			}
		}
	}

	result := validate.BuildResult(f, cap, validate.Registered(), groundTruth)
	if err := result.Validate(); err != nil {
		return emitToolError(opts.asJSON, toolerror.New(toolerror.Internal, "produced verdict is malformed: "+err.Error(), ""))
	}
	if fs := cap.FailedStatement(); fs != nil {
		fmt.Fprintf(os.Stderr, "rowshape validate: migration did not apply cleanly: %s (%s)\n", fs.ErrMsg, fs.ErrCode)
	}

	if err := emitResult(os.Stdout, result, opts.asJSON); err != nil {
		return emitToolError(opts.asJSON, toolerror.New(toolerror.Internal, "emitting the verdict failed: "+err.Error(), ""))
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
		return nil, toolerror.New(toolerror.TargetUnavailable, "could not create a disposable database", "check the admin connection (--ephemeral); a disposable Postgres must be reachable (PRD §17.2)")
	}
	defer func() { warnTeardown("validate", eph.Close(ctx)) }()

	report, err := target.Load(ctx, eph, f, hydrate.Options{Seed: opts.seed, Scale: scale, MaxRows: opts.maxRows})
	if err != nil {
		return nil, toolerror.New(toolerror.TargetUnavailable,
			redactedTargetError("hydration into the disposable database failed", err),
			"check the admin connection (--ephemeral) and that the fixture hydrates cleanly; set ROWSHAPE_DEBUG=1 for the underlying error")
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
			return nil, toolerror.New(toolerror.BadUsage, err.Error(), "check the --migrations path")
		}
		return applyStatements(ctx, t, stmts)
	}

	r, err := detectValidateRunner(opts)
	if err != nil {
		return nil, toolerror.New(toolerror.RunnerNotFound, err.Error(), "select a runner with --runner, or point --migrations at a raw-SQL file/directory")
	}
	raw, ok := r.(interface{ Files() []string })
	if r.Kind() != runner.RawSQL || !ok {
		return nil, toolerror.New(toolerror.RunnerNotFound, fmt.Sprintf("capturing %s migrations is not yet supported", r.Kind()), "point --migrations at a raw-SQL file or directory")
	}
	var stmts []validate.Located
	for _, name := range raw.Files() {
		s, err := readSQLFile(filepath.Join(opts.migrations, name))
		if err != nil {
			return nil, toolerror.New(toolerror.BadUsage, err.Error(), "check the --migrations path")
		}
		stmts = append(stmts, s...)
	}
	return applyStatements(ctx, t, stmts)
}

// applyStatements connects to the target and captures each statement.
func applyStatements(ctx context.Context, t target.Target, stmts []validate.Located) (*validate.Capture, error) {
	conn, err := t.Connect(ctx)
	if err != nil {
		return nil, toolerror.New(toolerror.ConnectFailed, "could not connect to the target", "check the target is reachable and the credentials are valid")
	}
	defer func() { _ = conn.Close(ctx) }()
	return validate.Apply(ctx, conn, stmts), nil
}

func detectValidateRunner(opts *validateOptions) (runner.Runner, error) {
	if opts.runnerKind != "" {
		return runner.ForKind(opts.migrations, runner.Kind(opts.runnerKind))
	}
	return runner.Detect(opts.migrations)
}

// emitToolError renders an operational failure as exit code 3 — clearly distinct
// from a verdict (PRD §10, INV-VERDICT-STABLE). --json writes the machine-readable
// payload to stdout (so an agent branches on `"error":"tool_error"` where a
// verdict has `"verdict"`); otherwise the same struct is rendered for a human on
// stderr. It never emits a PASS/FAIL/WARN.
func emitToolError(asJSON bool, te *toolerror.ToolError) error {
	if asJSON {
		_ = te.WriteJSON(os.Stdout)
	} else {
		te.WriteHuman(os.Stderr)
	}
	return &ExitError{Code: te.ExitCode()}
}

// warnTeardown reports a disposable-database teardown failure without changing
// the outcome of the command.
//
// CR-T19: these two sites discarded the error entirely (`_ = eph.Close(ctx)`),
// so a failed cleanup produced no diagnostic anywhere and orphan databases could
// accumulate across CI runs with nothing pointing at the cause. This is
// deliberately DIFFERENT from the ~14 reviewed `_ =` sites recorded under P0-T6:
// those are safe-by-inspection deferred closes on read paths, where there is
// genuinely nothing to report. Here the discarded value is the outcome of
// releasing a resource.
//
// It warns and does not fail. A teardown failure is not a verdict
// (INV-VERDICT-STABLE), and turning a clean PASS into an error because cleanup
// stumbled would make the tool wrong about the migration, which is the one thing
// it must not be. The message carries no connection details (PRD §5, and
// consistent with CR-T7).
func warnTeardown(cmdName string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "rowshape %s: warning: could not drop the disposable database; "+
		"it may need removing by hand (set ROWSHAPE_DEBUG=1 for the underlying error)\n", cmdName)
	if os.Getenv("ROWSHAPE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "rowshape %s: teardown error: %v\n", cmdName, err)
	}
}

// redactedTargetError describes a failure that touched the target WITHOUT
// echoing the error, because pgx embeds the connection in its message: a real
// one reads "failed to connect to `user=admin database=appdb`: hostname
// resolving error: lookup prod-db.internal.example.com". Host, port, username
// and database name all reach the user's terminal, CI log, and --json output.
// PRD §5 says connection details are never logged or persisted, and every other
// connect-failure path in this tree already substitutes a fixed string — these
// were the outliers.
//
// The detail is gated, not discarded: ROWSHAPE_DEBUG=1 prints it. That is an env
// var rather than a flag on purpose. The CLI surface is part of the public
// contract (INV-VERDICT-STABLE) and a debugging aid does not belong in it, while
// an env var is available exactly when someone is debugging and never appears in
// a verdict.
func redactedTargetError(what string, err error) string {
	if os.Getenv("ROWSHAPE_DEBUG") != "" {
		return what + ": " + err.Error()
	}
	return what
}

// asToolError coerces an error to a *toolerror.ToolError: those returned by the
// capture helpers pass through with their category; anything else is an Internal
// tool error (still exit 3, never a verdict).
func asToolError(err error) *toolerror.ToolError {
	var te *toolerror.ToolError
	if errors.As(err, &te) {
		return te
	}
	return toolerror.New(toolerror.Internal, err.Error(), "")
}

// fixtureParseError maps a fixture parse failure to a category: an unknown format
// major is a distinct refusal (RFC §12), never a partial-understanding verdict.
func fixtureParseError(err error) *toolerror.ToolError {
	var ve *fixture.VersionError
	if errors.As(err, &ve) {
		return toolerror.New(toolerror.UnknownVersion, ve.Error(), "this build understands fixture format version \""+fixture.FormatVersion+"\"")
	}
	return toolerror.New(toolerror.FixtureParse, err.Error(), "the fixture is not valid rowshape.yaml")
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
func readSQLFile(path string) ([]validate.Located, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return validate.SplitStatementsIn(path, string(b)), nil
}
