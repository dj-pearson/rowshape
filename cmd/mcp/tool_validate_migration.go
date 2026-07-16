package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	// Registers the RS-* analyzers so validate.Registered() is populated.
	_ "github.com/rowshape/rowshape/internal/findings"
	"github.com/rowshape/rowshape/internal/validate"
)

// validate_migration is the loop-closer (PRD §8.2): an agent writes a migration,
// calls this, reads the verdict, fixes, and re-validates — all in its own turn
// (PRD §2). It runs the SAME analyzer + confidence-capping path as the CLI
// (validate.BuildResult over the registered analyzers) and returns the verdict as
// compact finding CODES, not remediation prose — the agent expands a code with
// explain_finding when it needs the fix (PRD §8.2 token discipline).
//
// This is the fast, target-free path: it analyzes the migration SQL against the
// committed fixture (findings, extrapolation, capping) without hydrating a
// database, so an agent can call it every turn. The CLI's `rowshape validate`
// additionally hydrates a disposable Postgres and applies the migration to catch
// runtime failures; both return the same Verdict struct (PRD §10).

// compactFinding is a finding stripped to what an agent branches on. Remediation,
// detail, and evidence are intentionally omitted — explain_finding is the
// expansion path.
type compactFinding struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Confidence string `json:"confidence,omitempty"`
	Bucket     string `json:"bucket,omitempty"` // duration bucket, if the finding has one
	Explain    string `json:"explain"`          // e.g. "rowshape explain RS-LOCK-001"
}

// validateOutput is the compact verdict returned to the agent.
type validateOutput struct {
	Verdict  string           `json:"verdict"`   // PASS | WARN | FAIL
	ExitCode int              `json:"exit_code"` // 0 PASS / 1 FAIL / 2 WARN-only
	Findings []compactFinding `json:"findings"`
	Note     string           `json:"note,omitempty"`
}

// handleValidateMigration implements the validate_migration tool.
func handleValidateMigration(_ context.Context, _ *sdk.CallToolRequest, in validateMigrationInput) (*sdk.CallToolResult, any, error) {
	f, err := loadFixture(in.Fixture)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	stmts, err := migrationStatements(in.Migration)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if len(stmts) == 0 {
		return errorResult("no SQL statements found in the migration"), nil, nil
	}

	// Build a capture from the statements (no runtime apply) and run the SAME
	// analyzers + capping the CLI runs.
	var sc []validate.Statement
	for _, s := range stmts {
		sc = append(sc, validate.Statement{SQL: s})
	}
	cap := &validate.Capture{Success: true, Statements: sc}
	result := validate.BuildResult(f, cap, validate.Registered(), false)

	out := validateOutput{
		Verdict:  result.Verdict,
		ExitCode: result.ExitCode(false),
		Note:     "static analysis against the committed fixture; run `rowshape validate` for a full hydrate-and-apply. Expand a code with explain_finding.",
	}
	for _, fnd := range result.Findings {
		cf := compactFinding{
			Code:       fnd.Code,
			Severity:   fnd.Severity,
			Title:      fnd.Title,
			Confidence: fnd.Confidence,
			Explain:    fnd.Explain,
		}
		if fnd.Estimate != nil {
			cf.Bucket = fnd.Estimate.Bucket
		}
		out.Findings = append(out.Findings, cf)
	}

	summary := fmt.Sprintf("%s (exit %d), %d finding(s).", out.Verdict, out.ExitCode, len(out.Findings))
	return textResult(summary), out, nil
}

// migrationStatements resolves the `migration` argument to SQL statements: an
// existing .sql file or a directory of them is read from disk; anything else is
// treated as inline SQL, so an agent can pass the migration it just wrote without
// saving it first.
func migrationStatements(migration string) ([]string, error) {
	if migration == "" {
		return nil, fmt.Errorf("no migration given")
	}
	info, err := os.Stat(migration)
	switch {
	case err == nil && info.IsDir():
		entries, err := os.ReadDir(migration)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".sql") {
				b, err := os.ReadFile(filepath.Join(migration, e.Name()))
				if err != nil {
					return nil, err
				}
				out = append(out, validate.SplitStatements(string(b))...)
			}
		}
		return out, nil
	case err == nil:
		b, err := os.ReadFile(migration)
		if err != nil {
			return nil, err
		}
		return validate.SplitStatements(string(b)), nil
	default:
		// Not a path — treat the argument as inline SQL.
		return validate.SplitStatements(migration), nil
	}
}
