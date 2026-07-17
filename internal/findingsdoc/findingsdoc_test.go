package findingsdoc

import (
	"strings"
	"testing"

	"github.com/rowshape/rowshape/internal/findings"
)

func TestPageContainsCatalogFields(t *testing.T) {
	e := findings.Explanation{
		Code:        "RS-LOCK-001",
		Title:       "a title",
		Summary:     "one sentence. two sentence.",
		Remediation: "do the thing",
		References:  []string{"RFC §9.1", "PRD §10"},
	}
	page := Page(e)
	for _, want := range []string{
		"title: 'RS-LOCK-001 — a title'",
		"description: 'one sentence.'", // first sentence only
		"`RS-LOCK`",                    // namespace
		"one sentence. two sentence.",  // full summary in the body
		"## Remediation",
		"do the thing",
		"RFC §9.1",
		"rowshape explain RS-LOCK-001",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q:\n%s", want, page)
		}
	}
}

func TestPageFileAndNamespace(t *testing.T) {
	if got := PageFile("RS-DATA-014"); got != "rs-data-014.md" {
		t.Errorf("PageFile = %q, want rs-data-014.md", got)
	}
	if got := Namespace("RS-CONSTRAINT-010"); got != "RS-CONSTRAINT" {
		t.Errorf("Namespace = %q, want RS-CONSTRAINT", got)
	}
}

// TestYamlQuoteEscapes: a title/summary with an apostrophe must not break the
// single-quoted YAML frontmatter (RS-LOCK-001's summary has "column's").
func TestYamlQuoteEscapes(t *testing.T) {
	e := findings.Explanation{Code: "RS-X-001", Title: "a column's type", Summary: "it's fine."}
	page := Page(e)
	if !strings.Contains(page, "a column''s type") {
		t.Errorf("apostrophe should be doubled for YAML:\n%s", page)
	}
}

// TestIndexGroupsByNamespace: the index lists every code, grouped, with links.
func TestIndexGroupsByNamespace(t *testing.T) {
	idx := Index(Catalog())
	for _, want := range []string{
		"## RS-LOCK — Locks a migration takes, and for how long",
		"[`RS-LOCK-001`](./rs-lock-001/)",
		"## RS-REVERSE",
		"explain_finding",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("index missing %q:\n%s", want, idx)
		}
	}
}

// TestCatalogNonEmpty: Catalog() surfaces the real registered codes.
func TestCatalogNonEmpty(t *testing.T) {
	c := Catalog()
	if len(c) == 0 {
		t.Fatal("empty catalog")
	}
	if _, ok := c["RS-LOCK-001"]; !ok {
		t.Error("expected RS-LOCK-001 in the catalog")
	}
}
