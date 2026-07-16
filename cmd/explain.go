package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rowshape/rowshape/internal/findings"
	"github.com/spf13/cobra"
)

// newExplainCmd implements `rowshape explain <CODE>`: it returns the docs and
// remediation for a finding code, agent-readable and offline (no web search).
// The content comes from the same catalog the analyzers cite for their
// remediation, so a finding and its explanation can never drift (PRD §8.1, §10).
func newExplainCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "explain [CODE]",
		Short: "Explain a finding code (docs + remediation), agent-readable",
		Long: "explain returns structured documentation and the mandatory remediation\n" +
			"for a finding code (e.g. rowshape explain RS-LOCK-001). With no argument it\n" +
			"lists every code. The text is identical to the remediation the finding\n" +
			"carries — one source, no drift.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listCodes(os.Stdout, asJSON)
			}
			return explainCode(os.Stdout, args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the explanation as JSON")
	return cmd
}

func explainCode(w io.Writer, code string, asJSON bool) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	e, ok := findings.Explain(code)
	if !ok {
		fmt.Fprintf(os.Stderr, "rowshape explain: unknown finding code %q. Known codes:\n", code)
		for _, c := range findings.Codes() {
			fmt.Fprintf(os.Stderr, "  %s\n", c)
		}
		return toolError()
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(e)
	}
	fmt.Fprintf(w, "%s  %s\n\n", e.Code, e.Title)
	fmt.Fprintf(w, "%s\n\n", e.Summary)
	fmt.Fprintf(w, "Remediation:\n  %s\n", e.Remediation)
	if len(e.References) > 0 {
		fmt.Fprintf(w, "\nReferences: %s\n", strings.Join(e.References, ", "))
	}
	return nil
}

func listCodes(w io.Writer, asJSON bool) error {
	codes := findings.Codes()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(codes)
	}
	fmt.Fprintln(w, "Finding codes (rowshape explain <CODE> for details):")
	for _, c := range codes {
		e, _ := findings.Explain(c)
		fmt.Fprintf(w, "  %-18s %s\n", e.Code, e.Title)
	}
	return nil
}
