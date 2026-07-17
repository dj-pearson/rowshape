// Command genfindings writes the finding-catalog docs pages from the canonical
// findings catalog (internal/findings), so each page's remediation is identical
// to `rowshape explain <CODE>` (P4-T4). Run from the repo root:
//
//	go run ./tools/genfindings
//
// The generated pages are committed; gen_test.go fails if they are stale, so the
// catalog and the docs cannot drift.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rowshape/rowshape/internal/findingsdoc"
)

// findingsDir is the Starlight content directory for the catalog, relative to
// the repo root.
const findingsDir = "docs-site/src/content/docs/findings"

func main() {
	if err := generate(findingsDir); err != nil {
		fmt.Fprintln(os.Stderr, "genfindings:", err)
		os.Exit(1)
	}
}

// generate writes index.md plus one page per code into dir. It returns an error
// rather than exiting so the test can call it against a temp dir.
func generate(dir string) error {
	catalog := findingsdoc.Catalog()
	if len(catalog) == 0 {
		return fmt.Errorf("empty catalog — findings.Codes() returned nothing")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(findingsdoc.Index(catalog)), 0o644); err != nil {
		return err
	}
	for code, e := range catalog {
		if err := os.WriteFile(filepath.Join(dir, findingsdoc.PageFile(code)), []byte(findingsdoc.Page(e)), 0o644); err != nil {
			return err
		}
	}
	return nil
}
