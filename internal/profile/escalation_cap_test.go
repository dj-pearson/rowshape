package profile

import (
	"context"
	"strings"
	"testing"
)

// TestEffectiveCap: 0 resolves to the documented default; a negative value
// disables the ceiling; a positive value is used as-is (RFC §14.5).
func TestEffectiveCap(t *testing.T) {
	if got := effectiveCap(0); got != DefaultMaxEscalationRows {
		t.Errorf("effectiveCap(0) = %d, want default %d", got, DefaultMaxEscalationRows)
	}
	if got := effectiveCap(1000); got != 1000 {
		t.Errorf("effectiveCap(1000) = %d, want 1000", got)
	}
	if got := effectiveCap(-1); got >= 0 {
		t.Errorf("effectiveCap(-1) = %d, want a negative (unlimited) value", got)
	}

	// overEscalationCap: a positive cap trips above it; a non-positive cap never
	// trips (unlimited).
	over := (&reader{maxEscalationRows: 100}).overEscalationCap(101)
	under := (&reader{maxEscalationRows: 100}).overEscalationCap(100)
	unlimited := (&reader{maxEscalationRows: -1}).overEscalationCap(1 << 40)
	if !over || under || unlimited {
		t.Errorf("overEscalationCap: over=%v under=%v unlimited=%v, want true false false", over, under, unlimited)
	}
}

// TestEscalationCapOmitsAndWarns: above the cap, a dangerous column's escalation
// is skipped — `unique` is omitted (safe, §7.4), NOT blocked — and a WARN names
// the column and the row count. Silent truncation is forbidden (RFC §14.5).
func TestEscalationCapOmitsAndWarns(t *testing.T) {
	conn := adminConn(t)
	seedEscalation(t, conn) // ~5000 rows, look-unique external_ref/slug

	var warns []string
	f, err := Fast(context.Background(), conn, Options{
		Schemas:           []string{escSchema},
		MaxEscalationRows: 100, // far below the ~5000 rows
		Warn:              func(m string) { warns = append(warns, m) },
	})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}

	// Nothing escalated; mode stays fast.
	if f.Meta.Profile.Mode != "fast" {
		t.Errorf("mode = %q, want fast (escalation capped)", f.Meta.Profile.Mode)
	}
	if len(f.Meta.Profile.Escalated) != 0 {
		t.Errorf("escalated = %v, want empty (capped)", f.Meta.Profile.Escalated)
	}

	// external_ref: `unique` omitted (absent), distinct stays estimated — NOT a
	// wrong guess, just an honest decline (§7.4).
	er := f.Tables[escSchema+".users"].Columns["external_ref"]
	if er.Unique != nil {
		t.Errorf("external_ref.unique = %+v, want omitted (capped, unproven)", er.Unique)
	}
	if er.Distinct == nil || er.Distinct.Confidence != "estimated" {
		t.Errorf("external_ref.distinct = %+v, want still estimated (not escalated)", er.Distinct)
	}

	// A WARN names each skipped column and the triggering row count — no silent skip.
	if len(warns) < 2 {
		t.Fatalf("expected a WARN per skipped column, got %v", warns)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{"external_ref", "slug", "5000", "cap of 100"} {
		if !strings.Contains(joined, want) {
			t.Errorf("WARN missing %q; got:\n%s", want, joined)
		}
	}
}

// TestEscalationUnderCapStillEscalates: with the ceiling disabled (or above the
// row count), escalation proceeds as normal — the cap only skips, never blocks
// legitimately-affordable escalation.
func TestEscalationUnderCapStillEscalates(t *testing.T) {
	conn := adminConn(t)
	seedEscalation(t, conn)

	f, err := Fast(context.Background(), conn, Options{
		Schemas:           []string{escSchema},
		MaxEscalationRows: -1, // unlimited
	})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}
	if f.Meta.Profile.Mode != "targeted" {
		t.Errorf("mode = %q, want targeted (escalation not capped)", f.Meta.Profile.Mode)
	}
	er := f.Tables[escSchema+".users"].Columns["external_ref"]
	if er.Unique == nil || er.Unique.Confidence != "exact" {
		t.Errorf("external_ref.unique = %+v, want exact (escalated under unlimited cap)", er.Unique)
	}
}
