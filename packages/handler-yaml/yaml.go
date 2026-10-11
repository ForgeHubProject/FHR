// Package main is a format-aware handler for YAML config files (.yaml, .yml).
// It produces a structural diff — key paths with added/removed/modified kinds,
// mirroring the .json handler design — instead of a line patch that drowns a
// one-value change in indentation and reflow noise.
//
// Parsing uses gopkg.in/yaml.v3 (pure Go, wasm-safe). Anchors and aliases are
// resolved during decoding, so a change made through an alias shows up —
// already resolved — at every path that references it. Multi-document streams
// are diffed per document with a doc[N] path prefix.
//
// v0 is diff-only. Comment preservation and 3-way semantic merge are planned
// follow-ups — issue #26.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// maxDepth caps recursion for pathological nesting; deeper subtrees are
	// compared as opaque leaves.
	maxDepth = 64
	// maxChanges caps the number of reported changes for pathological inputs.
	maxChanges = 2000
	// maxValueLen is the compact-rendering cap for before/after values.
	maxValueLen = 120
)

// Handler is the YAML format handler.
type Handler struct{}

// Match returns true for .yaml and .yml files.
func (h *Handler) Match(path string) bool {
	ext := filepath.Ext(path)
	return strings.EqualFold(ext, ".yaml") || strings.EqualFold(ext, ".yml")
}

// parseYAML decodes every document in a YAML stream into generic Go values.
// yaml.v3 resolves anchors/aliases during decode. An empty blob yields no
// documents (not an error) — the added/deleted-file case.
func parseYAML(blob Blob) ([]any, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(blob))
	var docs []any
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing YAML: %w", err)
		}
		docs = append(docs, v)
	}
	return docs, nil
}

// Diff produces a structural semantic diff of two YAML blobs. An empty blob on
// either side is the added/deleted-file case (everything added / removed).
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	baseDocs, err := parseYAML(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	headDocs, err := parseYAML(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	d := &differ{changes: []DiffChange{}} // non-nil so an empty diff marshals as [] not null

	nDocs := max(len(baseDocs), len(headDocs))
	multi := nDocs > 1
	for i := 0; i < nDocs; i++ {
		prefix := ""
		if multi {
			prefix = fmt.Sprintf("doc[%d]", i+1)
		}
		av, aok := docAt(baseDocs, i)
		bv, bok := docAt(headDocs, i)
		d.diffDoc(prefix, av, aok, bv, bok)
	}

	return StructuredDiff{Version: "1.0", Format: "yaml", Changes: d.changes}, nil
}

// Merge is not yet supported for YAML (v0 is diff-only). Comment-preserving
// 3-way merge is a planned follow-up (issue #26).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for yaml")
}

func docAt(docs []any, i int) (any, bool) {
	if i < 0 || i >= len(docs) {
		return nil, false
	}
	return docs[i], true
}

// differ accumulates changes with a global cap.
type differ struct {
	changes []DiffChange
}

func (d *differ) full() bool { return len(d.changes) >= maxChanges }

func (d *differ) add(c DiffChange) {
	if d.full() {
		return
	}
	d.changes = append(d.changes, c)
}

// diffDoc diffs one document pair. A document present on only one side is
// wholly added/removed; if it is a mapping, it expands one level so the diff
// reads as each top-level key added/removed.
func (d *differ) diffDoc(prefix string, a any, aok bool, b any, bok bool) {
	switch {
	case aok && bok:
		d.walk(prefix, a, b, 0)
	case !aok && bok:
		d.addWhole(prefix, b, Added)
	case aok && !bok:
		d.addWhole(prefix, a, Removed)
	}
}

// addWhole reports a value present on only one side. Root mappings expand one
// level (per top-level key); anything else is one change at the root path.
func (d *differ) addWhole(prefix string, v any, kind ChangeKind) {
	if m, ok := asMap(v); ok && len(m) > 0 {
		for _, k := range sortedKeys(m) {
			c := DiffChange{Path: joinKey(prefix, k), Kind: kind, Label: k}
			if kind == Added {
				c.After = render(m[k])
			} else {
				c.Before = render(m[k])
			}
			d.add(c)
		}
		return
	}
	c := DiffChange{Path: rootPath(prefix), Kind: kind}
	if kind == Added {
		c.After = render(v)
	} else {
		c.Before = render(v)
	}
	d.add(c)
}

// walk recursively diffs two present values at path.
func (d *differ) walk(path string, a, b any, depth int) {
	if d.full() {
		return
	}
	if depth > maxDepth {
		// Too deep — compare the remaining subtree as an opaque leaf.
		d.leaf(path, a, b)
		return
	}

	am, aIsMap := asMap(a)
	bm, bIsMap := asMap(b)
	if aIsMap && bIsMap {
		for _, k := range unionKeys(am, bm) {
			av, aok := am[k]
			bv, bok := bm[k]
			p := joinKey(path, k)
			switch {
			case aok && bok:
				d.walk(p, av, bv, depth+1)
			case !aok:
				d.add(DiffChange{Path: p, Kind: Added, Label: k, After: render(bv)})
			default:
				d.add(DiffChange{Path: p, Kind: Removed, Label: k, Before: render(av)})
			}
			if d.full() {
				return
			}
		}
		return
	}

	as, aIsSeq := asSeq(a)
	bs, bIsSeq := asSeq(b)
	if aIsSeq && bIsSeq {
		if len(as) != len(bs) {
			d.add(DiffChange{
				Path:   rootPath(path),
				Kind:   Modified,
				Label:  fmt.Sprintf("length %d → %d", len(as), len(bs)),
				Before: len(as),
				After:  len(bs),
			})
		}
		n := min(len(as), len(bs))
		for i := 0; i < n; i++ {
			d.walk(fmt.Sprintf("%s[%d]", path, i), as[i], bs[i], depth+1)
			if d.full() {
				return
			}
		}
		return
	}

	// Leaves — and mismatched shapes (map vs sequence vs scalar).
	d.leaf(path, a, b)
}

// leaf reports a modified change when two values are not equal under
// re-marshaling.
func (d *differ) leaf(path string, a, b any) {
	if equalYAML(a, b) {
		return
	}
	d.add(DiffChange{Path: rootPath(path), Kind: Modified, Before: render(a), After: render(b)})
}

// equalYAML compares two values by re-marshaled equality.
func equalYAML(a, b any) bool {
	ab, aerr := yaml.Marshal(a)
	bb, berr := yaml.Marshal(b)
	if aerr != nil || berr != nil {
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
	return bytes.Equal(ab, bb)
}

// asMap normalizes a YAML mapping to map[string]any. yaml.v3 decodes string
// keys to map[string]any and falls back to map[any]any otherwise; non-string
// keys are stringified.
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[keyString(k)] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func asSeq(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

func keyString(k any) string {
	if s, ok := k.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", k)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// joinKey appends a map key to a dotted path.
func joinKey(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// rootPath makes a displayable path for changes at a document root.
func rootPath(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}

// render produces a compact single-line rendering of a value, truncated to
// maxValueLen runes.
func render(v any) string {
	return truncate(compact(v, 0))
}

func compact(v any, depth int) string {
	if depth > 8 {
		return "…"
	}
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case map[string]any, map[any]any:
		m, _ := asMap(t)
		parts := make([]string, 0, len(m))
		for _, k := range sortedKeys(m) {
			parts = append(parts, k+": "+compact(m[k], depth+1))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, compact(e, depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", t)
	}
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxValueLen {
		return s
	}
	return string(r[:maxValueLen]) + "…"
}
