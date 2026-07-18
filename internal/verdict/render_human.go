package verdict

import (
	"fmt"
	"io"
	"strings"
)

// Human renders a Result as human-readable text. It is the second marshaler of
// the single Result struct (INV-VERDICT-SHAPE): it reads the SAME struct the
// JSON marshaler serializes, never a separate code path. A test proves both
// marshalers consume one Result value (verdict_test.go).
//
// The output is a rendering, not a reformat of the JSON: it surfaces the fields
// an operator reads first (verdict, each finding's severity/title/location, the
// duration bucket with its basis, and the remediation) in a stable order.
func (r Result) Human() string {
	var b strings.Builder
	r.WriteHuman(&b)
	return b.String()
}

// WriteHuman writes the human rendering to w. Human is the string convenience;
// this is the streaming form used by the CLI.
func (r Result) WriteHuman(w io.Writer) {
	fmt.Fprintf(w, "%s  %s", verdictBadge(r.Verdict), r.Verdict)
	if r.Fixture.ID != "" {
		fmt.Fprintf(w, "  (fixture %s)", r.Fixture.ID)
	}
	fmt.Fprintf(w, "\n")

	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "  no findings\n")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "\n  %s  %s  %s\n", severityMark(f.Severity), f.Code, f.Title)
		if f.Location != nil {
			fmt.Fprintf(w, "    at %s:%d\n", f.Location.File, f.Location.Line)
		}
		if f.Detail != "" {
			fmt.Fprintf(w, "    %s\n", f.Detail)
		}
		if f.Estimate != nil {
			fmt.Fprintf(w, "    duration: %s\n", f.Estimate.human())
		}
		if len(f.DependsOn) > 0 {
			fmt.Fprintf(w, "    rests on: %s [confidence: %s]\n", strings.Join(f.DependsOn, ", "), humanConfidence(f.Confidence))
		}
		if f.Remediation != "" {
			fmt.Fprintf(w, "    fix: %s\n", f.Remediation)
		}
		if f.Explain != "" {
			fmt.Fprintf(w, "    why: %s\n", f.Explain)
		}
	}

	fmt.Fprintf(w, "\nduration: %dms\n", r.DurationMs)
}

func verdictBadge(v string) string {
	switch v {
	case VerdictPass:
		return "[PASS]"
	case VerdictWarn:
		return "[WARN]"
	case VerdictFail:
		return "[FAIL]"
	default:
		return "[????]"
	}
}

func severityMark(s string) string {
	switch s {
	case SeverityError:
		return "x"
	case SeverityWarn:
		return "!"
	case SeverityInfo:
		return "i"
	default:
		return "-"
	}
}

// humanConfidence renders a confidence for a reader.
//
// An absent confidence is the empty string — the fixture carries no such fact, so
// the finding rests on nothing and is capped to WARN. Printing that verbatim gave
// `[confidence: ]`, which is blank on the one line whose whole job is to say how
// well the answer is known. "absent" is the word RFC §7.4 uses for it; say it.
//
// The JSON marshaler is untouched: `null` there already means absent, and the
// verdict contract is the public API (INV-VERDICT-STABLE). This is a rendering of
// the same struct, which is the point (INV-VERDICT-SHAPE).
func humanConfidence(c string) string {
	if c == "" {
		return "absent"
	}
	return c
}

// human renders a duration estimate with its extrapolation basis attached
// (INV-DURATIONS-BUCKETS): a bucket alone is not an answer, the reader needs to
// know what it was projected from.
//
// CR-T9: this used to print the row basis unconditionally, so a BYTE-based
// estimate — a REINDEX bucketed from the index's on-disk size, which never
// touches a row count — rendered as "extrapolated from 0 rows in 0ms". Those
// zeros were not measurements; they were the struct's zero values being read as
// facts, on the one line whose job is to say how the answer was reached.
//
// The discriminator needs no new field, which is why this is a RENDERING fix and
// the DSSE-signable Estimate struct is untouched: findings.estimateFor floors its
// basis at 1ms for every row-based estimate, so BasisMs == 0 can only mean "this
// estimate did not come from a row/time basis". A genuine measured zero is not
// representable there and so cannot be confused with it.
func (e *Estimate) human() string {
	if e.BasisMs == 0 && e.BasisRows == 0 {
		// Not a row/time extrapolation. Name the model instead of inventing a
		// basis — "from the reindex_bytes model" is honest and still tells the
		// reader where the number came from.
		if e.Model != "" {
			return fmt.Sprintf("%s (from the %s model; not extrapolated from a row count)", e.Bucket, e.Model)
		}
		return e.Bucket
	}
	return fmt.Sprintf("%s (extrapolated from %d rows in %dms, %s model, %d rows declared)",
		e.Bucket, e.BasisRows, e.BasisMs, e.Model, e.DeclaredRows)
}
