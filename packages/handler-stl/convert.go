package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// Import converts an STL (ASCII or binary) to a GLB: one node holding one
// mesh. STL stores every triangle's three corners separately, so identical
// corners are welded into one vertex — the mesh the file describes, without
// the duplication. STL carries no units; the numbers pass through unchanged
// (glTF reads them as metres, as every consumer of an STL must guess).
func (h *Handler) Import(blob Blob) (Blob, error) {
	m, err := parseSTL(blob)
	if err != nil {
		return nil, err
	}
	doc := &gltf.Document{
		Asset:  gltf.Asset{Version: "2.0", Generator: "FHR stl handler"},
		Scene:  gltf.Index(0),
		Scenes: []*gltf.Scene{{Nodes: []int{0}}},
	}
	name := m.Name
	if name == "" {
		name = "mesh"
	}
	node := &gltf.Node{Name: name}
	doc.Nodes = []*gltf.Node{node}
	if len(m.Triangles) > 0 {
		weld := map[[3]float32]uint32{}
		var pos [][3]float32
		var idx []uint32
		for _, t := range m.Triangles {
			for _, v := range t {
				k := [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}
				i, ok := weld[k]
				if !ok {
					i = uint32(len(pos))
					weld[k] = i
					pos = append(pos, k)
				}
				idx = append(idx, i)
			}
		}
		doc.Meshes = []*gltf.Mesh{{Name: name, Primitives: []*gltf.Primitive{{
			Attributes: gltf.PrimitiveAttributes{gltf.POSITION: modeler.WritePosition(doc, pos)},
			Indices:    gltf.Index(modeler.WriteIndices(doc, idx)),
		}}}}
		node.Mesh = gltf.Index(0)
	}
	var buf bytes.Buffer
	enc := gltf.NewEncoder(&buf)
	enc.AsBinary = true
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encoding glTF: %w", err)
	}
	return buf.Bytes(), nil
}

// Export writes a GLB as a binary STL. STL is a single triangle soup, so every
// triangle in the scene — node transforms baked in — goes into one solid.
// Lines and points are dropped (STL cannot hold them), as are names,
// materials, colours and UVs; facet normals are recomputed from the winding.
// A scene with no triangles is an error rather than an empty file.
func (h *Handler) Export(glb Blob, format string) (Blob, error) {
	if !strings.EqualFold(format, ".stl") {
		return nil, fmt.Errorf("stl handler exports .stl, not %q", format)
	}
	roots, err := scene.Flatten(glb)
	if err != nil {
		return nil, fmt.Errorf("reading glTF: %w", err)
	}
	var tris []triangle
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
			if p.Kind != scene.KindTriangles {
				continue
			}
			for i := 0; i+2 < len(p.Indices); i += 3 {
				tris = append(tris, triangle{
					vec3(p.Positions[p.Indices[i]]),
					vec3(p.Positions[p.Indices[i+1]]),
					vec3(p.Positions[p.Indices[i+2]]),
				})
			}
		}
	})
	if len(tris) == 0 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}
	return writeBinarySTL(tris), nil
}

// writeBinarySTL is the 80-byte header, the triangle count, then 50 bytes per
// triangle: a facet normal, three vertices, a zero attribute word.
func writeBinarySTL(tris []triangle) Blob {
	var b bytes.Buffer
	header := make([]byte, 80)
	copy(header, "Exported by FHR stl handler")
	b.Write(header)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(tris)))
	for _, t := range tris {
		n := cross(sub(t[1], t[0]), sub(t[2], t[0]))
		if l := norm(n); l > 0 {
			n = vec3{n[0] / l, n[1] / l, n[2] / l}
		}
		rec := [12]float32{}
		for i, v := range []vec3{n, t[0], t[1], t[2]} {
			for j := 0; j < 3; j++ {
				f := float32(v[j])
				if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
					f = 0
				}
				rec[i*3+j] = f
			}
		}
		_ = binary.Write(&b, binary.LittleEndian, rec)
		b.Write([]byte{0, 0})
	}
	return b.Bytes()
}
