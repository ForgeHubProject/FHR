package scene

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// ── fixtures: scenes with real geometry, accessors and buffers ───────────────

// part is a node drawing one triangle at x = at, coloured by a named material.
type part struct {
	name, material string
	color          [4]float64
	at             float64
	move           [3]float64 // the node's translation
	children       []part
}

func (p part) object() *Object {
	c := p.color
	o := &Object{Name: p.name, Prims: []*FlatPrim{{
		Kind: KindTriangles, MaterialIndex: -1, Material: p.material, BaseColor: &c,
		Positions: [][3]float64{{p.at, 0, 0}, {p.at + 1, 0, 0}, {p.at, 1, 0}}, Indices: []uint32{0, 1, 2},
	}}}
	if p.move != [3]float64{} {
		m := [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, p.move[0], p.move[1], p.move[2], 1}
		o.Matrix = &m
	}
	for _, ch := range p.children {
		o.Children = append(o.Children, ch.object())
	}
	return o
}

func glbOf(t *testing.T, parts ...part) []byte {
	t.Helper()
	var roots []*Object
	for _, p := range parts {
		roots = append(roots, p.object())
	}
	doc, err := (&Model{Roots: roots}).Document()
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeBlob(doc, true)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var (
	grey  = [4]float64{0.5, 0.5, 0.5, 1}
	green = [4]float64{0, 1, 0, 1}
	brass = [4]float64{1, 0.8, 0, 1}
)

// drawn is what a merged blob draws, node by node: the first vertex in world
// space and the material colour — enough to tell whose geometry and whose
// material each node ended up with.
type drawn struct {
	first [3]float64
	color [4]float64
}

func drawnOf(t *testing.T, blob []byte) map[string]drawn {
	t.Helper()
	validDoc(t, blob)
	roots, err := Flatten(blob)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]drawn{}
	Walk(roots, func(n *FlatNode, _ int) {
		for _, p := range n.Prims {
			d := drawn{first: p.Positions[0]}
			if p.BaseColor != nil {
				d.color = *p.BaseColor
			}
			out[n.Name] = d
		}
	})
	return out
}

// validDoc fails the test when any index points outside its array — the defect
// the assembler exists to rule out.
func validDoc(t *testing.T, blob []byte) {
	t.Helper()
	doc, err := parseDoc(blob)
	if err != nil {
		t.Fatal(err)
	}
	in := func(what string, i, n int) {
		if i < 0 || i >= n {
			t.Fatalf("%s index %d out of range (%d)", what, i, n)
		}
	}
	parents := map[int]int{}
	for _, n := range doc.Nodes {
		if n.Mesh != nil {
			in("mesh", *n.Mesh, len(doc.Meshes))
		}
		for _, c := range n.Children {
			in("child", c, len(doc.Nodes))
			parents[c]++
			if parents[c] > 1 {
				t.Fatalf("node %d has more than one parent", c)
			}
		}
	}
	for _, m := range doc.Meshes {
		for _, p := range m.Primitives {
			for _, a := range p.Attributes {
				in("accessor", a, len(doc.Accessors))
			}
			if p.Indices != nil {
				in("indices", *p.Indices, len(doc.Accessors))
			}
			if p.Material != nil {
				in("material", *p.Material, len(doc.Materials))
			}
		}
	}
	for _, a := range doc.Accessors {
		if a.BufferView != nil {
			in("bufferView", *a.BufferView, len(doc.BufferViews))
		}
	}
	for _, bv := range doc.BufferViews {
		in("buffer", bv.Buffer, len(doc.Buffers))
		if bv.ByteOffset+bv.ByteLength > len(doc.Buffers[bv.Buffer].Data) {
			t.Fatalf("buffer view reaches past its buffer")
		}
	}
	for _, s := range doc.Scenes {
		for _, n := range s.Nodes {
			in("scene root", n, len(doc.Nodes))
		}
	}
}

func merge(t *testing.T, base, ours, theirs []byte) ([]byte, []SemanticConflict) {
	t.Helper()
	out, ci, err := (&Handler{}).Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if ci == nil {
		return out, nil
	}
	return out, ci.Conflicts
}

func conflictPaths(cs []SemanticConflict) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

// ── the merge ─────────────────────────────────────────────────────────────────

// The data loss this replaces: meshes were always ours, so a geometry edit made
// only on theirs vanished from the merge without a conflict.
func TestMergeKeepsTheirGeometryEdit(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	lid := part{name: "Lid", material: "Steel", color: grey, at: 3}
	base := glbOf(t, body, lid)
	moved := lid
	moved.move = [3]float64{0, 2, 0}
	ours := glbOf(t, body, moved)
	edited := body
	edited.at = 5
	theirs := glbOf(t, edited, lid)

	out, cs := merge(t, base, ours, theirs)
	if len(cs) != 0 {
		t.Fatalf("no conflict expected: %v", conflictPaths(cs))
	}
	d := drawnOf(t, out)
	if d["Body"].first[0] != 5 {
		t.Errorf("Body draws theirs' geometry at x=5, got %v", d["Body"].first)
	}
	if d["Lid"].first[1] != 2 {
		t.Errorf("Lid keeps ours' move up 2, got %v", d["Lid"].first)
	}
}

// Each side adds a part with its own mesh and material. Theirs' used to arrive
// with theirs' indices, pointing at ours' meshes and materials.
func TestMergeBringsEachSidesNewPartWithItsMeshAndMaterial(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	base := glbOf(t, body)
	ours := glbOf(t, body, part{name: "Shelf", material: "Oak", color: green, at: 10})
	theirs := glbOf(t, body, part{name: "Lamp", material: "Brass", color: brass, at: 20})

	out, cs := merge(t, base, ours, theirs)
	if len(cs) != 0 {
		t.Fatalf("no conflict expected: %v", conflictPaths(cs))
	}
	d := drawnOf(t, out)
	want := map[string]drawn{
		"Body":  {[3]float64{0, 0, 0}, grey},
		"Shelf": {[3]float64{10, 0, 0}, green},
		"Lamp":  {[3]float64{20, 0, 0}, brass},
	}
	for name, w := range want {
		g, ok := d[name]
		if !ok || g.first != w.first || !nearEq4(g.color, w.color) {
			t.Errorf("%s draws %+v, want %+v", name, g, w)
		}
	}
}

// A removal against an untouched element is a clean removal; against an edited
// one, a conflict that keeps it (ours) where it was.
func TestMergeRemovals(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	lid := part{name: "Lid", material: "Steel", color: grey, at: 3}
	base := glbOf(t, body, lid)
	theirs := glbOf(t, body) // removed the lid

	out, cs := merge(t, base, glbOf(t, body, lid, part{name: "Shelf", material: "Oak", color: green, at: 9}), theirs)
	if len(cs) != 0 {
		t.Fatalf("a removal of an untouched element is clean: %v", conflictPaths(cs))
	}
	if _, ok := drawnOf(t, out)["Lid"]; ok {
		t.Error("Lid should be gone")
	}

	moved := lid
	moved.move = [3]float64{0, 0, 4}
	out, cs = merge(t, base, glbOf(t, body, moved), theirs)
	if got := strings.Join(conflictPaths(cs), ","); got != "nodes/Lid" {
		t.Fatalf("conflicts %q, want nodes/Lid", got)
	}
	if d := drawnOf(t, out)["Lid"]; d.first[2] != 4 {
		t.Errorf("Lid kept with ours' edit, got %+v", d)
	}
}

// Ours shifted every index by inserting a part first; theirs edited the lid.
// Renumbering is not a change: no conflict, and every node draws its own mesh.
func TestMergeIgnoresRenumbering(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	lid := part{name: "Lid", material: "Steel", color: grey, at: 3}
	base := glbOf(t, body, lid)
	ours := glbOf(t, part{name: "Base", material: "Oak", color: green, at: -5}, body, lid)
	moved := lid
	moved.move = [3]float64{1, 0, 0}
	theirs := glbOf(t, body, moved)

	out, cs := merge(t, base, ours, theirs)
	if len(cs) != 0 {
		t.Fatalf("no conflict expected: %v", conflictPaths(cs))
	}
	d := drawnOf(t, out)
	if d["Base"].first[0] != -5 || d["Body"].first[0] != 0 || d["Lid"].first[0] != 4 {
		t.Errorf("drawn %+v", d)
	}
}

func TestMergeBothEditTheSameMeshIsAConflictKeepingOurs(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	base := glbOf(t, body)
	o, th := body, body
	o.at, th.at = 1, 2
	out, cs := merge(t, base, glbOf(t, o), glbOf(t, th))
	if got := strings.Join(conflictPaths(cs), ","); got != "meshes/Body" {
		t.Fatalf("conflicts %q, want meshes/Body", got)
	}
	if d := drawnOf(t, out)["Body"]; d.first[0] != 1 {
		t.Errorf("ours kept, got %+v", d)
	}
}

// Theirs moves the wheel under the body; ours moves the wheel itself. Both
// land, and the wheel has one parent.
func TestMergeHierarchyStaysATree(t *testing.T) {
	wheel := part{name: "Wheel", material: "Rubber", color: grey, at: 1}
	chassis := func(ch ...part) part { return part{name: "Chassis", material: "Steel", color: grey, children: ch} }
	body := func(ch ...part) part { return part{name: "Body", material: "Steel", color: grey, at: 7, children: ch} }
	base := glbOf(t, chassis(wheel), body())
	turned := wheel
	turned.move = [3]float64{0, 3, 0}
	ours := glbOf(t, chassis(turned), body())
	theirs := glbOf(t, chassis(), body(wheel))

	out, cs := merge(t, base, ours, theirs)
	if len(cs) != 0 {
		t.Fatalf("no conflict expected: %v", conflictPaths(cs))
	}
	doc, _ := parseDoc(out)
	parent := newMdoc(doc).parentOf("Wheel")
	if parent != "Body" {
		t.Errorf("Wheel's parent is %q, want Body", parent)
	}
	if d := drawnOf(t, out)["Wheel"]; d.first[1] != 3 {
		t.Errorf("Wheel keeps ours' move, got %+v", d)
	}

	// Both move it, to different parents: one parent (ours), and a conflict.
	lid := part{name: "Lid", material: "Steel", color: grey, at: 9}
	base = glbOf(t, chassis(wheel), body(), lid)
	ours = glbOf(t, chassis(), body(), part{name: "Lid", material: "Steel", color: grey, at: 9, children: []part{wheel}})
	theirs = glbOf(t, chassis(), body(wheel), lid)
	out, cs = merge(t, base, ours, theirs)
	if got := strings.Join(conflictPaths(cs), ","); got != "nodes/Wheel/parent" {
		t.Fatalf("conflicts %q, want nodes/Wheel/parent", got)
	}
	doc, _ = parseDoc(out)
	if p := newMdoc(doc).parentOf("Wheel"); p != "Lid" {
		t.Errorf("Wheel's parent is %q, want ours' Lid", p)
	}
	// …and taking theirs' parent moves it.
	resolved, err := (&Handler{}).ApplyChoices(out, theirs, []string{"nodes/Wheel/parent"})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ = parseDoc(resolved)
	if p := newMdoc(doc).parentOf("Wheel"); p != "Body" {
		t.Errorf("after taking theirs, Wheel's parent is %q, want Body", p)
	}
	validDoc(t, resolved)
}

// One side changed nothing: the other side is the merge, byte for byte.
func TestMergeOneSidedIsTheOtherSide(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	base := glbOf(t, body)
	edited := body
	edited.at = 4
	changed := glbOf(t, edited)
	if out, cs := merge(t, base, changed, base); len(cs) != 0 || !bytes.Equal(out, changed) {
		t.Error("theirs unchanged: the merge should be ours exactly")
	}
	if out, cs := merge(t, base, base, changed); len(cs) != 0 || !bytes.Equal(out, changed) {
		t.Error("ours unchanged: the merge should be theirs exactly")
	}
}

// Taking a whole element from theirs — what used to be refused — assembles like
// a merge, so whatever it refers to comes with it.
func TestApplyChoicesTakesWholeElements(t *testing.T) {
	body := part{name: "Body", material: "Steel", color: grey}
	base := glbOf(t, body)
	ours := glbOf(t, body, part{name: "Lamp", material: "Oak", color: green, at: 3})
	theirs := glbOf(t, body, part{name: "Lamp", material: "Brass", color: brass, at: 8})

	merged, cs := merge(t, base, ours, theirs)
	if len(cs) == 0 {
		t.Fatal("both added a different Lamp: want conflicts")
	}
	resolved, err := (&Handler{}).ApplyChoices(merged, theirs, []string{"meshes/Lamp"})
	if err != nil {
		t.Fatal(err)
	}
	d := drawnOf(t, resolved)
	// Theirs' Lamp mesh, with the Brass material it uses brought along.
	if d["Lamp"].first[0] != 8 || !nearEq4(d["Lamp"].color, brass) {
		t.Errorf("Lamp draws %+v, want theirs' at x=8 in brass", d["Lamp"])
	}

	// Taking an element theirs doesn't have removes it.
	gone, err := (&Handler{}).ApplyChoices(merged, glbOf(t, body), []string{"nodes/Lamp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drawnOf(t, gone)["Lamp"]; ok {
		t.Error("taking theirs' (absent) Lamp should remove it")
	}
}

// An animation targets nodes by index; theirs' animation must still drive the
// node it drove after ours renumbered the nodes.
func TestMergeRetargetsAnimations(t *testing.T) {
	scene := func(names []string, animated string) []byte {
		doc := &gltf.Document{Asset: gltf.Asset{Version: "2.0"}}
		var roots []int
		for i, n := range names {
			doc.Nodes = append(doc.Nodes, &gltf.Node{Name: n})
			roots = append(roots, i)
		}
		doc.Scenes = []*gltf.Scene{{Nodes: roots}}
		doc.Scene = gltf.Index(0)
		if animated != "" {
			in := modeler.WriteAccessor(doc, gltf.TargetNone, []float32{0, 1})
			out := modeler.WriteAccessor(doc, gltf.TargetNone, [][3]float32{{0, 0, 0}, {0, 5, 0}})
			target := 0
			for i, n := range names {
				if n == animated {
					target = i
				}
			}
			doc.Animations = []*gltf.Animation{{
				Name:     "Bounce",
				Samplers: []*gltf.AnimationSampler{{Input: in, Output: out}},
				Channels: []*gltf.AnimationChannel{{Sampler: 0, Target: gltf.AnimationChannelTarget{Node: gltf.Index(target), Path: gltf.TRSTranslation}}},
			}}
		}
		b, err := encodeBlob(doc, true)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := scene([]string{"A", "B"}, "")
	ours := scene([]string{"New", "A", "B"}, "") // every index shifted
	theirs := scene([]string{"A", "B"}, "B")     // animates B
	out, cs := merge(t, base, ours, theirs)
	if len(cs) != 0 {
		t.Fatalf("no conflict expected: %v", conflictPaths(cs))
	}
	doc, _ := parseDoc(out)
	if len(doc.Animations) != 1 {
		t.Fatalf("want theirs' animation, got %d", len(doc.Animations))
	}
	target := *doc.Animations[0].Channels[0].Target.Node
	if doc.Nodes[target].Name != "B" {
		t.Errorf("animation drives %q, want B", doc.Nodes[target].Name)
	}
}

// A node one side detached (no parent, no scene) stays detached: that is the
// side's edit, not an orphan to repair. Only a node one side deleted outright
// and the other kept is put back where it was.
func TestMergeKeepsADetachedNodeDetached(t *testing.T) {
	board := part{name: "Board", material: "PCB", color: green, at: 2}
	body := func(ch ...part) part { return part{name: "Body", material: "Steel", color: grey, children: ch} }
	base := glbOf(t, body(board))
	theirsDoc, _ := parseDoc(glbOf(t, body(board)))
	theirsDoc.Nodes[0].Children = nil // Board stays in the file, under nothing
	theirs, _ := encodeBlob(theirsDoc, true)
	moved := board
	moved.move = [3]float64{0, 1, 0}
	out, cs := merge(t, base, glbOf(t, body(moved)), theirs)
	if len(cs) != 0 {
		t.Fatalf("no conflict expected: %v", conflictPaths(cs))
	}
	doc, _ := parseDoc(out)
	m := newMdoc(doc)
	if p, r := m.parentOf("Board"), m.rootScene("Board"); p != "" || r != "" {
		t.Errorf("Board is under %q / root of %q, want detached", p, r)
	}
}
