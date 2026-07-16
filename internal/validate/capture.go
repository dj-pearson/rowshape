// Package validate orchestrates `rowshape validate`: it applies a proposed
// migration against a hydrated disposable (or user-provided) database and
// captures what actually happened, feeding those captures to the finding
// analyzers, the extrapolation model, and the confidence-capping engine to
// produce a Verdict.
//
// validate is free forever and never calls the cloud (INV-NEVER-GATE-VALIDATE):
// this package imports no network client. Its blast radius is zero
// (INV-BLAST-RADIUS-ZERO): there is no `apply`, and the CLI hard-refuses a target
// whose host matches the fixture's source host.
package validate

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Capture is the record of applying a migration: the six signal classes a
// verdict is built from (PRD §8.1) — success/failure, per-statement wall time,
// lock mode + duration, rows affected, constraint violations, and index build
// behavior.
type Capture struct {
	// Success is true when every statement applied without error.
	Success bool
	// DurationMs is the wall time of the whole migration.
	DurationMs int64
	// Statements is the per-statement record, in application order.
	Statements []Statement
}

// Statement is the capture of one applied SQL statement.
type Statement struct {
	SQL          string // the statement text (trimmed)
	DurationMs   int64  // wall time — also the lock hold time for a blocking DDL
	RowsAffected int64  // command-tag row count (0 for pure DDL)
	LockMode     string // strongest lock this statement held on a relation, "" if none
	LockTable    string // the relation the strongest lock was held on
	// ErrCode is the SQLSTATE when the statement failed, "" on success. Class 23
	// (e.g. 23505 unique_violation, 23502 not_null_violation) is a constraint
	// violation surfaced by applying the migration against production-shaped data.
	ErrCode string
	ErrMsg  string
	// IsIndexBuild / Concurrent describe index build behavior: whether the
	// statement built an index and whether it used CREATE INDEX CONCURRENTLY
	// (which holds no exclusive lock but cannot run in a transaction block).
	IsIndexBuild bool
	Concurrent   bool
}

// ConstraintViolation reports whether the statement failed on an integrity
// constraint (SQLSTATE class 23) — a real problem the migration hit against the
// data, not a tool error.
func (s Statement) ConstraintViolation() bool {
	return strings.HasPrefix(s.ErrCode, "23")
}

// FailedStatement returns the first statement that errored, or nil.
func (c *Capture) FailedStatement() *Statement {
	for i := range c.Statements {
		if c.Statements[i].ErrCode != "" {
			return &c.Statements[i]
		}
	}
	return nil
}

// Apply runs each statement against conn in application order, capturing the six
// signal classes. A statement that can run inside a transaction is executed in
// its own transaction so its held locks can be inspected before commit; a
// CONCURRENTLY index build (which cannot run in a transaction block) is executed
// directly. Application stops at the first error — a broken migration's later
// statements are not meaningful.
func Apply(ctx context.Context, conn *pgx.Conn, statements []string) *Capture {
	cap := &Capture{Success: true}
	start := time.Now()
	for _, raw := range statements {
		sql := strings.TrimSpace(raw)
		if sql == "" {
			continue
		}
		st := applyOne(ctx, conn, sql)
		cap.Statements = append(cap.Statements, st)
		if st.ErrCode != "" {
			cap.Success = false
			break
		}
	}
	cap.DurationMs = time.Since(start).Milliseconds()
	return cap
}

// applyOne executes and captures a single statement.
func applyOne(ctx context.Context, conn *pgx.Conn, sql string) Statement {
	st := Statement{SQL: sql}
	st.IsIndexBuild, st.Concurrent = classifyIndexBuild(sql)

	if st.Concurrent {
		// CREATE INDEX CONCURRENTLY cannot run inside a transaction block; run it
		// directly. It holds no exclusive lock, so there is nothing to inspect.
		start := time.Now()
		tag, err := conn.Exec(ctx, sql)
		st.DurationMs = time.Since(start).Milliseconds()
		recordResult(&st, tag, err)
		return st
	}

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		st.ErrCode = "TXBEGIN"
		st.ErrMsg = err.Error()
		return st
	}
	start := time.Now()
	tag, execErr := tx.Exec(ctx, sql)
	st.DurationMs = time.Since(start).Milliseconds()
	recordResult(&st, tag, execErr)

	if execErr == nil {
		// Locks are still held inside the open transaction — read the strongest.
		st.LockMode, st.LockTable = strongestLock(ctx, tx)
		if err := tx.Commit(ctx); err != nil {
			// A commit-time failure (e.g. a deferred constraint) is still a
			// migration failure worth capturing.
			recordResult(&st, pgconn.CommandTag{}, err)
		}
	} else {
		_ = tx.Rollback(ctx)
	}
	return st
}

// recordResult folds a command tag and error into the statement capture.
func recordResult(st *Statement, tag pgconn.CommandTag, err error) {
	if err != nil {
		var pgErr *pgconn.PgError
		if asPgError(err, &pgErr) {
			st.ErrCode = pgErr.Code
			st.ErrMsg = pgErr.Message
		} else {
			st.ErrCode = "EXEC"
			st.ErrMsg = err.Error()
		}
		return
	}
	st.RowsAffected = tag.RowsAffected()
}

// strongestLock reads the strongest relation lock the current transaction holds,
// resolving the relation name. Returns ("", "") when no relation lock is held.
func strongestLock(ctx context.Context, tx pgx.Tx) (mode, table string) {
	const q = `
		SELECT l.mode, c.relname
		FROM pg_locks l
		JOIN pg_class c ON c.oid = l.relation
		WHERE l.locktype = 'relation'
		  AND l.pid = pg_backend_pid()
		  AND c.relkind IN ('r','p')
		ORDER BY array_position(ARRAY[
			'AccessShareLock','RowShareLock','RowExclusiveLock',
			'ShareUpdateExclusiveLock','ShareLock','ShareRowExclusiveLock',
			'ExclusiveLock','AccessExclusiveLock'], l.mode) DESC
		LIMIT 1`
	row := tx.QueryRow(ctx, q)
	if err := row.Scan(&mode, &table); err != nil {
		return "", ""
	}
	return mode, table
}

// classifyIndexBuild reports whether sql builds an index and whether it does so
// CONCURRENTLY.
func classifyIndexBuild(sql string) (isIndex, concurrent bool) {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if !strings.HasPrefix(upper, "CREATE") || !strings.Contains(upper, "INDEX") {
		return false, false
	}
	// Guard against "CREATE TABLE ... " that merely mentions INDEX in a name.
	if !strings.Contains(upper, "CREATE INDEX") && !strings.Contains(upper, "CREATE UNIQUE INDEX") {
		return false, false
	}
	return true, strings.Contains(upper, "CONCURRENTLY")
}

// asPgError unwraps err into a *pgconn.PgError, reporting whether it matched.
func asPgError(err error, target **pgconn.PgError) bool {
	for e := err; e != nil; {
		if pe, ok := e.(*pgconn.PgError); ok {
			*target = pe
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}
