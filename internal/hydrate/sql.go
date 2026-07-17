package hydrate

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// WriteSQL renders generated data as deterministic INSERT statements. The output
// is a plain, portable SQL script; wiring it into a disposable Postgres target
// (transactions, COPY, dependency ordering) is P1-T9.
func WriteSQL(w io.Writer, res *Result) error {
	for _, t := range res.Tables {
		if len(t.Rows) == 0 || len(t.Columns) == 0 {
			continue
		}
		cols := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cols[i] = quoteIdent(c)
		}
		header := fmt.Sprintf("INSERT INTO %s (%s) VALUES\n", quoteTable(t.Name), strings.Join(cols, ", "))
		if _, err := io.WriteString(w, header); err != nil {
			return err
		}
		for ri, row := range t.Rows {
			vals := make([]string, len(row))
			for i, v := range row {
				vals[i] = sqlLiteral(v)
			}
			sep := ","
			if ri == len(t.Rows)-1 {
				sep = ";"
			}
			if _, err := fmt.Fprintf(w, "  (%s)%s\n", strings.Join(vals, ", "), sep); err != nil {
				return err
			}
		}
	}
	return nil
}

// sqlLiteral renders a Go value as a SQL literal. Strings are single-quote
// escaped; bytea uses the hex escape form; everything is deterministic.
func sqlLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return fmt.Sprintf("%g", x)
	case time.Time:
		return "'" + x.UTC().Format(time.RFC3339) + "'"
	case []byte:
		return "'\\x" + fmt.Sprintf("%x", x) + "'"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	default:
		return "'" + strings.ReplaceAll(fmt.Sprintf("%v", x), "'", "''") + "'"
	}
}

// quoteTable quotes a schema.table identifier.
func quoteTable(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return quoteIdent(name[:i]) + "." + quoteIdent(name[i+1:])
	}
	return quoteIdent(name)
}

// quoteIdent double-quotes a SQL identifier, escaping embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
