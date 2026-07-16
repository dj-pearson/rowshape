package cmd

import "github.com/spf13/cobra"

// newPullCmd reads production shape read-only and emits a committable
// rowshape.yaml. Full behavior lands in phase 1 (P1-T3..T6). Phase-0 stub.
func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Read a database's shape (read-only) and emit rowshape.yaml",
		RunE:  notImplemented("pull"),
	}
}
