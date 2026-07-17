// Package runner detects a project's migration tool and shells out to it to
// apply the migration set. Rowshape orchestrates; it does NOT reimplement
// migration logic — "rather than being the 40th runner" (PRD §8.1, §13).
//
// A Runner knows only how to build the command that applies a project's
// migrations against a database URL. The caller (`validate`, P2-T7) runs that
// command against the DISPOSABLE target only, then reads what happened. v1
// supports Alembic, Prisma, Drizzle, and a plain directory of raw `.sql` files
// applied with `psql -f` (PRD §13).
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Kind identifies a migration runner. It is the value a user passes to override
// auto-detection (`--runner`).
type Kind string

const (
	Alembic Kind = "alembic"
	Prisma  Kind = "prisma"
	Drizzle Kind = "drizzle"
	RawSQL  Kind = "rawsql"
)

// Runner applies a project's migration set to a target database by shelling out
// to the project's own tool. Rowshape never reimplements the tool.
type Runner interface {
	// Kind reports which tool this runner drives.
	Kind() Kind
	// ApplyCmd builds the command that applies the full migration set against the
	// database at dsn. The command is executed by the caller against the
	// disposable target only; the DSN is delivered to the tool the way that tool
	// expects it (DATABASE_URL for Alembic/Prisma/Drizzle, a psql connection
	// argument for raw SQL).
	ApplyCmd(ctx context.Context, dsn string) *exec.Cmd
}

// detector pairs a kind with the test that recognizes it in a project directory.
// Order matters: a project using a framework (Alembic/Prisma/Drizzle) is
// detected as that framework before the raw-SQL fallback, even though it also
// contains .sql files.
var detectors = []struct {
	kind  Kind
	build func(dir string) (Runner, bool)
}{
	{Alembic, detectAlembic},
	{Prisma, detectPrisma},
	{Drizzle, detectDrizzle},
	{RawSQL, detectRawSQL}, // fallback: a bare directory of .sql migrations
}

// Detect auto-detects the migration runner rooted at dir (PRD §8.1). It returns
// an error naming the supported runners when none is recognized, so the failure
// is actionable rather than a silent guess.
func Detect(dir string) (Runner, error) {
	for _, d := range detectors {
		if r, ok := d.build(dir); ok {
			return r, nil
		}
	}
	return nil, fmt.Errorf("runner: no supported migration runner detected in %s (looked for Alembic, Prisma, Drizzle, or a raw-SQL migrations directory); select one explicitly with --runner", dir)
}

// ForKind builds the runner of an explicitly selected kind, so detection is
// always overridable (`--runner alembic`). It still binds to dir so the runner
// can locate config and migration files, but it does not require the detection
// signature to be present — the user has asserted the choice.
func ForKind(dir string, kind Kind) (Runner, error) {
	switch kind {
	case Alembic:
		return &alembicRunner{root: dir}, nil
	case Prisma:
		return &prismaRunner{root: dir}, nil
	case Drizzle:
		return &drizzleRunner{root: dir}, nil
	case RawSQL:
		return newRawSQL(dir)
	default:
		return nil, fmt.Errorf("runner: unknown runner %q (supported: alembic, prisma, drizzle, rawsql)", kind)
	}
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// firstExisting returns the first path (joined under dir) that exists, or "".
func firstExisting(dir string, rels ...string) string {
	for _, r := range rels {
		p := filepath.Join(dir, r)
		if fileExists(p) || dirExists(p) {
			return p
		}
	}
	return ""
}
