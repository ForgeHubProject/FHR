package scene

import (
	"testing"

	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

func flatDoc(mode gltf.PrimitiveMode, n int, node *gltf.Node) Blob {
	doc := &gltf.Document{Asset: gltf.Asset{Version: "2.0"}, Scene: gltf.Index(0), Scenes: []*gltf.Scene{{Nodes: []int{0}}}}
	pos := make([][3]float32, n)
	for i := range pos {
		pos[i] = [3]float32{float32(i), 0, 0}
	}
	doc.Meshes = []*gltf.Mesh{{Primitives: []*gltf.Primitive{{
		Mode:       mode,
		Attributes: gltf.PrimitiveAttributes{gltf.POSITION: modeler.WritePosition(doc, pos)},
	}}}}
	node.Mesh = gltf.Index(0)
	doc.Nodes = []*gltf.Node{node}
	b, err := encodeBlob(doc, true)
	if err != nil {
		panic(err)
	}
	return b
}

func flatOne(t *testing.T, mode gltf.PrimitiveMode, n int, node *gltf.Node) *FlatPrim {
	t.Helper()
	roots, err := Flatten(flatDoc(mode, n, node))
	if err != nil || len(roots) != 1 || len(roots[0].Prims) != 1 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	return roots[0].Prims[0]
}

func eq(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFlattenExpandsModes(t *testing.T) {
	cases := []struct {
		name string
		mode gltf.PrimitiveMode
		n    int
		kind PrimKind
		want []uint32
	}{
		{"triangles", gltf.PrimitiveTriangles, 3, KindTriangles, []uint32{0, 1, 2}},
		{"strip alternates winding", gltf.PrimitiveTriangleStrip, 4, KindTriangles, []uint32{0, 1, 2, 2, 1, 3}},
		{"fan", gltf.PrimitiveTriangleFan, 4, KindTriangles, []uint32{0, 1, 2, 0, 2, 3}},
		{"lines", gltf.PrimitiveLines, 4, KindLines, []uint32{0, 1, 2, 3}},
		{"line strip", gltf.PrimitiveLineStrip, 3, KindLines, []uint32{0, 1, 1, 2}},
		{"line loop", gltf.PrimitiveLineLoop, 3, KindLines, []uint32{0, 1, 1, 2, 2, 0}},
		{"points", gltf.PrimitivePoints, 2, KindPoints, []uint32{0, 1}},
	}
	for _, c := range cases {
		p := flatOne(t, c.mode, c.n, &gltf.Node{})
		if p.Kind != c.kind || !eq(p.Indices, c.want) {
			t.Errorf("%s: kind=%d indices=%v, want %d %v", c.name, p.Kind, p.Indices, c.kind, c.want)
		}
	}
}

func TestFlattenBakesTransformAndFlipsMirroredWinding(t *testing.T) {
	p := flatOne(t, gltf.PrimitiveTriangles, 3, &gltf.Node{Translation: [3]float64{10, 0, 0}, Scale: [3]float64{2, 2, 2}})
	if p.Positions[1] != [3]float64{12, 0, 0} || !eq(p.Indices, []uint32{0, 1, 2}) {
		t.Fatalf("positions=%v indices=%v", p.Positions, p.Indices)
	}
	m := flatOne(t, gltf.PrimitiveTriangles, 3, &gltf.Node{Scale: [3]float64{-1, 1, 1}})
	if !eq(m.Indices, []uint32{0, 2, 1}) {
		t.Fatalf("a mirrored triangle must be re-wound, got %v", m.Indices)
	}
}

func TestFlattenRejectsCycles(t *testing.T) {
	doc := &gltf.Document{Asset: gltf.Asset{Version: "2.0"}, Scene: gltf.Index(0), Scenes: []*gltf.Scene{{Nodes: []int{0}}},
		Nodes: []*gltf.Node{{Children: []int{1}}, {Children: []int{0}}}}
	if _, err := FlattenDocument(doc); err == nil {
		t.Fatal("expected a cycle error")
	}
}

func TestSafeName(t *testing.T) {
	if got := SafeName("  Left  Wheel ", "node", 3); got != "Left_Wheel" {
		t.Errorf("got %q", got)
	}
	if got := SafeName("", "node", 3); got != "node3" {
		t.Errorf("got %q", got)
	}
}
