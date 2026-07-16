// Package target provides the disposable (or user-provided) database that
// hydrate loads into. A single Target interface abstracts the mechanism so the
// disposable backend — an ephemeral database, a Docker container, pg_tmp, or an
// embedded Postgres — can be swapped without touching the synthesis engine
// (OQ-TARGET / docs DECISIONS D-005).
package target

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// Target is a database hydrate materializes rows into.
type Target interface {
	// Connect opens a connection to the target database.
	Connect(ctx context.Context) (*pgx.Conn, error)
	// Disposable reports whether Close destroys the database. A non-disposable
	// target (a user's own database) is a safety signal: hydrate must never treat
	// it as throwaway.
	Disposable() bool
	// Close tears the target down: it drops the throwaway database (or terminates
	// the container) for a disposable target, and only closes resources for a
	// provided one. Close is idempotent.
	Close(ctx context.Context) error
}

// Provided wraps a user-supplied database URL (`hydrate --target <url>`). It is
// not disposable — Close never drops the database.
type Provided struct {
	dsn string
}

// NewProvided returns a Target for a user-supplied connection string.
func NewProvided(dsn string) *Provided { return &Provided{dsn: dsn} }

// Connect opens a connection to the provided database.
func (p *Provided) Connect(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, p.dsn)
}

// Disposable is always false for a user-provided database.
func (p *Provided) Disposable() bool { return false }

// Close is a no-op; a user's database is never dropped.
func (p *Provided) Close(context.Context) error { return nil }

// ephemeralCounter makes disposable database names unique within a process
// without needing a clock or randomness (both of which would hurt determinism).
var ephemeralCounter atomic.Uint64

// Ephemeral is a disposable database created on a reachable Postgres server and
// dropped on Close. It is the default hydrate target: dependency-light (a libpq
// connection, no Docker), and genuinely throwaway.
type Ephemeral struct {
	adminCfg *pgx.ConnConfig
	name     string
}

// NewEphemeral creates a fresh disposable database on the server named by
// adminDSN and returns a Target bound to it. adminDSN must have permission to
// CREATE DATABASE (a role rowshape does not otherwise need — this is the one
// privileged step, isolated here).
func NewEphemeral(ctx context.Context, adminDSN string) (*Ephemeral, error) {
	adminCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return nil, fmt.Errorf("parse admin connection: %w", err)
	}
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to admin database: %w", err)
	}
	defer admin.Close(ctx)

	name := ephemeralName(ephemeralCounter.Add(1))
	// CREATE DATABASE cannot run inside a transaction; a plain Exec is correct.
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoteIdent(name)); err != nil {
		return nil, fmt.Errorf("create disposable database: %w", err)
	}
	return &Ephemeral{adminCfg: adminCfg, name: name}, nil
}

// Name returns the disposable database name.
func (e *Ephemeral) Name() string { return e.name }

// Connect opens a connection to the disposable database.
func (e *Ephemeral) Connect(ctx context.Context) (*pgx.Conn, error) {
	cfg := e.adminCfg.Copy()
	cfg.Database = e.name
	return pgx.ConnectConfig(ctx, cfg)
}

// Disposable is always true.
func (e *Ephemeral) Disposable() bool { return true }

// Close drops the disposable database, forcing off any lingering connections.
func (e *Ephemeral) Close(ctx context.Context) error {
	if e.name == "" {
		return nil
	}
	admin, err := pgx.ConnectConfig(ctx, e.adminCfg)
	if err != nil {
		return fmt.Errorf("connect to drop disposable database: %w", err)
	}
	defer admin.Close(ctx)
	// WITH (FORCE) terminates other sessions (PG13+) so the drop always succeeds.
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(e.name)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop disposable database: %w", err)
	}
	e.name = ""
	return nil
}

// ephemeralName builds a unique, valid database name for a disposable target.
func ephemeralName(n uint64) string {
	return fmt.Sprintf("rowshape_hydrate_%d", n)
}

// quoteIdent double-quotes a SQL identifier, escaping embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
