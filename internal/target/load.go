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
	// SkippedIndexes names the secondary indexes that could not be created, with the
	// reason. It is never silently empty-on-failure: an index that production has and
	// the disposable database does not is a difference that can change a verdict, so
	// the caller reports these rather than discarding them.
	SkippedIndexes []string
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

	// Secondary indexes are built AFTER the rows, each inside a savepoint.
	//
	// Best-effort, because an index key can be an arbitrary expression and the fixture
	// records only the expression's TEXT — not the immutable functions or operator
	// classes it may call. Run unguarded, one such index would abort the whole load for
	// a schema that is otherwise perfectly hydratable. The savepoint keeps the failure
	// local: without one, the first error poisons the transaction and every later
	// statement fails with "current transaction is aborted" regardless of merit.
	//
	// AFTER, because partial and expression UNIQUE indexes are now emitted. Built
	// before the COPY, such an index turns any duplicate the synthesis engine produces
	// into a failed COPY that takes the entire load down — and a partial unique index
	// is precisely where duplicates are expected, since
	// `UNIQUE (email) WHERE deleted_at IS NULL` leaves `email` non-unique overall, so
	// the fixture marks it non-unique and the engine duly generates repeats. Building
	// afterwards means the index build is what fails, inside its savepoint, and the
	// index is REPORTED as skipped rather than ending the run.
	for i, stmt := range SecondaryIndexes(f) {
		sp := fmt.Sprintf("rowshape_idx_%d", i)
		if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
			return nil, fmt.Errorf("savepoint: %w", err)
		}
		if _, err := tx.Exec(ctx, stmt); err != nil {
			if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp); rbErr != nil {
				return nil, fmt.Errorf("rollback to savepoint: %w", rbErr)
			}
			report.SkippedIndexes = append(report.SkippedIndexes, fmt.Sprintf("%s: %v", stmt, err))
			continue
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
			return nil, fmt.Errorf("release savepoint: %w", err)
		}
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
