package scene

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

func triangleGLB(t *testing.T) Blob {
	t.Helper()
	doc := &gltf.Document{Asset: gltf.Asset{Version: "2.0"}, Scene: gltf.Index(0), Scenes: []*gltf.Scene{{Nodes: []int{0}}}}
	pos := modeler.WritePosition(doc, [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}})
	doc.Meshes = []*gltf.Mesh{{Name: "tri", Primitives: []*gltf.Primitive{{Attributes: gltf.PrimitiveAttributes{gltf.POSITION: pos}}}}}
	doc.Nodes = []*gltf.Node{{Name: "Tri", Mesh: gltf.Index(0)}}
	b, err := encodeBlob(doc, true)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExportGLTFEmbedsBufferAndImportsBack(t *testing.T) {
	h := &Handler{}
	glb := triangleGLB(t)
	text, err := h.Export(glb, ".gltf")
	if err != nil {
		t.Fatal(err)
	}
	if isGLB(text) || !strings.Contains(string(text), "data:application/") {
		t.Fatalf(".gltf export should be JSON with an embedded buffer, got %.80s", text)
	}
	back, err := h.Import(text)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, glb) {
		t.Fatal("glb → gltf → glb changed the asset")
	}
}

func TestExportGLBIsCanonical(t *testing.T) {
	glb := triangleGLB(t)
	out, err := (&Handler{}).Export(glb, ".GLB")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, glb) {
		t.Fatal("re-encoding a canonical GLB should be the identity")
	}
}

func TestExportRejectsOtherFormats(t *testing.T) {
	if _, err := (&Handler{}).Export(triangleGLB(t), ".obj"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestImportRejectsExternalBuffer(t *testing.T) {
	src := `{"asset":{"version":"2.0"},"buffers":[{"uri":"mesh.bin","byteLength":12}]}`
	_, err := (&Handler{}).Import([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "mesh.bin") {
		t.Fatalf("want an error naming the external file, got %v", err)
	}
}
