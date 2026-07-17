package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"
)

// The RFC's own examples must parse.
//
// RFC-0001 is the published spec, and PRD §3 makes it the position: anyone can
// emit this format. The example in §6 is what a third-party emitter reads and
// copies. If the model drifts from it — a renamed field, a changed shape — the
// spec starts lying to exactly the people it is meant to recruit, and nothing
// would notice: the existing round-trip test (internal/fixture) asserts against a
// hardcoded COPY of the example, so the document itself is unread by any test.
//
// This reads the document. It skips when the file is absent rather than failing,
// because P0-T2 moves RFC-0001 into the rowshape/fixture-spec repository and this
// suite goes with it — a hard failure there would be about layout, not conformance.
var yamlBlock = regexp.MustCompile("(?s)```yaml\n(.*?)```")

func rfcPath(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		filepath.Join("..", "..", "RFC-0001-rowshape-fixture-spec.md"), // monorepo layout
		filepath.Join("..", "RFC-0001.md"),                             // published layout
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("RFC-0001 not found next to the conformance suite")
	return ""
}

// TestRFCColumnExampleConforms: the §6 column-profile example, lifted out of the
// document verbatim and run through the emitter MUSTs.
func TestRFCColumnExampleConforms(t *testing.T) {
	data, err := os.ReadFile(rfcPath(t))
	if err != nil {
		t.Fatal(err)
	}
	blocks := yamlBlock.FindAllStringSubmatch(string(data), -1)
	if len(blocks) < 2 {
		t.Fatalf("expected the RFC to carry yaml examples, found %d", len(blocks))
	}

	// Find the block that defines column profiles (§6). Matching on content
	// rather than an index keeps this from silently testing the wrong block when
	// the document gains an example.
	var cols string
	for _, b := range blocks {
		if strings.Contains(b[1], "columns:") && strings.Contains(b[1], "null_fraction") {
			cols = b[1]
			break
		}
	}
	if cols == "" {
		t.Skip("no column-profile example found in the RFC")
	}

	// §5's fixture example is deliberately illustrative (`columns: { ... }`), so
	// the parseable example is §6's, embedded under a minimal table.
	doc := buildFixture(t, cols)

	// Through the third-party entry point on purpose: this is exactly what an
	// outside emitter does with the document — copy the example, hand over bytes.
	vs, err := CheckEmitterYAML(doc)
	if err != nil {
		t.Fatalf("the RFC's own §6 example does not parse — the published spec is wrong, and a "+
			"third-party emitter copying it would be building against fiction: %v\n%s", err, doc)
	}

	// The spec's own example must satisfy the spec's own MUSTs. If it cannot, the
	// document is not a position.
	for _, v := range vs {
		t.Errorf("the RFC's example violates a MUST it publishes: %s", v)
	}
	t.Logf("RFC §6 example parses and conforms (%d bytes)", len(doc))
}

// buildFixture wraps a bare `columns:` block into a minimal parseable fixture.
func buildFixture(t *testing.T, cols string) []byte {
	t.Helper()
	// Strip the document's own indentation, then re-indent under the table.
	lines := strings.Split(strings.TrimRight(cols, "\n"), "\n")
	prefix := ""
	for _, ln := range lines {
		if s := strings.TrimLeft(ln, " "); s != "" {
			prefix = ln[:len(ln)-len(s)]
			break
		}
	}
	var b strings.Builder
	tmpl := template.Must(template.New("f").Parse(`rowshape_fixture: "1"
meta:
  id: rfc-example
  engine: { name: postgres, version: "16" }
tables:
  public.users:
    rows: { value: 1200000, confidence: exact }
{{ .Cols }}`))
	var body strings.Builder
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			body.WriteString("\n")
			continue
		}
		body.WriteString("    " + strings.TrimPrefix(ln, prefix) + "\n")
	}
	if err := tmpl.Execute(&b, struct{ Cols string }{body.String()}); err != nil {
		t.Fatal(err)
	}
	return []byte(b.String())
}
