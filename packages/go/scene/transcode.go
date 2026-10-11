package scene

import (
	"fmt"
	"github.com/qmuntal/gltf"
	"strings"
)

// Import converts a glTF/GLB blob to a binary glTF — the pivot format of the
// 3D family's transcoding. A GLB is validated and re-encoded canonically; a
// .gltf must be self-contained (buffers embedded as data URIs), because a
// single blob cannot bring its .bin files with it.
func (h *Handler) Import(blob Blob) (Blob, error) {
	doc, err := parseDoc(blob)
	if err != nil {
		return nil, err
	}
	if err := requireSelfContained(doc.Buffers); err != nil {
		return nil, err
	}
	return encodeBlob(embedFirstBuffer(doc), true)
}

// Export writes a GLB as ".glb" (canonical re-encode) or ".gltf" (JSON with
// the buffer embedded as a data URI, so the one blob is the whole asset).
func (h *Handler) Export(glb Blob, format string) (Blob, error) {
	doc, err := parseDoc(glb)
	if err != nil {
		return nil, err
	}
	if err := requireSelfContained(doc.Buffers); err != nil {
		return nil, err
	}
	switch strings.ToLower(format) {
	case ".glb":
		return encodeBlob(embedFirstBuffer(doc), true)
	case ".gltf":
		return encodeBlob(doc, false)
	}
	return nil, fmt.Errorf("gltf-scene exports .glb or .gltf, not %q", format)
}

// requireSelfContained fails on a buffer whose bytes live in another file.
func requireSelfContained(buffers []*gltf.Buffer) error {
	for i, b := range buffers {
		if len(b.Data) == 0 && b.URI != "" {
			return fmt.Errorf("buffer %d is the external file %q: transcoding needs a self-contained glTF (embed the buffer, or use .glb)", i, b.URI)
		}
	}
	return nil
}

// embedFirstBuffer prepares a document for binary encoding: buffer 0 becomes
// the GLB's BIN chunk, so a data URI it carried (from a .gltf source) must go
// or the JSON would hold the same bytes a second time. Other buffers keep
// their URIs, which is all a GLB can do with them.
func embedFirstBuffer(doc *gltf.Document) *gltf.Document {
	if len(doc.Buffers) > 0 && strings.HasPrefix(doc.Buffers[0].URI, "data:") {
		doc.Buffers[0].URI = ""
	}
	return doc
}
