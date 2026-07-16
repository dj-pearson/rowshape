package findings

import (
	"fmt"
	"strings"

	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

func init() { validate.Register(rsPerf{}) }

// rsPerf detects performance pathologies driven by data-distribution shape that
// a summary statistic hides (RFC §6.6, PRD §10):
//
//   - RS-PERF-001: a DELETE (or TRUNCATE) on a parent table that is referenced
//     ON DELETE CASCADE with a long-tailed fan-out. The mean fan-out looks
//     harmless while the tail (max) turns one parent delete into a huge, slow,
//     lock-holding cascade — an outage a uniform mean would completely hide.
type rsPerf struct{}

func (rsPerf) Analyze(f *fixture.Fixture, c *validate.Capture) []verdict.Finding {
	var out []verdict.Finding
	for _, st := range c.Statements {
		upper := strings.ToUpper(collapseSpaces(stripSQLComments(st.SQL)))
		parent, ok := deleteTarget(upper, st.SQL)
		if !ok {
			continue
		}
		for _, fnd := range cascadeFanoutFindings(f, parent) {
			out = append(out, fnd)
		}
	}
	return out
}

// cascadeFanoutFindings returns a finding for each ON DELETE CASCADE reference
// into parent whose fan-out is long-tailed.
func cascadeFanoutFindings(f *fixture.Fixture, parent string) []verdict.Finding {
	var out []verdict.Finding
	for childName, child := range f.Tables {
		for _, ref := range child.References {
			if refParent(ref.To) != parent || !strings.EqualFold(ref.OnDelete, "cascade") || ref.Fanout == nil {
				continue
			}
			if !longTailed(ref.Fanout) {
				continue
			}
			out = append(out, verdict.Finding{
				Code:        "RS-PERF-001",
				Severity:    verdict.SeverityWarn,
				Title:       fmt.Sprintf("DELETE on %s cascades through a long-tailed fan-out (max %s vs mean %s children)", shortTable(parent), trimNum(ref.Fanout.Max), trimNum(ref.Fanout.Mean)),
				Detail:      fmt.Sprintf("%s.%s references %s ON DELETE CASCADE; one parent can cascade to as many as %s child rows while the mean is only %s — deleting the wrong parents is a slow, lock-holding cascade.", shortTable(childName), ref.Column, shortTable(parent), trimNum(ref.Fanout.Max), trimNum(ref.Fanout.Mean)),
				Evidence:    map[string]any{"fanout_mean": ref.Fanout.Mean, "fanout_max": ref.Fanout.Max, "cascade_from": childName + "." + ref.Column},
				DependsOn:   []string{childName + ".rows"},
				Remediation: remediation("RS-PERF-001"),
				Explain:     "rowshape explain RS-PERF-001",
			})
		}
	}
	return out
}

// deleteTarget returns the table a DELETE (or TRUNCATE) targets.
func deleteTarget(upper, sql string) (string, bool) {
	switch {
	case strings.HasPrefix(upper, "DELETE FROM "):
		return firstIdentAfter(sql, "DELETE FROM "), true
	case strings.HasPrefix(upper, "TRUNCATE "):
		// TRUNCATE [TABLE] <name>
		rest := "TRUNCATE "
		if strings.HasPrefix(upper, "TRUNCATE TABLE ") {
			rest = "TRUNCATE TABLE "
		}
		return firstIdentAfter(sql, rest), true
	}
	return "", false
}

// firstIdentAfter returns the first identifier following a case-insensitive
// prefix, stripped of a trailing clause/semicolon.
func firstIdentAfter(sql, prefix string) string {
	up := strings.ToUpper(sql)
	i := strings.Index(up, strings.ToUpper(prefix))
	if i < 0 {
		return ""
	}
	fields := strings.Fields(sql[i+len(prefix):])
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(strings.TrimRight(fields[0], ";"), `"`)
}

// refParent reduces a reference target ("public.users.id") to its table
// ("public.users").
func refParent(to string) string {
	if i := strings.LastIndexByte(to, '.'); i >= 0 {
		return to[:i]
	}
	return to
}

// longTailed reports whether a fan-out has a heavy tail: the max dwarfs the mean,
// so a uniform mean would hide the risk (RFC §6.6). Requires a meaningful
// absolute tail so tiny tables do not trip it.
func longTailed(fo *fixture.Fanout) bool {
	if fo.Mean <= 0 {
		return fo.Max >= 100
	}
	return fo.Max >= 100 && fo.Max >= fo.Mean*10
}
