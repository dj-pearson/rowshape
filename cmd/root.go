// Package cmd wires the rowshape CLI subcommand tree.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	// Registers the RS-* finding analyzers with the validate pipeline (P2-T8+).
	_ "github.com/rowshape/rowshape/internal/findings"
	"github.com/spf13/cobra"
)

// ExitError carries a process exit code up to main so each command maps its
// outcome onto the stable exit-code contract (INV-VERDICT-STABLE, PRD §10).
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit code %d", e.Code) }

// NewRootCmd builds the full command tree. Every subcommand named in PRD §8.1
// is present; in phase 0 each leaf is a stub that returns a tool error.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "rowshape",
		Short: "The type-checker for database migrations",
		Long: "rowshape — execute a proposed schema change against production-shaped\n" +
			"data in a disposable environment and return a machine-readable verdict.\n\n" +
			"A human and an agent get the same answer through the same contract.",
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newInitCmd(),
		newPullCmd(),
		newHydrateCmd(),
		newValidateCmd(),
		newExplainCmd(),
		newPlanCmd(),
		newVerifyCmd(),
		newInspectCmd(),
		newMCPCmd(),
		newAnnotateCmd(),
	)
	return root
}

// Execute runs the root command. main maps the returned error onto an exit code.
//
// It runs under a signal-aware context: the first SIGINT/SIGTERM cancels it
// instead of killing the process, so a command mid-run — hydrating a disposable
// database, say — unwinds normally and its deferred teardown drops that database
// rather than orphaning it on the admin server (the disposable-target lifecycle,
// INV-BLAST-RADIUS-ZERO's cleanup half).
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewRootCmd().ExecuteContext(ctx)
}
