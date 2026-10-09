package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

// ck builds a chunk by hand, from the documented layout, independently of the
// writer under test.
func ck(id uint16, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	out := make([]byte, 6, 6+len(body))
	binary.LittleEndian.PutUint16(out, id)
	binary.LittleEndian.PutUint32(out[2:], uint32(6+len(body)))
	return append(out, body...)
}

func u16(v ...int) []byte {
	b := make([]byte, 2*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(x))
	}
	return b
}

func f32s(v ...float64) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(float32(x)))
	}
	return b
}

func cstr(s string) []byte { return append([]byte(s), 0) }

// A Z-up quad (two faces) with a red material on face 0 only.
func sample() []byte {
	verts := ck(0x4110, u16(4), f32s(0, 0, 0, 4, 0, 0, 4, 3, 0, 0, 3, 0))
	uvs := ck(0x4140, u16(4), f32s(0, 0, 1, 0, 1, 1, 0, 1))
	faces := ck(0x4120, u16(2), u16(0, 1, 2, 7), u16(0, 2, 3, 7),
		ck(0x4130, cstr("Red"), u16(1), u16(0)))
	mesh := ck(0x4100, verts, uvs, faces)
	obj := ck(0x4000, cstr("Plate"), mesh)
	mat := ck(0xAFFF, ck(0xA000, cstr("Red")), ck(0xA020, ck(0x0011, []byte{255, 0, 0})))
	return ck(0x4D4D, ck(0x0002, []byte{3, 0, 0, 0}), ck(0x3D3D, ck(0x3D3E, []byte{3, 0, 0, 0}), mat, obj))
}

func TestDecodeHandBuiltFile(t *testing.T) {
	m, err := decode(sample())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Roots) != 1 || m.Roots[0].Name != "Plate" || len(m.Roots[0].Prims) != 2 {
		t.Fatalf("roots: %+v", m.Roots)
	}
	red, plain := m.Roots[0].Prims[1], m.Roots[0].Prims[0] // the unassigned group is first
	if plain.Material != "" || red.Material != "Red" || red.BaseColor == nil || red.BaseColor[0] != 1 || red.BaseColor[1] != 0 {
		t.Fatalf("materials: %q / %q %v", plain.Material, red.Material, red.BaseColor)
	}
	if len(red.Indices) != 3 || len(plain.Indices) != 3 || red.UVs == nil {
		t.Fatalf("faces: %d / %d, uvs=%v", len(red.Indices), len(plain.Indices), red.UVs != nil)
	}
	if red.UVs[0][1] != 1 { // v = 0 at the bottom in 3DS is 1 in glTF
		t.Fatalf("v not flipped: %v", red.UVs[0])
	}
}

func TestZUpBecomesYUpAndBack(t *testing.T) {
	glb, err := h().Import(sample())
	if err != nil {
		t.Fatal(err)
	}
	roots, _ := scene.Flatten(glb)
	// The Z-up corner (0,3,0) is (0,0,-3) in Y-up.
	var found bool
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
			for _, v := range p.Positions {
				if math.Abs(v[0]) < 1e-6 && math.Abs(v[1]) < 1e-6 && math.Abs(v[2]+3) < 1e-6 {
					found = true
				}
			}
		}
	})
	if !found {
		t.Fatal("(0,3,0) in Z-up should be (0,0,-3) in Y-up")
	}
	out, err := h().Export(glb, ".3ds")
	if err != nil {
		t.Fatal(err)
	}
	back, err := decode(out)
	if err != nil {
		t.Fatalf("our own export does not parse: %v", err)
	}
	if len(back.Roots) == 0 {
		t.Fatal("round trip lost the scene")
	}
	// Z-up numbers survive a full trip: the file's vertex (4,3,0) is still (4,3,0).
	if !bytes.Contains(out, f32s(4, 3, 0)) {
		t.Fatal("the exported file does not hold the original Z-up vertex")
	}
}

func TestExportIsStableAndKeepsMaterials(t *testing.T) {
	glb, _ := h().Import(sample())
	out, err := h().Export(glb, ".3ds")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := h().Import(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := h().Export(g2, ".3ds")
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("export is not stable across import/export (err=%v)", err)
	}
	m, _ := decode(out)
	var names []string
	for _, r := range m.Roots {
		for _, p := range r.Prims {
			names = append(names, p.Material)
		}
	}
	if !strings.Contains(strings.Join(names, ","), "Red") {
		t.Fatalf("material names after export: %v", names)
	}
}

func TestLargePrimitiveIsSplitToFit16BitCounts(t *testing.T) {
	// 30000 disjoint triangles = 90000 vertices, more than a 3DS mesh can hold.
	p := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
	for i := 0; i < 30000; i++ {
		x := float64(i)
		p.Positions = append(p.Positions, [3]float64{x, 0, 0}, [3]float64{x + 1, 0, 0}, [3]float64{x, 1, 0})
		p.Indices = append(p.Indices, uint32(3*i), uint32(3*i+1), uint32(3*i+2))
	}
	out, err := encode([]*scene.FlatNode{{Name: "Big", Prims: []*scene.FlatPrim{p}}}, ".3ds")
	if err != nil {
		t.Fatal(err)
	}
	m, err := decode(out)
	if err != nil {
		t.Fatal(err)
	}
	faces := 0
	for _, r := range m.Roots {
		for _, pr := range r.Prims {
			if len(pr.Positions) > 65535 {
				t.Fatalf("a mesh has %d vertices", len(pr.Positions))
			}
			faces += len(pr.Indices) / 3
		}
	}
	if len(m.Roots) < 2 || faces != 30000 {
		t.Fatalf("%d objects, %d faces; want a split with all 30000 faces", len(m.Roots), faces)
	}
	if m.Roots[0].Name != "Big" || m.Roots[1].Name != "Big_2" {
		t.Fatalf("split names: %q %q", m.Roots[0].Name, m.Roots[1].Name)
	}
}

func TestMalformed(t *testing.T) {
	good := sample()
	cases := map[string][]byte{
		"empty":        {},
		"not 3ds":      ck(0x1234),
		"short header": good[:3],
		"bad length":   append(append([]byte{}, good[:2]...), 0xff, 0xff, 0xff, 0x7f),
		"truncated":    good[:len(good)-5],
	}
	// A face that names a vertex that does not exist, and a material assigned to a missing face.
	badFace := ck(0x4D4D, ck(0x3D3D, ck(0x4000, cstr("x"), ck(0x4100,
		ck(0x4110, u16(1), f32s(0, 0, 0)), ck(0x4120, u16(1), u16(0, 1, 2, 0))))))
	cases["bad vertex ref"] = badFace
	badMat := ck(0x4D4D, ck(0x3D3D, ck(0x4000, cstr("x"), ck(0x4100,
		ck(0x4110, u16(3), f32s(0, 0, 0, 1, 0, 0, 0, 1, 0)),
		ck(0x4120, u16(1), u16(0, 1, 2, 0), ck(0x4130, cstr("m"), u16(1), u16(9)))))))
	cases["bad face assignment"] = badMat
	for name, b := range cases {
		if _, err := decode(b); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := encode(nil, ".3ds"); err == nil {
		t.Fatal("an empty scene is an error")
	}
	if _, err := h().Export([]byte("x"), ".obj"); err == nil {
		t.Fatal("wrong format is an error")
	}
}

func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	good := sample()
	try := func(b []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic %v", r)
			}
		}()
		_, _ = decode(b)
	}
	for n := 0; n <= len(good); n++ {
		try(good[:n])
	}
	for i := 0; i < 2000; i++ {
		b := append([]byte(nil), good...)
		for k := 0; k < 1+rng.Intn(4); k++ {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		try(b)
	}
}

func TestDiffAndMatch(t *testing.T) {
	moved := bytes.Replace(sample(), f32s(4, 3, 0), f32s(4, 5, 0), 1)
	d, err := h().Diff(sample(), moved)
	if err != nil || len(d.Changes) == 0 || d.Format != "3ds" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Chair.3DS") || h().Match("a.obj") {
		t.Fatal("Match is by extension")
	}
}
