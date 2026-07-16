package profile

import (
	"context"
	"strings"
	"testing"
)

func TestInferTextFormat(t *testing.T) {
	rep := func(s string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = s
		}
		return out
	}
	cases := []struct {
		name          string
		samples       []string
		distinct      int64
		distinctKnown bool
		want          string
	}{
		{"email", []string{"a@example.com", "b@x.io", "c@y.org"}, 3, true, fmtEmail},
		{"uuid", []string{"011300e2-35ed-4ef2-97c8-d896d63fe548", "019300e2-35ed-4ef2-97c8-d896d63fe549"}, 2, true, fmtUUID},
		{"url", []string{"https://a.com/x", "http://b.io/y"}, 2, true, fmtURL},
		{"ipv4", []string{"10.0.0.1", "192.168.1.1"}, 2, true, fmtIPv4},
		{"enum_over_slug", []string{"active", "trialing", "canceled"}, 3, true, fmtEnumLike},
		{"slug_high_card", append(rep("some-slug-value", 50), "another-slug-here", "third-one-x"), 500, true, fmtSlug},
		{"numeric_string", []string{"123456", "789012", "345678", "900011", "222333"}, 5000, true, fmtNumericString},
		{"free_text", []string{"the quick brown fox", "jumped over the lazy dog", "hello there world"}, 1000, true, fmtFreeText},
		{"opaque_mixed", []string{"a1!x", "??z9", "~m/q"}, 1000, true, fmtOpaque},
		{"empty", nil, 0, false, fmtOpaque},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inferTextFormat(tc.samples, tc.distinct, tc.distinctKnown); got != tc.want {
				t.Errorf("inferTextFormat(%v) = %q, want %q", tc.samples, got, tc.want)
			}
		})
	}
}

func TestInferFormatInClosedVocabulary(t *testing.T) {
	vocab := map[string]bool{
		fmtUUID: true, fmtEmail: true, fmtURL: true, fmtHostname: true, fmtIPv4: true,
		fmtIPv6: true, fmtPhone: true, fmtJSON: true, fmtJSONBShape: true, fmtBase64: true,
		fmtHex: true, fmtSlug: true, fmtISODate: true, fmtNumericString: true,
		fmtEnumLike: true, fmtFreeText: true, fmtOpaque: true,
	}
	// Whatever the input, the result is always a member of the closed §6.3 set.
	inputs := [][]string{
		nil,
		{"", "", ""},
		{"random gibberish 123", "!@#$%^", "mixed Content"},
		{"a@b.com"},
	}
	for _, in := range inputs {
		got := inferTextFormat(in, 100, true)
		if !vocab[got] {
			t.Errorf("inferTextFormat(%v) = %q, not in the closed vocabulary", in, got)
		}
	}
}

func TestJSONSkeletonHasTypesNotValues(t *testing.T) {
	samples := []string{
		`{"k1": 1, "k2": "v1", "nested": {"a": true, "b": 2}}`,
		`{"k1": 9, "k2": "v9", "nested": {"a": false, "b": 8}}`,
	}
	sk := jsonSkeleton(samples)
	m, ok := sk.(map[string]any)
	if !ok {
		t.Fatalf("skeleton is not an object: %#v", sk)
	}
	if m["k1"] != "number" {
		t.Errorf("k1 skeleton = %v, want number", m["k1"])
	}
	if m["k2"] != "string" {
		t.Errorf("k2 skeleton = %v, want string", m["k2"])
	}
	nested, ok := m["nested"].(map[string]any)
	if !ok || nested["a"] != "boolean" || nested["b"] != "number" {
		t.Errorf("nested skeleton = %#v, want {a:boolean, b:number}", m["nested"])
	}

	// Crucially, NO leaf value ("v1", "v9", 1, 9) appears anywhere (RFC §6.3).
	flat := flatten(sk)
	for _, forbidden := range []string{"v1", "v9", "1", "9", "true", "false"} {
		for _, leaf := range flat {
			if leaf == forbidden {
				t.Errorf("skeleton leaked a leaf value %q: %v", forbidden, flat)
			}
		}
	}
}

func TestJSONSkeletonMergesAndUnionsTypes(t *testing.T) {
	// A key that is a number in one doc and a string in another unions to a
	// sorted type set; a key present in only one doc still appears.
	samples := []string{
		`{"x": 1, "only_a": true}`,
		`{"x": "s", "only_b": null}`,
	}
	sk := jsonSkeleton(samples).(map[string]any)
	if sk["x"] != "number|string" {
		t.Errorf("x skeleton = %v, want number|string", sk["x"])
	}
	if sk["only_a"] != "boolean" || sk["only_b"] != "null" {
		t.Errorf("union skeleton missing keys: %#v", sk)
	}
}

func TestDistinctFromStats(t *testing.T) {
	cases := []struct {
		n     float64
		rows  int64
		want  int64
		known bool
	}{
		{5, 0, 5, true},        // positive: absolute
		{-1, 1000, 1000, true}, // -1: all distinct
		{-0.5, 1000, 500, true},
		{0, 1000, 0, false}, // unknown
	}
	for _, tc := range cases {
		got, known := distinctFromStats(tc.n, tc.rows)
		if known != tc.known || (known && got != tc.want) {
			t.Errorf("distinctFromStats(%v,%d) = (%d,%v), want (%d,%v)", tc.n, tc.rows, got, known, tc.want, tc.known)
		}
	}
}

func TestCategorize(t *testing.T) {
	cases := map[string]string{
		"text":                     "text",
		"character varying(255)":   "text",
		"bytea":                    "bytea",
		"jsonb":                    "json",
		"json":                     "json",
		"uuid":                     "uuid",
		"boolean":                  "bool",
		"timestamp with time zone": "temporal",
		"date":                     "temporal",
		"time without time zone":   "temporal",
		"integer":                  "numeric",
		"bigint":                   "numeric",
		"numeric(10,2)":            "numeric",
		"double precision":         "numeric",
	}
	for typ, want := range cases {
		if got := categorize(typ); got != want {
			t.Errorf("categorize(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestLengthStatsFromStrings(t *testing.T) {
	got := lengthStatsFromStrings([]string{"ab", "abcd", "abcdef"})
	if got == nil || *got.Min != 2 || *got.Max != 6 {
		t.Fatalf("length = %+v, want min 2 max 6", got)
	}
	if got.Mean == nil || *got.Mean != 4 {
		t.Errorf("mean = %v, want 4", got.Mean)
	}
	// Unicode length is counted in runes, not bytes.
	u := lengthStatsFromStrings([]string{"héllo"}) // 5 runes, 6 bytes
	if u == nil || *u.Max != 5 {
		t.Errorf("unicode length = %+v, want 5 runes", u)
	}
}

// flatten collects all leaf strings from a skeleton for leak-checking.
func flatten(v any) []string {
	switch x := v.(type) {
	case map[string]any:
		var out []string
		for _, val := range x {
			out = append(out, flatten(val)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	case string:
		return []string{x}
	default:
		return nil
	}
}

// TestFastProfiling is the integration test: it profiles the rich seeded table
// and asserts the §6.1 range prohibition, confidence tagging, format classes,
// the jsonb skeleton, and that no uniqueness is inferred from the sample.
func TestFastProfiling(t *testing.T) {
	conn := adminConn(t)
	seedRich(t, conn)

	f, err := Fast(context.Background(), conn, Options{Schemas: []string{richSchema}})
	if err != nil {
		t.Fatalf("Fast: %v", err)
	}
	tbl := f.Tables[richSchema+".t"]
	if len(tbl.Columns) == 0 {
		t.Fatalf("no columns profiled")
	}

	// Sample-derived scalar facts are estimated (RFC §7.1).
	for name, col := range tbl.Columns {
		if col.NullFraction != nil && col.NullFraction.Confidence != "estimated" {
			t.Errorf("%s null_fraction confidence = %q, want estimated", name, col.NullFraction.Confidence)
		}
		if col.Distinct != nil && col.Distinct.Confidence != "estimated" {
			t.Errorf("%s distinct confidence = %q, want estimated", name, col.Distinct.Confidence)
		}
	}

	// §6.1 MUST: text and bytea columns NEVER carry a range; they carry length.
	for _, name := range []string{"email", "status", "bio", "blob"} {
		col := tbl.Columns[name]
		if col.Range != nil {
			t.Errorf("%s (text/bytea) must NOT have a range, got %+v", name, col.Range)
		}
		if col.Length == nil {
			t.Errorf("%s should have length stats", name)
		}
	}

	// Numeric/temporal columns DO carry a range.
	for _, name := range []string{"amount", "n", "created_at"} {
		if tbl.Columns[name].Range == nil {
			t.Errorf("%s should have a range", name)
		}
	}

	// Format classes are from the closed vocabulary and correct where obvious.
	if got := tbl.Columns["email"].Format; got != fmtEmail {
		t.Errorf("email format = %q, want email", got)
	}
	if got := tbl.Columns["uid"].Format; got != fmtUUID {
		t.Errorf("uid format = %q, want uuid", got)
	}
	if got := tbl.Columns["status"].Format; got != fmtEnumLike {
		t.Errorf("status format = %q, want enum_like", got)
	}
	if got := tbl.Columns["blob"].Format; got != fmtOpaque {
		t.Errorf("blob format = %q, want opaque", got)
	}

	// jsonb carries a skeleton of type names, never leaf values (RFC §6.3).
	payload := tbl.Columns["payload"]
	if payload.Format != fmtJSONBShape || payload.Shape == nil {
		t.Fatalf("payload should be jsonb_shape with a skeleton, got format=%q shape=%v", payload.Format, payload.Shape)
	}
	for _, leaf := range flatten(payload.Shape) {
		if strings.HasPrefix(leaf, "v") { // seeded leaf values look like "v1", "v2", ...
			t.Errorf("jsonb skeleton leaked a leaf value: %q", leaf)
		}
	}

	// No uniqueness is inferred from the sample (INV-UNIQUENESS): email is not
	// unique in this table and must have no unique fact.
	if u := tbl.Columns["email"].Unique; u != nil {
		t.Errorf("email.unique = %+v, want absent (never inferred from a sample)", u)
	}
}
