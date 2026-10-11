package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/forgehubproject/fhr/packages/go/scene"
	"github.com/qmuntal/gltf"
)

// Import converts an OBJ blob to the GLB the diff is computed over — the same
// document Preview draws, without the preview's display materials, so a
// transcode carries only what the file says.
func (h *Handler) Import(blob fhr.Blob) (fhr.Blob, error) {
	f, err := parseOBJ(blob)
	if err != nil {
		return nil, err
	}
	return encodeGLB(toGLTF(f))
}

// Export writes a GLB as an OBJ. OBJ has no transform hierarchy, so every
// node's world transform is baked into its vertices; it has no PBR materials,
// so a primitive's material keeps only its name (usemtl, with no .mtl library:
// a single-file export cannot carry one). Cameras, lights, skins, animation
// and morph targets are dropped. Nodes become `o` (scene roots) and `g`
// (their descendants), the vocabulary Import reads back.
func (h *Handler) Export(glb fhr.Blob, format string) (fhr.Blob, error) {
	if !strings.EqualFold(format, ".obj") {
		return nil, fmt.Errorf("obj handler exports .obj, not %q", format)
	}
	roots, err := scene.Flatten(glb)
	if err != nil {
		return nil, fmt.Errorf("reading glTF: %w", err)
	}
	file := &objFile{} // the pools every written element indexes into
	var out []*writeNode
	for _, r := range roots {
		out = append(out, objNodeOf(file, r, 0))
	}
	return writeOBJ([]string{"# Exported by FHR obj handler"}, nil, out), nil
}

func encodeGLB(doc *gltf.Document) (fhr.Blob, error) {
	var buf bytes.Buffer
	enc := gltf.NewEncoder(&buf)
	enc.AsBinary = true
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encoding glTF: %w", err)
	}
	return buf.Bytes(), nil
}

// objNodeOf converts a flattened node: scene roots become `o`, descendants `g`
// (the vocabulary Import reads back).
func objNodeOf(f *objFile, n *scene.FlatNode, depth int) *writeNode {
	stmt := "g"
	if depth == 0 {
		stmt = "o"
	}
	w := &writeNode{stmt: stmt, rawName: scene.SafeName(n.Name, "node", n.Index)}
	for _, p := range n.Prims {
		w.elems = append(w.elems, objElems(f, p)...)
	}
	for _, c := range n.Children {
		w.children = append(w.children, objNodeOf(f, c, depth+1))
	}
	return w
}

// objElems appends the primitive's vertices to the shared pools and returns
// its elements, which index them.
func objElems(f *objFile, p *scene.FlatPrim) []placedElem {
	base, baseUV, baseN := len(f.positions), len(f.uvs), len(f.normals)
	for _, v := range p.Positions {
		f.positions = append(f.positions, v)
		f.posText = append(f.posText, vec3Text(v))
	}
	for _, uv := range p.UVs {
		t := [2]float64{uv[0], 1 - uv[1]} // glTF v is top-down, OBJ bottom-up
		f.uvs = append(f.uvs, t)
		f.uvText = append(f.uvText, num(t[0])+" "+num(t[1]))
	}
	for _, n := range p.Normals {
		f.normals = append(f.normals, n)
		f.normalText = append(f.normalText, vec3Text(n))
	}
	material := ""
	if p.MaterialIndex >= 0 {
		material = scene.SafeName(p.Material, "material", p.MaterialIndex)
	}
	ref := func(i uint32) vref {
		r := vref{v: base + int(i), vt: -1, vn: -1}
		if p.UVs != nil {
			r.vt = baseUV + int(i)
		}
		if p.Normals != nil {
			r.vn = baseN + int(i)
		}
		return r
	}
	kind, per := elemFace, 3
	switch p.Kind {
	case scene.KindLines:
		kind, per = elemLine, 2
	case scene.KindPoints:
		kind, per = elemPoint, 1
	}
	var out []placedElem
	for i := 0; i+per <= len(p.Indices); i += per {
		refs := make([]vref, per)
		for j := range refs {
			refs[j] = ref(p.Indices[i+j])
		}
		out = append(out, placedElem{src: f, objElem: objElem{kind: kind, material: material, refs: refs}})
	}
	return out
}

func num(v float64) string {
	f := float32(v)
	if f == 0 {
		f = 0 // not "-0": a negated zero is still zero, and noise in a diff
	}
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

func vec3Text(v [3]float64) string { return num(v[0]) + " " + num(v[1]) + " " + num(v[2]) }
