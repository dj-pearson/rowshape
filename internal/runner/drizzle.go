package runner

import (
	"context"
	"os"
	"os/exec"
)

// drizzleRunner drives Drizzle Kit. Signature: a `drizzle.config.{ts,js,mjs}` at
// the project root (PRD §13).
type drizzleRunner struct {
	root string
}

func (d *drizzleRunner) Kind() Kind { return Drizzle }

// ApplyCmd runs `drizzle-kit migrate`, which applies pending migrations. The
// target DSN is provided via DATABASE_URL, which the drizzle config resolves.
func (d *drizzleRunner) ApplyCmd(ctx context.Context, dsn string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "drizzle-kit", "migrate")
	cmd.Dir = d.root
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn)
	return cmd
}

// detectDrizzle recognizes a Drizzle project by its config file.
func detectDrizzle(dir string) (Runner, bool) {
	if firstExisting(dir, "drizzle.config.ts", "drizzle.config.js", "drizzle.config.mjs") != "" {
		return &drizzleRunner{root: dir}, true
	}
	return nil, false
}
