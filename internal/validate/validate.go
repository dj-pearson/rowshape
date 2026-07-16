package validate

import (
	"errors"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/profile"
	"github.com/rowshape/rowshape/internal/verdict"
)

// Analyzer turns a fixture plus the capture of applying a migration into
// findings. The RS-LOCK / RS-DATA / RS-CONSTRAINT / RS-INDEX analyzers
// (P2-T8..T11) implement this and Register themselves; P2-T7 ships the pipeline
// and an empty registry, so validate runs and emits a well-formed Verdict before
// any finding rule exists.
type Analyzer interface {
	// Analyze returns findings for a migration. Each finding declares the fixture
	// facts it rests on in DependsOn and carries the severity it argues for; the
	// pipeline caps its verdict by those facts' confidence (RFC §7.4).
	Analyze(f *fixture.Fixture, c *Capture) []verdict.Finding
}

// registered holds the analyzers plugged in by later phase-2 tasks.
var registered []Analyzer

// Register adds an analyzer to the default registry. Analyzers call this from an
// init function so `validate` picks them up without the CLI knowing each one.
func Register(a Analyzer) { registered = append(registered, a) }

// Registered returns the default analyzer set.
func Registered() []Analyzer { return registered }

// ErrHostMatchesSource is the hard refusal that keeps validate's blast radius at
// zero: the target host hashes to the fixture's source host, so validate would
// be about to touch the very database the fixture was pulled from
// (INV-BLAST-RADIUS-ZERO, PRD §11).
var ErrHostMatchesSource = errors.New("validate: refusing to run against the fixture's source host — validate only ever touches a disposable or provided target, never production")

// CheckHost enforces the host-match refusal (PRD §11). fixtureSource is
// meta.source (a salted host hash); targetHost is the plain host of the target
// URL. It refuses when the target hashes to the source. Empty inputs are safe
// (an ephemeral local target has no source to collide with).
func CheckHost(fixtureSource, targetHost string) error {
	if fixtureSource == "" || targetHost == "" {
		return nil
	}
	if profile.HashSource(targetHost) == fixtureSource {
		return ErrHostMatchesSource
	}
	return nil
}

// BuildResult assembles the Verdict from a capture: it runs each analyzer,
// confidence-caps every finding against the fixture (RFC §7.4), and combines the
// produced verdicts. When groundTruth is set — validate ran against a provided
// live branch whose data is production itself (PRD §15) — the evidence is exact,
// so capping cannot downgrade a finding: the facts were observed, not sampled.
func BuildResult(f *fixture.Fixture, c *Capture, analyzers []Analyzer, groundTruth bool) verdict.Result {
	eng := verdict.NewEngine(f)
	var findings []verdict.Finding
	var verdicts []string

	for _, a := range analyzers {
		for _, fnd := range a.Analyze(f, c) {
			want := wantFor(fnd.Severity)
			var got string
			if groundTruth {
				// Validated against real data: exact evidence, capping cannot fire.
				got = want
				fnd.Confidence = string(fixture.Exact)
			} else {
				got, fnd = eng.Cap(want, fnd)
			}
			verdicts = append(verdicts, got)
			// A clean certification is the silent default — only surface findings
			// that are actionable (a WARN/FAIL, or an error/warn severity).
			if got == verdict.VerdictPass && fnd.Severity == verdict.SeverityInfo {
				continue
			}
			findings = append(findings, fnd)
		}
	}

	overall := verdict.Combine(verdicts...)
	// A migration that did not apply cleanly is never a PASS — floor it to FAIL
	// so a broken apply cannot be certified even before the RS-* detectors that
	// name the specific problem exist (P2-T8+; broken-tool vs unsafe-migration is
	// refined in P2-T17).
	if !c.Success {
		overall = verdict.Combine(overall, verdict.VerdictFail)
	}

	return verdict.Result{
		Rowshape:   verdict.Rowshape,
		Verdict:    overall,
		Fixture:    verdict.FixtureRef{ID: f.Meta.ID, Digest: f.Meta.Digest},
		DurationMs: c.DurationMs,
		Findings:   findings,
	}
}

// wantFor maps a finding's severity to the verdict it argues for: an error is a
// detected FAIL, a warn is a WARN, and an info finding is a clean PASS that
// capping may downgrade if it rests on weak facts.
func wantFor(severity string) string {
	switch severity {
	case verdict.SeverityError:
		return verdict.VerdictFail
	case verdict.SeverityWarn:
		return verdict.VerdictWarn
	default:
		return verdict.VerdictPass
	}
}

// SplitStatements splits a SQL script into individual statements on top-level
// semicolons, skipping semicolons inside line/block comments, single- and
// double-quoted strings, and dollar-quoted bodies ($$...$$, $tag$...$tag$). It
// is enough to apply a raw-SQL migration statement-by-statement for capture; it
// does not validate SQL.
func SplitStatements(sql string) []string {
	var stmts []string
	var buf strings.Builder
	runes := []rune(sql)
	i, n := 0, len(runes)

	flush := func() {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			stmts = append(stmts, s)
		}
		buf.Reset()
	}

	for i < n {
		c := runes[i]
		switch {
		case c == '-' && i+1 < n && runes[i+1] == '-': // line comment
			for i < n && runes[i] != '\n' {
				buf.WriteRune(runes[i])
				i++
			}
		case c == '/' && i+1 < n && runes[i+1] == '*': // block comment
			buf.WriteString("/*")
			i += 2
			for i < n && !(runes[i] == '*' && i+1 < n && runes[i+1] == '/') {
				buf.WriteRune(runes[i])
				i++
			}
			if i < n {
				buf.WriteString("*/")
				i += 2
			}
		case c == '\'' || c == '"': // quoted string / identifier
			quote := c
			buf.WriteRune(c)
			i++
			for i < n {
				buf.WriteRune(runes[i])
				if runes[i] == quote {
					i++
					break
				}
				i++
			}
		case c == '$': // possible dollar-quote
			if tag, ok := dollarTag(runes, i); ok {
				buf.WriteString(tag)
				i += len([]rune(tag))
				for i < n {
					if strings.HasPrefix(string(runes[i:]), tag) {
						buf.WriteString(tag)
						i += len([]rune(tag))
						break
					}
					buf.WriteRune(runes[i])
					i++
				}
			} else {
				buf.WriteRune(c)
				i++
			}
		case c == ';':
			flush()
			i++
		default:
			buf.WriteRune(c)
			i++
		}
	}
	flush()
	return stmts
}

// dollarTag reads a dollar-quote opening tag ($$ or $tag$) starting at i,
// returning it and true if one is present.
func dollarTag(runes []rune, i int) (string, bool) {
	n := len(runes)
	if i >= n || runes[i] != '$' {
		return "", false
	}
	j := i + 1
	for j < n && (runes[j] == '_' || isAlnum(runes[j])) {
		j++
	}
	if j < n && runes[j] == '$' {
		return string(runes[i : j+1]), true
	}
	return "", false
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
