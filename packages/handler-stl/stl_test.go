package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// tri is a test triangle: three vertices of three coordinates.
type tri = [3][3]float64

// cube returns the 12 outward-oriented triangles of an axis-aligned unit cube
// with its minimum corner at (x, y, z): area 6, volume 1.
func cube(x, y, z float64) []tri {
	p := func(dx, dy, dz float64) [3]float64 { return [3]float64{x + dx, y + dy, z + dz} }
	return []tri{
		{p(0, 0, 0), p(1, 1, 0), p(1, 0, 0)}, {p(0, 0, 0), p(0, 1, 0), p(1, 1, 0)}, // bottom
		{p(0, 0, 1), p(1, 0, 1), p(1, 1, 1)}, {p(0, 0, 1), p(1, 1, 1), p(0, 1, 1)}, // top
		{p(0, 0, 0), p(1, 0, 0), p(1, 0, 1)}, {p(0, 0, 0), p(1, 0, 1), p(0, 0, 1)}, // front
		{p(0, 1, 0), p(1, 1, 1), p(1, 1, 0)}, {p(0, 1, 0), p(0, 1, 1), p(1, 1, 1)}, // back
		{p(0, 0, 0), p(0, 0, 1), p(0, 1, 1)}, {p(0, 0, 0), p(0, 1, 1), p(0, 1, 0)}, // left
		{p(1, 0, 0), p(1, 1, 0), p(1, 1, 1)}, {p(1, 0, 0), p(1, 1, 1), p(1, 0, 1)}, // right
	}
}

// asciiSTL builds an ASCII STL blob. Normals are written as 0 0 0 — the
// handler ignores them and recomputes geometry from the vertices.
func asciiSTL(name string, tris []tri) []byte {
	var b strings.Builder
	b.WriteString("solid")
	if name != "" {
		b.WriteString(" " + name)
	}
	b.WriteString("\n")
	for _, t := range tris {
		b.WriteString("  facet normal 0 0 0\n    outer loop\n")
		for _, v := range t {
			fmt.Fprintf(&b, "      vertex %g %g %g\n", v[0], v[1], v[2])
		}
		b.WriteString("    endloop\n  endfacet\n")
	}
	b.WriteString("endsolid")
	if name != "" {
		b.WriteString(" " + name)
	}
	b.WriteString("\n")
	return []byte(b.String())
}

// binarySTL builds a binary STL blob for the same triangles: 80-byte header,
// uint32 LE count, then 50-byte records (12 float32 + uint16 attribute).
func binarySTL(tris []tri) []byte {
	buf := make([]byte, 80, 84+50*len(tris))
	copy(buf, "binary stl test fixture")
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(tris)))
	for _, t := range tris {
		for i := 0; i < 3; i++ { // facet normal, ignored by the handler
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(0))
		}
		for _, v := range t {
			for c := 0; c < 3; c++ {
				buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(float32(v[c])))
			}
		}
		buf = binary.LittleEndian.AppendUint16(buf, 0) // attribute byte count
	}
	return buf
}

func diffJSON(t *testing.T, base, head []byte) (StructuredDiff, string) {
	t.Helper()
	h := &Handler{}
	d, err := h.Diff(base, head)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	return d, string(js)
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("parts/bracket.stl") || !h.Match("X.STL") {
		t.Fatal("should match .stl case-insensitively")
	}
	if h.Match("model.obj") {
		t.Fatal("should not match .obj")
	}
}

func TestIdenticalMeshesNoChanges(t *testing.T) {
	blob := asciiSTL("bracket", cube(0, 0, 0))
	_, js := diffJSON(t, blob, blob)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("identical files should yield an empty changes array, got %s", js)
	}
}

// meshRows is the statistic rows of a modified-mesh diff: the children of the one
// "nodes/<name>" change under the "nodes" wrapper (see nodeChange).
func meshRows(t *testing.T, d StructuredDiff) (node DiffChange, rows []DiffChange) {
	t.Helper()
	if len(d.Changes) != 1 || d.Changes[0].Path != "nodes" || len(d.Changes[0].Children) != 1 {
		t.Fatalf("want one nodes wrapper holding one node change, got %+v", d.Changes)
	}
	node = d.Changes[0].Children[0]
	return node, node.Children
}

func TestTranslatedCubeOnlyBoundsChange(t *testing.T) {
	base := asciiSTL("part", cube(0, 0, 0))
	head := asciiSTL("part", cube(1, 0, 0)) // same shape, moved +1 on x
	d, js := diffJSON(t, base, head)
	node, rows := meshRows(t, d)
	if node.Path != "nodes/part" || node.Kind != Modified {
		t.Fatalf("the change belongs to the preview's node, got %+v", node)
	}
	if len(rows) != 1 {
		t.Fatalf("translation must change bounds only (count/area/volume identical), got %s", js)
	}
	c := rows[0]
	if c.Path != "nodes/part/bounds" || c.Kind != Modified {
		t.Fatalf("expected a modified bounds change, got %+v", c)
	}
	if c.Before != "[0.0000 0.0000 0.0000] – [1.0000 1.0000 1.0000]" ||
		c.After != "[1.0000 0.0000 0.0000] – [2.0000 1.0000 1.0000]" {
		t.Fatalf("bounds printed with 4-decimal rounding, got %v → %v", c.Before, c.After)
	}
}

func TestASCIIAndBinaryParseToEqualStats(t *testing.T) {
	tris := cube(0, 0, 0)
	ascii := asciiSTL("", tris) // unnamed: binary STL has no solid name
	bin := binarySTL(tris)

	am, err := parseSTL(ascii)
	if err != nil {
		t.Fatal(err)
	}
	bm, err := parseSTL(bin)
	if err != nil {
		t.Fatal(err)
	}
	as, bs := computeStats(am), computeStats(bm)
	if as != bs {
		t.Fatalf("ASCII and binary of the same geometry must have equal stats:\nascii  %+v\nbinary %+v", as, bs)
	}
	if as.Triangles != 12 || !approxEqual(as.Area, 6) || !approxEqual(as.Volume, 1) {
		t.Fatalf("unit cube should have 12 triangles, area 6, volume 1; got %+v", as)
	}
	if as.Min != (vec3{0, 0, 0}) || as.Max != (vec3{1, 1, 1}) {
		t.Fatalf("unit cube bounds should be [0 0 0]–[1 1 1]; got %+v", as)
	}

	_, js := diffJSON(t, ascii, bin)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("ASCII vs binary of the same geometry must diff empty, got %s", js)
	}
}

func TestTriangleCountAndAreaChange(t *testing.T) {
	tris := cube(0, 0, 0)
	d, js := diffJSON(t, asciiSTL("part", tris), asciiSTL("part", tris[:11]))
	_, rows := meshRows(t, d)
	var counts, bounds bool
	for _, c := range rows {
		switch c.Path {
		case "nodes/part/triangles":
			if c.Before != 12 || c.After != 11 {
				t.Fatalf("expected triangle count 12 → 11, got %v → %v", c.Before, c.After)
			}
			counts = true
		case "nodes/part/bounds":
			bounds = true
		}
	}
	if !counts {
		t.Fatalf("expected a triangles change, got %s", js)
	}
	if bounds {
		t.Fatalf("dropping one cube face triangle must not change bounds (its corners remain in other triangles), got %s", js)
	}
	if !strings.Contains(js, `"nodes/part/surface_area"`) {
		t.Fatalf("dropping a triangle should change surface area, got %s", js)
	}
}

func TestNameChange(t *testing.T) {
	tris := cube(0, 0, 0)
	d, _ := diffJSON(t, asciiSTL("old", tris), asciiSTL("new", tris))
	node, rows := meshRows(t, d)
	// Keyed on the head side's name: the preview the reviewer is looking at.
	if node.Path != "nodes/new" || len(rows) != 1 || rows[0].Path != "nodes/new/name" || rows[0].Kind != Modified {
		t.Fatalf("expected a single solid-name change under nodes/new, got %+v", d.Changes)
	}
	if rows[0].Before != "old" || rows[0].After != "new" {
		t.Fatalf("expected old → new, got %+v", rows[0])
	}
}

func TestEmptyBaseIsOneAddedEntry(t *testing.T) {
	d, js := diffJSON(t, nil, asciiSTL("cube", cube(0, 0, 0)))
	node, _ := meshRows(t, d)
	if node.Kind != Added || node.Path != "nodes/cube" {
		t.Fatalf("empty base should yield one added mesh node, got %s", js)
	}
	after, _ := node.After.(string)
	if !strings.Contains(after, "12 triangles") || !strings.Contains(after, "volume 1.0000") {
		t.Fatalf("added entry should carry the mesh stats, got %q", after)
	}
	if strings.Contains(js, `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestEmptyHeadIsOneRemovedEntry(t *testing.T) {
	d, js := diffJSON(t, asciiSTL("cube", cube(0, 0, 0)), nil)
	node, _ := meshRows(t, d)
	if node.Kind != Removed || node.Path != "nodes/cube" {
		t.Fatalf("empty head should yield one removed mesh node, got %s", js)
	}
	before, _ := node.Before.(string)
	if !strings.Contains(before, "12 triangles") {
		t.Fatalf("removed entry should carry the mesh stats, got %q", before)
	}
	_, js = diffJSON(t, nil, nil)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("both sides empty must diff empty, got %s", js)
	}
}

// What the viewer relies on: every change path names a node that exists in the
// GLB the handler previews, so selecting a change flies to the mesh.
func TestChangePathsNameTheNodeThePreviewDraws(t *testing.T) {
	for _, name := range []string{"bracket", "", "a/b"} { // unnamed → "mesh"; a slash is escaped
		head := asciiSTL(name, cube(1, 0, 0))
		d, _ := diffJSON(t, asciiSTL(name, cube(0, 0, 0)), head)
		node, _ := meshRows(t, d)

		glb, err := (&Handler{}).Preview(head)
		if err != nil {
			t.Fatal(err)
		}
		roots, err := scene.Flatten(glb)
		if err != nil || len(roots) != 1 {
			t.Fatalf("roots=%v err=%v", roots, err)
		}
		want := "mesh"
		if name != "" {
			want = name
		}
		if roots[0].Name != want {
			t.Fatalf("preview node is %q, want %q", roots[0].Name, want)
		}
		if got := "nodes/" + escapeSegment(roots[0].Name); node.Path != got {
			t.Fatalf("change path %q does not name the preview's node (%q)", node.Path, got)
		}
	}
}
