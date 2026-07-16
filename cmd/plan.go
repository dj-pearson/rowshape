package cmd

import "github.com/spf13/cobra"

// newPlanCmd previews what validate would do without executing it. Full
// behavior lands in a later phase. Phase-0 stub.
func newPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Preview the validation plan for a migration",
		RunE:  notImplemented("plan"),
	}
}
