package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/spf13/cobra"
)

// planOptions holds the flags for `rowshape plan`.
type planOptions struct {
	against    string
	migrations string
}

// newPlanCmd implements `rowshape plan --against <url>`: a dry-run diff of a
// migration against a LIVE target's current schema. It reads the target's
// structure read-only (via profile.ReadStructure, INV-BLAST-RADIUS-ZERO) and
// reports what each statement would change — it never applies anything (PRD §8.1).
func newPlanCmd() *cobra.Command {
	opts := &planOptions{migrations: "migrations"}
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Dry-run diff of a migration against a live target (read-only, applies nothing)",
		Long: "plan reads a live target's current schema (read-only) and reports what\n" +
			"each migration statement would change against it — a dry run that applies\n" +
			"nothing. Point --against at the target and -m at a .sql file or directory.",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runPlan(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.against, "against", "", "live database URL to plan against (read-only)")
	f.StringVarP(&opts.migrations, "migrations", "m", opts.migrations, "migration .sql file or directory")
	_ = cmd.MarkFlagRequired("against")
	return cmd
}

func runPlan(opts *planOptions) error {
	ctx := context.Background()

	stmts, err := migrationStatements(opts.migrations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape plan: %v\n", err)
		return toolError()
	}

	current, err := readLiveSchema(ctx, opts.against)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rowshape plan: %v\n", err)
		return toolError()
	}

	items := planItems(current, stmts)
	writePlan(os.Stdout, opts.against, items)
	return nil
}

// planItem is one statement's planned effect on the live schema.
type planItem struct {
	Statement string
	Change    string // human description of the operation
	Status    string // ok | conflict | missing-target
	Note      string
}

// planItems classifies each migration statement against the current schema.
func planItems(current *fixture.Fixture, stmts []string) []planItem {
	var items []planItem
	for _, raw := range stmts {
		s := collapse(raw)
		if s == "" || isTxControlStmt(s) {
			continue
		}
		items = append(items, classifyPlan(current, s))
	}
	return items
}

func classifyPlan(current *fixture.Fixture, s string) planItem {
	up := strings.ToUpper(s)
	table := planTable(s, up)
	item := planItem{Statement: truncate(s, 90), Status: "ok"}

	tableExists := false
	var tbl fixture.Table
	if table != "" {
		tbl, tableExists = current.Tables[table]
	}

	switch {
	case strings.Contains(up, "ADD COLUMN") || (strings.HasPrefix(up, "ALTER TABLE") && addsBareColumn(up)):
		col := addColumnName(s, up)
		item.Change = fmt.Sprintf("add column %s.%s", short(table), col)
		if !tableExists {
			item.Status, item.Note = "missing-target", "target table not present on the live schema"
		} else if _, ok := tbl.Columns[col]; ok {
			item.Status, item.Note = "conflict", "column already exists"
		} else {
			item.Note = "column will be added"
		}
	case strings.Contains(up, "SET NOT NULL"):
		item.Change = fmt.Sprintf("set NOT NULL on %s", short(table))
		item.Note = existsNote(tableExists)
		if !tableExists {
			item.Status = "missing-target"
		}
	case strings.Contains(up, "ADD CONSTRAINT"):
		item.Change = fmt.Sprintf("add constraint on %s", short(table))
		item.Note = existsNote(tableExists)
		if !tableExists {
			item.Status = "missing-target"
		}
	case strings.HasPrefix(up, "CREATE INDEX") || strings.HasPrefix(up, "CREATE UNIQUE INDEX"):
		item.Change = fmt.Sprintf("create index on %s", short(table))
		item.Note = existsNote(tableExists)
		if !tableExists {
			item.Status = "missing-target"
		}
	case strings.HasPrefix(up, "DROP TABLE"):
		item.Change = fmt.Sprintf("drop table %s", short(table))
		if !tableExists {
			item.Status, item.Note = "conflict", "table is already absent"
		} else {
			item.Note = "table will be dropped"
		}
	default:
		item.Change = "schema change"
		item.Note = existsNote(tableExists)
	}
	return item
}

func existsNote(exists bool) string {
	if exists {
		return "target present"
	}
	return "target table not present on the live schema"
}

// writePlan renders the plan diff. It is purely descriptive — nothing is applied.
func writePlan(w io.Writer, against string, items []planItem) {
	fmt.Fprintf(w, "plan against %s (dry run — nothing is applied)\n\n", redactURL(against))
	if len(items) == 0 {
		fmt.Fprintln(w, "  no schema-changing statements")
		return
	}
	for _, it := range items {
		mark := "+"
		if it.Status == "conflict" {
			mark = "!"
		} else if it.Status == "missing-target" {
			mark = "?"
		}
		fmt.Fprintf(w, "  %s %s\n", mark, it.Change)
		if it.Note != "" {
			fmt.Fprintf(w, "      %s\n", it.Note)
		}
	}
}

// --- shared helpers for plan and verify ---

// readLiveSchema reads a live target's structure read-only (profile.ReadStructure
// runs inside a read-only transaction, INV-BLAST-RADIUS-ZERO) and upgrades the
// facts to `exact`: they come from a real target, not a sample (PRD §15).
func readLiveSchema(ctx context.Context, url string) (*fixture.Fixture, error) {
	if url == "" {
		return nil, fmt.Errorf("no target given (--against)")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to target failed")
	}
	defer conn.Close(ctx)
	f, err := profile.ReadStructure(ctx, conn, profile.Options{})
	if err != nil {
		return nil, fmt.Errorf("reading target schema failed: %w", err)
	}
	validate.MarkExact(f)
	return f, nil
}

// migrationStatements reads a .sql file (or a directory's .sql files) and splits
// it into statements.
func migrationStatements(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !info.IsDir() {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return validate.SplitStatements(string(b)), nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filePathExt(e.Name()), ".sql") {
			b, err := os.ReadFile(path + string(os.PathSeparator) + e.Name())
			if err != nil {
				return nil, err
			}
			out = append(out, validate.SplitStatements(string(b))...)
		}
	}
	return out, nil
}

func filePathExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func isTxControlStmt(s string) bool {
	up := strings.ToUpper(s)
	for _, kw := range []string{"BEGIN", "COMMIT", "ROLLBACK", "START TRANSACTION", "END", "SAVEPOINT", "RELEASE"} {
		if up == kw || strings.HasPrefix(up, kw+" ") || strings.HasPrefix(up, kw+";") {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func short(table string) string {
	if i := strings.LastIndexByte(table, '.'); i >= 0 {
		return table[i+1:]
	}
	return table
}

// planTable extracts the target table of a schema statement.
func planTable(s, up string) string {
	fields := strings.Fields(s)
	switch {
	case strings.HasPrefix(up, "ALTER TABLE"):
		i := 2
		if i < len(fields) && strings.EqualFold(fields[i], "ONLY") {
			i++
		}
		if i < len(fields) {
			return strings.Trim(fields[i], `"`)
		}
	case strings.HasPrefix(up, "DROP TABLE"):
		i := 2
		if i < len(fields) && strings.EqualFold(fields[i], "IF") {
			i += 2 // IF EXISTS
		}
		if i < len(fields) {
			return strings.Trim(strings.TrimRight(fields[i], ";"), `"`)
		}
	case strings.Contains(up, " ON "):
		j := strings.Index(up, " ON ")
		rest := strings.Fields(s[j+4:])
		if len(rest) > 0 {
			return strings.Trim(strings.TrimRight(rest[0], "("), `"`)
		}
	}
	return ""
}

func addsBareColumn(up string) bool {
	return strings.Contains(up, " ADD ") && !strings.Contains(up, "ADD CONSTRAINT") &&
		!strings.Contains(up, "ADD PRIMARY") && !strings.Contains(up, "ADD UNIQUE") &&
		!strings.Contains(up, "ADD FOREIGN") && !strings.Contains(up, "ADD CHECK")
}

func addColumnName(s, up string) string {
	key := "ADD COLUMN "
	i := strings.Index(up, key)
	if i < 0 {
		if k := strings.Index(up, " ADD "); k >= 0 {
			i, key = k, " ADD "
		} else {
			return ""
		}
	}
	fields := strings.Fields(s[i+len(key):])
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], `"`)
}

// redactURL strips credentials from a connection URL for display.
func redactURL(url string) string {
	if i := strings.Index(url, "@"); i >= 0 {
		if s := strings.Index(url, "://"); s >= 0 && s+3 < i {
			return url[:s+3] + "…@" + url[i+1:]
		}
	}
	return url
}
