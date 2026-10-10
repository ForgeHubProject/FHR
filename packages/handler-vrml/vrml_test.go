package main

import (
	"bytes"
	"compress/gzip"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

const sample = `#VRML V2.0 utf8
# a plate, once in place and once moved and turned
WorldInfo { title "test" }
NavigationInfo { type [ "EXAMINE" ] }
DEF Rig Transform {
  translation 10 0 0
  rotation 0 0 1 1.5707963267948966
  children [
    DEF Plate Shape {
      appearance Appearance {
        material DEF Steel Material { diffuseColor 1 0 0 transparency 0.25 }
      }
      geometry IndexedFaceSet {
        solid FALSE
        coord DEF C Coordinate { point [ 0 0 0, 4 0 0, 4 3 0, 0 3 0 ] }
        coordIndex [ 0, 1, 2, 3, -1 ]
        normal Normal { vector [ 0 0 1, 0 0 1, 0 0 1, 0 0 1 ] }
        texCoord TextureCoordinate { point [ 0 0, 1 0, 1 1, 0 1 ] }
      }
    }
  ]
}
DEF Copy Transform { translation 0 0 5 children [ USE Plate ] }
Transform { children [ Shape { geometry Box { size 2 4 6 } } ] }
Switch { whichChoice 1 choice [ Shape { geometry PointSet { coord Coordinate { point [ 0 0 0 ] } } } Shape { geometry PointSet { coord Coordinate { point [ 1 1 1 ] } } } ] }
Script { url "x.js" field SFFloat speed 1.5 eventIn SFBool go }
ROUTE A.out TO B.in
`

func TestDecodeFeatures(t *testing.T) {
	m, err := decode([]byte(sample))
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
	plate := names["Plate"]
	if plate == nil || len(plate.Prims) != 1 {
		t.Fatalf("objects: %v", names)
	}
	p := plate.Prims[0]
	if len(p.Indices) != 6 || p.Normals == nil || p.UVs == nil || p.Material != "Steel" {
		t.Fatalf("plate prim: %d indices normals=%v uvs=%v material=%q", len(p.Indices), p.Normals != nil, p.UVs != nil, p.Material)
	}
	if p.BaseColor == nil || p.BaseColor[0] != 1 || math.Abs(p.BaseColor[3]-0.75) > 1e-9 {
		t.Fatalf("colour %v (transparency 0.25 is alpha 0.75)", p.BaseColor)
	}
	if names["Copy"] == nil || names["Copy"].Children[0].Prims == nil {
		t.Fatal("USE must reuse the DEF'd shape")
	}
	rig := names["Rig"].Matrix
	if rig == nil || math.Abs(rig[0]) > 1e-9 || math.Abs(rig[1]-1) > 1e-9 || rig[12] != 10 {
		t.Fatalf("Rig matrix %v", rig)
	}
	// The Switch shows only its second choice; the Script, routes, info nodes leave nothing.
	points := 0
	for _, o := range names {
		for _, pr := range o.Prims {
			if pr.Kind == scene.KindPoints {
				points++
				if pr.Positions[0] != [3]float64{1, 1, 1} {
					t.Fatalf("Switch showed the wrong choice: %v", pr.Positions)
				}
			}
		}
	}
	if points != 1 {
		t.Fatalf("want 1 point set, got %d", points)
	}
}

func TestRoundTripBakesTransformsAndIsStable(t *testing.T) {
	glb, err := h().Import([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".wrl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "#VRML V2.0 utf8") || !strings.Contains(string(out), "IndexedFaceSet") || !strings.Contains(string(out), "material USE Steel") {
		t.Fatalf("export:\n%s", out)
	}
	// The plate's corner (4,0,0) turns to (0,4,0) and moves to (10,4,0).
	if !strings.Contains(string(out), "10 4 0") {
		t.Errorf("baked position missing:\n%s", out)
	}
	g2, err := h().Import(out)
	if err != nil {
		t.Fatalf("our own export does not parse: %v\n%s", err, out)
	}
	out2, err := h().Export(g2, ".wrl")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
}

func TestGzipAndHexAndLenientSyntax(t *testing.T) {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(sample))
	_ = w.Close()
	want, _ := h().Import([]byte(sample))
	got, err := h().Import(gz.Bytes())
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("a gzip-compressed .wrl must read like the plain one (err=%v)", err)
	}
	src := "#VRML V2.0 utf8\nShape{geometry IndexedFaceSet{coord Coordinate{point[0 0 0,1 0 0,0 1 0]}coordIndex[0x0,0x1,0x2,-1]}}\n"
	m, err := decode([]byte(src))
	if err != nil || len(m.Roots) != 1 || len(m.Roots[0].Prims[0].Indices) != 3 {
		t.Fatalf("compact syntax with hex indices: %+v err=%v", m, err)
	}
}

func TestRefusedByName(t *testing.T) {
	cases := map[string]string{
		"vrml 1.0":    "#VRML V1.0 ascii\nSeparator { }\n",
		"no header":   "Shape { }\n",
		"proto":       "#VRML V2.0 utf8\nPROTO Foo [ ] { Group { } }\n",
		"externproto": "#VRML V2.0 utf8\nEXTERNPROTO Foo [ ] \"foo.wrl\"\n",
		"inline":      "#VRML V2.0 utf8\nInline { url \"other.wrl\" }\n",
		"sphere":      "#VRML V2.0 utf8\nShape { geometry Sphere { radius 1 } }\n",
		"extrusion":   "#VRML V2.0 utf8\nShape { geometry Extrusion { } }\n",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMalformed(t *testing.T) {
	head := "#VRML V2.0 utf8\n"
	cases := map[string]string{
		"unterminated node":   head + "Shape { geometry Box { size 1 1 1 }",
		"unterminated string": head + "WorldInfo { title \"oops }",
		"unterminated list":   head + "Group { children [ ",
		"bad field":           head + "Shape { { } }",
		"deep nesting":        head + strings.Repeat("Group { children [ ", 100) + strings.Repeat("] } ", 100),
		"bad index":           head + "Shape { geometry IndexedFaceSet { coord Coordinate { point [ 0 0 0 ] } coordIndex [ 0 1 2 -1 ] } }",
		"bad point count":     head + "Shape { geometry IndexedFaceSet { coord Coordinate { point [ 0 0 ] } coordIndex [ 0 -1 ] } }",
		"use before def":      head + "Group { children [ USE Nope ] }",
		"bad gzip":            "\x1f\x8bnot gzip",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := encode(nil, ".wrl"); err == nil {
		t.Fatal("an empty scene is an error")
	}
}

func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	glb, _ := h().Import([]byte(sample))
	out, _ := h().Export(glb, ".wrl")
	for _, good := range [][]byte{[]byte(sample), out} {
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
			for k := 0; k < 1+rng.Intn(6); k++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
			try(b)
		}
	}
}

func TestDiffAndMatch(t *testing.T) {
	moved := strings.Replace(sample, "4 3 0", "4 5 0", 1)
	d, err := h().Diff([]byte(sample), []byte(moved))
	if err != nil || len(d.Changes) == 0 || d.Format != "vrml" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Scene.WRL") || h().Match("a.obj") {
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
