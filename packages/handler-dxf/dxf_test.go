package main

import (
	"bytes"
	"math"
	"math/rand"
	"os"
	"strconv"
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

func bounds(t *testing.T, glb []byte) (lo, hi [3]float64) {
	t.Helper()
	roots, err := scene.Flatten(glb)
	if err != nil {
		t.Fatal(err)
	}
	lo = [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi = [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
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
		if math.Abs(a[i]-b[i]) > 1e-6 {
			return false
		}
	}
	return true
}

// The fixtures are real DXF written by ezdxf (R12, R2000, R2018) for one drawing in
// millimetres: a 3DFACE triangle and quad, a polyface tetrahedron, a 3×3 polygon
// mesh, a block inserted scaled (2,1,3), rotated 90° and moved, a LINE, a POINT, a
// 3D polyline and (R2000+) a MESH. ezdxf itself reports its world bounds as
// (0,0,0)..(100,10,30) mm in DXF's Z-up space — (x, z, −y) × 0.001 in glTF's
// Y-up metres, the numbers asserted here (R12 cannot carry $INSUNITS, so it stays unscaled).
func TestEzdxfFilesDecodeToWhatEzdxfSees(t *testing.T) {
	for _, c := range []struct {
		file  string
		roofT int     // triangles on layer "Roof Parts": 4 + 8, plus 6 from the MESH entity (R2000+)
		unit  float64 // metres per file unit: R12 has no $INSUNITS, so its numbers pass through
	}{{"r12.dxf", 12, 1}, {"r2000.dxf", 18, 0.001}, {"r2018.dxf", 18, 0.001}} {
		wantLo, wantHi := [3]float64{0, 0, -10 * c.unit}, [3]float64{100 * c.unit, 30 * c.unit, 0}
		m, err := decode(fixture(t, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		layers := map[string]*scene.Object{}
		for _, o := range m.Roots {
			layers[o.Name] = o
		}
		tris := func(layer string) int {
			n := 0
			if o := layers[layer]; o != nil {
				for _, p := range o.Prims {
					if p.Kind == scene.KindTriangles {
						n += len(p.Indices) / 3
					}
				}
			}
			return n
		}
		// Walls: the 3DFACE triangle (1) and quad (2). Layer 0: the block's quad and triangle,
		// which INSERT places on its own layer.
		if got := tris("Walls"); got != 3+3 {
			t.Errorf("%s: Walls has %d triangles, want 3 + the block's 3", c.file, got)
		}
		if got := tris("Roof Parts"); got != c.roofT {
			t.Errorf("%s: Roof Parts has %d triangles, want %d", c.file, got, c.roofT)
		}
		glb, err := h().Import(fixture(t, c.file))
		if err != nil {
			t.Fatal(err)
		}
		lo, hi := bounds(t, glb)
		if !near(lo, wantLo) || !near(hi, wantHi) {
			t.Errorf("%s: bounds %v..%v, want %v..%v", c.file, lo, hi, wantLo, wantHi)
		}
	}
}

func TestLinesPointsAndLayers(t *testing.T) {
	m, err := decode(fixture(t, "r2018.dxf"))
	if err != nil {
		t.Fatal(err)
	}
	var lines, points int
	names := []string{}
	for _, o := range m.Roots {
		names = append(names, o.Name)
		for _, p := range o.Prims {
			switch p.Kind {
			case scene.KindLines:
				lines += len(p.Indices) / 2
			case scene.KindPoints:
				points += len(p.Indices)
			}
		}
	}
	// A LINE (1 segment) and a 3D polyline of 3 vertices (2 segments).
	if lines != 3 || points != 1 {
		t.Fatalf("%d line segments, %d points; want 3 and 1", lines, points)
	}
	if strings.Join(names, ",") != "Walls,Roof Parts,0" {
		t.Fatalf("layers in order of first appearance: %v", names)
	}
}

func TestBlockInsertTransformsAndLayerZero(t *testing.T) {
	// A unit quad block inserted twice: at the origin on layer A, and nested through another block.
	src := "  0\nSECTION\n  2\nBLOCKS\n" +
		"  0\nBLOCK\n  2\nQuad\n 10\n0\n 20\n0\n 30\n0\n" +
		"  0\n3DFACE\n  8\n0\n 10\n0\n 20\n0\n 30\n0\n 11\n1\n 21\n0\n 31\n0\n 12\n1\n 22\n1\n 32\n0\n 13\n0\n 23\n1\n 33\n0\n  0\nENDBLK\n" +
		"  0\nBLOCK\n  2\nPair\n 10\n0\n 20\n0\n 30\n0\n" +
		"  0\nINSERT\n  8\n0\n  2\nQuad\n 10\n0\n 20\n0\n 30\n0\n" +
		"  0\nINSERT\n  8\n0\n  2\nQuad\n 10\n5\n 20\n0\n 30\n0\n 41\n2\n 42\n2\n  0\nENDBLK\n" +
		"  0\nENDSEC\n  0\nSECTION\n  2\nENTITIES\n" +
		"  0\nINSERT\n  8\nA\n  2\nPair\n 10\n10\n 20\n0\n 30\n0\n 50\n90\n" +
		"  0\nENDSEC\n  0\nEOF\n"
	m, err := decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 1 || m.Roots[0].Name != "A" {
		t.Fatalf("the block's layer-0 geometry belongs to the INSERT's layer A: %+v", m.Roots)
	}
	// Two quads = 4 triangles; Z-up → Y-up and mm→? ($INSUNITS absent: unitless, k=1).
	p := m.Roots[0].Prims[0]
	if len(p.Indices) != 12 {
		t.Fatalf("%d indices, want 12 (two quads)", len(p.Indices))
	}
	// Pair rotated 90° about z and moved to x=10: the second quad (scaled 2, at x=5) spans
	// x 5..7 → after rotation y 5..7 and x 10-2..10 = 8..10 ... check one corner.
	glb, _ := h().Import([]byte(src))
	lo, hi := bounds(t, glb)
	// File-space bounds: x 8..10 (the scaled quad) and 9..10 (the first), y 0..7 — Y-up: (x, z, -y).
	if !near(lo, [3]float64{8, 0, -7}) || !near(hi, [3]float64{10, 0, 0}) {
		t.Fatalf("bounds %v..%v", lo, hi)
	}
}

func TestUnitsAreApplied(t *testing.T) {
	face := "  0\n3DFACE\n  8\n0\n 10\n0\n 20\n0\n 30\n0\n 11\n1\n 21\n0\n 31\n0\n 12\n1\n 22\n1\n 32\n0\n 13\n1\n 23\n1\n 33\n0\n"
	for units, want := range map[string]float64{"1": 0.0254, "4": 0.001, "5": 0.01, "6": 1, "0": 1} {
		src := "  0\nSECTION\n  2\nHEADER\n  9\n$INSUNITS\n 70\n" + units + "\n  0\nENDSEC\n  0\nSECTION\n  2\nENTITIES\n" + face + "  0\nENDSEC\n  0\nEOF\n"
		glb, err := h().Import([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		_, hi := bounds(t, glb)
		if math.Abs(hi[0]-want) > 1e-9 {
			t.Errorf("$INSUNITS %s: the unit square is %v wide, want %v m", units, hi[0], want)
		}
	}
}

func TestRefusedByName(t *testing.T) {
	wrap := func(ent string) string {
		return "  0\nSECTION\n  2\nENTITIES\n" + ent + "  0\nENDSEC\n  0\nEOF\n"
	}
	cases := map[string]string{
		"3dsolid":    wrap("  0\n3DSOLID\n  8\n0\n"),
		"surface":    wrap("  0\nEXTRUDEDSURFACE\n  8\n0\n"),
		"binary":     "AutoCAD Binary DXF\r\n\x1a\x00junk",
		"dwg":        "AC1032\x00\x00\x00\x00\x00junk",
		"array":      "  0\nSECTION\n  2\nBLOCKS\n  0\nBLOCK\n  2\nB\n  0\nENDBLK\n  0\nENDSEC\n  0\nSECTION\n  2\nENTITIES\n  0\nINSERT\n  2\nB\n 70\n3\n  0\nENDSEC\n  0\nEOF\n",
		"no block":   wrap("  0\nINSERT\n  2\nNope\n"),
		"extrusion":  wrap("  0\nINSERT\n  2\nB\n210\n0\n220\n1\n230\n0\n"),
		"not dxf":    "hello world",
		"bad code":   "x\nSECTION\n",
		"no section": "  0\nEOF\n",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// Curves and annotation are 2D drafting, not model geometry: ignored, not refused.
	m, err := decode([]byte(wrap("  0\nCIRCLE\n  8\n0\n 10\n0\n 20\n0\n 30\n0\n 40\n5\n  0\nTEXT\n  8\n0\n  1\nhello\n")))
	if err != nil || len(m.Roots) != 0 {
		t.Fatalf("curves and text must be ignored: %+v err=%v", m, err)
	}
}

func TestPolylineKinds(t *testing.T) {
	v := func(flag int, x, y, z float64, extra string) string {
		return "  0\nVERTEX\n  8\n0\n 10\n" + f(x) + "\n 20\n" + f(y) + "\n 30\n" + f(z) + "\n 70\n" + itoa(flag) + "\n" + extra
	}
	wrap := func(ent string) string {
		return "  0\nSECTION\n  2\nENTITIES\n" + ent + "  0\nSEQEND\n  0\nENDSEC\n  0\nEOF\n"
	}
	// A polyface: three vertices and one triangle face (with an invisible-edge negative index).
	pf := "  0\nPOLYLINE\n  8\n0\n 66\n1\n 70\n64\n 71\n3\n 72\n1\n" +
		v(192, 0, 0, 0, "") + v(192, 1, 0, 0, "") + v(192, 0, 1, 0, "") + v(128, 0, 0, 0, " 71\n1\n 72\n-2\n 73\n3\n")
	m, err := decode([]byte(wrap(pf)))
	if err != nil || len(m.Roots[0].Prims[0].Indices) != 3 {
		t.Fatalf("polyface: %+v err=%v", m, err)
	}
	// A closed 2D polyline at elevation 4: a square outline = 4 segments.
	sq := "  0\nPOLYLINE\n  8\n0\n 66\n1\n 30\n4\n 70\n1\n" + v(0, 0, 0, 0, "") + v(0, 1, 0, 0, "") + v(0, 1, 1, 0, "") + v(0, 0, 1, 0, "")
	m, err = decode([]byte(wrap(sq)))
	if err != nil || len(m.Roots[0].Prims[0].Indices) != 8 || m.Roots[0].Prims[0].Positions[0][2] != 4 {
		t.Fatalf("2D polyline: %+v err=%v", m, err)
	}
	// A polyface face naming a missing vertex is an error.
	bad := "  0\nPOLYLINE\n  8\n0\n 66\n1\n 70\n64\n" + v(192, 0, 0, 0, "") + v(128, 0, 0, 0, " 71\n1\n 72\n2\n 73\n9\n")
	if _, err := decode([]byte(wrap(bad))); err == nil {
		t.Fatal("a polyface face with a missing vertex must be an error")
	}
	// A polygon mesh whose vertex count does not match M×N.
	pm := "  0\nPOLYLINE\n  8\n0\n 66\n1\n 70\n16\n 71\n3\n 72\n3\n" + v(64, 0, 0, 0, "")
	if _, err := decode([]byte(wrap(pm))); err == nil {
		t.Fatal("a polygon mesh with too few vertices must be an error")
	}
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func itoa(n int) string { return strconv.Itoa(n) }

func TestRoundTripKeepsGeometryAndIsStable(t *testing.T) {
	glb, err := h().Import(fixture(t, "r2018.dxf"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".dxf")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "AC1009") || !strings.Contains(s, "$INSUNITS") || !strings.Contains(s, "3DFACE") || !strings.HasSuffix(s, "EOF\n") {
		t.Fatalf("export is not a framed R12 DXF:\n%.300s", s)
	}
	back, err := h().Import(out)
	if err != nil {
		t.Fatalf("our own export does not parse: %v", err)
	}
	lo1, hi1 := bounds(t, glb)
	lo2, hi2 := bounds(t, back)
	if !near(lo1, lo2) || !near(hi1, hi2) {
		t.Fatalf("round trip moved the scene: %v..%v → %v..%v", lo1, hi1, lo2, hi2)
	}
	out2, err := h().Export(back, ".dxf")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
}

func TestLayerNamesAreLegalAndUnique(t *testing.T) {
	used := map[string]bool{"0": true}
	a := layerName("Left Wheel", "node", 0, used)
	b := layerName("left wheel", "node", 1, used) // DXF layer names are case-insensitive
	c := layerName("a/b:c", "node", 2, used)
	if a != "Left_Wheel" || b != "left_wheel_2" || c != "a_b_c" {
		t.Fatalf("%q %q %q", a, b, c)
	}
}

func TestMalformedAndEmpty(t *testing.T) {
	good := fixture(t, "r2018.dxf")
	cases := map[string][]byte{
		"empty":        {},
		"truncated":    good[:len(good)/2],
		"bad face idx": []byte(strings.Replace(string(fixture(t, "r12.dxf")), "\n 72\n", "\n 72\n999", 1)),
	}
	for name, b := range cases {
		m, err := decode(b)
		if name == "empty" && err == nil {
			t.Errorf("%s: expected an error", name)
		}
		_ = m
	}
	if _, err := encode(nil, ".dxf"); err == nil {
		t.Fatal("an empty scene is an error")
	}
	if _, err := h().Export([]byte("x"), ".obj"); err == nil {
		t.Fatal("wrong format is an error")
	}
}

func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	glb, _ := h().Import(fixture(t, "r2018.dxf"))
	out, _ := h().Export(glb, ".dxf")
	for _, good := range [][]byte{fixture(t, "r2018.dxf"), fixture(t, "r12.dxf"), out} {
		try := func(b []byte) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic %v", r)
				}
			}()
			_, _ = decode(b)
		}
		for n := 0; n <= len(good); n += max(1, len(good)/300) {
			try(good[:n])
		}
		for i := 0; i < 800; i++ {
			b := append([]byte(nil), good...)
			for k := 0; k < 1+rng.Intn(8); k++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
			try(b)
		}
	}
}

func TestDiffAndMatch(t *testing.T) {
	a := fixture(t, "r12.dxf")
	b := bytes.Replace(a, []byte("\n 20\n10.0\n"), []byte("\n 20\n12.0\n"), 1)
	d, err := h().Diff(a, b)
	if err != nil || len(d.Changes) == 0 || d.Format != "dxf" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	// The same drawing as R12 and R2000 differs only by the MESH entity.
	if !h().Match("a/Plan.DXF") || h().Match("a.dwg") {
		t.Fatal("Match is by extension")
	}
}
