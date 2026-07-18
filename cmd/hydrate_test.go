package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/verdict"
)

// CR-T1. cmd/hydrate.go had no test file of any kind, and no host check:
// `validate` refused a target on the fixture's source host while `hydrate`,
// which issues the same class of write (CREATE SCHEMA/TABLE + COPY), wrote to it
// without a word. These tests pin the refusal (INV-BLAST-RADIUS-ZERO, PRD §11).

const hydrateProdHost = "prod-db.internal.example.com"

// writeHydrateFixture writes a minimal fixture whose meta.source is the salted
// hash of host, i.e. a fixture "pulled from" that machine (RFC §8.4).
func writeHydrateFixture(t *testing.T, dir, host string) string {
	t.Helper()
	path := filepath.Join(dir, "rowshape.yaml")
	writeFile(t, path, `rowshape_fixture: "1"
meta:
  id: t
  engine: {name: postgres, version: "16"}
  source: `+profile.HashSource(host)+`
tables:
  public.users:
    rows: {value: 10, confidence: exact}
    columns:
      email: {type: text, nullable: true}
`)
	return path
}

// TestHydrateRefusesSourceHostTarget: `hydrate --target` pointed at the host the
// fixture came from must refuse. This is the blocker CR-T1 was filed for — the
// refusal did not exist, so this ran a COPY into production instead.
func TestHydrateRefusesSourceHostTarget(t *testing.T) {
	fx := writeHydrateFixture(t, t.TempDir(), hydrateProdHost)

	opts := &hydrateOptions{
		fixturePath: fx,
		target:      "postgres://admin:secret@" + hydrateProdHost + ":5432/appdb",
		scale:       1.0,
	}
	var runErr error
	_, stderr := captureOutput(t, func() error { runErr = runHydrate(opts); return runErr })

	if !strings.Contains(stderr, "refusing to hydrate into the fixture's source host") {
		t.Errorf("expected a host-match refusal on stderr, got:\n%s", stderr)
	}
	assertExitCode(t, runErr, verdict.ExitToolError)
	// The refusal must be the ONLY thing that happened: if it had connected and
	// failed, stderr would carry a load error instead.
	if strings.Contains(stderr, "load failed") {
		t.Errorf("hydrate reached the target before refusing:\n%s", stderr)
	}
	// PRD §5: the refusal must not echo connection details.
	assertNoConnectionDetails(t, stderr)
}

// TestHydrateRefusesSourceHostEphemeral: the --ephemeral path is the more
// dangerous of the two, because target.NewEphemeral issues a CREATE DATABASE on
// the admin server — once it returns, the write to production has happened. The
// refusal therefore has to precede it, not merely precede the COPY.
func TestHydrateRefusesSourceHostEphemeral(t *testing.T) {
	fx := writeHydrateFixture(t, t.TempDir(), hydrateProdHost)

	opts := &hydrateOptions{
		fixturePath: fx,
		ephemeral:   "postgres://admin:secret@" + hydrateProdHost + ":5432/postgres",
		scale:       1.0,
	}
	var runErr error
	_, stderr := captureOutput(t, func() error { runErr = runHydrate(opts); return runErr })

	if !strings.Contains(stderr, "refusing to hydrate into the fixture's source host") {
		t.Errorf("expected a host-match refusal on stderr, got:\n%s", stderr)
	}
	assertExitCode(t, runErr, verdict.ExitToolError)
	if strings.Contains(stderr, "could not create a disposable database") {
		t.Errorf("hydrate tried to CREATE DATABASE before refusing:\n%s", stderr)
	}
	assertNoConnectionDetails(t, stderr)
}

// TestHydrateAllowsNonSourceHost: the refusal must not be so broad that hydrate
// cannot do its job. A different host is allowed through — it then fails at
// CONNECT (127.0.0.1:1 refuses instantly), which is the proof it got past the
// check rather than being stopped by it.
func TestHydrateAllowsNonSourceHost(t *testing.T) {
	fx := writeHydrateFixture(t, t.TempDir(), hydrateProdHost)

	opts := &hydrateOptions{
		fixturePath: fx,
		target:      "postgres://u:p@127.0.0.1:1/db",
		scale:       1.0,
	}
	var runErr error
	_, stderr := captureOutput(t, func() error { runErr = runHydrate(opts); return runErr })

	if strings.Contains(stderr, "refusing to hydrate") {
		t.Errorf("must NOT refuse a host the fixture did not come from:\n%s", stderr)
	}
	if runErr == nil {
		t.Error("expected a connect failure against a closed port")
	}
}

// TestHydrateHostRefusalEquivalence pins the decision predicate directly, across
// the spellings that are definitionally the same machine. A host is not a
// string: the single-hash compare this replaced was a proven bypass (a fixture
// pulled from `localhost`, hydrated against `127.0.0.1`, sailed past it).
//
// checkHydrateHost delegates to validate.CheckHost, so this asserts the wiring —
// that hydrate consults it with the right host — rather than re-testing the
// equivalence matrix that internal/validate already owns.
func TestHydrateHostRefusalEquivalence(t *testing.T) {
	cases := []struct {
		name       string
		sourceHost string // host the fixture was pulled from ("" = no source)
		opts       *hydrateOptions
		wantRefuse bool
	}{
		{"exact match via --target", hydrateProdHost,
			&hydrateOptions{target: "postgres://h@" + hydrateProdHost + "/d"}, true},
		{"exact match via --ephemeral", hydrateProdHost,
			&hydrateOptions{ephemeral: "postgres://h@" + hydrateProdHost + "/d"}, true},
		{"case differs (DNS is case-insensitive)", "DB.Internal",
			&hydrateOptions{target: "postgres://h@db.internal/d"}, true},
		{"trailing FQDN dot", "db.internal",
			&hydrateOptions{target: "postgres://h@db.internal./d"}, true},
		{"loopback alias: localhost source, IPv4 target", "localhost",
			&hydrateOptions{target: "postgres://h@127.0.0.1/d"}, true},
		{"loopback alias: IPv4 source, localhost target", "127.0.0.1",
			&hydrateOptions{target: "postgres://h@localhost/d"}, true},

		// Negative cases matter as much: a check that refuses everything stops
		// hydrate from doing its job at all.
		{"a genuinely different host is allowed", hydrateProdHost,
			&hydrateOptions{target: "postgres://h@staging.example.com/d"}, false},
		{"128.0.0.1 is not loopback and is allowed", "127.0.0.1",
			&hydrateOptions{target: "postgres://h@128.0.0.1/d"}, false},
		{"no source host means nothing to collide with", "",
			&hydrateOptions{target: "postgres://h@" + hydrateProdHost + "/d"}, false},
		{"no target at all (SQL emit path)", hydrateProdHost,
			&hydrateOptions{}, false},

		// --target wins over --ephemeral in loadIntoTarget, so the check must
		// look at the same one that will actually be written to.
		{"--target takes precedence over --ephemeral", hydrateProdHost,
			&hydrateOptions{
				target:    "postgres://h@" + hydrateProdHost + "/d",
				ephemeral: "postgres://h@staging.example.com/d",
			}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fixture.Fixture{Meta: fixture.Meta{Source: profile.HashSource(tc.sourceHost)}}
			err := checkHydrateHost(f, tc.opts)
			if tc.wantRefuse && err == nil {
				t.Error("expected a refusal, got none")
			}
			if !tc.wantRefuse && err != nil {
				t.Errorf("expected no refusal, got %v", err)
			}
		})
	}
}

// assertExitCode asserts the command exited with the given code. The exit code
// is part of the public contract (INV-VERDICT-STABLE), so a refusal that printed
// the right words while exiting 0 would still be a broken refusal.
func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected an ExitError, got %v", err)
	}
	if ee.Code != want {
		t.Errorf("exit code = %d, want %d", ee.Code, want)
	}
}

// assertNoConnectionDetails guards PRD §5: host, port and username never reach
// user-facing output.
func assertNoConnectionDetails(t *testing.T, out string) {
	t.Helper()
	for _, secret := range []string{"secret", "admin:", ":5432"} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaked a connection detail (%q):\n%s", secret, out)
		}
	}
}
