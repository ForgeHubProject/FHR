package main

import (
	"archive/zip"
	"bytes"
	"io"
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
	second := m.Roots[0].Children[1]
	if second.Matrix == nil || second.Matrix[12] != 20 {
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
