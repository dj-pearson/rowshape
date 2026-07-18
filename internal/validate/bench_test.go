package validate

import (
	"strings"
	"testing"
)

// SplitStatements runs on every migration the tool sees — the CLI, the MCP
// validate_migration and plan_against tools, and the corpus harness all funnel
// SQL through it. It is a hand-written rune scanner, so its cost is worth
// pinning: this benchmark makes a regression that turns the single pass into
// something super-linear (or allocation-heavy) visible. docs/TESTING-GAPS.md item 11.
//
// Run with:  go test ./internal/validate/ -run x -bench BenchmarkSplitStatements -benchmem

func BenchmarkSplitStatements(b *testing.B) {
	// A realistic multi-statement migration with comments, dollar-quoting, and an
	// escape string — the constructs the scanner tracks state through.
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("-- add a column and index\n")
		sb.WriteString("ALTER TABLE public.orders ADD COLUMN note text DEFAULT 'n/a';\n")
		sb.WriteString("CREATE INDEX CONCURRENTLY idx ON public.orders (note);\n")
		sb.WriteString("DO $tag$ BEGIN PERFORM 1; END $tag$;\n")
		sb.WriteString("UPDATE public.orders SET note = E'a\\';b' WHERE id = 1;\n")
	}
	sql := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SplitStatements(sql)
	}
}
