package target

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Container is a disposable Postgres running in a throwaway Docker container. It
// gives full OS-level isolation when that is wanted; the Ephemeral target is the
// dependency-light default (DECISIONS D-005). It drives the `docker` CLI rather
// than linking a container SDK, keeping the binary's dependency set small
// (INV-SUPPLY-CHAIN); testcontainers-go / pg_tmp remain clean swap-ins behind the
// Target interface.
type Container struct {
	id   string
	port string
	pass string
}

// ContainerAvailable reports whether a usable Docker daemon is present, so
// callers (and tests) can fall back or skip when it is not.
func ContainerAvailable() bool {
	path, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "info").Run() == nil
}

// NewContainer starts a throwaway postgres container and waits for it to accept
// connections. image is e.g. "postgres:16".
func NewContainer(ctx context.Context, image string) (*Container, error) {
	if image == "" {
		image = "postgres:16"
	}
	const pass = "rowshape"

	// The name is generated BEFORE the container is asked for, which is the whole
	// point (CR-T18). `docker run -d` prints the id on stdout, but ctx
	// cancellation kills the docker CLI, not what it has already launched — so in
	// the window between "the daemon started the container" and "we read its id",
	// a cancel left a running container nothing referenced. Naming it up front
	// means cleanup has a handle even when the id was never received, and the
	// label lets an operator find strays: docker ps -a --filter label=rowshape.
	name := containerName()
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--rm",
		"--name", name,
		"--label", containerLabel,
		"-e", "POSTGRES_PASSWORD="+pass,
		"-P", image).Output()
	if err != nil {
		// Best-effort: the container may be running even though this failed.
		// Removing by name is a no-op when it never started.
		_ = terminate(name)
		return nil, fmt.Errorf("docker run: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		// Started but no id echoed: fall back to the name so Close still works.
		id = name
	}

	port, err := exec.CommandContext(ctx, "docker", "port", id, "5432/tcp").Output()
	if err != nil {
		_ = terminate(id)
		return nil, fmt.Errorf("docker port: %w", err)
	}
	hostPort := hostPortOf(strings.TrimSpace(string(port)))
	c := &Container{id: id, port: hostPort, pass: pass}

	if err := c.waitReady(ctx); err != nil {
		_ = c.Close(ctx)
		return nil, err
	}
	return c, nil
}

// Connect opens a connection to the containerized database.
func (c *Container) Connect(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, c.dsn())
}

// Disposable is always true — the container is removed on Close.
func (c *Container) Disposable() bool { return true }

// Close removes the container, throwing the database away.
func (c *Container) Close(context.Context) error {
	if c.id == "" {
		return nil
	}
	err := terminate(c.id)
	c.id = ""
	return err
}

func (c *Container) dsn() string {
	return fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/postgres?sslmode=disable", c.pass, c.port)
}

// waitReady polls until the database accepts a connection or the context ends.
func (c *Container) waitReady(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("container not ready: %w", err)
		}
		conn, err := pgx.Connect(ctx, c.dsn())
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("container not ready: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// containerLabel marks every container rowshape starts, so a stray one is
// findable and removable without knowing its id:
//
//	docker ps -a --filter label=rowshape
//	docker rm -f $(docker ps -aq --filter label=rowshape)
const containerLabel = "rowshape=1"

// containerName generates a unique name for a disposable container. Random
// rather than derived from the fixture: two concurrent runs (a developer and a
// CI job on the same machine) must not collide on a name, and unlike hydrate
// this is infrastructure, so it carries no determinism obligation.
func containerName() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Cannot fail in practice; a time-based fallback still avoids collision
		// with anything already running.
		return fmt.Sprintf("rowshape-%d", time.Now().UnixNano())
	}
	return "rowshape-" + hex.EncodeToString(b[:])
}

// terminate removes a container by id OR name — both are valid handles for
// `docker rm`, which is what lets the cancel path clean up without an id.
func terminate(idOrName string) error {
	return exec.Command("docker", "rm", "-f", idOrName).Run()
}

// hostPortOf extracts the host port from `docker port` output like
// "0.0.0.0:49153" (possibly multiple lines).
func hostPortOf(s string) string {
	line := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		line = s[:i]
	}
	if i := strings.LastIndex(line, ":"); i >= 0 {
		return strings.TrimSpace(line[i+1:])
	}
	return strings.TrimSpace(line)
}
