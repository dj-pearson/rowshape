package cmd

import (
	"github.com/rowshape/rowshape/internal/runner"
)

// detectedStack is what init infers from a repo's layout — offline, never
// touching a database (PRD §8.1).
type detectedStack struct {
	// Engine is the database engine. v1 targets Postgres; init records it so the
	// config is explicit and `init --agent` (P3-T8+) can extend it.
	Engine string
	// Runner is the detected migration runner: "alembic", "prisma", "drizzle",
	// "rawsql", or "" when none is recognized (the user fills it in).
	Runner string
}

// detectStack infers the engine and migration runner from marker files in dir,
// using the SAME detector `validate` uses (internal/runner) so init and validate
// can never disagree about the runner. It NEVER connects to a database — the
// detection is purely from the repo layout (alembic.ini, prisma/schema.prisma, a
// drizzle config, or a SQL migrations directory).
func detectStack(dir string) detectedStack {
	s := detectedStack{Engine: "postgres"}
	if r, err := runner.Detect(dir); err == nil {
		s.Runner = string(r.Kind())
	}
	return s
}

// runnerLabel renders a detected runner for a message/config, naming the
// not-detected case so the scaffold is honest about what the user must supply.
func runnerLabel(kind string) string {
	if kind == "" {
		return "unknown (not detected — set it below)"
	}
	return kind
}
