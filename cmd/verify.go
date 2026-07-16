package cmd

import "github.com/spf13/cobra"

// newVerifyCmd read-only checks that a live database matches what the migration
// history claims (drift). Read-only by contract (INV-BLAST-RADIUS-ZERO). Full
// behavior lands in a later phase. Phase-0 stub.
func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Read-only check that a database matches its declared shape (drift)",
		RunE:  notImplemented("verify"),
	}
}
