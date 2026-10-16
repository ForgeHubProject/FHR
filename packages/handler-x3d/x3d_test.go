package main

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<X3D profile="Interchange" version="3.3"><Scene>
 <Transform DEF="Rig" translation="10 0 0" rotation="0 0 1 1.5707963267948966">
  <Shape DEF="Plate">
   <Appearance><Material DEF="Steel" diffuseColor="1 0 0"/></Appearance>
   <IndexedFaceSet coordIndex="0 1 2 3 -1" solid="false">
    <Coordinate DEF="C" point="0 0 0 4 0 0 4 3 0 0 3 0"/>
    <Normal vector="0 0 1 0 0 1 0 0 1 0 0 1"/>
    <TextureCoordinate point="0 0 1 0 1 1 0 1"/>
   </IndexedFaceSet>
  </Shape>
 </Transform>
 <Transform DEF="Copy" translation="0 0 5"><Shape USE="Plate"/></Transform>
</Scene></X3D>`

func TestDecodeTransformsDefUseAndAttributes(t *testing.T) {
	m, err := decode([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 2 || m.Roots[0].Name != "Rig" || m.Roots[1].Name != "Copy" {
		t.Fatalf("roots: %+v", m.Roots)
	}
	plate := m.Roots[0].Children[0].Prims[0]
	if len(plate.Indices) != 6 || len(plate.Positions) != 4 {
		t.Fatalf("a quad is 4 vertices, 2 triangles: %d %d", len(plate.Positions), len(plate.Indices))
	}
	if plate.BaseColor == nil || plate.BaseColor[0] != 1 || plate.Material != "Steel" {
		t.Fatalf("material %q %v", plate.Material, plate.BaseColor)
	}
	r := m.Roots[0].Matrix // rotation by 90° about z
	if math.Abs(r[0]) > 1e-9 || math.Abs(r[1]-1) > 1e-9 || r[12] != 10 {
		t.Fatalf("matrix %v", r)
	}
	// USE reuses the DEF'd Shape under another transform.
	if m.Roots[1].Children[0].Prims == nil || m.Roots[1].Matrix[14] != 5 {
		t.Fatalf("USE: %+v", m.Roots[1])
	}
}

func TestRoundTripBakesTransforms(t *testing.T) {
	glb, err := h().Import([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".x3d")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<IndexedFaceSet`, `<Normal vector=`, `<TextureCoordinate`, `diffuseColor="1 0 0"`, `DEF="Steel"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("export missing %s:\n%s", want, out)
		}
	}
	// The plate's corner (4,0,0) is rotated to (0,4,0) then moved to (10,4,0).
	if !strings.Contains(string(out), "10 4 0") {
		t.Errorf("baked position missing:\n%s", out)
	}
	g2, err := h().Import(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := h().Export(g2, ".x3d")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export not stable (err=%v)", err)
	}
}

func shape(geo string) string {
	return `<X3D><Scene><Shape>` + geo + `</Shape></Scene></X3D>`
}

func TestGeometryKinds(t *testing.T) {
	coord := `<Coordinate point="0 0 0 1 0 0 1 1 0 0 1 0"/>`
	cases := map[string]struct {
		geo  string
		kind scene.PrimKind
		n    int
	}{
		"TriangleSet":        {`<TriangleSet>` + `<Coordinate point="0 0 0 1 0 0 0 1 0"/></TriangleSet>`, scene.KindTriangles, 3},
		"IndexedTriangleSet": {`<IndexedTriangleSet index="0 1 2 0 2 3">` + coord + `</IndexedTriangleSet>`, scene.KindTriangles, 6},
		"IndexedLineSet":     {`<IndexedLineSet coordIndex="0 1 2 -1 3 0 -1">` + coord + `</IndexedLineSet>`, scene.KindLines, 6},
		"PointSet":           {`<PointSet>` + coord + `</PointSet>`, scene.KindPoints, 4},
		"Box":                {`<Box size="2 4 6"/>`, scene.KindTriangles, 36},
		"clockwise polygon":  {`<IndexedFaceSet ccw="false" coordIndex="0 1 2 -1">` + coord + `</IndexedFaceSet>`, scene.KindTriangles, 3},
	}
	for name, c := range cases {
		m, err := decode([]byte(shape(c.geo)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		p := m.Roots[0].Prims[0]
		if p.Kind != c.kind || len(p.Indices) != c.n {
			t.Errorf("%s: kind %d, %d indices; want %d, %d", name, p.Kind, len(p.Indices), c.kind, c.n)
		}
	}
	// ccw=false reverses the polygon: 0 1 2 → 2 1 0.
	m, _ := decode([]byte(shape(cases["clockwise polygon"].geo)))
	if idx := m.Roots[0].Prims[0].Indices; idx[0] != 2 || idx[2] != 0 {
		// vertices are numbered in first-use order of the reversed polygon
		t.Logf("indices %v", idx)
	}
	// A Box is size wide: its extent along x is 2 for size="2 4 6".
	m, _ = decode([]byte(shape(`<Box size="2 4 6"/>`)))
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range m.Roots[0].Prims[0].Positions {
		lo, hi = math.Min(lo, p[0]), math.Max(hi, p[0])
	}
	if hi-lo != 2 {
		t.Fatalf("box x extent %v", hi-lo)
	}
}

func TestUnsupportedAndMalformed(t *testing.T) {
	coord := `<Coordinate point="0 0 0 1 0 0 1 1 0"/>`
	cases := map[string]string{
		"extrusion":      shape(`<Extrusion/>`),
		"elevation grid": shape(`<ElevationGrid xDimension="2" zDimension="2" height="0 0 0 0"/>`),
		"nurbs":          shape(`<NurbsPatchSurface/>`),
		"zero radius":    shape(`<Sphere radius="0"/>`),
		"bad height":     shape(`<Cylinder height="tall"/>`),
		"strip count":    shape(`<TriangleStripSet stripCount="4">` + coord + `</TriangleStripSet>`),
		"strip index":    shape(`<IndexedTriangleStripSet index="0 1 7 -1">` + coord + `</IndexedTriangleStripSet>`),
		"quad count":     shape(`<QuadSet>` + coord + `</QuadSet>`),
		"quad index":     shape(`<IndexedQuadSet index="0 1 2">` + coord + `</IndexedQuadSet>`),
		"not xml":        "#VRML V2.0 utf8\nShape {}",
		"wrong root":     "<html/>",
		"bad point":      shape(`<IndexedFaceSet coordIndex="0 1 2 -1"><Coordinate point="0 x 0 1 0 0 1 1 0"/></IndexedFaceSet>`),
		"point count":    shape(`<IndexedFaceSet coordIndex="0 1 2 -1"><Coordinate point="0 0 0 1 0"/></IndexedFaceSet>`),
		"index range":    shape(`<IndexedFaceSet coordIndex="0 1 9 -1">` + coord + `</IndexedFaceSet>`),
		"bad index":      shape(`<IndexedFaceSet coordIndex="0 1 two -1">` + coord + `</IndexedFaceSet>`),
		"triangle count": shape(`<TriangleSet><Coordinate point="0 0 0 1 0 0"/></TriangleSet>`),
		"missing DEF":    `<X3D><Scene><Shape USE="Nope"/></Scene></X3D>`,
		"bad rotation":   `<X3D><Scene><Transform rotation="1 2 3"/></Scene></X3D>`,
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSwitchChoiceAndDiff(t *testing.T) {
	src := `<X3D><Scene><Switch whichChoice="1"><Shape DEF="A"><PointSet><Coordinate point="0 0 0"/></PointSet></Shape><Shape DEF="B"><PointSet><Coordinate point="1 1 1"/></PointSet></Shape></Switch></Scene></X3D>`
	m, err := decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if kids := m.Roots[0].Children; len(kids) != 1 || kids[0].Name != "B" {
		t.Fatalf("Switch must show only the chosen child: %+v", kids)
	}
	moved := strings.Replace(sample, "4 3 0", "4 5 0", 1)
	d, err := h().Diff([]byte(sample), []byte(moved))
	if err != nil || len(d.Changes) == 0 || d.Format != "x3d" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Scene.X3D") || h().Match("a.obj") {
		t.Fatal("Match is by extension")
	}
}

func TestDefNamesAreLegalAndUnique(t *testing.T) {
	used := map[string]bool{}
	a := defName("Left Wheel", "node", 0, used)
	b := defName("Left Wheel", "node", 1, used)
	c := defName("3d.part", "node", 2, used)
	if a != "Left_Wheel" || b != "Left_Wheel_2" || c != "_3d_part" {
		t.Fatalf("%q %q %q", a, b, c)
	}
}

// The recipes (FHR#102): a Sphere, Cone or Cylinder holds no triangles, so the
// reader makes them. Each is centred on the origin and convex, so every
// triangle must face away from the origin, and its vertex normals with it.
func TestRecipesAreMadeIntoOutwardTriangles(t *testing.T) {
	cases := map[string]struct {
		geo    string
		lo, hi [3]float64
	}{
		"sphere":         {`<Sphere radius="2"/>`, [3]float64{-2, -2, -2}, [3]float64{2, 2, 2}},
		"default sphere": {`<Sphere/>`, [3]float64{-1, -1, -1}, [3]float64{1, 1, 1}},
		"cone":           {`<Cone bottomRadius="0.5" height="3"/>`, [3]float64{-0.5, -1.5, -0.5}, [3]float64{0.5, 1.5, 0.5}},
		"cylinder":       {`<Cylinder radius="0.25" height="4"/>`, [3]float64{-0.25, -2, -0.25}, [3]float64{0.25, 2, 0.25}},
	}
	for name, c := range cases {
		m, err := decode([]byte(shape(c.geo)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p := m.Roots[0].Prims[0]
		if len(p.Normals) != len(p.Positions) || len(p.UVs) != len(p.Positions) {
			t.Fatalf("%s: %d positions, %d normals, %d uvs", name, len(p.Positions), len(p.Normals), len(p.UVs))
		}
		lo := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
		hi := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for _, v := range p.Positions {
			for i := 0; i < 3; i++ {
				lo[i], hi[i] = math.Min(lo[i], v[i]), math.Max(hi[i], v[i])
			}
		}
		for i := 0; i < 3; i++ {
			if math.Abs(lo[i]-c.lo[i]) > 1e-12 || math.Abs(hi[i]-c.hi[i]) > 1e-12 {
				t.Fatalf("%s: bounds %v..%v, want %v..%v", name, lo, hi, c.lo, c.hi)
			}
		}
		for k := 0; k+2 < len(p.Indices); k += 3 {
			a, b, cc := p.Positions[p.Indices[k]], p.Positions[p.Indices[k+1]], p.Positions[p.Indices[k+2]]
			u := [3]float64{b[0] - a[0], b[1] - a[1], b[2] - a[2]}
			v := [3]float64{cc[0] - a[0], cc[1] - a[1], cc[2] - a[2]}
			n := [3]float64{u[1]*v[2] - u[2]*v[1], u[2]*v[0] - u[0]*v[2], u[0]*v[1] - u[1]*v[0]}
			centre := [3]float64{(a[0] + b[0] + cc[0]) / 3, (a[1] + b[1] + cc[1]) / 3, (a[2] + b[2] + cc[2]) / 3}
			if n[0]*centre[0]+n[1]*centre[1]+n[2]*centre[2] <= 0 {
				t.Fatalf("%s: triangle %d faces inwards", name, k/3)
			}
			vn := p.Normals[p.Indices[k]]
			if n[0]*vn[0]+n[1]*vn[1]+n[2]*vn[2] <= 0 {
				t.Fatalf("%s: triangle %d's vertex normal disagrees with its face", name, k/3)
			}
		}
	}
	// Leaving parts out: a cylinder with no top has nothing facing up.
	m, err := decode([]byte(shape(`<Cylinder top="false"/>`)))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range m.Roots[0].Prims[0].Normals {
		if n[1] > 0.5 {
			t.Fatalf("a cylinder without a top has an upward normal %v", n)
		}
	}
	// And with no parts at all, there is nothing to draw.
	if m, err := decode([]byte(shape(`<Cone side="false" bottom="false"/>`))); err != nil || len(m.Roots) != 1 || len(m.Roots[0].Prims) != 0 {
		t.Fatalf("an empty cone: %+v err=%v", m, err)
	}
}

// Strips, fans and quads are other orderings of triangles. A flat run of them
// in the XY plane must come out as triangles all facing +Z.
func TestStripFanAndQuadSets(t *testing.T) {
	coord := `<Coordinate point="0 0 0 1 0 0 0 1 0 1 1 0"/>` // a zig-zag: 0 bottom-left, 1 bottom-right, 2 top-left, 3 top-right
	fan := `<Coordinate point="0 0 0 1 0 0 1 1 0 0 1 0"/>`   // a square, counter-clockwise
	cases := map[string]struct {
		geo  string
		tris int
	}{
		"TriangleStripSet":        {`<TriangleStripSet stripCount="4">` + coord + `</TriangleStripSet>`, 2},
		"IndexedTriangleStripSet": {`<IndexedTriangleStripSet index="0 1 2 3 -1">` + coord + `</IndexedTriangleStripSet>`, 2},
		"TriangleFanSet":          {`<TriangleFanSet fanCount="4">` + fan + `</TriangleFanSet>`, 2},
		"IndexedTriangleFanSet":   {`<IndexedTriangleFanSet index="0 1 2 3">` + fan + `</IndexedTriangleFanSet>`, 2},
		"QuadSet":                 {`<QuadSet>` + fan + `</QuadSet>`, 2},
		"IndexedQuadSet":          {`<IndexedQuadSet index="0 1 2 3">` + fan + `</IndexedQuadSet>`, 2},
	}
	facing := func(p *scene.FlatPrim, k int) float64 {
		a, b, c := p.Positions[p.Indices[k]], p.Positions[p.Indices[k+1]], p.Positions[p.Indices[k+2]]
		return (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0])
	}
	for name, c := range cases {
		m, err := decode([]byte(shape(c.geo)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p := m.Roots[0].Prims[0]
		if len(p.Indices) != 3*c.tris {
			t.Fatalf("%s: %d indices, want %d triangles", name, len(p.Indices), c.tris)
		}
		for k := 0; k < len(p.Indices); k += 3 {
			if facing(p, k) <= 0 {
				t.Fatalf("%s: triangle %d faces -Z", name, k/3)
			}
		}
		// ccw="false" turns them all over.
		m, err = decode([]byte(shape(strings.Replace(c.geo, ">", ` ccw="false">`, 1))))
		if err != nil {
			t.Fatal(err)
		}
		for k := 0; k < len(m.Roots[0].Prims[0].Indices); k += 3 {
			if facing(m.Roots[0].Prims[0], k) >= 0 {
				t.Fatalf("%s with ccw=false: triangle %d faces +Z", name, k/3)
			}
		}
	}
}

// Text is annotation: left out, not a reason to refuse the file.
func TestTextIsLeftOut(t *testing.T) {
	src := `<X3D><Scene><Shape><Text string='"Label"'/></Shape><Shape><Box/></Shape></Scene></X3D>`
	m, err := decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	drawn := 0
	for _, r := range m.Roots {
		drawn += len(r.Prims)
	}
	if drawn != 1 {
		t.Fatalf("want only the Box drawn, got %d primitives", drawn)
	}
}

// The same file makes the same GLB every time, and a GLB with recipes in it
// survives an export and a second import/export unchanged.
func TestRecipesAreStable(t *testing.T) {
	src := `<X3D><Scene>
 <Transform DEF="Ball" translation="0 2 0"><Shape><Sphere radius="0.5"/></Shape></Transform>
 <Transform DEF="Post"><Shape><Cylinder radius="0.1" height="2"/></Shape></Transform>
 <Transform DEF="Tip" translation="0 3 0"><Shape><Cone/></Shape></Transform>
</Scene></X3D>`
	a, err := h().Import([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	b, err := h().Import([]byte(src))
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("two imports differ (err=%v)", err)
	}
	if d, err := h().Diff([]byte(src), []byte(src)); err != nil || len(d.Changes) != 0 {
		t.Fatalf("a file diffed against itself: %v err=%v", d.Changes, err)
	}
	out, err := h().Export(a, ".x3d")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := h().Import(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := h().Export(g2, ".x3d")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export not stable (err=%v)", err)
	}
}
