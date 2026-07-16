package cmd

import "github.com/spf13/cobra"

// newExplainCmd explains a finding or verdict in human terms. Full behavior
// lands in a later phase. Phase-0 stub.
func newExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain",
		Short: "Explain a finding or verdict",
		RunE:  notImplemented("explain"),
	}
}
