package findings

import (
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/verdict"
)

// RS-INDEX-020 and RS-DATA-001 both carry remediation that names a Postgres
// version boundary ("REINDEX ... CONCURRENTLY (PG 12+)"; "a validated CHECK lets
// SET NOT NULL skip the full-table scan on PG 12+"). Unlike RS-LOCK-001's
// ADD-COLUMN-DEFAULT rule — which genuinely diverges (WARN on PG 10, PASS on
// 11+, covered by TestVersionMatrixAddColumnDefault) — these two are version
// INVARIANT: the finding is produced identically on every major, and the version
// caveat lives in static remediation text.
//
// That distinction was untested. If a refactor made either finding accidentally
// version-conditional, or dropped the "PG 12+" caveat (misleading a PG 10/11
// user toward a fix their server does not have), nothing would catch it. These
// tests pin the invariance AND the presence of the caveat across PG 10-17.
// docs/TESTING-GAPS.md item 12.

var allMajors = []string{"10", "11", "12", "13", "14", "15", "16", "17"}

// findByCode returns the first finding with the given code, or a zero finding.
func findByCode(fs []verdict.Finding, code string) (verdict.Finding, bool) {
	for _, f := range fs {
		if f.Code == code {
			return f, true
		}
	}
	return verdict.Finding{}, false
}

// TestRSIndex020VersionInvariant: a non-concurrent REINDEX warns identically on
// every major, and the remediation always names the PG 12+ CONCURRENTLY option.
func TestRSIndex020VersionInvariant(t *testing.T) {
	f, mig := loadCorpus(t, "rsindex-reindex-bloat")

	var reference *verdict.Finding
	for _, major := range allMajors {
		t.Run("pg"+major, func(t *testing.T) {
			fv := *f
			fv.Meta.Engine = f.Meta.Engine
			fv.Meta.Engine.Version = major

			got := rsIndex{}.Analyze(&fv, plainCapture(mig))
			fnd, ok := findByCode(got, "RS-INDEX-020")
			if !ok {
				t.Fatalf("PG %s: RS-INDEX-020 not produced; got %+v", major, got)
			}
			if fnd.Severity != verdict.SeverityWarn {
				t.Errorf("PG %s: severity = %s, want warn", major, fnd.Severity)
			}
			if !strings.Contains(fnd.Remediation, "PG 12+") {
				t.Errorf("PG %s: remediation dropped the PG 12+ caveat: %q", major, fnd.Remediation)
			}
			// Invariance: the finding's user-facing content is identical to the
			// first major's — no accidental version conditioning crept in.
			if reference == nil {
				fCopy := fnd
				reference = &fCopy
				return
			}
			if fnd.Title != reference.Title || fnd.Remediation != reference.Remediation ||
				(fnd.Estimate == nil) != (reference.Estimate == nil) ||
				(fnd.Estimate != nil && fnd.Estimate.Bucket != reference.Estimate.Bucket) {
				t.Errorf("PG %s: RS-INDEX-020 diverged from PG %s:\n  got %+v\n  ref %+v",
					major, allMajors[0], fnd, *reference)
			}
		})
	}
}

// TestRSData001VersionInvariant: SET NOT NULL against existing NULLs produces
// RS-DATA-001 identically on every major, with the "PG 12+" validated-CHECK
// caveat always present.
func TestRSData001VersionInvariant(t *testing.T) {
	f, mig := loadCorpus(t, "rsdata-notnull-has-nulls")

	var reference *verdict.Finding
	for _, major := range allMajors {
		t.Run("pg"+major, func(t *testing.T) {
			fv := *f
			fv.Meta.Engine = f.Meta.Engine
			fv.Meta.Engine.Version = major

			got := rsData{}.Analyze(&fv, plainCapture(mig))
			fnd, ok := findByCode(got, "RS-DATA-001")
			if !ok {
				t.Fatalf("PG %s: RS-DATA-001 not produced; got %+v", major, got)
			}
			if !strings.Contains(fnd.Remediation, "PG 12+") {
				t.Errorf("PG %s: remediation dropped the PG 12+ caveat: %q", major, fnd.Remediation)
			}
			if reference == nil {
				fCopy := fnd
				reference = &fCopy
				return
			}
			if fnd.Severity != reference.Severity || fnd.Remediation != reference.Remediation {
				t.Errorf("PG %s: RS-DATA-001 diverged from PG %s:\n  got %+v\n  ref %+v",
					major, allMajors[0], fnd, *reference)
			}
		})
	}
}
