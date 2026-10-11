package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

const bracketOBJ = `# test
o Plate
v 0 0 0
v 1 0 0
v 1 1 0
v 0 1 0
vt 0 0
vt 1 0
vt 1 1
vt 0 1
vn 0 0 1
usemtl steel
f 1/1/1 2/2/1 3/3/1 4/4/1
g Arm
v 0 0 1
v 1 0 1
v 1 0 2
f 5 6 7
`

func TestImportExportRoundTrip(t *testing.T) {
	h := &Handler{}
	glb, err := h.Import([]byte(bracketOBJ))
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.Export(glb, ".obj")
	if err != nil {
		t.Fatal(err)
	}
	// What a transcode must preserve is the scene: importing the exported file
	// gives back the same glTF. (Compared there, not by an OBJ diff, because
	// export writes triangles where the source had a quad.)
	back, err := h.Import(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(glb, back) {
		t.Fatalf("round trip changed the scene:\n%s", out)
	}
	s := string(out)
	for _, want := range []string{"o Plate", "g Arm", "usemtl steel"} {
		if !strings.Contains(s, want) {
			t.Errorf("export missing %q:\n%s", want, s)
		}
	}
}

func TestExportRejectsOtherFormats(t *testing.T) {
	if _, err := (&Handler{}).Export(nil, ".stl"); err == nil {
		t.Fatal("expected an error for a format the handler does not write")
	}
}

func TestExportRejectsGarbage(t *testing.T) {
	if _, err := (&Handler{}).Export([]byte("not a glb"), ".obj"); err == nil {
		t.Fatal("expected an error for a non-glTF blob")
	}
}

// triangleDoc is one triangle on a node with the given transform.
func triangleDoc(node *gltf.Node) []byte {
	doc := &gltf.Document{Asset: gltf.Asset{Version: "2.0"}, Scene: gltf.Index(0), Scenes: []*gltf.Scene{{Nodes: []int{0}}}}
	pos := modeler.WritePosition(doc, [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}})
	nrm := modeler.WriteNormal(doc, [][3]float32{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}})
	doc.Meshes = []*gltf.Mesh{{Name: "tri", Primitives: []*gltf.Primitive{{
		Attributes: gltf.PrimitiveAttributes{gltf.POSITION: pos, gltf.NORMAL: nrm},
	}}}}
	node.Name = "Tri"
	node.Mesh = gltf.Index(0)
	doc.Nodes = []*gltf.Node{node}
	var buf bytes.Buffer
	enc := gltf.NewEncoder(&buf)
	enc.AsBinary = true
	if err := enc.Encode(doc); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestExportBakesTransform(t *testing.T) {
	out, err := (&Handler{}).Export(triangleDoc(&gltf.Node{
		Translation: [3]float64{10, 0, 0},
		Scale:       [3]float64{2, 2, 2},
	}), ".obj")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"v 10 0 0\n", "v 12 0 0\n", "v 10 2 0\n", "f 1//1 2//2 3//3\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestExportMirrorKeepsFacesOutward(t *testing.T) {
	// Negative X scale mirrors the triangle; winding must flip so the face
	// still points the way its (also mirrored) normal does.
	out, err := (&Handler{}).Export(triangleDoc(&gltf.Node{Scale: [3]float64{-1, 1, 1}}), ".obj")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "f 1//1 3//3 2//2\n") {
		t.Errorf("winding not reversed:\n%s", out)
	}
	if !strings.Contains(string(out), "vn 0 0 1\n") {
		t.Errorf("normal should be unchanged by an X mirror:\n%s", out)
	}
}

func TestTranscodeThroughFhr(t *testing.T) {
	// The generic helper wires Import to Export; OBJ → OBJ is the identity on
	// what the diff sees.
	h := &Handler{}
	out, err := fhr.Transcode(h, h, []byte(bracketOBJ), ".obj")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := h.Import([]byte(bracketOBJ))
	got, _ := h.Import(out)
	if !bytes.Equal(want, got) {
		t.Fatalf("transcode changed the scene:\n%s", out)
	}
}

func TestInfoDeclaresTranscoding(t *testing.T) {
	var stdout, stderr bytes.Buffer
	info := fhr.Info{ID: "obj", Formats: []string{".obj"}, Capabilities: &fhr.Capabilities{}}
	if code := fhr.RunCLI(&Handler{}, info, []string{"info"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	if !strings.Contains(stdout.String(), `"import":true`) || !strings.Contains(stdout.String(), `"export":true`) {
		t.Fatalf("info should declare import and export: %s", stdout.String())
	}
}
