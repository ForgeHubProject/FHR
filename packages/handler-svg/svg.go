// Package main is a format-aware handler for .svg files. An SVG is an XML
// tree of elements (paths, shapes, groups) with attributes carrying geometry,
// transforms, and styles — so a text diff drowns a one-attribute tweak in
// serialization noise. This handler diffs the XML tree itself: elements added
// or removed, attributes added / removed / changed, and text content changes.
// Root-level width / height / viewBox changes fall out naturally as attribute
// changes on the svg element.
//
// v0 is the generic XML-tree diff from issue #19. Elements are aligned by
// their id attribute when present (key "#theId"), else by a structural path
// with a per-tag sibling index (e.g. svg/g[1]/path[0]). Shape-aware diffing
// (path d-data, transform and style deltas) is the planned follow-up.
//
// Security: parsing uses encoding/xml's token walk only. Go's encoding/xml
// does not expand external entities and this handler never fetches anything.
package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// Handler is the SVG format handler.
type Handler struct{}

// Match returns true for .svg files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".svg")
}

// maxChanges caps the total number of DiffChange nodes (including children) in
// one diff, so a pathological file cannot produce an unbounded payload.
const maxChanges = 1000

// element is a lightweight XML tree node: tag, attributes, trimmed text
// content, and child elements. Deliberately not an Unmarshal target — the tree
// is built by a token walk so any well-formed SVG round-trips losslessly
// enough to diff.
type element struct {
	tag      string
	attrs    map[string]string
	text     string
	children []*element
}

// qname flattens an xml.Name to a stable string. Namespaced attributes keep
// their resolved space as a prefix so both sides of a diff agree on the key.
func qname(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// parseSVG token-walks a blob into an element tree. An empty (or
// whitespace-only) blob yields a nil root — the added/deleted-file case, not
// an error. Malformed XML errors cleanly.
func parseSVG(blob Blob) (*element, error) {
	if len(bytes.TrimSpace(blob)) == 0 {
		return nil, nil
	}
	dec := xml.NewDecoder(bytes.NewReader(blob))
	var root *element
	var stack []*element
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing SVG XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := &element{tag: t.Name.Local, attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				el.attrs[qname(a.Name)] = a.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("parsing SVG XML: multiple root elements")
				}
				root = el
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, el)
			}
			stack = append(stack, el)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				cur := stack[len(stack)-1]
				cur.text += string(t)
			}
			// Comments, processing instructions, and directives carry no
			// semantic weight for a v0 tree diff — skipped.
		}
	}
	if root == nil {
		return nil, fmt.Errorf("parsing SVG XML: no root element")
	}
	trimText(root)
	return root, nil
}

func trimText(el *element) {
	el.text = strings.TrimSpace(el.text)
	for _, c := range el.children {
		trimText(c)
	}
}

// childKeys assigns each child a stable identity key: "#theId" when an id
// attribute is present, else "tag[i]" where i is the per-tag sibling index.
// A duplicate id (invalid SVG, but possible) falls back to the structural key
// so keys stay unique within one parent.
func childKeys(el *element) []string {
	keys := make([]string, len(el.children))
	tagCount := make(map[string]int)
	seen := make(map[string]bool)
	for i, c := range el.children {
		idx := tagCount[c.tag]
		tagCount[c.tag]++
		key := fmt.Sprintf("%s[%d]", c.tag, idx)
		if id := c.attrs["id"]; id != "" && !seen["#"+id] {
			key = "#" + id
		}
		keys[i] = key
		seen[key] = true
	}
	return keys
}

// elementLabel names an element for humans: "path #logo" for id-keyed
// elements, else the structural key ("path[0]").
func elementLabel(el *element, key string) string {
	if strings.HasPrefix(key, "#") {
		return el.tag + " " + key
	}
	return key
}

func sortedAttrNames(attrs map[string]string) []string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// summarize renders an element's opening tag (attributes sorted, values
// truncated) as the before/after value of an added or removed element.
func summarize(el *element) string {
	var b strings.Builder
	b.WriteString("<" + el.tag)
	for _, name := range sortedAttrNames(el.attrs) {
		fmt.Fprintf(&b, " %s=%q", name, truncate(el.attrs[name], 40))
	}
	b.WriteString(">")
	return truncate(b.String(), 200)
}

// differ tracks the change budget for one Diff call.
type differ struct {
	n int
}

// take consumes one unit of change budget, reporting whether the caller may
// emit another DiffChange node.
func (d *differ) take() bool {
	if d.n >= maxChanges {
		return false
	}
	d.n++
	return true
}

// addedChange emits an element (and, recursively, its subtree) as added.
func (d *differ) addedChange(el *element, path, key string) (DiffChange, bool) {
	if !d.take() {
		return DiffChange{}, false
	}
	c := DiffChange{Path: path, Kind: Added, Label: elementLabel(el, key), After: summarize(el)}
	keys := childKeys(el)
	for i, child := range el.children {
		if cc, ok := d.addedChange(child, path+"/"+keys[i], keys[i]); ok {
			c.Children = append(c.Children, cc)
		}
	}
	return c, true
}

// removedChange emits an element (and, recursively, its subtree) as removed.
func (d *differ) removedChange(el *element, path, key string) (DiffChange, bool) {
	if !d.take() {
		return DiffChange{}, false
	}
	c := DiffChange{Path: path, Kind: Removed, Label: elementLabel(el, key), Before: summarize(el)}
	keys := childKeys(el)
	for i, child := range el.children {
		if cc, ok := d.removedChange(child, path+"/"+keys[i], keys[i]); ok {
			c.Children = append(c.Children, cc)
		}
	}
	return c, true
}

// attrAndTextChanges compares one matched element pair's attributes (added /
// removed / changed, by name) and trimmed text content.
func (d *differ) attrAndTextChanges(a, b *element) []DiffChange {
	var kids []DiffChange
	names := make(map[string]bool, len(a.attrs)+len(b.attrs))
	for name := range a.attrs {
		names[name] = true
	}
	for name := range b.attrs {
		names[name] = true
	}
	union := make([]string, 0, len(names))
	for name := range names {
		union = append(union, name)
	}
	sort.Strings(union)
	for _, name := range union {
		av, aok := a.attrs[name]
		bv, bok := b.attrs[name]
		switch {
		case !aok && bok:
			if d.take() {
				kids = append(kids, DiffChange{Path: "@" + name, Kind: Added, Label: name, After: bv})
			}
		case aok && !bok:
			if d.take() {
				kids = append(kids, DiffChange{Path: "@" + name, Kind: Removed, Label: name, Before: av})
			}
		case av != bv:
			if d.take() {
				kids = append(kids, DiffChange{Path: "@" + name, Kind: Modified, Label: name, Before: av, After: bv})
			}
		}
	}
	if a.text != b.text {
		if d.take() {
			kids = append(kids, DiffChange{Path: "text", Kind: Modified, Label: "text", Before: a.text, After: b.text})
		}
	}
	return kids
}

// diffElement compares one matched element pair: its own attributes and text,
// then its children by identity key.
func (d *differ) diffElement(a, b *element, path, key string, out *[]DiffChange) {
	if kids := d.attrAndTextChanges(a, b); len(kids) > 0 && d.take() {
		*out = append(*out, DiffChange{Path: path, Kind: Modified, Label: elementLabel(b, key), Children: kids})
	}
	d.diffChildren(a, b, path, out)
}

// diffChildren matches the two sides' children by identity key — id-keyed
// elements pair up wherever they moved; keyless elements pair positionally per
// tag. Unmatched base children are removals, unmatched head children are
// additions.
func (d *differ) diffChildren(a, b *element, path string, out *[]DiffChange) {
	ak, bk := childKeys(a), childKeys(b)
	aIdx := make(map[string]int, len(ak))
	for i, k := range ak {
		aIdx[k] = i
	}
	bIdx := make(map[string]int, len(bk))
	for i, k := range bk {
		bIdx[k] = i
	}
	for i, k := range ak {
		if _, ok := bIdx[k]; !ok {
			if c, ok2 := d.removedChange(a.children[i], path+"/"+k, k); ok2 {
				*out = append(*out, c)
			}
		}
	}
	for i, k := range bk {
		if j, ok := aIdx[k]; ok {
			d.diffElement(a.children[j], b.children[i], path+"/"+k, k, out)
		} else if c, ok2 := d.addedChange(b.children[i], path+"/"+k, k); ok2 {
			*out = append(*out, c)
		}
	}
}

// Diff produces an XML-tree semantic diff of two SVG blobs. An empty blob on
// either side is the added/deleted-file case (the whole tree added / removed).
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	a, err := parseSVG(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := parseSVG(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	changes := []DiffChange{} // non-nil so an empty diff marshals as [] not null
	d := &differ{}
	switch {
	case a == nil && b == nil:
		// both empty — nothing to diff
	case a == nil:
		if c, ok := d.addedChange(b, b.tag, b.tag); ok {
			changes = append(changes, c)
		}
	case b == nil:
		if c, ok := d.removedChange(a, a.tag, a.tag); ok {
			changes = append(changes, c)
		}
	case a.tag != b.tag:
		if c, ok := d.removedChange(a, a.tag, a.tag); ok {
			changes = append(changes, c)
		}
		if c, ok := d.addedChange(b, b.tag, b.tag); ok {
			changes = append(changes, c)
		}
	default:
		d.diffElement(a, b, a.tag, a.tag, &changes)
	}

	return StructuredDiff{Version: "1.0", Format: "svg", Changes: changes}, nil
}

// Merge is not yet supported for SVG (v0 is diff-only). An element-level 3-way
// merge is a planned follow-up (issue #19).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for svg")
}
