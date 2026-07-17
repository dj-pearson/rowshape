package target

import (
	"context"
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
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--rm",
		"-e", "POSTGRES_PASSWORD="+pass,
		"-P", image).Output()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	id := strings.TrimSpace(string(out))

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

func terminate(id string) error {
	return exec.Command("docker", "rm", "-f", id).Run()
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
