// Package plan computes a dry-run diff of a migration against a live target's
// current schema — the shared core behind `rowshape plan --against` (CLI, P2-T15)
// and the plan_against MCP tool (P3-T6), so both produce the same diff with no
// reimplementation. Reading the target is strictly read-only
// (profile.ReadStructure runs in a read-only transaction, INV-BLAST-RADIUS-ZERO);
// nothing is ever applied.
package plan

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/validate"
)

// Item is one statement's planned effect on the live schema.
type Item struct {
	Statement string `json:"statement"`
	Change    string `json:"change"` // human description of the operation
	Status    string `json:"status"` // ok | conflict | missing-target
	Note      string `json:"note,omitempty"`
}

// ReadLiveSchema reads a live target's structure read-only and upgrades the facts
// to `exact`: they come from a real target, not a sample (PRD §15). The read runs
// inside a read-only transaction, so plan/verify can never write
// (INV-BLAST-RADIUS-ZERO).
func ReadLiveSchema(ctx context.Context, url string) (*fixture.Fixture, error) {
	if url == "" {
		return nil, fmt.Errorf("no target given")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to target failed")
	}
	defer func() { _ = conn.Close(ctx) }()
	f, err := profile.ReadStructure(ctx, conn, profile.Options{})
	if err != nil {
		return nil, fmt.Errorf("reading target schema failed: %w", err)
	}
	validate.MarkExact(f)
	return f, nil
}

// Items classifies each migration statement against the current schema, skipping
// transaction control. It applies nothing.
func Items(current *fixture.Fixture, stmts []string) []Item {
	var items []Item
	for _, raw := range stmts {
		s := collapse(raw)
		if s == "" || isTxControl(s) {
			continue
		}
		items = append(items, classify(current, s))
	}
	return items
}

func classify(current *fixture.Fixture, s string) Item {
	up := strings.ToUpper(s)
	table := planTable(s, up)
	item := Item{Statement: truncate(s, 90), Status: "ok"}

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

// RedactURL strips credentials from a connection URL for display (PRD §5: the
// connection URL and any credentials are never logged, persisted, or written into
// a fixture).
//
// It cuts at the LAST "@" inside the authority, not the first. A password may
// legally contain an unencoded "@" — and people write them — so splitting on the
// first one leaves the tail of the password in the output:
//
//	postgres://admin:p@ss@host/db  ->  postgres://…@ss@host/db   (leaks "ss")
//
// The search is bounded to the authority so an "@" in a path or query string is
// not mistaken for a credential separator, which would corrupt the URL it is
// supposed to be merely displaying.
func RedactURL(url string) string {
	s := strings.Index(url, "://")
	if s < 0 {
		return url
	}
	start := s + 3

	// The authority runs to the first "/", "?", or "#" after the scheme.
	end := len(url)
	for _, sep := range []string{"/", "?", "#"} {
		if i := strings.Index(url[start:], sep); i >= 0 && start+i < end {
			end = start + i
		}
	}

	// A host cannot contain "@", so the last one in the authority ends the
	// userinfo — everything before it is credentials.
	at := strings.LastIndex(url[start:end], "@")
	if at < 0 {
		return url
	}
	return url[:start] + "…@" + url[start+at+1:]
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func isTxControl(s string) bool {
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
			// The column list may abut the table with no space
			// (CREATE INDEX i ON t(col)), so cut at the first "(" as well
			// as trimming a trailing one; otherwise the table name carries
			// "(col)" and never matches the live schema — a real index on an
			// existing table would misreport as missing-target.
			tok := rest[0]
			if k := strings.IndexByte(tok, '('); k >= 0 {
				tok = tok[:k]
			}
			return strings.Trim(tok, `"`)
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
