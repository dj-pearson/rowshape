package cmd

import "github.com/spf13/cobra"

// newInitCmd scaffolds rowshape config in a repo. Full behavior lands in P1-T6
// (config scaffold) and P3 (init --agent). Phase-0 stub.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold rowshape config in the current repo",
		RunE:  notImplemented("init"),
	}
}
