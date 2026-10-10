package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

const quadASCII = `ply
format ascii 1.0
comment a coloured quad
element vertex 4
property float x
property float y
property float z
property float nx
property float ny
property float nz
property uchar red
property uchar green
property uchar blue
element face 1
property list uchar int vertex_indices
end_header
0 0 0 0 0 1 255 0 0
1 0 0 0 0 1 0 255 0
1 1 0 0 0 1 0 0 255
0 1 0 0 0 1 255 255 255
4 0 1 2 3
`

func h() *scene.CodecHandler { return Codec.Handler() }

func TestDecodeASCIIQuad(t *testing.T) {
	m, err := decode([]byte(quadASCII))
	if err != nil {
		t.Fatal(err)
	}
	p := m.Roots[0].Prims[0]
	if len(p.Positions) != 4 || len(p.Indices) != 6 || p.Normals == nil || p.Colors == nil {
		t.Fatalf("got %d vertices, %d indices, normals=%v colors=%v", len(p.Positions), len(p.Indices), p.Normals != nil, p.Colors != nil)
	}
	if p.Colors[0] != [4]float64{1, 0, 0, 1} {
		t.Fatalf("first colour %v", p.Colors[0])
	}
}

func TestBinaryAndASCIIAgree(t *testing.T) {
	glbA, err := h().Import([]byte(quadASCII))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := h().Export(glbA, ".ply")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bin, []byte("ply\nformat binary_little_endian")) {
		t.Fatalf("unexpected export header: %.60q", bin)
	}
	glbB, err := h().Import(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(glbA, glbB) {
		t.Fatal("ASCII → binary round trip changed the scene")
	}
	d, err := h().Diff([]byte(quadASCII), bin)
	if err != nil || len(d.Changes) != 0 {
		t.Fatalf("diff of equal scenes: %+v err=%v", d.Changes, err)
	}
}

func TestBigEndianBinary(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("ply\nformat binary_big_endian 1.0\nelement vertex 3\nproperty float x\nproperty float y\nproperty float z\nelement face 1\nproperty list uchar uint vertex_indices\nend_header\n")
	for _, v := range []float32{0, 0, 0, 1, 0, 0, 0, 1, 0} {
		_ = binary.Write(&b, binary.BigEndian, v)
	}
	b.WriteByte(3)
	for _, i := range []uint32{0, 1, 2} {
		_ = binary.Write(&b, binary.BigEndian, i)
	}
	m, err := decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Roots[0].Prims[0]; p.Positions[1] != [3]float64{1, 0, 0} || len(p.Indices) != 3 {
		t.Fatalf("got %v %v", p.Positions, p.Indices)
	}
}

func TestPointCloudAndEdges(t *testing.T) {
	cloud := "ply\nformat ascii 1.0\nelement vertex 2\nproperty float x\nproperty float y\nproperty float z\nend_header\n0 0 0\n1 1 1\n"
	m, err := decode([]byte(cloud))
	if err != nil || m.Roots[0].Prims[0].Kind != scene.KindPoints {
		t.Fatalf("point cloud: %+v err=%v", m, err)
	}
	edges := "ply\nformat ascii 1.0\nelement vertex 2\nproperty float x\nproperty float y\nproperty float z\nelement edge 1\nproperty int vertex1\nproperty int vertex2\nend_header\n0 0 0\n1 1 1\n0 1\n"
	m, err = decode([]byte(edges))
	if err != nil || m.Roots[0].Prims[0].Kind != scene.KindLines {
		t.Fatalf("edges: %+v err=%v", m, err)
	}
}

func TestMalformedFilesError(t *testing.T) {
	cases := map[string]string{
		"no magic":        "hello",
		"no end_header":   "ply\nformat ascii 1.0\nelement vertex 1\n",
		"truncated body":  "ply\nformat ascii 1.0\nelement vertex 2\nproperty float x\nproperty float y\nproperty float z\nend_header\n0 0 0\n",
		"huge count":      "ply\nformat ascii 1.0\nelement vertex 999999999\nproperty float x\nproperty float y\nproperty float z\nend_header\n0 0 0\n",
		"bad index":       "ply\nformat ascii 1.0\nelement vertex 1\nproperty float x\nproperty float y\nproperty float z\nelement face 1\nproperty list uchar int vertex_indices\nend_header\n0 0 0\n3 0 1 2\n",
		"unknown type":    "ply\nformat ascii 1.0\nelement vertex 1\nproperty bogus x\nend_header\n0\n",
		"missing xyz":     "ply\nformat ascii 1.0\nelement vertex 1\nproperty float x\nend_header\n0\n",
		"face bad length": "ply\nformat ascii 1.0\nelement vertex 1\nproperty float x\nproperty float y\nproperty float z\nelement face 1\nproperty list uchar int vertex_indices\nend_header\n0 0 0\n200 0\n",
	}
	for name, src := range cases {
		if _, err := decode([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDiffSeesAMovedVertex(t *testing.T) {
	moved := strings.Replace(quadASCII, "1 1 0 0 0 1", "1 2 0 0 0 1", 1)
	d, err := h().Diff([]byte(quadASCII), []byte(moved))
	if err != nil || len(d.Changes) == 0 {
		t.Fatalf("a moved vertex must show as a change: %+v err=%v", d.Changes, err)
	}
	if d.Format != "ply" {
		t.Fatalf("format %q", d.Format)
	}
}

func TestMatchAndEmpty(t *testing.T) {
	if !h().Match("a/b/Scan.PLY") || h().Match("a.obj") {
		t.Fatal("Match is by extension, case-insensitively")
	}
	d, err := h().Diff(nil, []byte(quadASCII))
	if err != nil || len(d.Changes) == 0 {
		t.Fatalf("added file: %+v err=%v", d.Changes, err)
	}
}
