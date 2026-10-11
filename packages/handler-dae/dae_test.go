package main

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

// A quad (polylist) with normals, UVs and a material, instanced twice: once
// directly and once through a library node translated 10 along x.
const sample = `<?xml version="1.0"?>
<COLLADA xmlns="http://www.collada.org/2005/11/COLLADASchema" version="1.4.1">
 <asset><up_axis>Y_UP</up_axis></asset>
 <library_effects><effect id="fx"><profile_COMMON><technique sid="s"><phong><diffuse><color>1 0 0 1</color></diffuse></phong></technique></profile_COMMON></effect></library_effects>
 <library_materials><material id="steel-mat" name="Steel"><instance_effect url="#fx"/></material></library_materials>
 <library_geometries>
  <geometry id="plate" name="PlateGeo"><mesh>
   <source id="p"><float_array id="pa" count="12">0 0 0 4 0 0 4 3 0 0 3 0</float_array><technique_common><accessor source="#pa" count="4" stride="3"/></technique_common></source>
   <source id="n"><float_array id="na" count="3">0 0 1</float_array><technique_common><accessor source="#na" count="1" stride="3"/></technique_common></source>
   <source id="t"><float_array id="ta" count="8">0 0 1 0 1 1 0 1</float_array><technique_common><accessor source="#ta" count="4" stride="2"/></technique_common></source>
   <vertices id="v"><input semantic="POSITION" source="#p"/></vertices>
   <polylist material="surf" count="1">
    <input semantic="VERTEX" source="#v" offset="0"/><input semantic="NORMAL" source="#n" offset="1"/><input semantic="TEXCOORD" source="#t" offset="2" set="0"/>
    <vcount>4</vcount><p>0 0 0 1 0 1 2 0 2 3 0 3</p>
   </polylist>
  </mesh></geometry>
 </library_geometries>
 <library_nodes><node id="shifted" name="Shifted"><translate>10 0 0</translate><instance_geometry url="#plate"><bind_material><technique_common><instance_material symbol="surf" target="#steel-mat"/></technique_common></bind_material></instance_geometry></node></library_nodes>
 <library_visual_scenes><visual_scene id="S">
  <node id="a" name="Plate"><instance_geometry url="#plate"><bind_material><technique_common><instance_material symbol="surf" target="#steel-mat"/></technique_common></bind_material></instance_geometry></node>
  <node id="b" name="Holder"><rotate>0 0 1 90</rotate><instance_node url="#shifted"/></node>
 </visual_scene></library_visual_scenes>
 <scene><instance_visual_scene url="#S"/></scene>
</COLLADA>`

func TestDecodeHierarchyMaterialsAndTransforms(t *testing.T) {
	m, err := decode([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 2 || m.Roots[0].Name != "Plate" || m.Roots[1].Name != "Holder" {
		t.Fatalf("roots: %+v", m.Roots)
	}
	p := m.Roots[0].Prims[0]
	if len(p.Positions) != 4 || len(p.Indices) != 6 || p.Normals == nil || p.UVs == nil {
		t.Fatalf("plate: %d vertices %d indices normals=%v uvs=%v", len(p.Positions), len(p.Indices), p.Normals != nil, p.UVs != nil)
	}
	if p.Material != "Steel" || p.BaseColor == nil || p.BaseColor[0] != 1 {
		t.Fatalf("material %q colour %v", p.Material, p.BaseColor)
	}
	if p.UVs[2][1] != 0 { // v = 1 in Collada is the top: 0 in glTF
		t.Fatalf("v not flipped: %v", p.UVs[2])
	}
	// Holder rotates 90° about z; its child Shifted translates 10 along x.
	holder := m.Roots[1]
	r := holder.Matrix
	if r == nil || math.Abs(r[0]) > 1e-9 || math.Abs(r[1]-1) > 1e-9 {
		t.Fatalf("rotation: %v", r)
	}
	if len(holder.Children) != 1 || holder.Children[0].Matrix[12] != 10 {
		t.Fatalf("instance_node child: %+v", holder.Children)
	}
}

func TestZUpIsRotatedToYUp(t *testing.T) {
	z := strings.Replace(sample, "<up_axis>Y_UP", "<up_axis>Z_UP", 1)
	glb, err := h().Import([]byte(z))
	if err != nil {
		t.Fatal(err)
	}
	// The plate's corner (0,3,0) is at z=0, y=3 in Z-up: in Y-up it is y=0, z=-3.
	roots, err := scene.Flatten(glb)
	if err != nil {
		t.Fatal(err)
	}
	got := roots[0].Prims[0].Positions[3]
	if math.Abs(got[0]) > 1e-6 || math.Abs(got[1]) > 1e-6 || math.Abs(got[2]+3) > 1e-6 {
		t.Fatalf("Z-up corner (0,3,0) → %v, want (0,0,-3)", got)
	}
}

func TestRoundTripKeepsWorldGeometry(t *testing.T) {
	glb, err := h().Import([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".dae")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<up_axis>Y_UP</up_axis>`, `name="Steel"`, `<color>1 0 0 1</color>`, `<triangles material="sym-0" count="2">`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("export missing %s:\n%s", want, out)
		}
	}
	// The exported file has world-space geometry (the instanced plate is baked
	// at its rotated+translated place), and is stable thereafter.
	g2, err := h().Import(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := h().Export(g2, ".dae")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
	// Plate corner (4,0,0) is rotated 90° about z to (0,4,0), then... the
	// translate comes first in the child, so it lands at (0,14,0).
	if !strings.Contains(string(out), "0 14 0") {
		t.Errorf("baked instance position missing:\n%s", out)
	}
}

func TestStripsFansLinesAndTrianglesParse(t *testing.T) {
	src := func(prim string) string {
		return `<COLLADA><library_geometries><geometry id="g"><mesh>
<source id="p"><float_array id="pa" count="12">0 0 0 1 0 0 1 1 0 0 1 0</float_array><technique_common><accessor source="#pa" count="4" stride="3"/></technique_common></source>
<vertices id="v"><input semantic="POSITION" source="#p"/></vertices>` + prim + `</mesh></geometry></library_geometries></COLLADA>`
	}
	cases := map[string]struct {
		prim string
		kind scene.PrimKind
		n    int
	}{
		"triangles": {`<triangles count="2"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 2 0 2 3</p></triangles>`, scene.KindTriangles, 6},
		"strip":     {`<tristrips count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 3 2</p></tristrips>`, scene.KindTriangles, 6},
		"fan":       {`<trifans count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 2 3</p></trifans>`, scene.KindTriangles, 6},
		"polygons":  {`<polygons count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 2 3</p></polygons>`, scene.KindTriangles, 6},
		"lines":     {`<lines count="2"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 2 3</p></lines>`, scene.KindLines, 4},
		"linestrip": {`<linestrips count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 2</p></linestrips>`, scene.KindLines, 4},
	}
	for name, c := range cases {
		m, err := decode([]byte(src(c.prim)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		p := m.Roots[0].Prims[0]
		if p.Kind != c.kind || len(p.Indices) != c.n {
			t.Errorf("%s: kind %d, %d indices; want %d, %d", name, p.Kind, len(p.Indices), c.kind, c.n)
		}
	}
}

func TestMalformed(t *testing.T) {
	geo := func(body string) string {
		return `<COLLADA><library_geometries><geometry id="g"><mesh>
<source id="p"><float_array id="pa" count="9">0 0 0 1 0 0 1 1 0</float_array><technique_common><accessor source="#pa" count="3" stride="3"/></technique_common></source>
<vertices id="v"><input semantic="POSITION" source="#p"/></vertices>` + body + `</mesh></geometry></library_geometries></COLLADA>`
	}
	cases := map[string]string{
		"not xml":        "hello",
		"wrong root":     "<html/>",
		"bad index":      geo(`<triangles count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 9</p></triangles>`),
		"partial tri":    geo(`<triangles count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1</p></triangles>`),
		"missing source": geo(`<triangles count="1"><input semantic="VERTEX" source="#nope" offset="0"/><p>0 1 2</p></triangles>`),
		"bad number":     strings.Replace(geo(`<triangles count="1"><input semantic="VERTEX" source="#v" offset="0"/><p>0 1 2</p></triangles>`), "0 0 0 1", "0 x 0 1", 1),
		"bad vcount":     geo(`<polylist count="1"><input semantic="VERTEX" source="#v" offset="0"/><vcount>5</vcount><p>0 1 2</p></polylist>`),
		"node cycle": `<COLLADA><library_nodes><node id="a"><instance_node url="#a"/></node></library_nodes>
<library_visual_scenes><visual_scene id="S"><node id="r"><instance_node url="#a"/></node></visual_scene></library_visual_scenes></COLLADA>`,
		"skew": `<COLLADA><library_visual_scenes><visual_scene id="S"><node id="r"><skew>1 2 3 4 5 6 7</skew></node></visual_scene></library_visual_scenes></COLLADA>`,
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDiffMatchAndExportErrors(t *testing.T) {
	moved := strings.Replace(sample, "4 3 0", "4 5 0", 1)
	d, err := h().Diff([]byte(sample), []byte(moved))
	if err != nil || len(d.Changes) == 0 {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Scene.DAE") || h().Match("a.obj") {
		t.Fatal("Match is by extension")
	}
	if _, err := encode(nil, ".dae"); err == nil {
		t.Fatal("an empty scene is an error")
	}
}
