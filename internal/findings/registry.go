package findings

import "sort"

// Explanation is the canonical documentation for a finding code: what it means,
// why it matters, and how to fix it. It is the SINGLE source of the remediation
// text — analyzers set a finding's Remediation from here, and `rowshape explain`
// renders the same entry, so the two can never drift (PRD §8.1, §10).
type Explanation struct {
	Code        string   `json:"code"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Remediation string   `json:"remediation"`
	References  []string `json:"references"`
}

// catalog documents every finding code the analyzers can emit. A code missing
// here has no remediation (and no explain entry), which the tests forbid.
var catalog = map[string]Explanation{
	"RS-LOCK-001": {
		Code:        "RS-LOCK-001",
		Title:       "ACCESS EXCLUSIVE lock for a full table rewrite",
		Summary:     "Adding a column with a volatile default, or changing a column's type, rewrites every row while holding an ACCESS EXCLUSIVE lock — no reads or writes proceed until it finishes. On a large table that is a write outage.",
		Remediation: "Avoid the full-table ACCESS EXCLUSIVE rewrite. For a volatile default: ADD the column nullable with no default, backfill in batches, then attach the default and SET NOT NULL via a validated CHECK. For a type change: add a new column of the target type, backfill it in batches, swap reads/writes, and drop the old column. Each step is online.",
		References:  []string{"RFC §9.1", "PRD §10"},
	},
	"RS-DATA-001": {
		Code:        "RS-DATA-001",
		Title:       "SET NOT NULL against existing NULLs",
		Summary:     "SET NOT NULL scans the table and rejects rows that are NULL. If the column's null_fraction is above zero the migration fails; if the zero is only estimated, it cannot be certified safe.",
		Remediation: "Backfill or delete the NULL rows first, or add a DEFAULT; then SET NOT NULL. A validated CHECK (col IS NOT NULL) lets SET NOT NULL skip the full-table scan on PG 12+.",
		References:  []string{"RFC §7.4", "PRD §10"},
	},
	"RS-DATA-014": {
		Code:        "RS-DATA-014",
		Title:       "ADD UNIQUE without proven uniqueness",
		Summary:     "ADD CONSTRAINT UNIQUE can only succeed if the column is actually unique. Uniqueness is never inferred from a sample (INV-UNIQUENESS): unproven uniqueness cannot certify PASS, and proven duplicates make the constraint fail to build.",
		Remediation: "Prove uniqueness before adding the constraint. If duplicates already exist, de-duplicate the column first (remove or merge the duplicate rows).",
		References:  []string{"RFC §7.2", "RFC §7.4", "PRD §10"},
	},
	"RS-DATA-020": {
		Code:        "RS-DATA-020",
		Title:       "FOREIGN KEY validated against pre-existing orphans",
		Summary:     "Validating a foreign key scans every child row for a matching parent. If the reference's orphan_fraction is above zero, rows already violate the key and the VALIDATE fails.",
		Remediation: "Delete or repair the orphaned rows before validating the foreign key: ADD the constraint NOT VALID, clean up the orphans, then VALIDATE CONSTRAINT.",
		References:  []string{"RFC §6.6", "PRD §10"},
	},
	"RS-CONSTRAINT-001": {
		Code:        "RS-CONSTRAINT-001",
		Title:       "NOT VALID constraint validated in the same transaction",
		Summary:     "Adding a constraint NOT VALID and VALIDATE-ing it in one transaction still runs the full validating scan under the transaction's locks — the two-step split whose entire purpose is to avoid a long lock buys nothing.",
		Remediation: "Split across transactions: ADD CONSTRAINT ... NOT VALID and COMMIT, then VALIDATE CONSTRAINT in a separate transaction. VALIDATE then takes only a SHARE UPDATE EXCLUSIVE lock and does not block reads or writes.",
		References:  []string{"RFC §6.4", "RFC §9.1", "PRD §10"},
	},
	"RS-CONSTRAINT-010": {
		Code:        "RS-CONSTRAINT-010",
		Title:       "CHECK constraint conflicts with existing data",
		Summary:     "The column's profiled range violates the CHECK predicate, so existing rows already fail it and adding the constraint (or validating it) fails.",
		Remediation: "Repair or exclude the rows that violate the predicate before adding the CHECK (or widen the predicate). Add the constraint NOT VALID, fix the data, then VALIDATE.",
		References:  []string{"RFC §6.1", "RFC §6.4", "PRD §10"},
	},
	"RS-INDEX-001": {
		Code:        "RS-INDEX-001",
		Title:       "Non-concurrent CREATE INDEX blocks writes",
		Summary:     "A plain CREATE INDEX holds a lock that blocks writes for the whole O(n log n) build. On a large table that is a long write outage.",
		Remediation: "Use CREATE INDEX CONCURRENTLY: it builds in two passes without an exclusive lock, so writes continue. Run it outside a transaction block.",
		References:  []string{"RFC §6.5", "RFC §9.1", "PRD §10"},
	},
	"RS-INDEX-010": {
		Code:        "RS-INDEX-010",
		Title:       "CREATE UNIQUE INDEX without proven uniqueness",
		Summary:     "A unique index can only build if the column is actually unique. Uniqueness is never inferred from a sample (INV-UNIQUENESS): unproven uniqueness cannot certify PASS, and proven duplicates make the build fail.",
		Remediation: "Prove uniqueness before creating the unique index. If duplicates already exist, de-duplicate the column first (remove or merge the duplicate rows).",
		References:  []string{"RFC §6.5", "RFC §7.2", "PRD §10"},
	},
	"RS-INDEX-020": {
		Code:        "RS-INDEX-020",
		Title:       "Non-concurrent REINDEX rebuilds under lock",
		Summary:     "A non-concurrent REINDEX rewrites the whole index while holding a lock that blocks writes. Its cost is driven by the index's on-disk size and bloat.",
		Remediation: "Use REINDEX INDEX CONCURRENTLY (PG 12+) so the rebuild does not block writes.",
		References:  []string{"RFC §6.5", "PRD §10"},
	},
}

// Explain returns the documentation for a finding code.
func Explain(code string) (Explanation, bool) {
	e, ok := catalog[code]
	return e, ok
}

// Codes returns every documented finding code, sorted.
func Codes() []string {
	codes := make([]string, 0, len(catalog))
	for c := range catalog {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}

// remediation returns the canonical remediation for a code — the text analyzers
// attach to their findings, identical to what `rowshape explain` prints.
func remediation(code string) string { return catalog[code].Remediation }
