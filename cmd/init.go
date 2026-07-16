package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// configFile is the scaffolded config filename.
const configFile = "rowshape.toml"

// newInitCmd detects the migration stack and scaffolds a rowshape config file
// (PRD §8.1). Agent-aware scaffolding (`init --agent`) lands in phase 3.
func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold rowshape config in the current repo",
		Long: "init detects your migration tooling and writes a starter " + configFile +
			" with sensible defaults you can edit.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(force)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing "+configFile)
	return cmd
}

func runInit(force bool) error {
	if _, err := os.Stat(configFile); err == nil && !force {
		fmt.Fprintf(os.Stderr, "rowshape init: %s already exists (use --force to overwrite)\n", configFile)
		return toolError()
	}

	runner := detectRunner()
	content := scaffoldConfig(runner)
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "rowshape init: writing %s failed: %v\n", configFile, err)
		return toolError()
	}
	fmt.Fprintf(os.Stderr, "rowshape init: wrote %s (detected migration runner: %s)\n", configFile, runner)
	return nil
}

// detectRunner infers the migration tool from marker files in the working
// directory. It is a hint written into the config, not a hard dependency —
// rowshape orchestrates the user's own runner rather than reimplementing it
// (PRD §8.1).
func detectRunner() string {
	markers := []struct {
		path   string
		runner string
	}{
		{"alembic.ini", "alembic"},
		{"alembic", "alembic"},
		{filepath.Join("prisma", "schema.prisma"), "prisma"},
		{"knexfile.js", "knex"},
		{"knexfile.ts", "knex"},
		{filepath.Join("db", "migrate"), "rails"},
		{"dbmate", "dbmate"},
		{filepath.Join("db", "migrations"), "dbmate"},
		{"migrations", "generic"},
	}
	for _, m := range markers {
		if _, err := os.Stat(m.path); err == nil {
			return m.runner
		}
	}
	return "psql"
}

// scaffoldConfig renders the starter config with the detected runner filled in.
func scaffoldConfig(runner string) string {
	return `# rowshape configuration — https://rowshape.com
# Commit this file; it carries no secrets. The database connection comes from
# the standard libpq environment variables (PGHOST, PGUSER, ...) or a URL passed
# to ` + "`rowshape pull`" + `.

# Privacy level for pull: strict | standard | permissive.
# strict emits no numeric/temporal ranges, histograms, values, or verbatim
# CHECK expressions (RFC-0001 §8). Never commit a fixture you have not reviewed.
privacy = "standard"

# Where pull writes the fixture.
out = "rowshape.yaml"

[pull]
# Restrict profiling to specific schemas (default: all non-system schemas).
# schemas = ["public"]

[migrations]
# Your migration runner. rowshape orchestrates it; it does not reimplement it.
runner = "` + runner + `"
`
}
