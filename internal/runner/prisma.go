package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// prismaRunner drives Prisma Migrate. Signature: a `schema.prisma`, at the root
// or under `prisma/` (PRD §13).
type prismaRunner struct {
	root string
}

func (p *prismaRunner) Kind() Kind { return Prisma }

// ApplyCmd runs `prisma migrate deploy` — the non-interactive apply Prisma
// intends for CI. Prisma reads its target from DATABASE_URL, its own convention.
func (p *prismaRunner) ApplyCmd(ctx context.Context, dsn string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "prisma", "migrate", "deploy")
	cmd.Dir = p.root
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn)
	return cmd
}

// detectPrisma recognizes a Prisma project by its schema.prisma (prisma/ first,
// then the repo root).
func detectPrisma(dir string) (Runner, bool) {
	if firstExisting(dir, filepath.Join("prisma", "schema.prisma"), "schema.prisma") != "" {
		return &prismaRunner{root: dir}, true
	}
	return nil, false
}
