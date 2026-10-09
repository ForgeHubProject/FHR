package main

import (
	"cmp"
	"math"
	"slices"

	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// toGLTF converts a parsed OBJ into the glTF document the scene engine diffs
// and the preview encodes — one document for both, which is the point: the
// names the diff's paths use are the node names in the GLB the viewer draws.
//
//   - every o/g node becomes a glTF node of the same name, keeping the o → g
//     parent/child relationship; a node with elements gets a mesh of the same
//     name;
//   - a mesh has one primitive per (element kind, material, attribute set), in
//     order of first use, so a face without UVs never forces UVs onto its
//     neighbours or drops theirs;
//   - faces are fan-triangulated (as three.js's OBJLoader does), lines become
//     LINES segments, points POINTS;
//   - each usemtl name becomes a material of that name. Its properties live in
//     the .mtl library, which a single-file handler cannot read, so every
//     material gets the same neutral surface and material *property* changes
//     are out of scope (#15's pairing question); assignment changes are not.
//
// The conversion is deterministic — the same OBJ always yields the same
// document — which the engine's content signatures rely on.
func toGLTF(f *objFile) *gltf.Document {
	doc := &gltf.Document{
		Asset:  gltf.Asset{Version: "2.0", Generator: "FHR obj handler"},
		Scene:  gltf.Index(0),
		Scenes: []*gltf.Scene{{}},
	}
	c := converter{f: f, doc: doc, materials: map[string]int{}}
	for _, r := range f.roots {
		doc.Scenes[0].Nodes = append(doc.Scenes[0].Nodes, c.node(r))
	}
	return doc
}

type converter struct {
	f         *objFile
	doc       *gltf.Document
	materials map[string]int
}

// node appends n (and, depth-first, its children) and returns its index.
func (c *converter) node(n *objNode) int {
	idx := len(c.doc.Nodes)
	gn := &gltf.Node{Name: n.name}
	c.doc.Nodes = append(c.doc.Nodes, gn)
	if len(n.elems) > 0 {
		gn.Mesh = gltf.Index(c.mesh(n))
	}
	for _, ch := range n.children {
		gn.Children = append(gn.Children, c.node(ch))
	}
	return idx
}

// primKey groups elements that can share one glTF primitive.
type primKey struct {
	kind                  elemKind
	material              string
	uv, normal, withColor bool
}

func (c *converter) mesh(n *objNode) int {
	var order []primKey
	groups := map[primKey][]objElem{}
	for _, e := range n.elems {
		k := primKey{kind: e.kind, material: e.material, uv: true, normal: true, withColor: true}
		for _, r := range e.refs {
			k.uv = k.uv && r.vt >= 0
			k.normal = k.normal && r.vn >= 0
			k.withColor = k.withColor && c.f.hasColor[r.v]
		}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], e)
	}

	m := &gltf.Mesh{Name: n.name}
	for _, k := range order {
		m.Primitives = append(m.Primitives, c.primitive(k, groups[k]))
	}
	c.doc.Meshes = append(c.doc.Meshes, m)
	return len(c.doc.Meshes) - 1
}

func (c *converter) primitive(k primKey, elems []objElem) *gltf.Primitive {
	// Each distinct (v, vt, vn) reference is one glTF vertex; glTF indexes all
	// attributes together where OBJ indexes them apart. Vertices are ordered by
	// their references — the order of the file's own v/vt/vn pools — not by
	// first use, so reordering or re-winding faces changes only the indices and
	// a POSITION change always means a vertex actually moved.
	used := map[vref]uint32{}
	for _, e := range elems {
		for _, r := range e.refs {
			used[r] = 0
		}
	}
	refs := make([]vref, 0, len(used))
	for r := range used {
		refs = append(refs, r)
	}
	slices.SortFunc(refs, func(a, b vref) int {
		return cmp.Or(cmp.Compare(a.v, b.v), cmp.Compare(a.vt, b.vt), cmp.Compare(a.vn, b.vn))
	})

	pos := make([][3]float32, len(refs))
	var uvs [][2]float32
	var normals, colors [][3]float32
	for i, r := range refs {
		used[r] = uint32(i)
		pos[i] = f32x3(c.f.positions[r.v])
		if k.uv {
			uv := c.f.uvs[r.vt]
			// OBJ puts v = 0 at the bottom of the image, glTF at the top.
			uvs = append(uvs, [2]float32{float32(uv[0]), float32(1 - uv[1])})
		}
		if k.normal {
			normals = append(normals, f32x3(unit(c.f.normals[r.vn])))
		}
		if k.withColor {
			colors = append(colors, f32x3(c.f.colors[r.v]))
		}
	}

	var indices []uint32
	mode := gltf.PrimitiveTriangles
	for _, e := range elems {
		switch e.kind {
		case elemFace:
			for j := 1; j+1 < len(e.refs); j++ {
				indices = append(indices, used[e.refs[0]], used[e.refs[j]], used[e.refs[j+1]])
			}
		case elemLine:
			mode = gltf.PrimitiveLines
			for j := 0; j+1 < len(e.refs); j++ {
				indices = append(indices, used[e.refs[j]], used[e.refs[j+1]])
			}
		case elemPoint:
			mode = gltf.PrimitivePoints
			for _, r := range e.refs {
				indices = append(indices, used[r])
			}
		}
	}

	p := &gltf.Primitive{
		Mode:       mode,
		Attributes: gltf.PrimitiveAttributes{gltf.POSITION: modeler.WritePosition(c.doc, pos)},
		Indices:    gltf.Index(modeler.WriteIndices(c.doc, indices)),
	}
	if k.normal {
		p.Attributes[gltf.NORMAL] = modeler.WriteNormal(c.doc, normals)
	}
	if k.uv {
		p.Attributes[gltf.TEXCOORD_0] = modeler.WriteTextureCoord(c.doc, uvs)
	}
	if k.withColor {
		p.Attributes[gltf.COLOR_0] = modeler.WriteColor(c.doc, colors)
	}
	if k.material != "" {
		p.Material = gltf.Index(c.material(k.material))
	}
	return p
}

// material returns the index of the material named name, adding it on first use.
//
// The material states nothing but its name. Its properties live in the .mtl
// library, which a single-file handler cannot read, and anything stated here
// would be identical for every material — which the engine's content tier
// would read as evidence that a removed material and an added one are the same
// material renamed. Stating nothing keeps OBJ materials matched by name alone;
// the preview gets its appearance from scene.DressForPreview instead.
func (c *converter) material(name string) int {
	if i, ok := c.materials[name]; ok {
		return i
	}
	c.doc.Materials = append(c.doc.Materials, &gltf.Material{Name: name})
	c.materials[name] = len(c.doc.Materials) - 1
	return c.materials[name]
}

func f32x3(v [3]float64) [3]float32 {
	return [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}
}

// unit normalizes an OBJ normal; glTF requires unit normals, OBJ does not. A
// zero normal stays zero rather than becoming NaN.
func unit(v [3]float64) [3]float64 {
	l := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
	if l == 0 {
		return v
	}
	return [3]float64{v[0] / l, v[1] / l, v[2] / l}
}
