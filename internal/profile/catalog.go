// Package profile reads a database's shape from its catalog and turns it into a
// rowshape fixture. It is the emitter half of the format (RFC §13): it reads
// only via catalog views and (in later tasks) sampled/streamed queries, never
// SELECT * on user tables, and it retains no row values (INV-NO-ROWS).
//
// This file implements structure-only reads (P1-T3): tables, columns, types,
// structural nullability, constraints, indexes, and references. Column
// profiling — null fractions, distinct counts, samples — is layered on by the
// fast-mode profiler (P1-T4).
package profile

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
)

// Options controls a structure read.
type Options struct {
	// Schemas restricts the read to these schemas. Empty means every non-system
	// schema (everything but pg_catalog, information_schema, and pg_toast*).
	Schemas []string
	// Privacy is the level the profiler targets. It governs whether candidate
	// value sets are even gathered (only under permissive); the final emit-time
	// gate is ApplyPrivacy. Empty is treated as standard (never permissive).
	Privacy Privacy
	// MaxEscalationRows is the soft cost ceiling for auto-escalation (RFC §14.5).
	// 0 uses DefaultMaxEscalationRows; a negative value disables the ceiling.
	MaxEscalationRows int64
	// Exact runs the full-pass `exact` profile mode (RFC §7.3): every column gets
	// exact null counts and measured (HLL) distinct over the whole table, and
	// uniqueness is probed exactly. Minutes to hours; opt-in via `pull --exact`.
	Exact bool
	// Warn, if set, receives operational warnings — notably a column whose
	// uniqueness escalation was skipped because the table exceeds the cap. pull
	// wires this to stderr; silent truncation is forbidden.
	Warn func(string)
}

// querier is the subset of pgx used here, satisfied by both *pgx.Conn and
// pgx.Tx, so reads run inside a read-only transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ReadStructure reads engine identity and every in-scope table's structure into
// a fixture — no column profiling. All reads run inside a read-only transaction
// so a conformant emitter can never write (RFC §13, INV-BLAST-RADIUS-ZERO).
func ReadStructure(ctx context.Context, conn *pgx.Conn, opts Options) (*fixture.Fixture, error) {
	return read(ctx, conn, opts, false)
}

// read is the shared body behind ReadStructure and Fast. When profile is set it
// also runs fast-mode column profiling (P1-T4) on each table.
func read(ctx context.Context, conn *pgx.Conn, opts Options, profile bool) (*fixture.Fixture, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	// A read-only transaction makes no changes; rolling back is the correct,
	// cheap close whether or not an error occurred.
	defer func() { _ = tx.Rollback(ctx) }()

	r := &reader{
		tx:                tx,
		attNames:          map[uint32]map[int16]string{},
		privacy:           opts.Privacy,
		maxEscalationRows: effectiveCap(opts.MaxEscalationRows),
		warn:              opts.Warn,
		exact:             opts.Exact,
	}

	f := &fixture.Fixture{
		RowshapeFixture: fixture.FormatVersion,
		Tables:          map[string]fixture.Table{},
	}

	engine, err := r.engine(ctx)
	if err != nil {
		return nil, err
	}
	f.Meta.Engine = engine
	r.serverMajor = majorVersion(engine.Version)

	tables, err := r.tables(ctx, opts.Schemas)
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		tbl, err := r.readTable(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", t.qualified, err)
		}
		if profile {
			if err := r.profileTable(ctx, t, &tbl); err != nil {
				return nil, fmt.Errorf("profile %s: %w", t.qualified, err)
			}
		}
		f.Tables[t.qualified] = tbl
	}

	// Record the profile mode (RFC §7.3): `exact` for a full pass, `targeted` when
	// auto-escalation fired on a sample-based pass, otherwise `fast`.
	if profile {
		switch {
		case r.exact:
			f.Meta.Profile.Mode = "exact"
		case len(r.escalated) > 0:
			sort.Strings(r.escalated)
			f.Meta.Profile.Mode = "targeted"
			f.Meta.Profile.Escalated = r.escalated
		default:
			f.Meta.Profile.Mode = "fast"
		}
	}
	return f, nil
}

// reader holds a read transaction and a per-relation attribute-name cache so
// column numbers in constraints and indexes resolve to names cheaply.
type reader struct {
	tx                querier
	attNames          map[uint32]map[int16]string
	serverMajor       int      // major server version, gating version-specific catalog columns
	privacy           Privacy  // target level; gates whether value sets are gathered
	escalated         []string // qualified columns auto-escalated to a full pass (P1b-T3)
	maxEscalationRows int64    // soft cost ceiling for escalation (P1b-T4)
	warn              func(string)
	exact             bool // full-pass exact mode (P1b-T5)
}

// warnf emits an operational warning if a sink is configured.
func (r *reader) warnf(format string, args ...any) {
	if r.warn != nil {
		r.warn(fmt.Sprintf(format, args...))
	}
}

// majorVersion parses the leading integer of a Postgres server_version string
// ("16.13" -> 16, "11.22" -> 11).
func majorVersion(v string) int {
	n := 0
	seen := false
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			break
		}
		n = n*10 + int(v[i]-'0')
		seen = true
	}
	if !seen {
		return 0
	}
	return n
}

// engine reads the engine name and version. The version is mandatory because
// cost models are engine-version-conditional (RFC §9.1).
func (r *reader) engine(ctx context.Context) (fixture.Engine, error) {
	var version string
	if err := r.tx.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		return fixture.Engine{}, fmt.Errorf("read server_version: %w", err)
	}
	return fixture.Engine{Name: "postgres", Version: version}, nil
}

// tableRef identifies one relation.
type tableRef struct {
	oid       uint32
	schema    string
	name      string
	qualified string // schema.name
	reltuples float64
	bytes     int64
	relkind   string // 'r' ordinary, 'p' partitioned parent
}

// tables lists ordinary and partitioned tables in the requested schemas.
func (r *reader) tables(ctx context.Context, schemas []string) ([]tableRef, error) {
	const q = `
SELECT c.oid, n.nspname, c.relname, c.reltuples, pg_total_relation_size(c.oid), c.relkind::text
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
  AND ($1::text[] IS NULL OR n.nspname = ANY($1))
ORDER BY n.nspname, c.relname`

	var schemaArg any
	if len(schemas) > 0 {
		schemaArg = schemas
	}
	rows, err := r.tx.Query(ctx, q, schemaArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []tableRef
	for rows.Next() {
		var t tableRef
		if err := rows.Scan(&t.oid, &t.schema, &t.name, &t.reltuples, &t.bytes, &t.relkind); err != nil {
			return nil, err
		}
		t.qualified = t.schema + "." + t.name
		out = append(out, t)
	}
	return out, rows.Err()
}

// readTable assembles one table's columns, constraints, indexes, and references.
func (r *reader) readTable(ctx context.Context, t tableRef) (fixture.Table, error) {
	tbl := fixture.Table{
		Rows:  rowEstimate(t.reltuples),
		Bytes: t.bytes,
	}
	cols, order, err := r.columns(ctx, t.oid)
	if err != nil {
		return tbl, err
	}
	constraints, err := r.constraints(ctx, t.oid)
	if err != nil {
		return tbl, err
	}
	indexes, err := r.indexes(ctx, t.oid)
	if err != nil {
		return tbl, err
	}
	refs, err := r.references(ctx, t.oid)
	if err != nil {
		return tbl, err
	}

	// A single-column unique constraint or unique index is a free proof of
	// uniqueness at exact confidence (RFC §6.4 / §7.2). Uniqueness is never
	// inferred any other way (INV-UNIQUENESS).
	applyUniquenessProofs(cols, constraints, indexes)

	tbl.Columns = cols
	tbl.Constraints = constraints
	tbl.Indexes = indexes
	tbl.References = refs
	_ = order
	return tbl, nil
}

// rowEstimate turns pg_class.reltuples into a rows fact. reltuples is the
// planner's estimate (RFC §7.1 estimated); a never-analyzed table reports -1,
// which we clamp to 0. An exact count is the profiler's job (P1-T4), not here.
func rowEstimate(reltuples float64) fixture.Fact[int64] {
	v := int64(reltuples)
	if v < 0 {
		v = 0
	}
	return fixture.Fact[int64]{Value: v, Confidence: fixture.Estimated}
}

// columns reads column structure. It returns the column map plus the on-disk
// order (attnum ascending) so downstream emitters can be deterministic.
//
// pg_attribute.attgenerated arrived with generated columns in PG12. On 10 and 11
// the column does not exist and selecting it is a hard error — `pull` could not
// read a catalog at all on those servers:
//
//	ERROR: column a.attgenerated does not exist (SQLSTATE 42703)
//
// rowshape claims PG 11-17, so that was `pull` not working on a supported
// version. Below 12 the concept does not exist, so it degrades to a constant
// empty string — the same shape as the indnullsnotdistinct guard below.
func (r *reader) columns(ctx context.Context, oid uint32) (map[string]fixture.Column, []string, error) {
	generated := `''` // PG < 12: no generated columns
	if r.serverMajor >= 12 {
		generated = "a.attgenerated::text"
	}
	q := `
SELECT a.attnum, a.attname, format_type(a.atttypid, a.atttypmod),
       a.attnotnull, a.attidentity::text, ` + generated + `
FROM pg_attribute a
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

	rows, err := r.tx.Query(ctx, q, oid)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols := map[string]fixture.Column{}
	var order []string
	names := map[int16]string{}
	for rows.Next() {
		var (
			attnum              int16
			name, typ           string
			notnull             bool
			identity, generated string
		)
		if err := rows.Scan(&attnum, &name, &typ, &notnull, &identity, &generated); err != nil {
			return nil, nil, err
		}
		col := fixture.Column{
			Type:     typ,
			Nullable: !notnull, // structural nullability from the DDL, always exact (§6.1)
		}
		switch {
		case identity == "a" || identity == "d":
			col.Generated = "identity"
		case generated == "s":
			col.Generated = "stored"
		}
		cols[name] = col
		order = append(order, name)
		names[attnum] = name
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	r.attNames[oid] = names
	return cols, order, nil
}

// constraints reads primary-key, unique, and check constraints.
//
// NULLS [NOT] DISTINCT is a property of the backing unique index
// (pg_index.indnullsnotdistinct), added in PG15 — it is NOT on pg_constraint.
// On PG < 15 the concept doesn't exist (NULLs were always distinct), so the
// column read degrades to a constant false there, keeping the reader correct
// across the whole PG 11–17 matrix (P2-T13).
func (r *reader) constraints(ctx context.Context, oid uint32) ([]fixture.Constraint, error) {
	nnd := "false"
	from := "FROM pg_constraint con"
	if r.serverMajor >= 15 {
		nnd = "COALESCE(ix.indnullsnotdistinct, false)"
		from = "FROM pg_constraint con LEFT JOIN pg_index ix ON ix.indexrelid = con.conindid"
	}
	q := `
SELECT con.conname, con.contype::text, con.convalidated, ` + nnd + `,
       con.conkey,
       CASE WHEN con.contype = 'c' THEN pg_get_expr(con.conbin, con.conrelid) END
` + from + `
WHERE con.conrelid = $1 AND con.contype IN ('p', 'u', 'c')
ORDER BY con.conname`

	rows, err := r.tx.Query(ctx, q, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []fixture.Constraint
	for rows.Next() {
		var (
			name             string
			contype          string
			validated        bool
			nullsNotDistinct bool
			conkey           []int16
			checkExpr        *string
		)
		if err := rows.Scan(&name, &contype, &validated, &nullsNotDistinct, &conkey, &checkExpr); err != nil {
			return nil, err
		}
		c := fixture.Constraint{
			Name:    name,
			Kind:    constraintKind(contype),
			Columns: r.namesFor(oid, conkey),
		}
		// A NOT VALID constraint (validated:false) MUST be preserved (§6.4).
		if !validated {
			v := false
			c.Validated = &v
		}
		if contype == "u" {
			// Default is NULLS DISTINCT; record it explicitly (§6.4).
			nd := !nullsNotDistinct
			c.NullsDistinct = &nd
		}
		if contype == "c" && checkExpr != nil {
			c.Expression = *checkExpr // verbatim (§6.4); opaque under privacy:strict
			c.Columns = nil           // a CHECK's columns aren't meaningful here
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// indexes reads index structure and size.
func (r *reader) indexes(ctx context.Context, oid uint32) ([]fixture.Index, error) {
	const q = `
SELECT ic.relname, am.amname, ix.indisunique,
       pg_get_expr(ix.indpred, ix.indrelid),
       pg_relation_size(ic.oid),
       array_to_string(ix.indkey, ' ')
FROM pg_index ix
JOIN pg_class ic ON ic.oid = ix.indexrelid
JOIN pg_am am ON am.oid = ic.relam
WHERE ix.indrelid = $1
ORDER BY ic.relname`

	rows, err := r.tx.Query(ctx, q, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []fixture.Index
	for rows.Next() {
		var (
			name, method string
			unique       bool
			partial      *string
			bytes        int64
			indkey       string
		)
		if err := rows.Scan(&name, &method, &unique, &partial, &bytes, &indkey); err != nil {
			return nil, err
		}
		idx := fixture.Index{
			Name:    name,
			Method:  method,
			Unique:  unique,
			Columns: r.namesFor(oid, parseAttnums(indkey)),
			Bytes:   bytes,
		}
		if partial != nil {
			idx.Partial = *partial
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// references reads foreign keys and their on_delete action. Fan-out and
// orphan_fraction (the measured §6.6 fields) are added by P1-T11.
func (r *reader) references(ctx context.Context, oid uint32) ([]fixture.Reference, error) {
	const q = `
SELECT con.conkey, con.confkey, con.confrelid, con.confdeltype::text,
       refcl.relname, refns.nspname
FROM pg_constraint con
JOIN pg_class refcl ON refcl.oid = con.confrelid
JOIN pg_namespace refns ON refns.oid = refcl.relnamespace
WHERE con.conrelid = $1 AND con.contype = 'f'
ORDER BY con.conname`

	rows, err := r.tx.Query(ctx, q, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type fkRow struct {
		conkey, confkey     []int16
		confrelid           uint32
		delType             string
		refTable, refSchema string
	}
	var fks []fkRow
	for rows.Next() {
		var fk fkRow
		if err := rows.Scan(&fk.conkey, &fk.confkey, &fk.confrelid, &fk.delType, &fk.refTable, &fk.refSchema); err != nil {
			return nil, err
		}
		fks = append(fks, fk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []fixture.Reference
	for _, fk := range fks {
		localCols := r.namesFor(oid, fk.conkey)
		refCols, err := r.namesForRel(ctx, fk.confrelid, fk.confkey)
		if err != nil {
			return nil, err
		}
		for i := range localCols {
			refCol := ""
			if i < len(refCols) {
				refCol = refCols[i]
			}
			out = append(out, fixture.Reference{
				Column:   localCols[i],
				To:       fk.refSchema + "." + fk.refTable + "." + refCol,
				OnDelete: onDeleteAction(fk.delType),
			})
		}
	}
	return out, nil
}

// namesFor maps a relation's attribute numbers to names using the cache built
// by columns(). Zero (an expression column) is skipped.
func (r *reader) namesFor(oid uint32, attnums []int16) []string {
	names := r.attNames[oid]
	var out []string
	for _, n := range attnums {
		if n == 0 {
			continue
		}
		if nm, ok := names[n]; ok {
			out = append(out, nm)
		}
	}
	return out
}

// namesForRel resolves attribute names on a possibly-uncached relation (the
// referenced side of a foreign key), filling the cache on demand.
func (r *reader) namesForRel(ctx context.Context, oid uint32, attnums []int16) ([]string, error) {
	if _, ok := r.attNames[oid]; !ok {
		const q = `SELECT attnum, attname FROM pg_attribute WHERE attrelid = $1 AND attnum > 0 AND NOT attisdropped`
		rows, err := r.tx.Query(ctx, q, oid)
		if err != nil {
			return nil, err
		}
		names := map[int16]string{}
		for rows.Next() {
			var n int16
			var name string
			if err := rows.Scan(&n, &name); err != nil {
				rows.Close()
				return nil, err
			}
			names[n] = name
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		r.attNames[oid] = names
	}
	return r.namesFor(oid, attnums), nil
}

// applyUniquenessProofs sets unique:{true, exact, via:constraint} on any column
// proven unique by a single-column unique constraint or unique index (RFC §6.4).
// Composite uniqueness proves nothing about an individual column, so only
// single-column proofs count.
func applyUniquenessProofs(cols map[string]fixture.Column, constraints []fixture.Constraint, indexes []fixture.Index) {
	proven := map[string]bool{}
	for _, c := range constraints {
		if (c.Kind == "unique" || c.Kind == "primary_key") && len(c.Columns) == 1 {
			proven[c.Columns[0]] = true
		}
	}
	for _, idx := range indexes {
		if idx.Unique && idx.Partial == "" && len(idx.Columns) == 1 {
			proven[idx.Columns[0]] = true
		}
	}
	names := make([]string, 0, len(proven))
	for name := range proven {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		col, ok := cols[name]
		if !ok {
			continue
		}
		t := true
		col.Unique = &fixture.Fact[bool]{Value: t, Confidence: fixture.Exact, Via: "constraint"}
		cols[name] = col
	}
}

// constraintKind maps a pg_constraint contype to the fixture vocabulary (§6.4).
func constraintKind(contype string) string {
	switch contype {
	case "p":
		return "primary_key"
	case "u":
		return "unique"
	case "c":
		return "check"
	case "f":
		return "foreign_key"
	default:
		return contype
	}
}

// onDeleteAction maps a pg_constraint confdeltype to a readable keyword (§6.6).
func onDeleteAction(t string) string {
	switch t {
	case "c":
		return "cascade"
	case "r":
		return "restrict"
	case "n":
		return "set_null"
	case "d":
		return "set_default"
	case "a":
		return "no_action"
	default:
		return t
	}
}

// parseAttnums parses the space-separated attnum list from an int2vector's text
// form (pg_index.indkey) into attribute numbers.
func parseAttnums(s string) []int16 {
	var out []int16
	var cur int16
	inNum := false
	neg := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '-' && !inNum:
			neg = true
			inNum = true
		case ch >= '0' && ch <= '9':
			cur = cur*10 + int16(ch-'0')
			inNum = true
		case ch == ' ':
			if inNum {
				if neg {
					cur = -cur
				}
				out = append(out, cur)
			}
			cur, inNum, neg = 0, false, false
		}
	}
	if inNum {
		if neg {
			cur = -cur
		}
		out = append(out, cur)
	}
	return out
}
