// Package main is a format-aware handler for .toml files. It produces a
// semantic diff at table / key / value granularity rather than a line patch,
// mirroring the .json handler design: tables nest as maps, arrays of tables
// as arrays of maps, and the diff is a structural walk with dotted paths
// (servers.alpha.port), so a single changed key reads as exactly that.
//
// v0 is diff-only and compares arrays positionally. Comment/format
// preservation and a key-level 3-way merge are the planned follow-ups — see
// issue #27.
package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// maxDepth caps recursion into nested tables/arrays; below it, subtrees
	// are compared wholesale by re-marshaled equality.
	maxDepth = 64
	// maxChanges caps the total number of emitted changes so a pathological
	// document can't produce an unbounded diff.
	maxChanges = 2000
	// maxValueLen is the rune budget for a rendered before/after value.
	maxValueLen = 120
)

// Handler is the TOML format handler.
type Handler struct{}

// Match returns true for .toml files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".toml")
}

// parseTOML decodes a blob into a generic document map. An empty blob yields
// an empty map (not an error) — the added/deleted file case.
func parseTOML(blob Blob) (map[string]any, error) {
	doc := map[string]any{}
	if len(blob) == 0 {
		return doc, nil
	}
	if err := toml.Unmarshal(blob, &doc); err != nil {
		return nil, fmt.Errorf("parsing TOML: %w", err)
	}
	return doc, nil
}

// Diff produces a table/key/value structural diff of two TOML blobs. An empty
// blob on either side is the added/deleted-file case (everything added /
// removed).
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	baseDoc, err := parseTOML(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	headDoc, err := parseTOML(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	// Non-nil so an empty diff marshals as [] (never null).
	d := &differ{changes: []DiffChange{}}
	d.diffTables("", baseDoc, headDoc, 0)

	return StructuredDiff{Version: "1.0", Format: "toml", Changes: d.changes}, nil
}

// differ accumulates changes from the structural walk, enforcing maxChanges.
type differ struct {
	changes []DiffChange
	full    bool
}

func (d *differ) emit(c DiffChange) {
	if len(d.changes) >= maxChanges {
		d.full = true
		return
	}
	d.changes = append(d.changes, c)
}

// diffTables walks two tables (maps), recursing into nested tables and
// arrays. Keys are visited in sorted order so output is deterministic.
func (d *differ) diffTables(path string, a, b map[string]any, depth int) {
	if d.full {
		return
	}
	for _, k := range sortedKeys(a, b) {
		av, aok := a[k]
		bv, bok := b[k]
		p := joinPath(path, k)
		switch {
		case !aok && bok:
			d.emit(DiffChange{Path: p, Kind: Added, Label: k, After: compactValue(bv)})
		case aok && !bok:
			d.emit(DiffChange{Path: p, Kind: Removed, Label: k, Before: compactValue(av)})
		default:
			d.diffValues(p, k, av, bv, depth+1)
		}
	}
}

// diffValues compares two values at the same path: tables recurse, arrays
// compare positionally, and leaves compare by re-marshaled equality. At
// maxDepth, whole subtrees fall back to leaf comparison.
func (d *differ) diffValues(path, label string, a, b any, depth int) {
	if d.full {
		return
	}
	if depth >= maxDepth {
		d.diffLeaves(path, label, a, b)
		return
	}
	am, aIsTable := asTable(a)
	bm, bIsTable := asTable(b)
	if aIsTable && bIsTable {
		d.diffTables(path, am, bm, depth)
		return
	}
	as, aIsArray := asArray(a)
	bs, bIsArray := asArray(b)
	if aIsArray && bIsArray {
		d.diffArrays(path, as, bs, depth)
		return
	}
	d.diffLeaves(path, label, a, b)
}

// diffArrays compares arrays positionally: index i vs index i, extra tail
// entries are added/removed. Covers both plain arrays and arrays of tables.
func (d *differ) diffArrays(path string, a, b []any, depth int) {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("%s.%d", path, i)
		label := fmt.Sprintf("[%d]", i)
		switch {
		case i >= len(a):
			d.emit(DiffChange{Path: p, Kind: Added, Label: label, After: compactValue(b[i])})
		case i >= len(b):
			d.emit(DiffChange{Path: p, Kind: Removed, Label: label, Before: compactValue(a[i])})
		default:
			d.diffValues(p, label, a[i], b[i], depth+1)
		}
	}
}

// diffLeaves compares two leaf values by re-marshaled equality and emits a
// modified change when they differ.
func (d *differ) diffLeaves(path, label string, a, b any) {
	if marshalValue(a) == marshalValue(b) {
		return
	}
	d.emit(DiffChange{Path: path, Kind: Modified, Label: label, Before: compactValue(a), After: compactValue(b)})
}

// asTable normalizes a decoded value to a table (map). BurntSushi decodes
// tables and inline tables as map[string]any.
func asTable(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// asArray normalizes a decoded value to []any. BurntSushi decodes plain
// arrays as []any but arrays of tables as []map[string]any.
func asArray(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []map[string]any:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out, true
	default:
		return nil, false
	}
}

func sortedKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for k := range b {
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

// marshalValue renders a value canonically for equality checks. JSON is
// deterministic here (map keys sort), and every TOML value type marshals.
func marshalValue(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// compactValue renders a value for display, truncated to maxValueLen runes.
func compactValue(v any) string {
	s := marshalValue(v)
	runes := []rune(s)
	if len(runes) <= maxValueLen {
		return s
	}
	return string(runes[:maxValueLen]) + "…"
}

// Merge is not yet supported for TOML (v0 is diff-only). Decoding to maps
// drops comments and formatting, which is fine for diff but not for writing a
// merged file back out — comment/format-preserving merge is the planned
// follow-up (issue #27).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for toml")
}
