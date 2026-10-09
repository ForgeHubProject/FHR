package main

import (
	"archive/zip"
	"bytes"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

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

// The fixtures are real Blender 5.2 exports of one scene — text, binary crate
// and package — which Blender itself reads as spanning (-3.5, -2.232, -1) ..
// (3.866, 2.232, 3.5) in its Z-up space, i.e. (x, z, -y) in glTF's Y-up.
func TestBlenderExportsAllDecodeToTheSameScene(t *testing.T) {
	wantLo, wantHi := [3]float64{-3.5, -1, -2.232}, [3]float64{3.866, 3.5, 2.232}
	var first []byte
	for _, name := range []string{"blender-scene.usda", "blender-scene.usdc", "blender-scene.usdz"} {
		glb, err := h().Import(fixture(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lo, hi, prims := bounds(t, glb)
		if prims != 3 || !near(lo, wantLo) || !near(hi, wantHi) {
			t.Errorf("%s: %d prims, bounds %v..%v; want 3, %v..%v", name, prims, lo, hi, wantLo, wantHi)
		}
		// Text and binary are the same layer, so the same scene to the byte.
		if first == nil {
			first = glb
		} else if !bytes.Equal(first, glb) {
			t.Errorf("%s decodes to a different scene than the .usda", name)
		}
	}
}

func TestDecodeHierarchyMaterialAndBinding(t *testing.T) {
	m, err := decode(fixture(t, "blender-scene.usdc"))
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
	cube := names["Cube"]
	if cube == nil || len(cube.Prims) != 1 || cube.Prims[0].Material != "Red" || cube.Prims[0].BaseColor == nil || cube.Prims[0].BaseColor[0] != 1 {
		t.Fatalf("the cube must be bound to the red material: %+v", cube)
	}
	if names["Icosphere"].Prims[0].Material != "" {
		t.Fatal("the sphere has no binding")
	}
	if names["_materials"] != nil {
		t.Fatal("a Scope that only holds materials leaves no trace")
	}
}

func TestExportRoundTripAndStability(t *testing.T) {
	glb, err := h().Import(fixture(t, "blender-scene.usda"))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{".usda", ".usd", ".usdz"} {
		out, err := h().Export(glb, format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		back, err := h().Import(out)
		if err != nil {
			t.Fatalf("%s: our own export does not parse: %v", format, err)
		}
		lo1, hi1, _ := bounds(t, glb)
		lo2, hi2, prims := bounds(t, back)
		if prims != 3 || !near(lo1, lo2) || !near(hi1, hi2) {
			t.Errorf("%s moved the scene: %v..%v → %v..%v", format, lo1, hi1, lo2, hi2)
		}
		out2, err := h().Export(back, format)
		if err != nil || !bytes.Equal(out, out2) {
			t.Errorf("%s export is not stable across import/export (err=%v)", format, err)
		}
	}
}

func TestUSDZIsStoredAndAligned(t *testing.T) {
	out, err := usdz("scene.usda", []byte("#usda 1.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	f := zr.File[0]
	if f.Method != zip.Store {
		t.Fatal("usdz entries must be stored, not compressed")
	}
	off, err := f.DataOffset()
	if err != nil || off%64 != 0 {
		t.Fatalf("data offset %d (err=%v) is not 64-byte aligned", off, err)
	}
	// Every name length must align, including the awkward 1–3 byte paddings.
	for n := 1; n < 70; n++ {
		name := strings.Repeat("a", n) + ".usda"
		o, err := usdz(name, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		z, err := zip.NewReader(bytes.NewReader(o), int64(len(o)))
		if err != nil {
			t.Fatalf("name length %d: %v", n, err)
		}
		if off, _ := z.File[0].DataOffset(); off%64 != 0 {
			t.Fatalf("name length %d: offset %d", n, off)
		}
	}
}

func TestWritingTheCrateIsRefused(t *testing.T) {
	glb, _ := h().Import(fixture(t, "blender-scene.usda"))
	if _, err := h().Export(glb, ".usdc"); err == nil || !strings.Contains(err.Error(), "binary USD") {
		t.Fatalf("got %v", err)
	}
}

const layerHead = "#usda 1.0\n(\n    metersPerUnit = 1\n    upAxis = \"Y\"\n)\n"

func meshBody(extra string) string {
	return `    def Mesh "m"
    {
        int[] faceVertexCounts = [4]
        int[] faceVertexIndices = [0, 1, 2, 3]
        point3f[] points = [(0, 0, 0), (1, 0, 0), (1, 1, 0), (0, 1, 0)]
` + extra + `    }
`
}

func TestXformOps(t *testing.T) {
	src := layerHead + `def Xform "a"
{
    double3 xformOp:translate = (10, 0, 0)
    float3 xformOp:rotateXYZ = (0, 0, 90)
    float3 xformOp:scale = (2, 2, 2)
    uniform token[] xformOpOrder = ["xformOp:translate", "xformOp:rotateXYZ", "xformOp:scale"]
` + meshBody("") + `}
`
	glb, err := h().Import([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// (1,0,0): scale → (2,0,0), rotate 90° about z → (0,2,0), translate → (10,2,0).
	roots, _ := scene.Flatten(glb)
	got := roots[0].Children[0].Prims[0].Positions[1]
	if math.Abs(got[0]-10) > 1e-6 || math.Abs(got[1]-2) > 1e-6 {
		t.Fatalf("(1,0,0) → %v, want (10,2,0)", got)
	}

	inv := layerHead + `def Xform "a"
{
    double3 xformOp:translate = (5, 0, 0)
    uniform token[] xformOpOrder = ["xformOp:translate", "!invert!xformOp:translate"]
` + meshBody("") + `}
`
	glb, err = h().Import([]byte(inv))
	if err != nil {
		t.Fatal(err)
	}
	roots, _ = scene.Flatten(glb)
	if p := roots[0].Children[0].Prims[0].Positions[1]; p != [3]float64{1, 0, 0} {
		t.Fatalf("a translate followed by its inverse is the identity: %v", p)
	}

	mat := layerHead + `def Xform "a"
{
    matrix4d xformOp:transform = ( (1, 0, 0, 0), (0, 1, 0, 0), (0, 0, 1, 0), (7, 8, 9, 1) )
    uniform token[] xformOpOrder = ["xformOp:transform"]
` + meshBody("") + `}
`
	glb, err = h().Import([]byte(mat))
	if err != nil {
		t.Fatal(err)
	}
	roots, _ = scene.Flatten(glb)
	if p := roots[0].Children[0].Prims[0].Positions[0]; p != [3]float64{7, 8, 9} {
		t.Fatalf("a row-vector matrix puts the translation in the last row: %v", p)
	}
}

func TestUnitsAndUpAxis(t *testing.T) {
	cm := "#usda 1.0\n(\n    metersPerUnit = 0.01\n    upAxis = \"Z\"\n)\ndef Xform \"a\"\n{\n" + meshBody("") + "}\n"
	glb, err := h().Import([]byte(cm))
	if err != nil {
		t.Fatal(err)
	}
	lo, hi, _ := bounds(t, glb)
	// 1 cm square in Z-up: x 0..0.01, y 0..0.01 → Y-up (x, z, -y): z spans -0.01..0.
	if !near(lo, [3]float64{0, 0, -0.01}) || !near(hi, [3]float64{0.01, 0, 0}) {
		t.Fatalf("bounds %v..%v", lo, hi)
	}
}

func TestMeshFeatures(t *testing.T) {
	// Holes, subsets with their own binding, left-handed winding.
	src := layerHead + `def Xform "a"
{
    def Mesh "m"
    {
        int[] faceVertexCounts = [3, 3, 3]
        int[] faceVertexIndices = [0, 1, 2, 0, 2, 3, 0, 3, 1]
        int[] holeIndices = [2]
        uniform token orientation = "leftHanded"
        point3f[] points = [(0, 0, 0), (1, 0, 0), (1, 1, 0), (0, 1, 0)]
        def GeomSubset "second"
        {
            uniform token elementType = "face"
            int[] indices = [1]
            rel material:binding = </a/mats/Blue>
        }
    }
    def Scope "mats"
    {
        def Material "Blue"
        {
            def Shader "s"
            {
                uniform token info:id = "UsdPreviewSurface"
                color3f inputs:diffuseColor = (0, 0, 1)
            }
        }
    }
}
`
	m, err := decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prims := m.Roots[0].Children[0].Prims
	if len(prims) != 2 {
		t.Fatalf("the subset splits the mesh in two: %d prims", len(prims))
	}
	if prims[0].Material != "" || prims[1].Material != "Blue" || prims[1].BaseColor[2] != 1 {
		t.Fatalf("materials: %q, %q", prims[0].Material, prims[1].Material)
	}
	if len(prims[0].Indices)+len(prims[1].Indices) != 6 { // the hole removed face 2
		t.Fatalf("holes: %d + %d indices", len(prims[0].Indices), len(prims[1].Indices))
	}
	// Left-handed: face 0 (0,1,2) is wound (2,1,0).
	if p := prims[0].Positions; p[0] != [3]float64{1, 1, 0} {
		t.Fatalf("left-handed face must be reversed, first vertex %v", p[0])
	}
}

func TestCubeAndPoints(t *testing.T) {
	src := layerHead + `def Cube "c"
{
    double size = 4
}
def Points "p"
{
    point3f[] points = [(0, 0, 0), (1, 1, 1)]
}
`
	m, err := decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 2 || len(m.Roots[0].Prims[0].Indices) != 36 || m.Roots[1].Prims[0].Kind != scene.KindPoints {
		t.Fatalf("roots: %+v", m.Roots)
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range m.Roots[0].Prims[0].Positions {
		lo, hi = math.Min(lo, v[0]), math.Max(hi, v[0])
	}
	if hi-lo != 4 {
		t.Fatalf("a cube of size 4 spans 4, got %v", hi-lo)
	}
}

func TestTimeSamplesUseTheFirst(t *testing.T) {
	src := layerHead + `def Xform "a"
{
    double3 xformOp:translate.timeSamples = { 1: (3, 0, 0), 2: (9, 0, 0) }
    uniform token[] xformOpOrder = ["xformOp:translate"]
` + meshBody("") + `}
`
	glb, err := h().Import([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	roots, _ := scene.Flatten(glb)
	if p := roots[0].Children[0].Prims[0].Positions[0]; p[0] != 3 {
		t.Fatalf("the first sample is taken, got %v", p)
	}
}

func TestUnsupportedIsRefusedByName(t *testing.T) {
	cases := map[string]string{
		"reference":     layerHead + "def Xform \"a\" (\n    references = @other.usd@</x>\n)\n{\n}\n",
		"variantSet":    layerHead + "def Xform \"a\"\n{\n    variantSet \"v\" = {\n        \"x\" {\n        }\n    }\n}\n",
		"sphere":        layerHead + "def Sphere \"s\"\n{\n    double radius = 1\n}\n",
		"curves":        layerHead + "def BasisCurves \"c\"\n{\n}\n",
		"sublayers":     "#usda 1.0\n(\n    subLayers = [@a.usd@]\n)\n",
		"x-up":          "#usda 1.0\n(\n    upAxis = \"X\"\n)\ndef Xform \"a\"\n{\n}\n",
		"bad xform":     layerHead + "def Xform \"a\"\n{\n    uniform token[] xformOpOrder = [\"xformOp:bogus\"]\n}\n",
		"reset":         layerHead + "def Xform \"a\"\n{\n    uniform token[] xformOpOrder = [\"!resetXformStack!\"]\n}\n",
		"counts":        layerHead + "def Mesh \"m\"\n{\n    int[] faceVertexCounts = [3]\n    int[] faceVertexIndices = [0, 1]\n    point3f[] points = [(0,0,0),(1,0,0),(0,1,0)]\n}\n",
		"index range":   layerHead + "def Mesh \"m\"\n{\n    int[] faceVertexCounts = [3]\n    int[] faceVertexIndices = [0, 1, 9]\n    point3f[] points = [(0,0,0),(1,0,0),(0,1,0)]\n}\n",
		"no header":     "def Xform \"a\"\n{\n}\n",
		"unterminated":  layerHead + "def Xform \"a\"\n{\n",
		"bad string":    layerHead + "def Xform \"a\n{\n}\n",
		"junk":          "hello",
		"not a package": "PKnot a zip",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := decode([]byte(layerHead + "over \"ext\"\n{\n}\ndef Xform \"a\"\n{\n}\n")); err != nil {
		t.Errorf("an over with nothing to override is skipped, not an error: %v", err)
	}
}

func TestDiffMatchAndEmpty(t *testing.T) {
	a := fixture(t, "blender-scene.usda")
	b := bytes.Replace(a, []byte("(1, 1, -1), (1, 1, 1)]"), []byte("(1, 1, -1), (1, 5, 1)]"), 1)
	d, err := h().Diff(a, b)
	if err != nil || len(d.Changes) == 0 || d.Format != "usd" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	// The same scene as text and as binary diffs as no change at all.
	d, err = h().Diff(a, fixture(t, "blender-scene.usdc"))
	if err != nil || len(d.Changes) != 0 {
		t.Fatalf("usda vs usdc: %+v err=%v", d.Changes, err)
	}
	for _, p := range []string{"a.usd", "a.USDA", "a.usdc", "a.usdz"} {
		if !h().Match(p) {
			t.Errorf("Match(%q)", p)
		}
	}
	if h().Match("a.obj") {
		t.Fatal("Match is by extension")
	}
}

// Malformed and hostile input must come back as errors, never panics: this runs
// in a server-side wasm worker.
func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, name := range []string{"blender-scene.usdc", "blender-scene.usda", "blender-scene.usdz"} {
		good := fixture(t, name)
		try := func(b []byte) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panic %v", name, r)
				}
			}()
			_, _ = decode(b)
		}
		for n := 0; n < len(good); n += max(1, len(good)/200) { // truncations
			try(good[:n])
		}
		for i := 0; i < 400; i++ { // corruptions
			b := append([]byte(nil), good...)
			for k := 0; k < 1+rng.Intn(6); k++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
			try(b)
		}
	}
}

func TestLZ4(t *testing.T) {
	// "abcabcabcabc": literals "abc" then a match of offset 3, length 9.
	src := []byte{0x35, 'a', 'b', 'c', 0x03, 0x00}
	// token: 3 literals, match length 5+4=9; ends after the match → needs a final literal run? A
	// valid block ends with literals, so append an empty last sequence.
	src = append(src, 0x00)
	got, err := lz4Block(src, 12)
	if err != nil || string(got) != "abcabcabcabc" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := lz4Block(src, 13); err == nil {
		t.Fatal("a wrong expected size is an error")
	}
	if _, err := lz4Block([]byte{0x10, 'a', 0x09, 0x00}, 100); err == nil {
		t.Fatal("an offset before the start of the output is an error")
	}
}
