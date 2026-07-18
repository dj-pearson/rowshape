package runner

import (
	"os"
	"strings"
)

// databaseURLVar is the variable every supported framework runner reads its
// target from (Alembic, Prisma and Drizzle all use this convention).
const databaseURLVar = "DATABASE_URL"

// envWithDSN builds the child environment for a framework runner: the parent's
// environment with DATABASE_URL REPLACED by rowshape's disposable target, never
// appended alongside an inherited one.
//
// The three runners previously each wrote `append(os.Environ(), "DATABASE_URL="+dsn)`.
// That is not the wrong-DSN bug it looks like — os/exec documents that "if Env
// contains duplicate environment keys, only the last value in the slice for each
// duplicate key is used", and that was confirmed by running it: with a stale
// DATABASE_URL exported by the shell, the child still received rowshape's. So the
// appended value wins, by a documented guarantee rather than by luck.
//
// It is replaced anyway, for reasons that do not depend on that guarantee:
//
//   - The blast-radius promise (INV-BLAST-RADIUS-ZERO) should be legible at the
//     call site. A reader checking "can a framework migration reach the
//     developer's real database?" should not have to know a dedup rule in
//     os/exec to answer it, and the answer should not change if this code is
//     ever ported to a different exec path (a container runtime, an SSH
//     invocation) that has no such rule.
//   - It was written three times, so it could drift in one place.
//   - Framework-migration capture is not yet wired into validate, so the moment
//     to make this explicit is before it carries traffic, not after.
func envWithDSN(dsn string) []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, databaseURLVar) {
			// Drop the inherited one; rowshape's is appended below. EqualFold
			// because Windows environment variables are case-insensitive, so an
			// inherited `Database_Url` is the same variable.
			continue
		}
		out = append(out, kv)
	}
	return append(out, databaseURLVar+"="+dsn)
}
