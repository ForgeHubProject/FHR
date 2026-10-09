// Command forge-handler-obj is the Wavefront OBJ handler.
//
// It does not diff OBJ text. It converts each side to a glTF document
// (convert.go) and diffs those with the 3D family's scene engine
// (packages/go/scene) — the same identity matching, geometry signatures and
// change paths as gltf-scene — then adds what glTF has no place for: the
// mtllib references and any statements it does not interpret. The same
// document, given a viewable surface and encoded as GLB, is the preview a
// viewer draws, so a change path always names a node that exists in it.
//
// Merge works on OBJ itself, not on the converted glTF — a merged glTF could
// only be written back as triangles — per object/group, and writes a fresh
// file with every index recomputed (merge.go, write.go).
package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Handler is the Wavefront OBJ format handler.
type Handler struct{}

// Match returns true for .obj files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".obj")
}

// Diff produces the semantic diff of two OBJ blobs. An empty blob on either
// side is the added/deleted-file case and diffs as all-added/all-removed.
func (h *Handler) Diff(base, head fhr.Blob) (fhr.StructuredDiff, error) {
	a, err := parseOBJ(base)
	if err != nil {
		return fhr.StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := parseOBJ(head)
	if err != nil {
		return fhr.StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	changes := collapseGroupTwins(scene.DiffDocuments(toGLTF(a), toGLTF(b)))
	changes = addSmoothing(changes, a, b)
	relabel(changes)
	changes = append(changes, diffMtlLibs(a.mtlLibs, b.mtlLibs)...)
	changes = append(changes, diffUninterpreted(a, b)...)
	return fhr.StructuredDiff{Version: "1.0", Format: "obj", Changes: changes}, nil
}

// PreviewMediaType is the GLB the preview produces.
func (h *Handler) PreviewMediaType() string { return fhr.MediaTypeGLB }

// Preview converts an OBJ blob to the GLB the diff was computed over, dressed
// with a viewable surface (dressForPreview).
func (h *Handler) Preview(blob fhr.Blob) (fhr.Blob, error) {
	f, err := parseOBJ(blob)
	if err != nil {
		return nil, err
	}
	doc := toGLTF(f)
	dressForPreview(doc)
	return encodeGLB(doc)
}

// topLabels renames the engine's top-level groups into OBJ vocabulary. Only
// labels change; paths stay the engine's, because they are what the preview's
// node names and the gltf viewer are keyed on.
var topLabels = map[string]string{
	"nodes":  "objects & groups",
	"meshes": "geometry",
}

func relabel(changes []fhr.DiffChange) {
	for i := range changes {
		if l, ok := topLabels[changes[i].Path]; ok {
			changes[i].Label = l
		}
	}
}

// collapseGroupTwins drops the mesh half of a group that was added, removed or
// renamed as a whole. glTF models an OBJ group as a node plus a mesh of its
// own, and the engine reports each — so without this a reviewer reads
// "Drawer added" twice. OBJ has only the group. The node row stays (it carries
// the parent and the mesh reference); a mesh row stays whenever it says
// something the node row cannot, which is any row with children: geometry and
// material changes.
func collapseGroupTwins(changes []fhr.DiffChange) []fhr.DiffChange {
	var nodes, meshes *fhr.DiffChange
	for i := range changes {
		switch changes[i].Path {
		case "nodes":
			nodes = &changes[i]
		case "meshes":
			meshes = &changes[i]
		}
	}
	if nodes == nil || meshes == nil {
		return changes
	}

	// The mesh each wholly added/removed node references, by its diff key —
	// the `mesh` child row's value — and each renamed node's rename.
	twins := map[string]fhr.ChangeKind{}
	renames := map[[2]any]bool{}
	for _, n := range nodes.Children {
		switch n.Kind {
		case fhr.Added, fhr.Removed:
			for _, c := range n.Children {
				if c.Path != n.Path+"/mesh" {
					continue
				}
				key := c.After
				if n.Kind == fhr.Removed {
					key = c.Before
				}
				if k, ok := key.(string); ok {
					twins["meshes/"+escapeSegment(k)] = n.Kind
				}
			}
		case fhr.Renamed:
			renames[[2]any{n.Before, n.After}] = true
		}
	}

	kept := meshes.Children[:0]
	for _, m := range meshes.Children {
		twin := len(m.Children) == 0 &&
			(twins[m.Path] == m.Kind && m.Kind != "" || m.Kind == fhr.Renamed && renames[[2]any{m.Before, m.After}])
		if !twin {
			kept = append(kept, m)
		}
	}
	meshes.Children = kept
	if len(kept) > 0 {
		return changes
	}
	out := changes[:0]
	for _, c := range changes {
		if c.Path != "meshes" {
			out = append(out, c)
		}
	}
	return out
}

// addSmoothing reports smoothing-group changes per object/group. glTF has no
// smoothing groups, so the engine never sees them; they are diffed here and
// hung under the engine's row for that node (`nodes/Top/smoothing`), keyed by
// the same node keys its paths use. A node that only exists on one side is
// already reported whole, so only nodes present on both sides are compared.
func addSmoothing(changes []fhr.DiffChange, a, b *objFile) []fhr.DiffChange {
	aNodes, aKeys := flatten(a)
	bNodes, bKeys := flatten(b)
	aByKey := make(map[string]*objNode, len(aNodes))
	for i, n := range aNodes {
		aByKey[aKeys[i]] = n
	}

	var rows []fhr.DiffChange
	for i, bn := range bNodes {
		an, ok := aByKey[bKeys[i]]
		if !ok || smoothingSeq(an) == smoothingSeq(bn) {
			continue
		}
		path := "nodes/" + escapeSegment(bKeys[i])
		row := sideChange(path+"/smoothing", "smoothing", smoothingSummary(an), smoothingSummary(bn))
		if row.Before == row.After {
			// Same groups and counts, on different faces.
			row.After = row.After.(string) + " (on other faces)"
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return changes
	}

	nodesAt := -1
	for i := range changes {
		if changes[i].Path == "nodes" {
			nodesAt = i
		}
	}
	if nodesAt < 0 {
		changes = append([]fhr.DiffChange{{Path: "nodes", Kind: fhr.Modified, Label: "nodes"}}, changes...)
		nodesAt = 0
	}
	nodes := &changes[nodesAt]
	for _, row := range rows {
		parent := strings.TrimSuffix(row.Path, "/smoothing")
		found := false
		for i := range nodes.Children {
			if nodes.Children[i].Path == parent {
				nodes.Children[i].Children = append(nodes.Children[i].Children, row)
				found = true
				break
			}
		}
		if !found {
			nodes.Children = append(nodes.Children, fhr.DiffChange{
				Path: parent, Kind: fhr.Modified, Label: unescapeSegment(strings.TrimPrefix(parent, "nodes/")),
				Children: []fhr.DiffChange{row},
			})
		}
	}
	return changes
}

// smoothingSeq is a node's per-element smoothing, in element order: the
// state a change is judged on.
func smoothingSeq(n *objNode) string {
	var b strings.Builder
	for _, e := range n.elems {
		b.WriteString(e.smooth)
		b.WriteByte(0)
	}
	return b.String()
}

// smoothingSummary says what a node's faces are smoothed with, e.g.
// "1 (6 faces)", "1 (4 faces), off (2 faces)"; "" when nothing is smoothed.
func smoothingSummary(n *objNode) string {
	var order []string
	count := map[string]int{}
	for _, e := range n.elems {
		if count[e.smooth] == 0 {
			order = append(order, e.smooth)
		}
		count[e.smooth]++
	}
	if len(order) == 0 || len(order) == 1 && order[0] == "" {
		return ""
	}
	parts := make([]string, len(order))
	for i, g := range order {
		name := g
		if g == "" {
			name = "off"
		}
		unit := "faces"
		if count[g] == 1 {
			unit = "face"
		}
		parts[i] = fmt.Sprintf("%s (%d %s)", name, count[g], unit)
	}
	return strings.Join(parts, ", ")
}

// flatten lists a file's nodes in document order — the order toGLTF writes
// them — with the keys the engine gives them: the name, and `name#1`, `#2`, …
// for repeats (packages/go/scene uniqueKeys).
func flatten(f *objFile) ([]*objNode, []string) {
	var nodes []*objNode
	var walk func(n *objNode)
	walk = func(n *objNode) {
		nodes = append(nodes, n)
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, r := range f.roots {
		walk(r)
	}
	keys := make([]string, len(nodes))
	taken := make(map[string]bool, len(nodes))
	for i, n := range nodes {
		key := n.name
		for dup := 1; taken[key]; dup++ {
			key = fmt.Sprintf("%s#%d", n.name, dup)
		}
		taken[key] = true
		keys[i] = key
	}
	return nodes, keys
}

func unescapeSegment(s string) string {
	return strings.NewReplacer("%2F", "/", "%25", "%").Replace(s)
}

// escapeSegment is the engine's path-segment escaping (SPEC.md §7 change
// paths): "%" and "/" are the two characters that would make a path ambiguous.
func escapeSegment(s string) string {
	return strings.NewReplacer("%", "%25", "/", "%2F").Replace(s)
}

// diffMtlLibs reports a change to the mtllib references. glTF has no place for
// them, so they are diffed here, beside the engine's output.
func diffMtlLibs(a, b []string) []fhr.DiffChange {
	as, bs := strings.Join(a, ", "), strings.Join(b, ", ")
	if as == bs {
		return nil
	}
	return []fhr.DiffChange{sideChange("mtllib", "material libraries", as, bs)}
}

// diffUninterpreted is the honesty row. Statements outside the interpreted set
// (smoothing groups, free-form curves and surfaces, …) are not diffed
// semantically, but if they changed the diff says so instead of reporting a
// changed file as unchanged.
func diffUninterpreted(a, b *objFile) []fhr.DiffChange {
	if a.otherHash == b.otherHash && a.otherSummary() == b.otherSummary() {
		return nil
	}
	c := sideChange("other", "statements not diffed semantically", a.otherSummary(), b.otherSummary())
	if c.Kind == fhr.Modified && a.otherSummary() == b.otherSummary() {
		// Same keywords and counts, different content.
		c.Before, c.After = a.otherSummary(), b.otherSummary()+" (content changed)"
	}
	return []fhr.DiffChange{c}
}

// sideChange builds a before/after row whose kind follows which sides exist.
func sideChange(path, label, before, after string) fhr.DiffChange {
	c := fhr.DiffChange{Path: path, Label: label}
	switch {
	case before == "":
		c.Kind, c.After = fhr.Added, after
	case after == "":
		c.Kind, c.Before = fhr.Removed, before
	default:
		c.Kind, c.Before, c.After = fhr.Modified, before, after
	}
	return c
}
