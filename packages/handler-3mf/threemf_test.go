package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func pkg(t *testing.T, model string) []byte {
	t.Helper()
	b, err := zipParts([]part{{"3D/3dmodel.model", model}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func h() *scene.CodecHandler { return Codec.Handler() }

const assembly = `<?xml version="1.0"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources>
  <basematerials id="5"><base name="Red" displaycolor="#FF0000"/><base name="Blue" displaycolor="#0000FF80"/></basematerials>
  <object id="1" name="Plate" pid="5" pindex="0"><mesh>
   <vertices><vertex x="0" y="0" z="0"/><vertex x="10" y="0" z="0"/><vertex x="10" y="10" z="0"/><vertex x="0" y="10" z="0"/></vertices>
   <triangles><triangle v1="0" v2="1" v3="2"/><triangle v1="0" v2="2" v3="3" p1="1"/></triangles>
  </mesh></object>
  <object id="2" name="Pair"><components>
   <component objectid="1"/>
   <component objectid="1" transform="1 0 0 0 1 0 0 0 1 20 0 0"/>
  </components></object>
 </resources>
 <build><item objectid="2"/></build>
</model>`

func TestDecodeAssemblyMaterialsAndTransforms(t *testing.T) {
	m, err := decode(pkg(t, assembly))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 1 || m.Roots[0].Name != "Pair" || len(m.Roots[0].Children) != 2 {
		t.Fatalf("want one Pair root with two children, got %+v", m.Roots)
	}
	// The 20 mm offset, in metres: a node's own placement is converted, not only
	// the world-space size, so a diff of it reads in real units.
	second := m.Roots[0].Children[1]
	if second.Matrix == nil || math.Abs(second.Matrix[12]-0.02) > 1e-12 {
		t.Fatalf("component transform lost: %v", second.Matrix)
	}
	plate := m.Roots[0].Children[0]
	if len(plate.Prims) != 2 {
		t.Fatalf("two triangles with two materials are two primitives, got %d", len(plate.Prims))
	}
	if plate.Prims[0].Material != "Red" || plate.Prims[1].Material != "Blue" || plate.Prims[1].BaseColor[3] < 0.49 || plate.Prims[1].BaseColor[3] > 0.51 {
		t.Fatalf("materials: %+v %+v", plate.Prims[0], plate.Prims[1])
	}
	if plate.Prims[0].BaseColor[0] != 1 || plate.Prims[0].BaseColor[1] != 0 {
		t.Fatalf("red base colour: %v", plate.Prims[0].BaseColor)
	}
}

func TestExportThenImportKeepsTheScene(t *testing.T) {
	src := pkg(t, assembly)
	glb, err := h().Import(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	// Export bakes the component transforms, so compare geometry in world
	// space: the exported file's vertices must include both placements.
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := modelFile(zr)
	rc, _ := f.Open()
	data, _ := io.ReadAll(rc)
	for _, want := range []string{`x="30"`, `x="20"`, `name="Red"`, `displaycolor="#FF0000"`, `displaycolor="#0000FF80"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("exported model missing %s:\n%s", want, data)
		}
	}
	back, err := h().Export(mustImport(t, out), ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, back) {
		t.Fatal("export is not stable across a second import/export")
	}
}

func mustImport(t *testing.T, b []byte) []byte {
	t.Helper()
	g, err := h().Import(b)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDiffSeesAMovedVertex(t *testing.T) {
	moved := strings.Replace(assembly, `x="10" y="10"`, `x="12" y="10"`, 1)
	d, err := h().Diff(pkg(t, assembly), pkg(t, moved))
	if err != nil || len(d.Changes) == 0 {
		t.Fatalf("a moved vertex must show as a change: %+v err=%v", d.Changes, err)
	}
}

func TestMalformed(t *testing.T) {
	cases := map[string][]byte{
		"not a zip":    []byte("hello"),
		"no model":     mustZip(t, []part{{"x.txt", "hi"}}),
		"bad xml":      pkg(t, "<model><resources>"),
		"bad vertex":   pkg(t, `<model><resources><object id="1"><mesh><vertices><vertex x="0" y="0" z="0"/></vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object></resources><build><item objectid="1"/></build></model>`),
		"missing obj":  pkg(t, `<model><resources/><build><item objectid="9"/></build></model>`),
		"bad colour":   pkg(t, `<model><resources><basematerials id="1"><base name="x" displaycolor="red"/></basematerials></resources></model>`),
		"bad xform":    pkg(t, `<model><resources><object id="1"><mesh><vertices/><triangles/></mesh></object></resources><build><item objectid="1" transform="1 2 3"/></build></model>`),
		"cycle":        pkg(t, `<model><resources><object id="1"><components><component objectid="1"/></components></object></resources><build><item objectid="1"/></build></model>`),
		"bad unit":     pkg(t, strings.Replace(assembly, `unit="millimeter"`, `unit="parsec"`, 1)),
		"external ref": pkg(t, `<model><resources><object id="1"><components><component objectid="2" path="/3D/other.model"/></components></object></resources><build><item objectid="1"/></build></model>`),
	}
	for name, b := range cases {
		if _, err := decode(b); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func mustZip(t *testing.T, p []part) []byte {
	b, err := zipParts(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExportNeedsTriangles(t *testing.T) {
	if _, err := encode(nil, ".3mf"); err == nil {
		t.Fatal("an empty scene should be an error")
	}
	if _, err := h().Export([]byte("junk"), ".stl"); err == nil {
		t.Fatal("wrong format should be an error")
	}
}

func TestColourRoundTrip(t *testing.T) {
	for _, s := range []string{"#FF0000", "#12ABEF", "#00000080", "#FFFFFF"} {
		c, err := parseColor(s)
		if err != nil {
			t.Fatal(err)
		}
		if got := hexColor(c); got != s {
			t.Errorf("%s → %s", s, got)
		}
	}
}

// The production extension: a root model whose build items and components point,
// by path, into other .model parts of the package. Object and material ids are
// local to their part, so id 1 means two different things below.
const rootModel = `<?xml version="1.0"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
 <resources>
  <object id="1" name="Assembly"><components>
   <component objectid="1" p:path="/3D/Objects/plate.model" transform="1 0 0 0 1 0 0 0 1 5 0 0"/>
   <component objectid="1" p:path="/3D/Objects/plate.model" transform="1 0 0 0 1 0 0 0 1 20 0 0"/>
  </components></object>
 </resources>
 <build><item objectid="1"/><item objectid="1" p:path="/3D/Objects/plate.model"/></build>
</model>`

const plateModel = `<?xml version="1.0"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources>
  <basematerials id="1"><base name="Brass" displaycolor="#B5A642"/></basematerials>
  <object id="1" name="Plate" pid="1" pindex="0"><mesh>
   <vertices><vertex x="0" y="0" z="0"/><vertex x="10" y="0" z="0"/><vertex x="0" y="10" z="0"/></vertices>
   <triangles><triangle v1="0" v2="1" v3="2"/></triangles>
  </mesh></object>
 </resources>
 <build/>
</model>`

func multiPart(t *testing.T, root, plate string) []byte {
	t.Helper()
	b, err := zipParts([]part{
		{"3D/3dmodel.model", root},
		{"3D/Objects/plate.model", plate},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMultiPartPackage(t *testing.T) {
	m, err := decode(multiPart(t, rootModel, plateModel))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 2 || m.Roots[0].Name != "Assembly" || m.Roots[1].Name != "Plate" {
		t.Fatalf("roots: %+v", m.Roots)
	}
	asm := m.Roots[0]
	if len(asm.Children) != 2 || asm.Children[0].Name != "Plate" || math.Abs(asm.Children[1].Matrix[12]-0.02) > 1e-12 {
		t.Fatalf("components across parts: %+v", asm.Children)
	}
	// The material lives in the plate's part, not the root's.
	p := asm.Children[0].Prims[0]
	if p.Material != "Brass" || p.BaseColor == nil {
		t.Fatalf("material from the other part: %q %v", p.Material, p.BaseColor)
	}
	// An item pointing straight at a part's object builds it too.
	if len(m.Roots[1].Prims) != 1 {
		t.Fatalf("direct item: %+v", m.Roots[1])
	}
	// Exported, it is one flat model again, and reads back to the same scene.
	glb, err := h().Import(multiPart(t, rootModel, plateModel))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h().Import(out); err != nil {
		t.Fatalf("our export does not parse: %v", err)
	}
}

func TestMultiPartErrors(t *testing.T) {
	// A path to a part the package does not have.
	missing, _ := zipParts([]part{{"3D/3dmodel.model", rootModel}})
	if _, err := decode(missing); err == nil || !strings.Contains(err.Error(), "no model part") {
		t.Fatalf("missing part: %v", err)
	}
	// An object id the part does not define.
	if _, err := decode(multiPart(t, strings.Replace(rootModel, `objectid="1" p:path="/3D/Objects/plate.model" transform="1 0 0 0 1 0 0 0 1 5`, `objectid="9" p:path="/3D/Objects/plate.model" transform="1 0 0 0 1 0 0 0 1 5`, 1), plateModel)); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("missing object: %v", err)
	}
	// A cycle between parts: the plate's object is a component of the root's, which holds it.
	cycle := strings.Replace(plateModel, `<mesh>`, `<components><component objectid="1" p:path="/3D/3dmodel.model"/></components><mesh>`, 1)
	cycle = strings.Replace(cycle, `<model unit`, `<model xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" unit`, 1)
	if _, err := decode(multiPart(t, rootModel, cycle)); err == nil || !strings.Contains(err.Error(), "nest deeper") {
		t.Fatalf("a cycle across parts must be refused: %v", err)
	}
}

// cube is a 3MF model of one axis-aligned cube of the given edge, in `unit`; with
// unit "" the model states none, so the specification's millimetre applies.
func cube(unit string, edge float64) string {
	attr := ""
	if unit != "" {
		attr = ` unit="` + unit + `"`
	}
	var v strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&v, `<vertex x="%g" y="%g" z="%g"/>`, float64(i&1)*edge, float64(i>>1&1)*edge, float64(i>>2&1)*edge)
	}
	return `<?xml version="1.0"?>
<model` + attr + ` xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources><object id="1" name="Cube"><mesh><vertices>` + v.String() + `</vertices><triangles>
  <triangle v1="0" v2="2" v3="1"/><triangle v1="1" v2="2" v3="3"/><triangle v1="4" v2="5" v3="6"/><triangle v1="5" v2="7" v3="6"/>
  <triangle v1="0" v2="1" v3="4"/><triangle v1="1" v2="5" v3="4"/><triangle v1="2" v2="6" v3="3"/><triangle v1="3" v2="6" v3="7"/>
  <triangle v1="0" v2="4" v3="2"/><triangle v1="2" v2="4" v3="6"/><triangle v1="1" v2="3" v3="5"/><triangle v1="3" v2="7" v3="5"/>
 </triangles></mesh></object></resources>
 <build><item objectid="1"/></build>
</model>`
}

// bounds is the world-space box of everything a GLB draws.
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

// The bug this pins (FHR#103): a 20 mm cube came in as a 20 m one.
func TestDeclaredUnitBecomesMetres(t *testing.T) {
	for _, c := range []struct {
		unit string
		want float64 // the cube's edge in metres
	}{
		{"", 0.02}, {"millimeter", 0.02}, {"micron", 20e-6}, {"centimeter", 0.2},
		{"inch", 0.508}, {"foot", 6.096}, {"meter", 20}, {"MilliMeter", 0.02},
	} {
		lo, hi := bounds(t, mustImport(t, pkg(t, cube(c.unit, 20))))
		for i := 0; i < 3; i++ {
			if edge := hi[i] - lo[i]; math.Abs(edge-c.want) > c.want*1e-6 {
				t.Errorf("unit %q: edge %d is %g m, want %g", c.unit, i, edge, c.want)
			}
		}
	}
}

// A part's numbers are in that part's unit, and a component's transform in the
// unit of the part that writes it: a 10 cm plate placed 20 mm along x.
func TestMultiPartUnitsMix(t *testing.T) {
	plateCM := strings.Replace(plateModel, `unit="millimeter"`, `unit="centimeter"`, 1)
	m, err := decode(multiPart(t, rootModel, plateCM))
	if err != nil {
		t.Fatal(err)
	}
	glb, err := h().Import(multiPart(t, strings.Replace(rootModel, `<build><item objectid="1"/><item objectid="1" p:path="/3D/Objects/plate.model"/></build>`, `<build><item objectid="1"/></build>`, 1), plateCM))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 2 {
		t.Fatalf("roots: %+v", m.Roots)
	}
	lo, hi := bounds(t, glb)
	// Two plates, at 5 mm and 20 mm, each 10 cm wide, lying flat: 3MF's y is
	// glTF's −z.
	if math.Abs(lo[0]-0.005) > 1e-7 || math.Abs(hi[0]-0.12) > 1e-7 || math.Abs(lo[2]+0.1) > 1e-7 || hi[1] != 0 || lo[1] != 0 {
		t.Fatalf("bounds %v..%v, want x 0.005..0.12, z -0.1..0, flat at y 0", lo, hi)
	}
}

// Writing converts metres to millimetres, the unit slicers assume, so a
// transcode keeps physical size.
func TestExportWritesMillimetres(t *testing.T) {
	glb := mustImport(t, pkg(t, cube("inch", 20)))
	out, err := h().Export(glb, ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := modelFile(zr)
	data, _ := readPart(f)
	for _, want := range []string{`unit="millimeter"`, `x="508"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("export missing %s:\n%s", want, data)
		}
	}
	lo1, hi1 := bounds(t, glb)
	lo2, hi2 := bounds(t, mustImport(t, out))
	for i := 0; i < 3; i++ {
		if math.Abs(lo1[i]-lo2[i]) > 1e-6 || math.Abs(hi1[i]-hi2[i]) > 1e-6 {
			t.Fatalf("round trip changed the size: %v..%v → %v..%v", lo1, hi1, lo2, hi2)
		}
	}
}

// Millimetres → metres (float32 in the GLB) → millimetres must settle on the
// first export: the second import/export writes the same bytes.
func TestUnitConversionIsStable(t *testing.T) {
	r := rand.New(rand.NewSource(103))
	var v, tri strings.Builder
	for i := 0; i < 300; i++ {
		c := func() float64 { return float64(r.Intn(2_000_000)-1_000_000) / 1000 } // ±1000 mm, to the micron
		fmt.Fprintf(&v, `<vertex x="%g" y="%g" z="%g"/>`, c(), c(), c())
		if i >= 2 {
			fmt.Fprintf(&tri, `<triangle v1="%d" v2="%d" v3="%d"/>`, i-2, i-1, i)
		}
	}
	model := `<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="1"><mesh><vertices>` +
		v.String() + `</vertices><triangles>` + tri.String() + `</triangles></mesh></object></resources><build><item objectid="1"/></build></model>`
	out, err := h().Export(mustImport(t, pkg(t, model)), ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	back, err := h().Export(mustImport(t, out), ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, back) {
		t.Fatal("export is not stable across a second import/export")
	}
}

// The bracket from FHR#118: a 60 × 40 × 5 mm plate lying on the build plate
// with a 20 mm pin standing on it at (10, 10, 5). 3MF is Z-up; glTF is Y-up.
const bracket = `<?xml version="1.0"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources>
  <object id="1" name="Plate"><mesh><vertices>
   <vertex x="0" y="0" z="0"/><vertex x="60" y="0" z="0"/><vertex x="0" y="40" z="0"/><vertex x="60" y="40" z="0"/>
   <vertex x="0" y="0" z="5"/><vertex x="60" y="0" z="5"/><vertex x="0" y="40" z="5"/><vertex x="60" y="40" z="5"/>
  </vertices><triangles>
   <triangle v1="0" v2="2" v3="1"/><triangle v1="1" v2="2" v3="3"/><triangle v1="4" v2="5" v3="6"/><triangle v1="5" v2="7" v3="6"/>
   <triangle v1="0" v2="1" v3="4"/><triangle v1="1" v2="5" v3="4"/><triangle v1="2" v2="6" v3="3"/><triangle v1="3" v2="6" v3="7"/>
   <triangle v1="0" v2="4" v3="2"/><triangle v1="2" v2="4" v3="6"/><triangle v1="1" v2="3" v3="5"/><triangle v1="3" v2="7" v3="5"/>
  </triangles></mesh></object>
  <object id="2" name="Pin"><mesh><vertices>
   <vertex x="0" y="0" z="0"/><vertex x="6" y="0" z="0"/><vertex x="0" y="6" z="0"/><vertex x="0" y="0" z="20"/>
  </vertices><triangles>
   <triangle v1="0" v2="2" v3="1"/><triangle v1="0" v2="1" v3="3"/><triangle v1="0" v2="3" v3="2"/><triangle v1="1" v2="2" v3="3"/>
  </triangles></mesh></object>
  <object id="3" name="Bracket"><components>
   <component objectid="1"/>
   <component objectid="2" transform="1 0 0 0 1 0 0 0 1 10 10 5"/>
  </components></object>
 </resources>
 <build><item objectid="3"/></build>
</model>`

// FHR#118: the plate lies flat (its 5 mm is glTF's y), the pin stands up, and
// the pin's own placement is in Y-up too, not only the world.
func TestZUpBecomesYUp(t *testing.T) {
	m, err := decode(pkg(t, bracket))
	if err != nil {
		t.Fatal(err)
	}
	pin := m.Roots[0].Children[1]
	if got := [3]float64{pin.Matrix[12], pin.Matrix[13], pin.Matrix[14]}; math.Abs(got[0]-0.01) > 1e-12 || math.Abs(got[1]-0.005) > 1e-12 || math.Abs(got[2]+0.01) > 1e-12 {
		t.Fatalf("pin placed at %v, want (0.01, 0.005, -0.01)", got)
	}
	lo, hi := bounds(t, mustImport(t, pkg(t, bracket)))
	// Plate: x 0..60, y (3MF z) 0..5, z (3MF −y) −40..0; the pin rises to 5 + 20 mm.
	want := [2][3]float64{{0, 0, -0.04}, {0.06, 0.025, 0}}
	for i := 0; i < 3; i++ {
		if math.Abs(lo[i]-want[0][i]) > 1e-7 || math.Abs(hi[i]-want[1][i]) > 1e-7 {
			t.Fatalf("bounds %v..%v, want %v..%v", lo, hi, want[0], want[1])
		}
	}
	// A rotation keeps handedness, so every triangle of the closed plate still
	// faces out of it.
	plate := m.Roots[0].Children[0].Prims[0]
	centre := [3]float64{0.03, 0.0025, -0.02}
	for k := 0; k+2 < len(plate.Indices); k += 3 {
		a, b, c := plate.Positions[plate.Indices[k]], plate.Positions[plate.Indices[k+1]], plate.Positions[plate.Indices[k+2]]
		u := [3]float64{b[0] - a[0], b[1] - a[1], b[2] - a[2]}
		v := [3]float64{c[0] - a[0], c[1] - a[1], c[2] - a[2]}
		n := [3]float64{u[1]*v[2] - u[2]*v[1], u[2]*v[0] - u[0]*v[2], u[0]*v[1] - u[1]*v[0]}
		d := [3]float64{(a[0]+b[0]+c[0])/3 - centre[0], (a[1]+b[1]+c[1])/3 - centre[1], (a[2]+b[2]+c[2])/3 - centre[2]}
		if n[0]*d[0]+n[1]*d[1]+n[2]*d[2] <= 0 {
			t.Fatalf("plate triangle %d faces inwards", k/3)
		}
	}
}

// Writing turns Y-up back into Z-up: the plate is written lying on the build
// plate, 5 mm high in z, and reads back to the same scene.
func TestExportWritesZUp(t *testing.T) {
	glb := mustImport(t, pkg(t, bracket))
	out, err := h().Export(glb, ".3mf")
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	f, _ := modelFile(zr)
	data, _ := readPart(f)
	if !strings.Contains(string(data), `z="5"`) || !strings.Contains(string(data), `y="40"`) {
		t.Fatalf("plate not written flat (5 mm in z, 40 mm in y):\n%s", data)
	}
	lo1, hi1 := bounds(t, glb)
	lo2, hi2 := bounds(t, mustImport(t, out))
	for i := 0; i < 3; i++ {
		if math.Abs(lo1[i]-lo2[i]) > 1e-9 || math.Abs(hi1[i]-hi2[i]) > 1e-9 {
			t.Fatalf("round trip moved the scene: %v..%v → %v..%v", lo1, hi1, lo2, hi2)
		}
	}
	back, err := h().Export(mustImport(t, out), ".3mf")
	if err != nil || !bytes.Equal(out, back) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
}

// R·m·Rᵀ: a quarter turn about 3MF's z (up) is a quarter turn about glTF's y.
func TestYUpMatrix(t *testing.T) {
	// 3MF row-vector transform: x' = -y, y' = x (a +90° turn about z), then +7 up.
	m, err := parseTransform("0 1 0 -1 0 0 0 0 1 0 0 7", 1)
	if err != nil {
		t.Fatal(err)
	}
	yUpMatrix(m)
	p := func(v [3]float64) [3]float64 {
		return [3]float64{m[0]*v[0] + m[4]*v[1] + m[8]*v[2] + m[12], m[1]*v[0] + m[5]*v[1] + m[9]*v[2] + m[13], m[2]*v[0] + m[6]*v[1] + m[10]*v[2] + m[14]}
	}
	// The same point, in either axes, must land on the same place.
	src := [3]float64{2, 3, 4}
	wantZ := [3]float64{-3, 2, 4 + 7}
	if got, want := p(yUp(src)), yUp(wantZ); got != want {
		t.Fatalf("turned point %v, want %v", got, want)
	}
	for _, v := range m {
		if math.Signbit(v) && v == 0 {
			t.Fatalf("negative zero in %v", m)
		}
	}
}
