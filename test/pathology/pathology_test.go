// Package pathology is the Week-6 kill-criteria gate (PRD §14.1): it proves that
// pull -> rowshape.yaml -> hydrate produces a database that actually reproduces a
// known migration pathology. If these fail, the fixture premise is wrong and
// `validate` must not be built on top of it — stop and surface.
package pathology

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/hydrate"
	"github.com/rowshape/rowshape/internal/target"
)

// testAdminEnv holds a Postgres admin connection for the gate. Without it the
// gate skips; CI sets it against a real Postgres so the gate runs as an
// executable smoke test.
const testAdminEnv = "ROWSHAPE_TEST_PG_DSN"

// hydrateFixture loads a checked-in fixture, hydrates it into a fresh disposable
// database, and returns a connection to that database. The caller runs SQL
// assertions against real hydrated data.
func hydrateFixture(t *testing.T, path string) (*pgx.Conn, func()) {
	t.Helper()
	dsn := os.Getenv(testAdminEnv)
	if dsn == "" {
		t.Skipf("set %s to run the Week-6 pathology gate", testAdminEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f, err := fixture.Parse(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	ctx := context.Background()
	eph, err := target.NewEphemeral(ctx, dsn)
	if err != nil {
		t.Fatalf("create disposable database: %v", err)
	}
	if _, err := target.Load(ctx, eph, f, hydrate.Options{Seed: 42}); err != nil {
		_ = eph.Close(ctx)
		t.Fatalf("hydrate+load: %v", err)
	}
	conn, err := eph.Connect(ctx)
	if err != nil {
		_ = eph.Close(ctx)
		t.Fatalf("connect: %v", err)
	}
	return conn, func() {
		_ = conn.Close(ctx)
		_ = eph.Close(ctx)
	}
}

// TestPathologyNullFraction: a 3.2%-null column round-trips through hydrate and a
// direct query confirms the ratio within tolerance (RFC §6.1).
func TestPathologyNullFraction(t *testing.T) {
	conn, cleanup := hydrateFixture(t, filepath.Join("email_null.yaml"))
	defer cleanup()
	ctx := context.Background()

	var ratio float64
	err := conn.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE email IS NULL)::float8 / count(*) FROM public.accounts`).Scan(&ratio)
	if err != nil {
		t.Fatalf("query null ratio: %v", err)
	}
	t.Logf("hydrated null ratio: %.4f (fixture 0.032)", ratio)
	if ratio < 0.032-0.005 || ratio > 0.032+0.005 {
		t.Errorf("null ratio %.4f outside ±0.5%% of 0.032 — the fixture premise fails, STOP", ratio)
	}
}

// TestPathologyFanoutTail: a long-tailed fan-out round-trips through hydrate and
// a direct query confirms the tail exists — max fan-out far above the mean, and
// p95/max in the ballpark of the fixture (RFC §6.6).
func TestPathologyFanoutTail(t *testing.T) {
	conn, cleanup := hydrateFixture(t, filepath.Join("fanout_tail.yaml"))
	defer cleanup()
	ctx := context.Background()

	// LEFT JOIN from the parent, not GROUP BY on the child.
	//
	// `SELECT count(*) FROM orders GROUP BY user_id` only ever sees parents that
	// HAVE children, so a hydration that leaves most parents childless is
	// invisible to it — the fan-out could collapse onto a handful of parents and
	// every statistic here would still look healthy. Counting from users outward
	// includes the zeros, which is the population the fixture's p50 describes.
	var mean, p50, p95, max float64
	var childless, parents int64
	err := conn.QueryRow(ctx, `
SELECT avg(c)::float8,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY c),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY c),
       max(c)::float8,
       count(*) FILTER (WHERE c = 0),
       count(*)
FROM (SELECT u.id, count(o.user_id)::float8 AS c
      FROM public.users u LEFT JOIN public.orders o ON o.user_id = u.id
      GROUP BY u.id) g`).Scan(&mean, &p50, &p95, &max, &childless, &parents)
	if err != nil {
		t.Fatalf("query fan-out: %v", err)
	}
	t.Logf("hydrated fan-out over ALL %d parents: mean=%.1f p50=%.0f p95=%.0f max=%.0f (%d childless) "+
		"— fixture declares mean 20, p50 5, p95 100, max 800", parents, mean, p50, p95, max, childless)

	// p50 is the shape claim that a mean cannot make. A hydration that dumps every
	// child onto a few parents reproduces the mean exactly and still has a median
	// of zero, which is not this fixture's distribution.
	if p50 < 5/2.5 || p50 > 5*1.5 {
		t.Errorf("fan-out p50=%.0f not within tolerance of fixture p50 5 — the shape is not reproduced, "+
			"only the mean (RFC §6.6, P1-T7)", p50)
	}

	// The tail must exist: max fan-out is many times the mean (the whole point —
	// a uniform distribution would fail here).
	if max < mean*5 {
		t.Errorf("fan-out tail missing: max=%.0f is not >> mean=%.1f — STOP", max, mean)
	}
	// p95 and max recover the fixture's shape within a factor (hydrate's fan-out
	// model is approximate but must land in the right ballpark).
	if max < 800/2.5 || max > 800*1.5 {
		t.Errorf("fan-out max=%.0f not within tolerance of fixture max 800", max)
	}
	if p95 < 100/2.5 || p95 > 100*1.5 {
		t.Errorf("fan-out p95=%.0f not within tolerance of fixture p95 100", p95)
	}
}
