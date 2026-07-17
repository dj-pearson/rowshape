package target

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
)

// LoadReport summarizes what a hydration loaded.
type LoadReport struct {
	Tables map[string]int64 // qualified table name -> rows inserted
}

// Load orchestrates a full hydration into a target: it connects, creates the
// schema and tables, generates rows with the deterministic engine, and inserts
// them — all inside one transaction so a failure leaves the target clean.
//
// The caller owns the target's lifecycle (Close tears a disposable one down);
// Load only fills it.
func Load(ctx context.Context, t Target, f *fixture.Fixture, opts hydrate.Options) (*LoadReport, error) {
	res, err := hydrate.Generate(f, opts)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	conn, err := t.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to target: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	for _, stmt := range DDL(f) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return nil, fmt.Errorf("ddl failed: %w", err)
		}
	}

	report := &LoadReport{Tables: map[string]int64{}}
	for _, gt := range res.Tables {
		n, err := insertRows(ctx, tx, gt)
		if err != nil {
			return nil, fmt.Errorf("insert into %s: %w", gt.Name, err)
		}
		report.Tables[gt.Name] = n
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return report, nil
}

// insertRows bulk-loads one table's generated rows using the binary COPY
// protocol, which is both fast and avoids any SQL-literal quoting concerns.
func insertRows(ctx context.Context, tx pgx.Tx, gt hydrate.GeneratedTable) (int64, error) {
	if len(gt.Rows) == 0 || len(gt.Columns) == 0 {
		return 0, nil
	}
	rows := make([][]any, len(gt.Rows))
	copy(rows, gt.Rows)
	return tx.CopyFrom(ctx, tableIdentifier(gt.Name), gt.Columns, pgx.CopyFromRows(rows))
}

// tableIdentifier splits a qualified name into a pgx.Identifier for COPY.
func tableIdentifier(name string) pgx.Identifier {
	if i := indexByte(name, '.'); i >= 0 {
		return pgx.Identifier{name[:i], name[i+1:]}
	}
	return pgx.Identifier{name}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
