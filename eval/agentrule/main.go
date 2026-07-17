// Command agentrule-eval scores coding-agent sessions for adherence to the
// rowshape agent rule (P4-T8, PRD §8.3 / §15 — "the bet is the loop, not the
// tool"). The rule is a product artifact iterated like a prompt; this harness is
// how a rule change is MEASURED instead of guessed.
//
// It consumes SESSION TRACES — an ordered list of the tool calls and actions an
// agent took (describe_shape, write_sql, validate, explain, open_pr) — and scores
// three rule behaviours plus loop closure:
//
//   - describe_shape before the first write_sql   (rule: read the shape first)
//   - validate before open_pr                     (rule: validate before a PR)
//   - no hand-waved FAIL/WARN                      (rule: a WARN is not a pass; a
//     FAIL is not an opinion — never open a PR on a non-PASS verdict)
//   - loop closed: reached PASS before the PR, having started non-PASS
//
// A live agent runner (Claude Code driven against demo/repo) EMITS these traces;
// producing them is a non-deterministic, out-of-CI step. Scoring them is
// deterministic and is what lets a rule version be A/B-compared: a deliberately
// weakened rule yields sessions that score measurably lower (see the traces/ and
// harness_test.go).
//
// Usage:
//
//	go run ./eval/agentrule eval/agentrule/traces
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Event is one action in a session trace.
type Event struct {
	Type     string   `json:"type"`               // describe_shape | write_sql | validate | explain | open_pr
	Tables   []string `json:"tables,omitempty"`   // describe_shape
	Path     string   `json:"path,omitempty"`     // write_sql
	Verdict  string   `json:"verdict,omitempty"`  // validate: PASS | WARN | FAIL
	Findings []string `json:"findings,omitempty"` // validate
	Code     string   `json:"code,omitempty"`     // explain
}

// Session is one scored agent run.
type Session struct {
	Name        string  `json:"name"`
	RuleVersion int     `json:"rule_version"`
	RuleLabel   string  `json:"rule_label"` // e.g. "v2" or "weakened"
	Events      []Event `json:"events"`
}

// Scorecard is the per-session result. The three adherence booleans map directly
// to rule clauses; LoopClosed is the outcome the rule exists to produce.
type Scorecard struct {
	DescribeBeforeSQL bool
	ValidateBeforePR  bool
	NoHandWavedFail   bool
	LoopClosed        bool
}

// Adherence is the fraction of the three rule behaviours the session followed.
func (s Scorecard) Adherence() float64 {
	n := 0
	for _, b := range []bool{s.DescribeBeforeSQL, s.ValidateBeforePR, s.NoHandWavedFail} {
		if b {
			n++
		}
	}
	return float64(n) / 3
}

// Score evaluates one session against the rule behaviours.
func Score(sess Session) Scorecard {
	var (
		sawDescribe   bool
		firstSQLSeen  bool
		sawValidate   bool
		lastVerdict   string
		startedNonPS  bool
		reachedPASS   bool
		card          Scorecard
		describeFirst = true // until proven otherwise
	)
	// Defaults: a session that never writes SQL or opens a PR trivially satisfies
	// the "before" checks; they only fail on an actual ordering violation.
	card.DescribeBeforeSQL = true
	card.ValidateBeforePR = true
	card.NoHandWavedFail = true

	for _, e := range sess.Events {
		switch e.Type {
		case "describe_shape":
			sawDescribe = true
		case "write_sql":
			if !firstSQLSeen {
				firstSQLSeen = true
				if !sawDescribe {
					describeFirst = false
				}
			}
		case "validate":
			sawValidate = true
			lastVerdict = e.Verdict
			if e.Verdict == "PASS" {
				reachedPASS = true
			} else {
				startedNonPS = true
			}
		case "open_pr":
			if !sawValidate {
				card.ValidateBeforePR = false
			}
			// Opening a PR while the most recent verdict is not PASS is exactly the
			// hand-wave the rule forbids ("a WARN is not a pass; a FAIL is not an
			// opinion").
			if lastVerdict != "PASS" {
				card.NoHandWavedFail = false
			}
		}
	}
	card.DescribeBeforeSQL = describeFirst
	// The loop closed if the agent started from a non-PASS verdict and ended at a
	// PASS before opening the PR.
	card.LoopClosed = startedNonPS && reachedPASS && lastVerdict == "PASS"
	return card
}

// Aggregate is the harness's headline output across a set of sessions for one
// rule version: mean adherence and the loop-closure rate.
type Aggregate struct {
	RuleLabel     string
	Sessions      int
	AdherenceMean float64
	ClosureRate   float64
}

// Summarize groups sessions by rule label and aggregates each group.
func Summarize(sessions []Session) []Aggregate {
	byLabel := map[string][]Scorecard{}
	labels := []string{}
	for _, s := range sessions {
		if _, ok := byLabel[s.RuleLabel]; !ok {
			labels = append(labels, s.RuleLabel)
		}
		byLabel[s.RuleLabel] = append(byLabel[s.RuleLabel], Score(s))
	}
	sort.Strings(labels)
	var out []Aggregate
	for _, label := range labels {
		cards := byLabel[label]
		var adh, closed float64
		for _, c := range cards {
			adh += c.Adherence()
			if c.LoopClosed {
				closed++
			}
		}
		n := float64(len(cards))
		out = append(out, Aggregate{
			RuleLabel:     label,
			Sessions:      len(cards),
			AdherenceMean: adh / n,
			ClosureRate:   closed / n,
		})
	}
	return out
}

// LoadSessions reads every *.json trace in dir.
func LoadSessions(dir string) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var s Session
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if s.Name == "" {
			s.Name = name
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func main() {
	dir := "eval/agentrule/traces"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	sessions, err := LoadSessions(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentrule-eval:", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "agentrule-eval: no traces in", dir)
		os.Exit(1)
	}

	fmt.Printf("Scored %d session(s) from %s\n\n", len(sessions), dir)
	for _, s := range sessions {
		c := Score(s)
		fmt.Printf("  %-28s [%s]  describe=%-5v validate=%-5v no-handwave=%-5v  closed=%-5v  adherence=%.0f%%\n",
			s.Name, s.RuleLabel, c.DescribeBeforeSQL, c.ValidateBeforePR, c.NoHandWavedFail, c.LoopClosed, c.Adherence()*100)
	}
	fmt.Println("\nBy rule version:")
	for _, a := range Summarize(sessions) {
		fmt.Printf("  %-12s  sessions=%d  adherence=%.0f%%  loop-closure=%.0f%%\n",
			a.RuleLabel, a.Sessions, a.AdherenceMean*100, a.ClosureRate*100)
	}
}
