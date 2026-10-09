package main

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
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
	doc := new(gltf.Document)
	if err := gltf.NewDecoder(bytes.NewReader(glb)).Decode(doc); err != nil {
		return nil, fmt.Errorf("reading glTF: %w", err)
	}
	e := objExporter{doc: doc, file: &objFile{}}
	var roots []*writeNode
	for _, ni := range sceneRoots(doc) {
		n, err := e.node(ni, identity4, 0, map[int]bool{})
		if err != nil {
			return nil, err
		}
		if n != nil {
			roots = append(roots, n)
		}
	}
	header := []string{"# Exported by FHR obj handler"}
	return writeOBJ(header, nil, roots), nil
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

// sceneRoots are the default scene's root nodes; a document with no scenes
// exports every node that no other node lists as a child.
func sceneRoots(doc *gltf.Document) []int {
	if len(doc.Scenes) > 0 {
		i := 0
		if doc.Scene != nil {
			i = *doc.Scene
		}
		if i >= 0 && i < len(doc.Scenes) {
			out := make([]int, 0, len(doc.Scenes[i].Nodes))
			for _, n := range doc.Scenes[i].Nodes {
				out = append(out, n)
			}
			return out
		}
	}
	child := map[int]bool{}
	for _, n := range doc.Nodes {
		for _, c := range n.Children {
			child[c] = true
		}
	}
	var out []int
	for i := range doc.Nodes {
		if !child[i] {
			out = append(out, i)
		}
	}
	return out
}

type objExporter struct {
	doc  *gltf.Document
	file *objFile // the pools every written element indexes into
}

// node converts glTF node ni and, depth-first, its children. world is the
// parent's world matrix.
func (e *objExporter) node(ni int, parent mat4, depth int, onPath map[int]bool) (*writeNode, error) {
	if ni < 0 || ni >= len(e.doc.Nodes) {
		return nil, fmt.Errorf("node index %d out of range", ni)
	}
	if onPath[ni] {
		return nil, fmt.Errorf("node %d is its own ancestor", ni)
	}
	onPath[ni] = true
	defer delete(onPath, ni)

	gn := e.doc.Nodes[ni]
	world := parent.mul(nodeMatrix(gn))
	stmt := "g"
	if depth == 0 {
		stmt = "o"
	}
	name := objName(gn.Name, "node", ni)
	w := &writeNode{stmt: stmt, rawName: name}
	if gn.Mesh != nil {
		elems, err := e.mesh(*gn.Mesh, world)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", gn.Name, err)
		}
		w.elems = elems
	}
	for _, c := range gn.Children {
		cw, err := e.node(c, world, depth+1, onPath)
		if err != nil {
			return nil, err
		}
		w.children = append(w.children, cw)
	}
	return w, nil
}

func (e *objExporter) mesh(mi int, world mat4) ([]placedElem, error) {
	if mi < 0 || mi >= len(e.doc.Meshes) {
		return nil, fmt.Errorf("mesh index %d out of range", mi)
	}
	flip := world.det3() < 0 // a mirroring transform reverses winding
	nm := world.normalMatrix()
	var out []placedElem
	for pi, p := range e.doc.Meshes[mi].Primitives {
		posAcc, ok := p.Attributes[gltf.POSITION]
		if !ok {
			continue // nothing to draw
		}
		pos, err := modeler.ReadPosition(e.doc, e.doc.Accessors[posAcc], nil)
		if err != nil {
			return nil, fmt.Errorf("primitive %d positions: %w", pi, err)
		}
		var uvs [][2]float32
		if a, ok := p.Attributes[gltf.TEXCOORD_0]; ok {
			if uvs, err = modeler.ReadTextureCoord(e.doc, e.doc.Accessors[a], nil); err != nil {
				return nil, fmt.Errorf("primitive %d texcoords: %w", pi, err)
			}
		}
		var normals [][3]float32
		if a, ok := p.Attributes[gltf.NORMAL]; ok {
			if normals, err = modeler.ReadNormal(e.doc, e.doc.Accessors[a], nil); err != nil {
				return nil, fmt.Errorf("primitive %d normals: %w", pi, err)
			}
		}
		if (uvs != nil && len(uvs) != len(pos)) || (normals != nil && len(normals) != len(pos)) {
			return nil, fmt.Errorf("primitive %d: attribute counts disagree with POSITION", pi)
		}

		// One pool entry per glTF vertex, in the exported (world) space.
		f := e.file
		base, baseUV, baseN := len(f.positions), len(f.uvs), len(f.normals)
		for _, v := range pos {
			w := world.point(v)
			f.positions = append(f.positions, w)
			f.posText = append(f.posText, vec3Text(w))
		}
		for _, uv := range uvs {
			t := [2]float64{float64(uv[0]), 1 - float64(uv[1])} // glTF v is top-down, OBJ bottom-up
			f.uvs = append(f.uvs, t)
			f.uvText = append(f.uvText, num(t[0])+" "+num(t[1]))
		}
		for _, n := range normals {
			t := normalize(nm.vector([3]float64{float64(n[0]), float64(n[1]), float64(n[2])}))
			f.normals = append(f.normals, t)
			f.normalText = append(f.normalText, vec3Text(t))
		}
		ref := func(i uint32) vref {
			r := vref{v: base + int(i), vt: -1, vn: -1}
			if uvs != nil {
				r.vt = baseUV + int(i)
			}
			if normals != nil {
				r.vn = baseN + int(i)
			}
			return r
		}

		idx, err := e.indices(p, len(pos))
		if err != nil {
			return nil, fmt.Errorf("primitive %d: %w", pi, err)
		}
		material := ""
		if p.Material != nil && *p.Material >= 0 && *p.Material < len(e.doc.Materials) {
			material = objName(e.doc.Materials[*p.Material].Name, "material", *p.Material)
		}
		emit := func(kind elemKind, ids ...uint32) {
			refs := make([]vref, len(ids))
			for i, id := range ids {
				refs[i] = ref(id)
			}
			out = append(out, placedElem{src: f, objElem: objElem{kind: kind, material: material, refs: refs}})
		}
		tri := func(a, b, c uint32) {
			if flip {
				b, c = c, b
			}
			emit(elemFace, a, b, c)
		}
		switch p.Mode {
		case gltf.PrimitiveTriangles:
			for i := 0; i+2 < len(idx); i += 3 {
				tri(idx[i], idx[i+1], idx[i+2])
			}
		case gltf.PrimitiveTriangleStrip:
			for i := 0; i+2 < len(idx); i++ {
				if i%2 == 0 { // every other triangle of a strip is wound backwards
					tri(idx[i], idx[i+1], idx[i+2])
				} else {
					tri(idx[i+1], idx[i], idx[i+2])
				}
			}
		case gltf.PrimitiveTriangleFan:
			for i := 1; i+1 < len(idx); i++ {
				tri(idx[0], idx[i], idx[i+1])
			}
		case gltf.PrimitiveLines:
			for i := 0; i+1 < len(idx); i += 2 {
				emit(elemLine, idx[i], idx[i+1])
			}
		case gltf.PrimitiveLineStrip:
			for i := 0; i+1 < len(idx); i++ {
				emit(elemLine, idx[i], idx[i+1])
			}
		case gltf.PrimitiveLineLoop:
			for i := 0; i < len(idx) && len(idx) > 1; i++ {
				emit(elemLine, idx[i], idx[(i+1)%len(idx)])
			}
		case gltf.PrimitivePoints:
			for _, id := range idx {
				emit(elemPoint, id)
			}
		default:
			return nil, fmt.Errorf("primitive %d: unsupported mode %d", pi, p.Mode)
		}
	}
	return out, nil
}

// indices is the primitive's index list, or 0..n-1 for a non-indexed one.
func (e *objExporter) indices(p *gltf.Primitive, n int) ([]uint32, error) {
	var idx []uint32
	if p.Indices != nil {
		var err error
		if idx, err = modeler.ReadIndices(e.doc, e.doc.Accessors[*p.Indices], nil); err != nil {
			return nil, fmt.Errorf("indices: %w", err)
		}
	} else {
		idx = make([]uint32, n)
		for i := range idx {
			idx[i] = uint32(i)
		}
	}
	for _, i := range idx {
		if int(i) >= n {
			return nil, fmt.Errorf("index %d out of range (%d vertices)", i, n)
		}
	}
	return idx, nil
}

// objName makes a glTF name usable as an OBJ statement argument: OBJ splits
// on whitespace, so a name with any would read back as several. An empty name
// becomes <fallback><index>.
func objName(name, fallback string, i int) string {
	name = strings.Join(strings.Fields(name), "_")
	if name == "" {
		return fallback + strconv.Itoa(i)
	}
	return name
}

func num(v float64) string {
	f := float32(v)
	if f == 0 {
		f = 0 // not "-0": a negated zero is still zero, and noise in a diff
	}
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

func vec3Text(v [3]float64) string { return num(v[0]) + " " + num(v[1]) + " " + num(v[2]) }

func normalize(v [3]float64) [3]float64 { return unit(v) }

// mat4 is a column-major 4×4 matrix, glTF's layout: element (row r, column c)
// is m[c*4+r].
type mat4 [16]float64

var identity4 = mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}

func (a mat4) mul(b mat4) mat4 {
	var o mat4
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			var s float64
			for k := 0; k < 4; k++ {
				s += a[k*4+r] * b[c*4+k]
			}
			o[c*4+r] = s
		}
	}
	return o
}

func (a mat4) point(v [3]float32) [3]float64 {
	x, y, z := float64(v[0]), float64(v[1]), float64(v[2])
	return [3]float64{
		a[0]*x + a[4]*y + a[8]*z + a[12],
		a[1]*x + a[5]*y + a[9]*z + a[13],
		a[2]*x + a[6]*y + a[10]*z + a[14],
	}
}

// vector applies the upper 3×3 only (no translation).
func (a mat4) vector(v [3]float64) [3]float64 {
	return [3]float64{
		a[0]*v[0] + a[4]*v[1] + a[8]*v[2],
		a[1]*v[0] + a[5]*v[1] + a[9]*v[2],
		a[2]*v[0] + a[6]*v[1] + a[10]*v[2],
	}
}

func (a mat4) det3() float64 {
	return a[0]*(a[5]*a[10]-a[9]*a[6]) - a[4]*(a[1]*a[10]-a[9]*a[2]) + a[8]*(a[1]*a[6]-a[5]*a[2])
}

// normalMatrix is the cofactor matrix of the upper 3×3 — the inverse transpose
// up to a scale, which normalisation removes — so normals stay perpendicular
// under non-uniform scale. Returned as a mat4 so vector() applies it.
func (a mat4) normalMatrix() mat4 {
	cof := func(r0, r1, c0, c1 int) float64 { // minor of the 3×3 at rows r0,r1 × cols c0,c1
		return a[c0*4+r0]*a[c1*4+r1] - a[c1*4+r0]*a[c0*4+r1]
	}
	n := identity4
	sign := 1.0
	if a.det3() < 0 {
		sign = -1 // the cofactor matrix is det·inverse-transpose: undo its sign
	}
	// n(r,c) = (-1)^(r+c) · minor excluding row r, col c
	rows := [3][2]int{{1, 2}, {0, 2}, {0, 1}}
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			s := sign
			if (r+c)%2 == 1 {
				s = -1
			}
			n[c*4+r] = s * cof(rows[r][0], rows[r][1], rows[c][0], rows[c][1])
		}
	}
	return n
}

// nodeMatrix is a node's local transform: its matrix when it has one, else
// translation · rotation · scale.
func nodeMatrix(n *gltf.Node) mat4 {
	if m := n.MatrixOrDefault(); m != [16]float64(identity4) {
		return mat4(m)
	}
	t, q, s := n.TranslationOrDefault(), n.RotationOrDefault(), n.ScaleOrDefault()
	x, y, z, w := q[0], q[1], q[2], q[3]
	if l := math.Sqrt(x*x + y*y + z*z + w*w); l > 0 {
		x, y, z, w = x/l, y/l, z/l, w/l
	}
	return mat4{
		(1 - 2*(y*y+z*z)) * s[0], (2 * (x*y + z*w)) * s[0], (2 * (x*z - y*w)) * s[0], 0,
		(2 * (x*y - z*w)) * s[1], (1 - 2*(x*x+z*z)) * s[1], (2 * (y*z + x*w)) * s[1], 0,
		(2 * (x*z + y*w)) * s[2], (2 * (y*z - x*w)) * s[2], (1 - 2*(x*x+y*y)) * s[2], 0,
		t[0], t[1], t[2], 1,
	}
}
