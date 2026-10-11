// Package main is a format-aware handler for .json files. It produces a
// structural diff at key-path granularity rather than a line patch, so a deep
// config edit reads as `servers[2].port: 80 → 8080` instead of a wall of
// reflowed lines.
//
// Objects diff by the union of their keys — added / removed / changed keys,
// recursing into nested values. Arrays diff positionally (index N vs index N):
// unequal lengths report a length change plus per-index compares up to the
// shorter length, so a single inserted element makes later indexes read as
// changed. That cascade is the documented v0 tradeoff; key-based element
// alignment hints are the planned follow-up — see issue #25.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// maxDepth bounds recursion so a pathologically nested document can't
	// blow the stack (relevant for the wasm build too).
	maxDepth = 64
	// maxCollected caps the number of collected changes; past it the diff
	// stops cleanly and the final change notes the truncation.
	maxCollected = 2000
	// maxValueChars bounds how much of a value is rendered in before/after.
	maxValueChars = 120
)

// Handler is the JSON format handler.
type Handler struct{}

// Match returns true for .json files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
}

// parseJSON decodes a blob into a generic value. The second return reports
// whether a document was present at all: an empty (or whitespace-only) blob is
// the added/deleted-file case, distinct from the valid document `null`.
// Numbers decode as json.Number so they re-render exactly as written.
func parseJSON(blob Blob) (any, bool, error) {
	if len(bytes.TrimSpace(blob)) == 0 {
		return nil, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(blob))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false, fmt.Errorf("parsing JSON: %w", err)
	}
	if dec.More() {
		return nil, false, fmt.Errorf("parsing JSON: unexpected trailing data after top-level value")
	}
	return v, true, nil
}

// Diff produces a key-path structural diff of two JSON blobs. An empty blob on
// either side is the added/deleted-file case (everything added / removed).
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	a, aok, err := parseJSON(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, bok, err := parseJSON(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	// Non-nil so an empty diff marshals as [] (never null).
	d := &differ{changes: []DiffChange{}}
	switch {
	case !aok && !bok:
		// Both absent — nothing to diff.
	case !aok:
		d.recordAll(b, Added)
	case !bok:
		d.recordAll(a, Removed)
	default:
		d.walk("", a, b, 0)
	}

	return StructuredDiff{Version: "1.0", Format: "json", Changes: d.changes}, nil
}

// Merge is not yet supported for JSON (v0 is diff-only). A key-path-level
// 3-way merge is a planned follow-up (issue #25).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for json")
}

// ── structural walk ────────────────────────────────────────────────────────

type differ struct {
	changes   []DiffChange
	truncated bool
}

// record appends a change, converting it into a single truncation marker once
// the cap is hit. Returns false once collection has stopped.
func (d *differ) record(c DiffChange) bool {
	if d.truncated {
		return false
	}
	if len(d.changes) >= maxCollected {
		d.truncated = true
		d.changes = append(d.changes, DiffChange{
			Path:  "$",
			Kind:  Modified,
			Label: fmt.Sprintf("diff truncated after %d changes", maxCollected),
		})
		return false
	}
	d.changes = append(d.changes, c)
	return true
}

// recordAll reports a whole document as added or removed. For an object each
// top-level key becomes its own change; anything else is one document-level
// change — enough granularity without exploding huge arrays.
func (d *differ) recordAll(v any, kind ChangeKind) {
	if obj, ok := v.(map[string]any); ok && len(obj) > 0 {
		for _, k := range sortedKeys(obj) {
			c := DiffChange{Path: k, Kind: kind, Label: k}
			d.setValue(&c, kind, obj[k])
			if !d.record(c) {
				return
			}
		}
		return
	}
	c := DiffChange{Path: "$", Kind: kind, Label: "document"}
	d.setValue(&c, kind, v)
	d.record(c)
}

func (d *differ) setValue(c *DiffChange, kind ChangeKind, v any) {
	if kind == Added {
		c.After = compact(v)
	} else {
		c.Before = compact(v)
	}
}

// walk compares two values at the same key path, recursing into objects and
// arrays and recording leaf-level changes.
func (d *differ) walk(path string, a, b any, depth int) {
	if d.truncated {
		return
	}
	if depth >= maxDepth {
		// Too deep to keep recursing — compare the subtrees wholesale.
		if !jsonEqual(a, b) {
			d.record(DiffChange{
				Path: pathOrRoot(path), Kind: Modified,
				Label:  "beyond max depth",
				Before: compact(a), After: compact(b),
			})
		}
		return
	}

	ao, aIsObj := a.(map[string]any)
	bo, bIsObj := b.(map[string]any)
	aa, aIsArr := a.([]any)
	ba, bIsArr := b.([]any)

	switch {
	case aIsObj && bIsObj:
		d.walkObject(path, ao, bo, depth)
	case aIsArr && bIsArr:
		d.walkArray(path, aa, ba, depth)
	case typeName(a) != typeName(b):
		d.record(DiffChange{
			Path: pathOrRoot(path), Kind: Modified,
			Label:  fmt.Sprintf("type: %s → %s", typeName(a), typeName(b)),
			Before: compact(a), After: compact(b),
		})
	default:
		// Same-type scalars — compare by re-marshaled equality.
		if !jsonEqual(a, b) {
			d.record(DiffChange{
				Path: pathOrRoot(path), Kind: Modified,
				Before: compact(a), After: compact(b),
			})
		}
	}
}

// walkObject diffs two objects over the union of their keys, in sorted order
// so output is deterministic.
func (d *differ) walkObject(path string, a, b map[string]any, depth int) {
	seen := make(map[string]struct{}, len(a)+len(b))
	ordered := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if _, dup := seen[k]; !dup {
				seen[k] = struct{}{}
				ordered = append(ordered, k)
			}
		}
	}
	sort.Strings(ordered)

	for _, k := range ordered {
		if d.truncated {
			return
		}
		kp := joinKey(path, k)
		av, aok := a[k]
		bv, bok := b[k]
		switch {
		case !aok:
			d.record(DiffChange{Path: kp, Kind: Added, Label: k, After: compact(bv)})
		case !bok:
			d.record(DiffChange{Path: kp, Kind: Removed, Label: k, Before: compact(av)})
		default:
			d.walk(kp, av, bv, depth+1)
		}
	}
}

// walkArray diffs two arrays positionally. Unequal lengths report an explicit
// length change plus per-index compares up to the shorter length; trailing
// elements are covered by the length change (positional v0 semantics).
func (d *differ) walkArray(path string, a, b []any, depth int) {
	if len(a) != len(b) {
		if !d.record(DiffChange{
			Path: pathOrRoot(path), Kind: Modified,
			Label:  fmt.Sprintf("length %d → %d", len(a), len(b)),
			Before: fmt.Sprintf("%d items", len(a)),
			After:  fmt.Sprintf("%d items", len(b)),
		}) {
			return
		}
	}
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if d.truncated {
			return
		}
		d.walk(joinIndex(path, i), a[i], b[i], depth+1)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// sortedKeys returns an object's keys in sorted order for deterministic output.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// joinKey extends a key path with an object key: "" + "port" → "port",
// "server" + "port" → "server.port".
func joinKey(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// joinIndex extends a key path with an array index: "servers" + 2 → "servers[2]".
func joinIndex(path string, i int) string {
	return fmt.Sprintf("%s[%d]", path, i)
}

// pathOrRoot names the document root "$" when a change lands on an empty path.
func pathOrRoot(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

// typeName reports the JSON type of a decoded value.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// jsonEqual compares two decoded values by re-marshaled equality. Marshal sorts
// object keys, so key order never causes a false difference.
func jsonEqual(a, b any) bool {
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)
	if aerr != nil || berr != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// compact renders a value as compact JSON, truncated to maxValueChars with an
// ellipsis so huge strings or subtrees don't flood the diff.
func compact(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(data)
	if len(s) > maxValueChars {
		// Truncate on a rune boundary so we never split a UTF-8 sequence.
		runes := []rune(s)
		if len(runes) > maxValueChars {
			s = string(runes[:maxValueChars]) + "…"
		}
	}
	return s
}
