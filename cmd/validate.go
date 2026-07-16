package cmd

import "github.com/spf13/cobra"

// newValidateCmd applies a proposed migration against a hydrated disposable
// target and returns a verdict. It is free forever and never calls the cloud
// (INV-NEVER-GATE-VALIDATE). Full behavior lands in phase 2. Phase-0 stub.
func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate a migration against production-shaped data; return a verdict",
		RunE:  notImplemented("validate"),
	}
}
