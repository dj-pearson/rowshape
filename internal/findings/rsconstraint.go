package findings

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rowshape/rowshape/internal/estimate"
	"github.com/rowshape/rowshape/internal/fixture"
	"github.com/rowshape/rowshape/internal/validate"
	"github.com/rowshape/rowshape/internal/verdict"
)

func init() { validate.Register(rsConstraint{}) }

// rsConstraint detects constraint pathologies (RFC §6.4, §9.1, PRD §10):
//
//   - RS-CONSTRAINT-001: a constraint added NOT VALID and VALIDATE-d in the SAME
//     transaction — the validating O(n) scan runs while holding the lock the
//     NOT VALID split was meant to avoid, so the split buys nothing.
//   - RS-CONSTRAINT-010: a CHECK constraint that conflicts with the profiled data
//     shape (e.g. CHECK (c >= 0) on a column whose range dips below 0) — the
//     validation will fail on existing rows.
//
// Findings report the validation scan as a bucket, declare depends_on, are
// confidence-capped by the pipeline, and carry mandatory remediation.
type rsConstraint struct{}

// addInfo remembers a NOT VALID constraint add so a later VALIDATE in the same
// transaction can be recognized.
type addInfo struct {
	table string
	epoch int
	stmt  validate.Statement
}

func (rsConstraint) Analyze(f *fixture.Fixture, c *validate.Capture) []verdict.Finding {
	_, hasVersion := estimate.Major(f.Meta.Engine.Version)

	var out []verdict.Finding
	notValidAdds := map[string]addInfo{} // upper(constraint name) -> add
	epoch := 0                           // transaction epoch: increments on each COMMIT/ROLLBACK

	for i, st := range c.Statements {
		clean := collapseSpaces(stripSQLComments(st.SQL))
		upper := strings.ToUpper(clean)

		if name, table, kind, notValid, checkExpr, ok := parseAddConstraint(clean, upper); ok {
			if notValid {
				notValidAdds[strings.ToUpper(name)] = addInfo{table: table, epoch: epoch, stmt: st}
			}
			if kind == "CHECK" && checkExpr != "" {
				if fnd, ok := checkConflict(f, table, checkExpr); ok {
					out = append(out, fnd)
				}
			}
		}

		if vname, ok := parseValidateConstraint(upper); ok {
			if add, known := notValidAdds[strings.ToUpper(vname)]; known && add.epoch == epoch {
				out = append(out, sameTxFinding(f, c, i, add.table, vname, hasVersion))
			}
		}

		if isTxEnd(upper) {
			epoch++
		}
	}
	return out
}

// sameTxFinding reports a NOT VALID constraint validated in the same transaction
// (the VALIDATE is statement i).
func sameTxFinding(f *fixture.Fixture, c *validate.Capture, i int, table, name string, hasVersion bool) verdict.Finding {
	tbl := f.Tables[table]

	fnd := verdict.Finding{
		Code:        "RS-CONSTRAINT-001",
		Severity:    verdict.SeverityWarn,
		Title:       fmt.Sprintf("Constraint %s on %s is validated in the same transaction it is added NOT VALID", name, shortTable(table)),
		Detail:      "Adding a constraint NOT VALID and VALIDATE-ing it in one transaction still runs the full validating scan under the transaction's locks — the two-step split that avoids a long lock is defeated.",
		DependsOn:   []string{table + ".rows"},
		Remediation: remediation("RS-CONSTRAINT-001"),
		Explain:     "rowshape explain RS-CONSTRAINT-001",
	}
	if hasVersion {
		fnd.Estimate = estimateFor(c, i, estimate.ConstraintValidation, table, tbl.Rows.Value, tbl.Rows.Confidence)
	}
	return fnd
}

// checkConflict flags a CHECK constraint whose comparison conflicts with the
// column's profiled range (RFC §6.1/§6.4): CHECK (c >= K) against a range whose
// minimum is below K means existing rows violate the constraint.
func checkConflict(f *fixture.Fixture, table, expr string) (verdict.Finding, bool) {
	col, op, k, ok := parseComparison(expr)
	if !ok {
		return verdict.Finding{}, false
	}
	c, ok := f.Tables[table].Columns[col]
	if !ok || c.Range == nil {
		return verdict.Finding{}, false
	}
	lo, loOK := numeric(c.Range.Min)
	hi, hiOK := numeric(c.Range.Max)

	violated := false
	switch op {
	case ">":
		violated = loOK && lo <= k
	case ">=":
		violated = loOK && lo < k
	case "<":
		violated = hiOK && hi >= k
	case "<=":
		violated = hiOK && hi > k
	}
	if !violated {
		return verdict.Finding{}, false
	}
	return verdict.Finding{
		Code:        "RS-CONSTRAINT-010",
		Severity:    verdict.SeverityError,
		Title:       fmt.Sprintf("CHECK (%s %s %s) on %s.%s conflicts with existing data", col, op, trimNum(k), shortTable(table), col),
		Detail:      fmt.Sprintf("The column's profiled range [%s, %s] violates CHECK (%s %s %s); adding the constraint will fail on existing rows.", trimNum(lo), trimNum(hi), col, op, trimNum(k)),
		Evidence:    map[string]any{"range_min": c.Range.Min, "range_max": c.Range.Max, "check": expr},
		DependsOn:   []string{table + ".rows"},
		Remediation: remediation("RS-CONSTRAINT-010"),
		Explain:     "rowshape explain RS-CONSTRAINT-010",
	}, true
}

// parseAddConstraint recognizes ALTER TABLE ... ADD CONSTRAINT <name> <kind> ...
// and returns the name, table, kind (CHECK/FK/UNIQUE/...), whether it is NOT
// VALID, and the CHECK expression (for a CHECK).
func parseAddConstraint(clean, upper string) (name, table, kind string, notValid bool, checkExpr string, ok bool) {
	if !strings.HasPrefix(upper, "ALTER TABLE") || !strings.Contains(upper, "ADD CONSTRAINT") {
		return "", "", "", false, "", false
	}
	table = alterTableTarget(clean)
	ci := strings.Index(upper, "ADD CONSTRAINT")
	rest := strings.Fields(clean[ci+len("ADD CONSTRAINT"):])
	if len(rest) == 0 {
		return "", "", "", false, "", false
	}
	name = strings.Trim(rest[0], `"`)
	notValid = strings.Contains(upper, "NOT VALID")

	switch {
	case strings.Contains(upper, "CHECK"):
		kind = "CHECK"
		checkExpr = parenAfter(clean, "CHECK")
	case strings.Contains(upper, "FOREIGN KEY"):
		kind = "FK"
	case strings.Contains(upper, "UNIQUE"):
		kind = "UNIQUE"
	case strings.Contains(upper, "EXCLUDE"):
		kind = "EXCLUDE"
	default:
		kind = "OTHER"
	}
	if table == "" {
		return "", "", "", false, "", false
	}
	return name, table, kind, notValid, checkExpr, true
}

// parseComparison extracts "<col> <op> <number>" from a CHECK body, op one of
// >, >=, <, <=. It handles a leading/trailing set of parentheses and spacing.
func parseComparison(expr string) (col, op string, k float64, ok bool) {
	e := strings.TrimSpace(strings.Trim(strings.TrimSpace(expr), "()"))
	// Longest operators first.
	for _, o := range []string{">=", "<=", ">", "<"} {
		if i := strings.Index(e, o); i >= 0 {
			left := strings.TrimSpace(e[:i])
			right := strings.TrimSpace(e[i+len(o):])
			n, err := strconv.ParseFloat(strings.Fields(right + " ")[0], 64)
			if err != nil {
				return "", "", 0, false
			}
			col = strings.Trim(lastField(left), `"`)
			if col == "" {
				return "", "", 0, false
			}
			return col, o, n, true
		}
	}
	return "", "", 0, false
}

// parenAfter returns the content of the first balanced parenthesized group
// following keyword (case-insensitive), e.g. after "CHECK" -> "amount_cents > 0".
func parenAfter(s, keyword string) string {
	up := strings.ToUpper(s)
	i := strings.Index(up, strings.ToUpper(keyword))
	if i < 0 {
		return ""
	}
	open := strings.IndexByte(s[i:], '(')
	if open < 0 {
		return ""
	}
	open += i
	depth := 0
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[open+1 : j])
			}
		}
	}
	return ""
}

// isTxEnd reports whether a statement ends a transaction (COMMIT / ROLLBACK / END).
func isTxEnd(upper string) bool {
	return strings.HasPrefix(upper, "COMMIT") || strings.HasPrefix(upper, "ROLLBACK") || strings.HasPrefix(upper, "END")
}

// numeric coerces a YAML-decoded range bound to a float, if it is a number.
func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// trimNum renders a float without a trailing ".0" for whole numbers.
func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
