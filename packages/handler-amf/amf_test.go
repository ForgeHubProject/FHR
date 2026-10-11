package main

import (
	"archive/zip"
	"bytes"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

// A 10 × 10 mm quad (two triangles) in millimetres, red, instanced twice: once in
// place and once moved 20 mm along x and turned 90° about z.
const sample = `<?xml version="1.0" encoding="UTF-8"?>
<amf unit="millimeter" version="1.1">
 <metadata type="cad">test</metadata>
 <object id="0">
  <metadata type="name">Plate</metadata>
  <mesh>
   <vertices>
    <vertex><coordinates><x>0</x><y>0</y><z>0</z></coordinates></vertex>
    <vertex><coordinates><x>10</x><y>0</y><z>0</z></coordinates></vertex>
    <vertex><coordinates><x>10</x><y>10</y><z>0</z></coordinates></vertex>
    <vertex><coordinates><x>0</x><y>10</y><z>0</z></coordinates></vertex>
   </vertices>
   <volume materialid="1">
    <triangle><v1>0</v1><v2>1</v2><v3>2</v3></triangle>
    <triangle><v1>0</v1><v2>2</v2><v3>3</v3></triangle>
   </volume>
  </mesh>
 </object>
 <material id="1"><metadata type="name">Steel</metadata><color><r>1</r><g>0</g><b>0</b><a>1</a></color></material>
 <constellation id="0">
  <instance objectid="0"><deltax>0</deltax><deltay>0</deltay><deltaz>0</deltaz><rx>0</rx><ry>0</ry><rz>0</rz></instance>
  <instance objectid="0"><deltax>20</deltax><deltay>0</deltay><deltaz>0</deltaz><rx>0</rx><ry>0</ry><rz>90</rz></instance>
 </constellation>
</amf>`

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

func TestDecodeAppliesUnitsConstellationsAndMaterials(t *testing.T) {
	m, err := decode([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 2 || m.Roots[0].Name != "Plate" {
		t.Fatalf("roots: %+v", m.Roots)
	}
	p := m.Roots[0].Prims[0]
	if p.Material != "Steel" || p.BaseColor == nil || p.BaseColor[0] != 1 || len(p.Indices) != 6 {
		t.Fatalf("prim: %q %v %d", p.Material, p.BaseColor, len(p.Indices))
	}
	glb, err := h().Import([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	// First plate 0..10 mm = 0..0.01 m. Second: turned 90° about z ((x,y) → (-y,x)) → x in -10..0 mm,
	// y in 0..10 mm, then +20 mm along x → x in 10..20 mm. So x spans 0..0.02 m and y 0..0.01 m.
	// (No axis change is made: AMF does not define an up axis.)
	lo, hi := bounds(t, glb)
	if math.Abs(lo[0]) > 1e-6 || math.Abs(hi[0]-0.02) > 1e-6 || math.Abs(lo[1]) > 1e-6 || math.Abs(hi[1]-0.01) > 1e-6 {
		t.Fatalf("bounds %v..%v, want x 0..0.02 and y 0..0.01", lo, hi)
	}
}

func TestZipCompressedAMF(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("model.amf")
	_, _ = w.Write([]byte(sample))
	_ = zw.Close()
	want, _ := h().Import([]byte(sample))
	got, err := h().Import(b.Bytes())
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("a zipped AMF must read like the plain one (err=%v)", err)
	}
}

func TestRoundTripKeepsSizeAndIsStable(t *testing.T) {
	glb, err := h().Import([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".amf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `unit="meter"`) || !strings.Contains(string(out), "<metadata type=\"name\">Steel</metadata>") {
		t.Fatalf("export: %s", out)
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
	out2, err := h().Export(back, ".amf")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
}

func TestMalformed(t *testing.T) {
	cases := map[string]string{
		"not xml":      "hello",
		"wrong root":   "<html/>",
		"bad unit":     strings.Replace(sample, `unit="millimeter"`, `unit="parsec"`, 1),
		"bad vertex":   strings.Replace(sample, "<x>10</x>", "<x>ten</x>", 1),
		"no coords":    strings.Replace(sample, "<coordinates><x>0</x><y>0</y><z>0</z></coordinates>", "", 1),
		"bad index":    strings.Replace(sample, "<v3>2</v3>", "<v3>9</v3>", 1),
		"missing v":    strings.Replace(sample, "<v1>0</v1>", "", 1),
		"bad instance": strings.Replace(sample, `objectid="0"><deltax>20`, `objectid="7"><deltax>20`, 1),
		"bad colour":   strings.Replace(sample, "<r>1</r>", "<r>red</r>", 1),
		"not a zip":    "PKjunk",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := encode(nil, ".amf"); err == nil {
		t.Fatal("an empty scene is an error")
	}
}

func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	glb, _ := h().Import([]byte(sample))
	out, _ := h().Export(glb, ".amf")
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
		for i := 0; i < 600; i++ {
			b := append([]byte(nil), good...)
			for k := 0; k < 1+rng.Intn(6); k++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
			try(b)
		}
	}
}

func TestDiffAndMatch(t *testing.T) {
	moved := strings.Replace(sample, "<x>10</x><y>10</y>", "<x>10</x><y>12</y>", 1)
	d, err := h().Diff([]byte(sample), []byte(moved))
	if err != nil || len(d.Changes) == 0 || d.Format != "amf" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Part.AMF") || h().Match("a.obj") {
		t.Fatal("Match is by extension")
	}
}
