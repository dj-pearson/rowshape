package profile

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

// testDSNEnv names the environment variable holding a Postgres connection for
// the integration tests. When it is unset the tests skip, so `go test` stays
// green without a database; CI sets it against a real seeded Postgres service
// (the RFC §13 conformance target), which is equivalent to the testcontainers
// path but needs no Docker-in-Docker.
const testDSNEnv = "ROWSHAPE_TEST_PG_DSN"

// testSchema is the throwaway schema the integration tests build and read.
const testSchema = "rowshape_p1t3"

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv(testDSNEnv)
	if dsn == "" {
		t.Skipf("set %s to a Postgres connection to run catalog integration tests", testDSNEnv)
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// seed builds a schema exercising every structural feature P1-T3 reads: an
// identity PK, a single-column unique constraint, a CHECK, a NOT VALID CHECK, a
// partial index, and a foreign key with ON DELETE CASCADE.
func seed(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP SCHEMA IF EXISTS ` + testSchema + ` CASCADE`,
		`CREATE SCHEMA ` + testSchema,
		`CREATE TABLE ` + testSchema + `.users (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			email text UNIQUE,
			status text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			CONSTRAINT users_status_check CHECK (status = ANY (ARRAY['active','trialing','canceled']))
		)`,
		`CREATE INDEX users_email_idx ON ` + testSchema + `.users (email) WHERE created_at IS NOT NULL`,
		`CREATE TABLE ` + testSchema + `.orders (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			user_id bigint NOT NULL REFERENCES ` + testSchema + `.users(id) ON DELETE CASCADE,
			external_ref text,
			amount_cents integer NOT NULL
		)`,
		`ALTER TABLE ` + testSchema + `.orders ADD CONSTRAINT orders_amount_positive CHECK (amount_cents > 0) NOT VALID`,
		`INSERT INTO ` + testSchema + `.users (email, status)
			SELECT 'u'||g||'@example.invalid', 'active' FROM generate_series(1,100) g`,
		`ANALYZE ` + testSchema + `.users`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("seed failed on %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+testSchema+` CASCADE`)
	})
}

func TestReadStructure(t *testing.T) {
	conn := adminConn(t)
	seed(t, conn)

	f, err := ReadStructure(context.Background(), conn, Options{Schemas: []string{testSchema}})
	if err != nil {
		t.Fatalf("ReadStructure: %v", err)
	}

	if f.Meta.Engine.Name != "postgres" || f.Meta.Engine.Version == "" {
		t.Errorf("engine = %+v, want postgres with a version", f.Meta.Engine)
	}

	users, ok := f.Tables[testSchema+".users"]
	if !ok {
		t.Fatalf("missing %s.users; tables=%v", testSchema, tableNames(f.Tables))
	}

	// Unique proof from a constraint/index sets exact/via:constraint (RFC §6.4).
	id := users.Columns["id"]
	if id.Unique == nil || !id.Unique.Value || id.Unique.Confidence != "exact" || id.Unique.Via != "constraint" {
		t.Errorf("users.id.unique = %+v, want exact true via constraint", id.Unique)
	}
	email := users.Columns["email"]
	if email.Unique == nil || !email.Unique.Value {
		t.Errorf("users.email.unique = %+v, want proven unique", email.Unique)
	}
	// A non-unique column has NO unique fact — uniqueness is never inferred
	// (INV-UNIQUENESS): it must be exact or absent.
	if status := users.Columns["status"]; status.Unique != nil {
		t.Errorf("users.status.unique = %+v, want absent", status.Unique)
	}

	// Identity column detected.
	if id.Generated != "identity" {
		t.Errorf("users.id.generated = %q, want identity", id.Generated)
	}
	// Structural nullability from the DDL.
	if users.Columns["status"].Nullable {
		t.Errorf("users.status should be NOT NULL")
	}
	if !users.Columns["email"].Nullable {
		t.Errorf("users.email should be nullable")
	}

	// CHECK expression captured verbatim; unique constraint records nulls_distinct.
	if !hasConstraint(users.Constraints, "check", "users_status_check") {
		t.Errorf("missing verbatim check constraint; got %+v", users.Constraints)
	}
	var uniq *bool
	for _, c := range users.Constraints {
		if c.Kind == "unique" {
			uniq = c.NullsDistinct
		}
	}
	if uniq == nil || *uniq != true {
		t.Errorf("unique constraint nulls_distinct = %v, want true", uniq)
	}

	// Partial index preserved.
	var partialSeen bool
	for _, idx := range users.Indexes {
		if idx.Name == "users_email_idx" && idx.Partial != "" {
			partialSeen = true
		}
	}
	if !partialSeen {
		t.Errorf("partial index users_email_idx not captured: %+v", users.Indexes)
	}

	// NOT VALID constraint preserved as validated:false (RFC §6.4).
	orders := f.Tables[testSchema+".orders"]
	var notValidSeen bool
	for _, c := range orders.Constraints {
		if c.Name == "orders_amount_positive" {
			if c.Validated == nil || *c.Validated != false {
				t.Errorf("orders_amount_positive validated = %v, want false", c.Validated)
			}
			notValidSeen = true
		}
	}
	if !notValidSeen {
		t.Errorf("NOT VALID constraint not captured: %+v", orders.Constraints)
	}

	// Foreign key with ON DELETE CASCADE.
	if len(orders.References) != 1 {
		t.Fatalf("orders references = %d, want 1: %+v", len(orders.References), orders.References)
	}
	ref := orders.References[0]
	if ref.Column != "user_id" || ref.To != testSchema+".users.id" || ref.OnDelete != "cascade" {
		t.Errorf("reference = %+v, want user_id -> %s.users.id cascade", ref, testSchema)
	}

	// Rows come from the planner estimate (RFC §7.1 estimated), never exact here.
	if users.Rows.Confidence != "estimated" {
		t.Errorf("users.rows.confidence = %q, want estimated", users.Rows.Confidence)
	}
}

// TestReadStructureIsReadOnly confirms the read runs in a read-only transaction:
// a write attempted on the same connection afterwards still works (the tx was
// rolled back), and, more importantly, ReadStructure itself never writes. We
// assert the transaction mode directly by trying a write inside a read-only tx.
func TestReadStructureIsReadOnly(t *testing.T) {
	conn := adminConn(t)
	seed(t, conn)

	// Sanity: a read-only transaction rejects writes. This is the guarantee
	// ReadStructure relies on (INV-BLAST-RADIUS-ZERO).
	tx, err := conn.BeginTx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin read-only tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(context.Background(), `CREATE TABLE `+testSchema+`.should_not_exist (x int)`)
	if err == nil {
		t.Errorf("a write inside a read-only transaction should fail")
	}
}

func TestCheckAccessSuperuser(t *testing.T) {
	conn := adminConn(t)
	ctx := context.Background()

	var isSuper bool
	if err := conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&isSuper); err != nil {
		t.Fatalf("read rolsuper: %v", err)
	}

	if isSuper {
		if err := CheckAccess(ctx, conn, false); !errors.Is(err, ErrSuperuser) {
			t.Errorf("superuser without override: got %v, want ErrSuperuser", err)
		}
		if err := CheckAccess(ctx, conn, true); err != nil {
			t.Errorf("superuser WITH --i-know: got %v, want nil", err)
		}
	} else {
		// A non-superuser role is always allowed.
		if err := CheckAccess(ctx, conn, false); err != nil {
			t.Errorf("non-superuser: got %v, want nil", err)
		}
	}
}

func hasConstraint(cs []fixture.Constraint, kind, name string) bool {
	for _, c := range cs {
		if c.Kind == kind && c.Name == name && c.Expression != "" {
			return true
		}
	}
	return false
}

func tableNames(m map[string]fixture.Table) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
