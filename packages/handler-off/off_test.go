package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func h() *scene.CodecHandler { return Codec.Handler() }

const quad = `OFF
# a unit quad
4 1 4
0 0 0
1 0 0
1 1 0
0 1 0
4 0 1 2 3
`

func TestDecodeQuadFanTriangulates(t *testing.T) {
	m, err := decode([]byte(quad))
	if err != nil {
		t.Fatal(err)
	}
	p := m.Roots[0].Prims[0]
	if len(p.Positions) != 4 || len(p.Indices) != 6 || p.Kind != scene.KindTriangles {
		t.Fatalf("got %d vertices, %d indices, kind %d", len(p.Positions), len(p.Indices), p.Kind)
	}
}

func TestColourVariantsAndFaceColour(t *testing.T) {
	coff := "COFF\n3 1 0\n0 0 0 255 0 0 255\n1 0 0 0 255 0 255\n0 1 0 0 0 255 128\n3 0 1 2 200 200 200\n"
	m, err := decode([]byte(coff))
	if err != nil {
		t.Fatal(err)
	}
	c := m.Roots[0].Prims[0].Colors
	if c[0] != [4]float64{1, 0, 0, 1} || c[2][3] < 0.5 || c[2][3] > 0.51 {
		t.Fatalf("colours %v", c)
	}
	// Float colours (0..1) are not rescaled.
	m, err = decode([]byte("COFF\n1 0 0\n0 0 0 0.5 0.25 1.0 1.0\n"))
	if err != nil || m.Roots[0].Prims[0].Colors[0] != [4]float64{0.5, 0.25, 1, 1} {
		t.Fatalf("float colours: %v err=%v", m, err)
	}
}

func TestFourDimensionalAndPointCloud(t *testing.T) {
	m, err := decode([]byte("4OFF\n2 0 0\n1 2 3 1\n4 5 6 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := m.Roots[0].Prims[0]
	if p.Kind != scene.KindPoints || p.Positions[1] != [3]float64{4, 5, 6} {
		t.Fatalf("got kind %d positions %v", p.Kind, p.Positions)
	}
}

func TestRoundTripThroughGLB(t *testing.T) {
	glb, err := h().Import([]byte(quad))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".off")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "OFF\n") {
		t.Fatalf("header: %q", out)
	}
	back, err := h().Import(out)
	if err != nil || !bytes.Equal(glb, back) {
		t.Fatalf("round trip changed the scene (err=%v):\n%s", err, out)
	}
	d, err := h().Diff([]byte(quad), out)
	if err != nil || len(d.Changes) != 0 {
		t.Fatalf("diff of equal scenes: %+v err=%v", d.Changes, err)
	}
}

func TestColourRoundTripWritesCOFF(t *testing.T) {
	coff := "COFF\n3 1 0\n0 0 0 255 0 0 255\n1 0 0 0 255 0 255\n0 1 0 0 0 255 255\n3 0 1 2\n"
	glb, err := h().Import([]byte(coff))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h().Export(glb, ".off")
	if err != nil || !strings.HasPrefix(string(out), "COFF\n") || !strings.Contains(string(out), "0 0 0 255 0 0 255") {
		t.Fatalf("COFF export: %q err=%v", out, err)
	}
}

func TestMalformed(t *testing.T) {
	cases := map[string]string{
		"no header":      "3 1 0\n",
		"unknown prefix": "XOFF\n0 0 0\n",
		"truncated":      "OFF\n3 1 0\n0 0 0\n",
		"bad number":     "OFF\n1 0 0\n0 x 0\n",
		"bad index":      "OFF\n1 1 0\n0 0 0\n3 0 1 2\n",
		"huge counts":    "OFF\n999999999 999999999 0\n0 0 0\n",
		"negative count": "OFF\n-1 0 0\n",
		"bad colour":     "COFF\n1 0 0\n0 0 0 1 2\n",
		"empty":          "",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDiffAndMatch(t *testing.T) {
	moved := strings.Replace(quad, "1 1 0", "1 2 0", 1)
	d, err := h().Diff([]byte(quad), []byte(moved))
	if err != nil || len(d.Changes) == 0 || d.Format != "off" {
		t.Fatalf("a moved vertex must show: %+v err=%v", d.Changes, err)
	}
	if !h().Match("x/Teapot.OFF") || h().Match("x.obj") {
		t.Fatal("Match is by extension")
	}
	if _, err := h().Export([]byte("junk"), ".off"); err == nil {
		t.Fatal("junk glb must error")
	}
}
