// Package main is a format-aware handler for Wavefront OBJ (.obj) files. OBJ
// is line-based text with named objects/groups, so the diff is structural
// rather than a line patch: per-group face-count and material changes, group
// add/remove, global vertex/normal/uv count changes, and material-library
// (mtllib) changes.
//
// v0 matches only .obj. The .obj ↔ .mtl pairing model (resolving the sibling
// library to surface material *property* changes) is the main open design
// question and is deferred — see issue #15.
package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// defaultGroup is the group faces land in when a file declares no o/g line.
const defaultGroup = "(default)"

// Handler is the Wavefront OBJ format handler.
type Handler struct{}

// Match returns true for .obj files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".obj")
}

// objGroup is one named object/group (o or g statement) and the geometry
// assigned to it.
type objGroup struct {
	Name      string
	Faces     int
	Materials map[string]bool // usemtl names attached to this group's faces
}

// objModel is the parsed structural summary of an OBJ file.
type objModel struct {
	Vertices int      // v
	Normals  int      // vn
	UVs      int      // vt
	MtlLibs  []string // mtllib references, in order of first appearance
	Groups   map[string]*objGroup
	Order    []string // group names in order of first appearance
}

func (m *objModel) group(name string) *objGroup {
	if g, ok := m.Groups[name]; ok {
		return g
	}
	g := &objGroup{Name: name, Materials: map[string]bool{}}
	m.Groups[name] = g
	m.Order = append(m.Order, name)
	return g
}

// parseOBJ reads a blob into a structural model. An empty blob yields an empty
// model (not an error) — the added/deleted-file case. Unknown keywords are
// ignored for forward compatibility; malformed face statements are an error.
func parseOBJ(blob Blob) (*objModel, error) {
	m := &objModel{Groups: map[string]*objGroup{}}
	if len(blob) == 0 {
		return m, nil
	}
	if !utf8.Valid(blob) || strings.ContainsRune(string(blob), 0) {
		return nil, fmt.Errorf("parsing OBJ: not valid text")
	}

	current := defaultGroup // group that receives faces until the next o/g
	material := ""          // active usemtl, recorded per face

	for i, line := range strings.Split(string(blob), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "v":
			m.Vertices++
		case "vn":
			m.Normals++
		case "vt":
			m.UVs++
		case "o", "g":
			name := strings.Join(fields[1:], " ")
			if name == "" {
				name = defaultGroup
			}
			current = name
			m.group(name) // a declared group exists even before its first face
		case "usemtl":
			material = strings.Join(fields[1:], " ")
		case "mtllib":
			for _, lib := range fields[1:] {
				if !contains(m.MtlLibs, lib) {
					m.MtlLibs = append(m.MtlLibs, lib)
				}
			}
		case "f":
			if err := checkFace(fields[1:]); err != nil {
				return nil, fmt.Errorf("parsing OBJ: line %d: %w", i+1, err)
			}
			g := m.group(current)
			g.Faces++
			if material != "" {
				g.Materials[material] = true
			}
		}
	}
	return m, nil
}

// checkFace validates a face statement's vertex references (forms: v, v/vt,
// v/vt/vn, v//vn — indices are non-zero integers, possibly negative).
func checkFace(refs []string) error {
	if len(refs) < 3 {
		return fmt.Errorf("face has %d vertex references, need at least 3", len(refs))
	}
	for _, ref := range refs {
		for j, part := range strings.Split(ref, "/") {
			if part == "" {
				if j == 0 {
					return fmt.Errorf("invalid face vertex reference %q", ref)
				}
				continue // v//vn form
			}
			n, err := strconv.Atoi(part)
			if err != nil || n == 0 {
				return fmt.Errorf("invalid face vertex reference %q", ref)
			}
		}
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (g *objGroup) materialList() []string {
	out := make([]string, 0, len(g.Materials))
	for name := range g.Materials {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// summary describes a whole group for the added/removed cases.
func (g *objGroup) summary() string {
	s := fmt.Sprintf("%d faces", g.Faces)
	if mats := g.materialList(); len(mats) > 0 {
		s += ", materials: " + strings.Join(mats, ", ")
	}
	return s
}

// countKind picks the change kind for a scalar count so that the empty-file
// cases (added/deleted file) read as pure additions/removals.
func countKind(before, after int) ChangeKind {
	switch {
	case before == 0:
		return Added
	case after == 0:
		return Removed
	default:
		return Modified
	}
}

// Diff produces a structural semantic diff of two OBJ blobs: global counts,
// material-library references, and per-group face/material changes. An empty
// blob on either side is the added/deleted-file case.
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	a, err := parseOBJ(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := parseOBJ(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	// Non-nil so an empty diff marshals as [] (never null).
	changes := []DiffChange{}
	changes = append(changes, diffCounts(a, b)...)
	changes = append(changes, diffMtlLibs(a.MtlLibs, b.MtlLibs)...)
	changes = append(changes, diffGroups(a, b)...)

	return StructuredDiff{Version: "1.0", Format: "obj", Changes: changes}, nil
}

// diffCounts compares the global vertex/normal/uv counts.
func diffCounts(a, b *objModel) []DiffChange {
	var ch []DiffChange
	counts := []struct {
		path, label   string
		before, after int
	}{
		{"counts/vertices", "vertex count", a.Vertices, b.Vertices},
		{"counts/normals", "normal count", a.Normals, b.Normals},
		{"counts/uvs", "uv count", a.UVs, b.UVs},
	}
	for _, c := range counts {
		if c.before == c.after {
			continue
		}
		ch = append(ch, DiffChange{
			Path:   c.path,
			Kind:   countKind(c.before, c.after),
			Label:  c.label,
			Before: c.before,
			After:  c.after,
		})
	}
	return ch
}

// diffMtlLibs compares the mtllib reference lists.
func diffMtlLibs(a, b []string) []DiffChange {
	as, bs := strings.Join(a, ", "), strings.Join(b, ", ")
	if as == bs {
		return nil
	}
	c := DiffChange{Path: "materials/libraries", Label: "material libraries"}
	switch {
	case len(a) == 0:
		c.Kind = Added
		c.After = bs
	case len(b) == 0:
		c.Kind = Removed
		c.Before = as
	default:
		c.Kind = Modified
		c.Before = as
		c.After = bs
	}
	return []DiffChange{c}
}

// diffGroups compares objects/groups by name: added, removed, or modified
// (face count, materials used). Base order first, then head-only names in
// head order.
func diffGroups(a, b *objModel) []DiffChange {
	var ch []DiffChange
	for _, name := range a.Order {
		ag := a.Groups[name]
		bg, ok := b.Groups[name]
		path := "objects/" + name
		label := fmt.Sprintf("object %q", name)
		if !ok {
			ch = append(ch, DiffChange{Path: path, Kind: Removed, Label: label, Before: ag.summary()})
			continue
		}
		var kids []DiffChange
		if ag.Faces != bg.Faces {
			kids = append(kids, DiffChange{Path: "faces", Kind: Modified, Label: "face count", Before: ag.Faces, After: bg.Faces})
		}
		am, bm := strings.Join(ag.materialList(), ", "), strings.Join(bg.materialList(), ", ")
		if am != bm {
			k := DiffChange{Path: "materials", Label: "materials"}
			switch {
			case am == "":
				k.Kind = Added
				k.After = bm
			case bm == "":
				k.Kind = Removed
				k.Before = am
			default:
				k.Kind = Modified
				k.Before = am
				k.After = bm
			}
			kids = append(kids, k)
		}
		if len(kids) > 0 {
			ch = append(ch, DiffChange{Path: path, Kind: Modified, Label: label, Children: kids})
		}
	}
	for _, name := range b.Order {
		if _, ok := a.Groups[name]; ok {
			continue
		}
		bg := b.Groups[name]
		ch = append(ch, DiffChange{
			Path:  "objects/" + name,
			Kind:  Added,
			Label: fmt.Sprintf("object %q", name),
			After: bg.summary(),
		})
	}
	return ch
}

// Merge is not yet supported for OBJ (v0 is diff-only). A group-level 3-way
// merge is a planned follow-up (issue #15).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for obj")
}
