package cmd

import "github.com/spf13/cobra"

// newHydrateCmd deterministically reconstructs a disposable database from a
// fixture. Full behavior lands in phase 1 (P1-T7..T9). Phase-0 stub.
func newHydrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hydrate",
		Short: "Reconstruct a deterministic disposable database from rowshape.yaml",
		RunE:  notImplemented("hydrate"),
	}
}
