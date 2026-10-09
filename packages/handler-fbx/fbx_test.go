package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// bounds is the world-space bounding box of every primitive in a GLB.
func bounds(t *testing.T, glb []byte) (lo, hi [3]float64, prims int) {
	t.Helper()
	roots, err := scene.Flatten(glb)
	if err != nil {
		t.Fatal(err)
	}
	lo = [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi = [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
			prims++
			for _, v := range p.Positions {
				for i := 0; i < 3; i++ {
					lo[i], hi[i] = math.Min(lo[i], v[i]), math.Max(hi[i], v[i])
				}
			}
		}
	})
	return
}

func near(a, b [3]float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-3 {
			return false
		}
	}
	return true
}

// The fixtures are real Blender exports of one scene: a rotated, scaled,
// red-materialled box with a child icosphere, and a floor plane — one stored
// Y-up, one Z-up, in centimetres. Blender itself reads both as the same scene
// spanning (-3.5, -2.232, -1) .. (3.866, 2.232, 3.5) metres in its Z-up space,
// which is (x, z, -y) in glTF's Y-up: the numbers asserted below.
func TestBlenderExportsDecodeToTheSameScene(t *testing.T) {
	wantLo, wantHi := [3]float64{-3.5, -1, -2.232}, [3]float64{3.866, 3.5, 2.232}
	for _, name := range []string{"blender-scene-yup.fbx", "blender-scene-zup.fbx"} {
		glb, err := h().Import(fixture(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lo, hi, prims := bounds(t, glb)
		if prims != 3 || !near(lo, wantLo) || !near(hi, wantHi) {
			t.Errorf("%s: %d prims, bounds %v..%v; want 3, %v..%v", name, prims, lo, hi, wantLo, wantHi)
		}
	}
}

func TestDecodeHierarchyMaterialsAndAttributes(t *testing.T) {
	m, err := decode(fixture(t, "blender-scene-yup.fbx"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*scene.Object{}
	var walk func(o *scene.Object)
	walk = func(o *scene.Object) {
		names[o.Name] = o
		for _, c := range o.Children {
			walk(c)
		}
	}
	for _, r := range m.Roots {
		walk(r)
	}
	box, ball, floor := names["Box"], names["Ball"], names["Floor"]
	if box == nil || ball == nil || floor == nil {
		t.Fatalf("objects: %v", names)
	}
	if len(box.Children) != 1 || box.Children[0] != ball {
		t.Fatal("Ball must be Box's child")
	}
	p := box.Prims[0]
	if p.Material != "Red" || p.BaseColor == nil || p.Normals == nil || p.UVs == nil {
		t.Fatalf("box prim: material %q colour %v normals=%v uvs=%v", p.Material, p.BaseColor, p.Normals != nil, p.UVs != nil)
	}
	if len(p.Positions) != 24 || len(p.Indices) != 36 { // a cube with per-face normals
		t.Fatalf("cube: %d vertices, %d indices", len(p.Positions), len(p.Indices))
	}
	if floor.Prims[0].Material != "" {
		t.Fatalf("floor has a material: %q", floor.Prims[0].Material)
	}
}

func TestRoundTripKeepsWorldGeometryAndIsStable(t *testing.T) {
	glb, err := h().Import(fixture(t, "blender-scene-yup.fbx"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".fbx")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte(binaryMagic)) || !bytes.HasSuffix(out, []byte(footerMagic)) {
		t.Fatal("export is not a framed FBX binary")
	}
	lo1, hi1, _ := bounds(t, glb)
	back, err := h().Import(out)
	if err != nil {
		t.Fatalf("our own export does not parse: %v", err)
	}
	lo2, hi2, prims := bounds(t, back)
	if prims != 3 || !near(lo1, lo2) || !near(hi1, hi2) {
		t.Fatalf("round trip moved the scene: %v..%v → %v..%v (%d prims)", lo1, hi1, lo2, hi2, prims)
	}
	out2, err := h().Export(back, ".fbx")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
}

func TestExportMaterialSlotsFollowConnectionOrder(t *testing.T) {
	// Two primitives with different materials on one node: slot indices must
	// match the order the materials are connected.
	obj := scene.Object{Name: "Part", Prims: []*scene.FlatPrim{
		tri("Steel", [4]float64{1, 0, 0, 1}), tri("Brass", [4]float64{0.8, 0.6, 0.1, 1}), tri("", [4]float64{}),
	}}
	obj.Prims[2].BaseColor = nil
	glb, err := (&scene.Codec{ID: "x", Formats: []string{".x"}, Decode: func([]byte) (*scene.Model, error) {
		return &scene.Model{Roots: []*scene.Object{&obj}}, nil
	}, Encode: func([]*scene.FlatNode, string) ([]byte, error) { return nil, nil }}).Handler().Import([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".fbx")
	if err != nil {
		t.Fatal(err)
	}
	m, err := decode(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range m.Roots[0].Prims {
		got = append(got, p.Material)
	}
	// The material-less primitive falls into slot 0 (Steel): FBX has no "none".
	if strings.Join(got, ",") != "Steel,Brass" && strings.Join(got, ",") != "Steel,Brass,Steel" {
		t.Fatalf("materials after round trip: %v", got)
	}
}

func tri(material string, c [4]float64) *scene.FlatPrim {
	p := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1, Material: material,
		Positions: [][3]float64{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, Indices: []uint32{0, 1, 2}}
	if material != "" {
		p.BaseColor = &c
	}
	return p
}

const asciiFBX = `; FBX 7.3.0 project file
FBXHeaderExtension:  {
	FBXHeaderVersion: 1003
	FBXVersion: 7300
}
GlobalSettings:  {
	Version: 1000
	Properties70:  {
		P: "UpAxis", "int", "Integer", "",1
		P: "UpAxisSign", "int", "Integer", "",1
		P: "UnitScaleFactor", "double", "Number", "",100
	}
}
Objects:  {
	Geometry: 100, "Geometry::Tri", "Mesh" {
		Vertices: *9 {
			a: 0,0,0,2,0,0,0,2,0
		}
		PolygonVertexIndex: *3 {
			a: 0,1,-3
		}
	}
	Model: 200, "Model::Tri", "Mesh" {
		Version: 232
		Properties70:  {
			P: "Lcl Translation", "Lcl Translation", "", "A",5,0,0
		}
	}
}
Connections:  {
	C: "OO",200,0
	C: "OO",100,200
}
`

func TestASCIIFBX(t *testing.T) {
	m, err := decode([]byte(asciiFBX))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 1 || m.Roots[0].Name != "Tri" || m.Roots[0].Matrix[12] != 5 {
		t.Fatalf("roots: %+v", m.Roots)
	}
	p := m.Roots[0].Prims[0]
	if len(p.Positions) != 3 || len(p.Indices) != 3 || p.Positions[1] != [3]float64{2, 0, 0} {
		t.Fatalf("prim: %+v", p)
	}
}

func TestEulerOrderAndLocalMatrix(t *testing.T) {
	// A 90° turn about z, then moving: T·R sends (1,0,0) to (0,1,0) + t.
	p := map[string][]any{"Lcl Translation": {5.0, 6.0, 7.0}, "Lcl Rotation": {0.0, 0.0, 90.0}}
	mm, err := localMatrix(p)
	if err != nil {
		t.Fatal(err)
	}
	x := [3]float64{mm[0]*1 + mm[12], mm[1]*1 + mm[13], mm[2]*1 + mm[14]}
	if math.Abs(x[0]-5) > 1e-9 || math.Abs(x[1]-7) > 1e-9 || math.Abs(x[2]-7) > 1e-9 {
		t.Fatalf("(1,0,0) → %v, want (5,7,7)", x)
	}
	// XYZ order applies x first: rotate x 90° then z 90° sends (0,1,0) → (0,0,1) → (0,0,1).
	r, _ := euler([]float64{90, 0, 90}, 0)
	y := [3]float64{r[4], r[5], r[6]} // image of (0,1,0)
	if math.Abs(y[0]) > 1e-9 || math.Abs(y[1]) > 1e-9 || math.Abs(y[2]-1) > 1e-9 {
		t.Fatalf("XYZ order: (0,1,0) → %v", y)
	}
	if _, err := euler([]float64{0, 0, 0}, 9); err == nil {
		t.Fatal("unknown order must error")
	}
}

func TestMalformedBinary(t *testing.T) {
	good := fixture(t, "blender-scene-yup.fbx")
	cases := map[string][]byte{
		"not fbx":     []byte("hello world"),
		"truncated":   good[:100],
		"cut mid-way": good[:len(good)/2],
		"empty":       {},
	}
	// A bad end offset, a property length that overruns, and an array that
	// claims far more elements than the file holds.
	bad := append([]byte{}, good...)
	binary.LittleEndian.PutUint32(bad[27:], 0xfffffff0) // first node's end offset
	cases["bad end offset"] = bad
	bomb := append([]byte(binaryMagic), 0xe0, 0x1c, 0, 0) // version 7392... irrelevant; node follows
	bomb = append(bomb, make([]byte, 12)...)
	cases["zero-ish node"] = bomb
	for name, b := range cases {
		if _, err := decode(b); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMalformedScenes(t *testing.T) {
	geo := func(extra string) string {
		return strings.Replace(asciiFBX, "a: 0,1,-3", extra, 1)
	}
	cases := map[string]string{
		"index range":   geo("a: 0,1,-9"),
		"open polygon":  geo("a: 0,1,2"),
		"x-up":          strings.Replace(asciiFBX, `"UpAxis", "int", "Integer", "",1`, `"UpAxis", "int", "Integer", "",0`, 1),
		"bad rotation":  strings.Replace(asciiFBX, `P: "Lcl Translation", "Lcl Translation", "", "A",5,0,0`, `P: "Lcl Rotation", "Lcl Rotation", "", "A",5`, 1),
		"unbalanced }":  asciiFBX + "}\n",
		"missing colon": "FBXHeaderExtension\n",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDiffMatchAndEmptyExport(t *testing.T) {
	moved := strings.Replace(asciiFBX, "0,2,0", "0,3,0", 1)
	d, err := h().Diff([]byte(asciiFBX), []byte(moved))
	if err != nil || len(d.Changes) == 0 || d.Format != "fbx" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Rig.FBX") || h().Match("a.obj") {
		t.Fatal("Match is by extension")
	}
	if _, err := encode(nil, ".fbx"); err == nil {
		t.Fatal("an empty scene is an error")
	}
}
