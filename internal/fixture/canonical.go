package fixture

import (
	"bytes"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Canonical renders a fixture into its canonical form (RFC §11):
//
//   - keys sorted lexicographically at every level,
//   - two-space indent,
//   - no anchors or aliases,
//   - floats to 6 significant figures,
//   - "\n" line endings,
//   - meta.digest and meta.generated_at excluded.
//
// This is the ONE canonical implementation shared by CLI and the phase-5 API
// (INV-ONE-CANONICAL-FORM). Two canonicalizations of an equal fixture are
// byte-identical, and the bytes are stable across runs and platforms, because
// the procedure never depends on Go map iteration order (INV-DETERMINISM).
func Canonical(f *Fixture) ([]byte, error) {
	raw, err := yaml.Marshal(f)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	// doc is a DocumentNode; its single child is the root mapping.
	if len(doc.Content) == 0 {
		return []byte{}, nil
	}
	root := doc.Content[0]
	dropExcludedMetaFields(root)
	canonicalizeNode(root)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// canonicalizeNode rewrites a node tree in place into canonical shape: mapping
// keys sorted, floats reformatted, anchors/aliases stripped. Sequence order is
// preserved — it is semantically significant (e.g. columns/frequencies).
func canonicalizeNode(n *yaml.Node) {
	// Strip any anchor so the encoder never emits an alias (RFC §11).
	n.Anchor = ""
	switch n.Kind {
	case yaml.MappingNode:
		sortMapping(n)
		for i := 1; i < len(n.Content); i += 2 {
			canonicalizeNode(n.Content[i])
		}
		// Keys are scalars but recurse anyway to strip anchors defensively.
		for i := 0; i < len(n.Content); i += 2 {
			n.Content[i].Anchor = ""
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			canonicalizeNode(c)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!float" {
			if v, err := strconv.ParseFloat(n.Value, 64); err == nil {
				n.Value = formatFloat6(v)
				n.Style = 0
			}
		}
	case yaml.AliasNode:
		// Resolve an alias into its target so the output has no aliases.
		if n.Alias != nil {
			resolved := *n.Alias
			*n = resolved
			canonicalizeNode(n)
		}
	}
}

// sortMapping reorders a mapping node's key/value pairs by key, lexicographically
// over the raw key string. Sorting is what makes the form independent of Go map
// iteration order (INV-DETERMINISM).
func sortMapping(n *yaml.Node) {
	type pair struct{ k, v *yaml.Node }
	pairs := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs = append(pairs, pair{n.Content[i], n.Content[i+1]})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		return pairs[i].k.Value < pairs[j].k.Value
	})
	n.Content = n.Content[:0]
	for _, p := range pairs {
		n.Content = append(n.Content, p.k, p.v)
	}
}

// dropExcludedMetaFields removes meta.digest and meta.generated_at from the
// root mapping's meta block. They are excluded from the hashed canonical form
// (RFC §11): the digest cannot hash itself, and generated_at is a timestamp
// that must not affect identity.
func dropExcludedMetaFields(root *yaml.Node) {
	meta := mappingValue(root, "meta")
	if meta == nil {
		return
	}
	removeKey(meta, "digest")
	removeKey(meta, "generated_at")
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// removeKey deletes a key/value pair from a mapping node.
func removeKey(n *yaml.Node, key string) {
	if n.Kind != yaml.MappingNode {
		return
	}
	out := n.Content[:0]
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			continue
		}
		out = append(out, n.Content[i], n.Content[i+1])
	}
	n.Content = out
}

// formatFloat6 renders a float to exactly 6 significant figures (RFC §11). The
// 'g' verb caps significant digits and drops trailing zeros, so the result is a
// stable, minimal decimal representation.
func formatFloat6(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}
