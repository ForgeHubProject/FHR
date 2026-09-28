package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/qmuntal/gltf"
)

// quad is one square face in the XY plane.
const quad = `o Cube
v 0 0 0
v 1 0 0
v 1 1 0
v 0 1 0
f 1 2 3 4
`

func diffOf(t *testing.T, base, head string) fhr.StructuredDiff {
	t.Helper()
	d, err := (&Handler{}).Diff([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if d.Format != "obj" || d.Version != "1.0" {
		t.Fatalf("format/version = %q/%q", d.Format, d.Version)
	}
	return d
}

// flat lists every change as "kind path", depth-first.
func flat(d fhr.StructuredDiff) []string {
	var out []string
	var walk func([]fhr.DiffChange)
	walk = func(cs []fhr.DiffChange) {
		for _, c := range cs {
			out = append(out, string(c.Kind)+" "+c.Path)
			walk(c.Children)
		}
	}
	walk(d.Changes)
	return out
}

func find(d fhr.StructuredDiff, path string) *fhr.DiffChange {
	var hit *fhr.DiffChange
	var walk func([]fhr.DiffChange)
	walk = func(cs []fhr.DiffChange) {
		for i := range cs {
			if cs[i].Path == path {
				hit = &cs[i]
			}
			walk(cs[i].Children)
		}
	}
	walk(d.Changes)
	return hit
}

func mustHave(t *testing.T, d fhr.StructuredDiff, want ...string) {
	t.Helper()
	got := flat(d)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing %q in\n  %s", w, strings.Join(got, "\n  "))
		}
	}
}

func mustLack(t *testing.T, d fhr.StructuredDiff, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if c := find(d, p); c != nil {
			t.Errorf("unexpected change at %q: %s", p, c.Kind)
		}
	}
}

func TestIdenticalFilesDiffAsEmptyList(t *testing.T) {
	d := diffOf(t, quad, quad)
	js, _ := json.Marshal(d)
	if !strings.Contains(string(js), `"changes":[]`) {
		t.Fatalf("identical files must diff as [] (never null): %s", js)
	}
}

// The #31 regression: a vertex that moves without any count changing is the
// most common edit to a mesh, and it must never read as "no changes".
func TestMovedVertexIsAPositionChange(t *testing.T) {
	d := diffOf(t, quad, strings.Replace(quad, "v 1 1 0", "v 5 5 3", 1))
	mustHave(t, d,
		"modified meshes/Cube/primitives/0/geometry/POSITION",
		"modified meshes/Cube/primitives/0/bounds",
		"modified meshes/Cube/primitives/0/centroid",
	)
	mustLack(t, d, "meshes/Cube/primitives/0/geometry/indices")
}

// Re-winding a face (or reordering faces) changes which way it points, not
// where any vertex is: it must show as an index change and nothing else.
func TestRewoundFaceIsAnIndexChangeOnly(t *testing.T) {
	d := diffOf(t, quad, strings.Replace(quad, "f 1 2 3 4", "f 4 3 2 1", 1))
	mustHave(t, d, "modified meshes/Cube/primitives/0/geometry/indices")
	mustLack(t, d, "meshes/Cube/primitives/0/geometry/POSITION", "meshes/Cube/primitives/0/bounds")
}

func TestAddedAndDeletedFiles(t *testing.T) {
	d := diffOf(t, "", quad)
	mustHave(t, d, "added nodes/Cube")
	d = diffOf(t, quad, "")
	mustHave(t, d, "removed nodes/Cube")
}

func TestObjectAddedAndRemoved(t *testing.T) {
	two := quad + "o Lid\nv 0 0 1\nv 1 0 1\nv 1 1 1\nf 5 6 7\n"
	mustHave(t, diffOf(t, quad, two), "added nodes/Lid")
	mustHave(t, diffOf(t, two, quad), "removed nodes/Lid")
}

// Renaming an object without touching its geometry is one rename, found by
// the engine's content tier — not a removal plus an unrelated addition.
func TestRenamedObjectIsOneRename(t *testing.T) {
	d := diffOf(t, quad, strings.Replace(quad, "o Cube", "o Box", 1))
	c := find(d, "nodes/Box")
	if c == nil || c.Kind != fhr.Renamed || c.Before != "Cube" || !strings.HasPrefix(c.After.(string), "Box") {
		t.Fatalf("want nodes/Box renamed Cube → Box, got %+v in\n  %s", c, strings.Join(flat(d), "\n  "))
	}
	mustLack(t, d, "nodes/Cube")
}

// A group added, removed or renamed whole is one row, not a node row plus a
// mesh row: OBJ has no separate mesh to report. Mesh rows that say more
// (geometry, material) stay.
func TestWholeGroupChangesAreReportedOnce(t *testing.T) {
	two := quad + "o Lid\nv 0 0 1\nv 1 0 1\nv 1 1 1\nf 5 6 7\n"
	for name, d := range map[string]fhr.StructuredDiff{
		"added":   diffOf(t, quad, two),
		"removed": diffOf(t, two, quad),
		"renamed": diffOf(t, quad, strings.Replace(quad, "o Cube", "o Box", 1)),
	} {
		for _, row := range flat(d) {
			if strings.Contains(row, "meshes/") {
				t.Errorf("%s: mesh twin still reported: %v", name, flat(d))
			}
		}
		if len(flat(d)) == 0 {
			t.Errorf("%s: the node row itself must remain", name)
		}
	}
	// Geometry that changed alongside the move of another group still shows.
	d := diffOf(t, two, strings.Replace(two, "o Lid", "o Cap", 1)+"o Extra\nv 9 9 9\nf 1 2 8\n")
	mustHave(t, d, "renamed nodes/Cap", "added nodes/Extra")
	mustLack(t, d, "meshes/Cap", "meshes/Extra")
	d = diffOf(t, two, strings.Replace(two, "v 1 1 1", "v 2 2 2", 1))
	mustHave(t, d, "modified meshes/Lid")
}

// o → g is a parent/child relationship, and a group name is scoped to its
// object: two objects' Wheel groups are two nodes, not one merged bag.
func TestGroupsNestUnderTheirObject(t *testing.T) {
	src := `v 0 0 0
v 1 0 0
v 0 1 0
v 0 0 1
o Car
g Wheel
f 1 2 3
o Truck
g Wheel
f 1 2 4
`
	doc := toGLTF(mustParse(t, src))
	var names []string
	for _, n := range doc.Nodes {
		names = append(names, n.Name)
	}
	if got := strings.Join(names, ","); got != "Car,Wheel,Truck,Wheel" {
		t.Fatalf("nodes = %s", got)
	}
	if len(doc.Scenes[0].Nodes) != 2 || len(doc.Nodes[0].Children) != 1 || len(doc.Nodes[2].Children) != 1 {
		t.Fatalf("want two roots with one child each, got roots %v", doc.Scenes[0].Nodes)
	}
	// Moving the truck's wheel changes the truck's wheel only.
	d := diffOf(t, src, strings.Replace(src, "f 1 2 4", "f 1 3 4", 1))
	mustHave(t, d, "modified meshes/Wheel#1")
	mustLack(t, d, "meshes/Wheel")
}

// An `o` statement is a new object every time: two objects that share a name
// stay two nodes (the engine suffixes the second), not one merged mesh.
func TestRepeatedObjectNameIsTwoObjects(t *testing.T) {
	src := "v 0 0 0\nv 1 0 0\nv 0 1 0\nv 0 0 1\no Sensor\nf 1 2 3\no Sensor\nf 1 2 4\n"
	doc := toGLTF(mustParse(t, src))
	if len(doc.Nodes) != 2 || len(doc.Meshes) != 2 {
		t.Fatalf("want two Sensor objects, got %d nodes %d meshes", len(doc.Nodes), len(doc.Meshes))
	}
	d := diffOf(t, src, strings.Replace(src, "f 1 2 4", "f 2 3 4", 1))
	mustHave(t, d, "modified meshes/Sensor#1")
	mustLack(t, d, "meshes/Sensor")
}

// OBJ groups are sets: reopening a name adds to the same node.
func TestReopenedGroupIsOneNode(t *testing.T) {
	src := "v 0 0 0\nv 1 0 0\nv 0 1 0\ng A\nf 1 2 3\ng B\nf 1 2 3\ng A\nf 3 2 1\n"
	doc := toGLTF(mustParse(t, src))
	if len(doc.Nodes) != 2 {
		t.Fatalf("want nodes A and B, got %d", len(doc.Nodes))
	}
	if n := indexCount(t, doc, 0); n != 6 {
		t.Fatalf("A should hold both of its faces (6 indices), got %d", n)
	}
}

func TestElementsBeforeAnyGroupLandInDefault(t *testing.T) {
	doc := toGLTF(mustParse(t, "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"))
	if len(doc.Nodes) != 1 || doc.Nodes[0].Name != defaultNode {
		t.Fatalf("got %+v", doc.Nodes)
	}
}

func TestMaterialAssignmentChange(t *testing.T) {
	src := "mtllib a.mtl\nusemtl Steel\n" + quad
	d := diffOf(t, src, strings.Replace(src, "usemtl Steel", "usemtl Brass", 1))
	mustHave(t, d, "removed materials/Steel", "added materials/Brass")
	mustHave(t, d, "modified meshes/Cube/primitives/0/material")
}

// Every OBJ material converts to the same (unstated) glTF surface, so the
// content tier must not pair a removed material with an added one.
func TestMaterialsNeverPairByContent(t *testing.T) {
	d := diffOf(t, "usemtl A\n"+quad, "usemtl B\n"+quad)
	for _, c := range flat(d) {
		if strings.HasPrefix(c, "renamed materials") {
			t.Fatalf("materials paired by content: %s", strings.Join(flat(d), "\n  "))
		}
	}
}

func TestMtlLibChange(t *testing.T) {
	d := diffOf(t, "mtllib a.mtl\n"+quad, "mtllib b.mtl\n"+quad)
	c := find(d, "mtllib")
	if c == nil || c.Kind != fhr.Modified || c.Before != "a.mtl" || c.After != "b.mtl" {
		t.Fatalf("mtllib change = %+v", c)
	}
	mustHave(t, diffOf(t, quad, "mtllib a.mtl\n"+quad), "added mtllib")
}

// A statement the handler does not interpret must still surface when it
// changes: "no changes" for a changed file is the one answer never allowed.
func TestUninterpretedStatementsSurface(t *testing.T) {
	a := strings.Replace(quad, "f 1 2 3 4", "s 1\nf 1 2 3 4", 1)
	b := strings.Replace(quad, "f 1 2 3 4", "s off\nf 1 2 3 4", 1)
	c := find(diffOf(t, a, b), "other")
	if c == nil || c.Kind != fhr.Modified || c.After != "s ×1 (content changed)" {
		t.Fatalf("smoothing change = %+v", c)
	}
	c = find(diffOf(t, quad, a), "other")
	if c == nil || c.Kind != fhr.Added || c.After != "s ×1" {
		t.Fatalf("added smoothing = %+v", c)
	}
	if find(diffOf(t, a, a), "other") != nil {
		t.Fatal("unchanged statements must not be reported")
	}
}

// Comments and formatting are not content.
func TestCommentsAndWhitespaceAreNotChanges(t *testing.T) {
	noisy := "# exported by something\n\n" + strings.ReplaceAll(quad, " ", "   ") + "\n# end\n"
	if got := flat(diffOf(t, quad, noisy)); len(got) != 0 {
		t.Fatalf("want no changes, got %v", got)
	}
}

// Negative references count back from the vertices defined so far; a file
// written with them is the same mesh as one written with positive indices.
func TestNegativeReferences(t *testing.T) {
	neg := strings.Replace(quad, "f 1 2 3 4", "f -4 -3 -2 -1", 1)
	if got := flat(diffOf(t, quad, neg)); len(got) != 0 {
		t.Fatalf("negative references should resolve to the same mesh, got %v", got)
	}
}

func TestReferenceForms(t *testing.T) {
	src := `v 0 0 0
v 1 0 0
v 0 1 0
vt 0 0
vt 1 0
vt 0 1
vn 0 0 2
g UV
f 1/1 2/2 3/3
g N
f 1//1 2//1 3//1
g Both
f 1/1/1 2/2/1 3/3/1
`
	doc := toGLTF(mustParse(t, src))
	want := map[string][]string{
		"UV":   {gltf.POSITION, gltf.TEXCOORD_0},
		"N":    {gltf.NORMAL, gltf.POSITION},
		"Both": {gltf.NORMAL, gltf.POSITION, gltf.TEXCOORD_0},
	}
	for _, m := range doc.Meshes {
		var got []string
		for k := range m.Primitives[0].Attributes {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, want[m.Name]) {
			t.Errorf("%s attributes = %v, want %v", m.Name, got, want[m.Name])
		}
	}
}

// A face missing UVs gets its own primitive instead of stripping its
// neighbours' UVs or inventing its own.
func TestMixedAttributeFacesSplitPrimitives(t *testing.T) {
	src := "v 0 0 0\nv 1 0 0\nv 0 1 0\nvt 0 0\nf 1/1 2/1 3/1\nf 1 2 3\n"
	doc := toGLTF(mustParse(t, src))
	if n := len(doc.Meshes[0].Primitives); n != 2 {
		t.Fatalf("want 2 primitives, got %d", n)
	}
}

func TestLinesAndPoints(t *testing.T) {
	src := "v 0 0 0\nv 1 0 0\nv 1 1 0\no Wire\nl 1 2 3\no Dots\np 1 3\n"
	doc := toGLTF(mustParse(t, src))
	if m := doc.Meshes[0].Primitives[0]; m.Mode != gltf.PrimitiveLines || indexCount(t, doc, 0) != 4 {
		t.Fatalf("polyline 1-2-3 should be 2 LINES segments, got mode %v", m.Mode)
	}
	if m := doc.Meshes[1].Primitives[0]; m.Mode != gltf.PrimitivePoints {
		t.Fatalf("p should be POINTS, got %v", m.Mode)
	}
	// Moving a polyline vertex is a geometry change like any other.
	d := diffOf(t, src, strings.Replace(src, "v 1 1 0", "v 2 2 0", 1))
	mustHave(t, d, "modified meshes/Wire/primitives/0/geometry/POSITION")
}

func TestVertexColors(t *testing.T) {
	src := "v 0 0 0 1 0 0\nv 1 0 0 0 1 0\nv 0 1 0 0 0 1\nf 1 2 3\n"
	d := diffOf(t, src, strings.Replace(src, "v 1 0 0 0 1 0", "v 1 0 0 1 1 1", 1))
	mustHave(t, d, "modified meshes/(default)/primitives/0/geometry/COLOR_0")
	mustLack(t, d, "meshes/(default)/primitives/0/geometry/POSITION")
}

func TestLineContinuation(t *testing.T) {
	cont := strings.Replace(quad, "f 1 2 3 4", "f 1 2 \\\n 3 4", 1)
	if got := flat(diffOf(t, quad, cont)); len(got) != 0 {
		t.Fatalf("a continued statement is the same statement, got %v", got)
	}
}

func TestSlashInNamesStaysOneSegment(t *testing.T) {
	d := diffOf(t, "", strings.Replace(quad, "o Cube", "o a/b", 1))
	mustHave(t, d, "added nodes/a%2Fb")
}

func TestMalformedInputIsACleanError(t *testing.T) {
	for name, src := range map[string]string{
		"bad number":        "v 0 x 0\n",
		"short vertex":      "v 0 0\n",
		"short face":        "v 0 0 0\nv 1 0 0\nf 1 2\n",
		"zero index":        "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 0 1 2\n",
		"out of range":      "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 9\n",
		"uv out of range":   "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1/1 2/1 3/1\n",
		"before first":      "v 0 0 0\nf -1 -2 -3\n",
		"garbage reference": "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3/a\n",
		"binary":            "v 0 0 0\x00\n",
	} {
		if _, err := (&Handler{}).Diff(nil, []byte(src)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	for path, want := range map[string]bool{
		"model.obj": true, "MODEL.OBJ": true, "a/b/c.Obj": true,
		"model.mtl": false, "model.obj.bak": false, "obj": false,
	} {
		if h.Match(path) != want {
			t.Errorf("Match(%q) = %v", path, !want)
		}
	}
}

func TestMergeIsUnsupported(t *testing.T) {
	if _, _, err := (&Handler{}).Merge(nil, nil, nil); err == nil {
		t.Fatal("want an error")
	}
}

// The preview is the diffed document: a GLB whose node and mesh names are
// the ones the diff's paths address, dressed with a viewable surface.
func TestPreviewIsTheDiffedDocument(t *testing.T) {
	src := "usemtl Steel\n" + quad + "o Lid\nv 0 0 1\nv 1 0 1\nv 1 1 1\nf 5 6 7\n"
	glb, err := (&Handler{}).Preview([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(glb, []byte("glTF")) {
		t.Fatalf("preview is not a GLB: % x", glb[:min(len(glb), 12)])
	}
	var doc gltf.Document
	if err := gltf.NewDecoder(bytes.NewReader(glb)).Decode(&doc); err != nil {
		t.Fatalf("preview does not decode: %v", err)
	}
	var nodes, meshes []string
	for _, n := range doc.Nodes {
		nodes = append(nodes, n.Name)
	}
	for _, m := range doc.Meshes {
		meshes = append(meshes, m.Name)
	}
	if strings.Join(nodes, ",") != "Cube,Lid" || strings.Join(meshes, ",") != "Cube,Lid" {
		t.Fatalf("nodes %v meshes %v", nodes, meshes)
	}
	for _, m := range doc.Meshes {
		for _, p := range m.Primitives {
			if p.Material == nil {
				t.Fatalf("%s: every preview primitive gets a material", m.Name)
			}
			mat := doc.Materials[*p.Material]
			if !mat.DoubleSided || mat.PBRMetallicRoughness.MetallicFactorOrDefault() != 0 {
				t.Fatalf("%s: preview material not dressed: %+v", m.Name, mat)
			}
		}
	}
	// The diff and the preview agree on names: every diff path's element exists.
	d := diffOf(t, "", src)
	for _, c := range d.Changes {
		for _, child := range c.Children {
			seg := strings.SplitN(child.Path, "/", 3)
			if len(seg) < 2 {
				continue
			}
			list := map[string][]string{"nodes": nodes, "meshes": meshes}[seg[0]]
			if list != nil && !slices.Contains(list, seg[1]) {
				t.Errorf("diff path %q names no element of the preview", child.Path)
			}
		}
	}
}

func TestPreviewOfMalformedInputIsAnError(t *testing.T) {
	if _, err := (&Handler{}).Preview([]byte("v 0 x 0\n")); err == nil {
		t.Fatal("want an error")
	}
}

func mustParse(t *testing.T, src string) *objFile {
	t.Helper()
	f, err := parseOBJ([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func indexCount(t *testing.T, doc *gltf.Document, mesh int) int {
	t.Helper()
	p := doc.Meshes[mesh].Primitives[0]
	return doc.Accessors[*p.Indices].Count
}
