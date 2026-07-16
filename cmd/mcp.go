package cmd

import "github.com/spf13/cobra"

// newMCPCmd serves rowshape as an MCP server so an agent can reach the tools in
// its own turn. Full behavior lands in phase 3. Phase-0 stub.
func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run rowshape as an MCP server",
		RunE:  notImplemented("mcp"),
	}
}
