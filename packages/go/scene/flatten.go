package scene

import (
	"fmt"
	"math"
	"strings"

	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// The exporters of the 3D family (OBJ, STL, PLY, …) all start from the same
// question: what does this glTF actually draw, in world space, as plain
// triangles, lines and points? Flatten answers it once — node transforms
// baked in, strips and fans expanded, mirroring accounted for — so no
// exporter reimplements glTF's scene graph.

// PrimKind is what a flattened primitive draws.
type PrimKind uint8

const (
	KindTriangles PrimKind = iota
	KindLines
	KindPoints
)

// FlatNode is one glTF node: its name, the primitives of its mesh (if any),
// and its children, depth-first as in the scene.
type FlatNode struct {
	Index    int
	Name     string
	Prims    []*FlatPrim
	Children []*FlatNode
}

// FlatPrim is one glTF primitive in world space.
type FlatPrim struct {
	Kind PrimKind
	// Material is the glTF material's name ("" when it has none or no
	// material); MaterialIndex is its index, -1 when absent.
	Material      string
	MaterialIndex int
	// BaseColor is the material's base colour factor (RGBA, 0..1), nil when the
	// material states none. Formats that colour whole surfaces (3MF) read it;
	// per-vertex colour is Colors.
	BaseColor *[4]float64
	// Per-vertex data, parallel to Positions. Normals, UVs and Colors are nil
	// when the primitive has none. Normals are unit length and in world space;
	// UVs keep glTF's orientation (v down); Colors are RGBA in 0..1.
	Positions [][3]float64
	Normals   [][3]float64
	UVs       [][2]float64
	Colors    [][4]float64
	// Indices into the vertex arrays: triples for triangles (already
	// re-wound when the node's transform mirrors, so the front face is still
	// the front), pairs for lines, singles for points.
	Indices []uint32
}

// Flatten reads a GLB/glTF blob and returns the default scene's node trees. A
// document with no scenes yields every node nothing else lists as a child.
func Flatten(blob Blob) ([]*FlatNode, error) {
	doc, err := parseDoc(blob)
	if err != nil {
		return nil, err
	}
	return FlattenDocument(doc)
}

// FlattenDocument is Flatten over a parsed document.
//
// A hostile or corrupt document can name an accessor, buffer view or buffer
// that does not exist, and the glTF reader indexes those without checking. The
// indices this file follows are checked here; anything the reader still trips
// over is turned into an error rather than a panic, because this runs in a
// server-side wasm worker.
func FlattenDocument(doc *gltf.Document) (out []*FlatNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("the glTF is malformed: %v", r)
		}
	}()
	for _, ni := range sceneRootNodes(doc) {
		n, err := flattenNode(doc, ni, identity4, map[int]bool{})
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// Walk calls fn for every node, parents before children.
func Walk(roots []*FlatNode, fn func(n *FlatNode, depth int)) {
	var rec func(n *FlatNode, d int)
	rec = func(n *FlatNode, d int) {
		fn(n, d)
		for _, c := range n.Children {
			rec(c, d+1)
		}
	}
	for _, r := range roots {
		rec(r, 0)
	}
}

func sceneRootNodes(doc *gltf.Document) []int {
	if len(doc.Scenes) > 0 {
		i := 0
		if doc.Scene != nil {
			i = *doc.Scene
		}
		if i >= 0 && i < len(doc.Scenes) {
			return append([]int(nil), doc.Scenes[i].Nodes...)
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

func flattenNode(doc *gltf.Document, ni int, parent mat4, onPath map[int]bool) (*FlatNode, error) {
	if ni < 0 || ni >= len(doc.Nodes) {
		return nil, fmt.Errorf("node index %d out of range", ni)
	}
	if onPath[ni] {
		return nil, fmt.Errorf("node %d is its own ancestor", ni)
	}
	onPath[ni] = true
	defer delete(onPath, ni)

	gn := doc.Nodes[ni]
	world := parent.mul(nodeMatrix(gn))
	n := &FlatNode{Index: ni, Name: gn.Name}
	if gn.Mesh != nil {
		prims, err := flattenMesh(doc, *gn.Mesh, world)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", gn.Name, err)
		}
		n.Prims = prims
	}
	for _, c := range gn.Children {
		cn, err := flattenNode(doc, c, world, onPath)
		if err != nil {
			return nil, err
		}
		n.Children = append(n.Children, cn)
	}
	return n, nil
}

// accessor is doc.Accessors[i], or an error when the document has no such one.
func accessor(doc *gltf.Document, i int, what string) (*gltf.Accessor, error) {
	if i < 0 || i >= len(doc.Accessors) || doc.Accessors[i] == nil {
		return nil, fmt.Errorf("%s accessor %d does not exist", what, i)
	}
	return doc.Accessors[i], nil
}

func flattenMesh(doc *gltf.Document, mi int, world mat4) ([]*FlatPrim, error) {
	if mi < 0 || mi >= len(doc.Meshes) {
		return nil, fmt.Errorf("mesh index %d out of range", mi)
	}
	flip := world.det3() < 0 // a mirroring transform reverses winding
	nm := world.normalMatrix()
	var out []*FlatPrim
	for pi, p := range doc.Meshes[mi].Primitives {
		posAcc, ok := p.Attributes[gltf.POSITION]
		if !ok {
			continue // nothing to draw
		}
		acc, err := accessor(doc, posAcc, "POSITION")
		if err != nil {
			return nil, fmt.Errorf("primitive %d: %w", pi, err)
		}
		pos, err := modeler.ReadPosition(doc, acc, nil)
		if err != nil {
			return nil, fmt.Errorf("primitive %d positions: %w", pi, err)
		}
		fp := &FlatPrim{MaterialIndex: -1}
		for _, v := range pos {
			fp.Positions = append(fp.Positions, world.point(v))
		}
		if a, ok := p.Attributes[gltf.NORMAL]; ok {
			acc, err := accessor(doc, a, "NORMAL")
			if err != nil {
				return nil, fmt.Errorf("primitive %d: %w", pi, err)
			}
			ns, err := modeler.ReadNormal(doc, acc, nil)
			if err != nil {
				return nil, fmt.Errorf("primitive %d normals: %w", pi, err)
			}
			for _, n := range ns {
				fp.Normals = append(fp.Normals, unit3(nm.vector([3]float64{float64(n[0]), float64(n[1]), float64(n[2])})))
			}
		}
		if a, ok := p.Attributes[gltf.TEXCOORD_0]; ok {
			acc, err := accessor(doc, a, "TEXCOORD_0")
			if err != nil {
				return nil, fmt.Errorf("primitive %d: %w", pi, err)
			}
			uvs, err := modeler.ReadTextureCoord(doc, acc, nil)
			if err != nil {
				return nil, fmt.Errorf("primitive %d texcoords: %w", pi, err)
			}
			for _, uv := range uvs {
				fp.UVs = append(fp.UVs, [2]float64{float64(uv[0]), float64(uv[1])})
			}
		}
		if a, ok := p.Attributes[gltf.COLOR_0]; ok {
			acc, err := accessor(doc, a, "COLOR_0")
			if err != nil {
				return nil, fmt.Errorf("primitive %d: %w", pi, err)
			}
			cs, err := modeler.ReadColor64(doc, acc, nil)
			if err != nil {
				return nil, fmt.Errorf("primitive %d colors: %w", pi, err)
			}
			for _, c := range cs {
				fp.Colors = append(fp.Colors, [4]float64{float64(c[0]) / 65535, float64(c[1]) / 65535, float64(c[2]) / 65535, float64(c[3]) / 65535})
			}
		}
		n := len(fp.Positions)
		if (fp.Normals != nil && len(fp.Normals) != n) || (fp.UVs != nil && len(fp.UVs) != n) || (fp.Colors != nil && len(fp.Colors) != n) {
			return nil, fmt.Errorf("primitive %d: attribute counts disagree with POSITION", pi)
		}
		if p.Material != nil && *p.Material >= 0 && *p.Material < len(doc.Materials) {
			fp.MaterialIndex = *p.Material
			fp.Material = doc.Materials[*p.Material].Name
			if pbr := doc.Materials[*p.Material].PBRMetallicRoughness; pbr != nil && pbr.BaseColorFactor != nil {
				c := *pbr.BaseColorFactor
				fp.BaseColor = &c
			}
		}

		idx, err := primIndices(doc, p, n)
		if err != nil {
			return nil, fmt.Errorf("primitive %d: %w", pi, err)
		}
		if err := expandPrim(fp, p.Mode, idx, flip); err != nil {
			return nil, fmt.Errorf("primitive %d: %w", pi, err)
		}
		out = append(out, fp)
	}
	return out, nil
}

func primIndices(doc *gltf.Document, p *gltf.Primitive, n int) ([]uint32, error) {
	var idx []uint32
	if p.Indices != nil {
		acc, err := accessor(doc, *p.Indices, "indices")
		if err != nil {
			return nil, err
		}
		if idx, err = modeler.ReadIndices(doc, acc, nil); err != nil {
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

// expandPrim turns any glTF primitive mode into plain lists: triangle triples,
// line pairs or point singles.
func expandPrim(fp *FlatPrim, mode gltf.PrimitiveMode, idx []uint32, flip bool) error {
	tri := func(a, b, c uint32) {
		if flip {
			b, c = c, b
		}
		fp.Indices = append(fp.Indices, a, b, c)
	}
	switch mode {
	case gltf.PrimitiveTriangles:
		fp.Kind = KindTriangles
		for i := 0; i+2 < len(idx); i += 3 {
			tri(idx[i], idx[i+1], idx[i+2])
		}
	case gltf.PrimitiveTriangleStrip:
		fp.Kind = KindTriangles
		for i := 0; i+2 < len(idx); i++ {
			if i%2 == 0 { // every other triangle of a strip is wound backwards
				tri(idx[i], idx[i+1], idx[i+2])
			} else {
				tri(idx[i+1], idx[i], idx[i+2])
			}
		}
	case gltf.PrimitiveTriangleFan:
		fp.Kind = KindTriangles
		for i := 1; i+1 < len(idx); i++ {
			tri(idx[0], idx[i], idx[i+1])
		}
	case gltf.PrimitiveLines:
		fp.Kind = KindLines
		for i := 0; i+1 < len(idx); i += 2 {
			fp.Indices = append(fp.Indices, idx[i], idx[i+1])
		}
	case gltf.PrimitiveLineStrip:
		fp.Kind = KindLines
		for i := 0; i+1 < len(idx); i++ {
			fp.Indices = append(fp.Indices, idx[i], idx[i+1])
		}
	case gltf.PrimitiveLineLoop:
		fp.Kind = KindLines
		for i := 0; i < len(idx) && len(idx) > 1; i++ {
			fp.Indices = append(fp.Indices, idx[i], idx[(i+1)%len(idx)])
		}
	case gltf.PrimitivePoints:
		fp.Kind = KindPoints
		fp.Indices = append(fp.Indices, idx...)
	default:
		return fmt.Errorf("unsupported mode %d", mode)
	}
	return nil
}

// SafeName makes a glTF name usable where a format splits on whitespace: runs
// of whitespace become one underscore. An empty name becomes <fallback><index>.
func SafeName(name, fallback string, i int) string {
	name = strings.Join(strings.Fields(name), "_")
	if name == "" {
		return fmt.Sprintf("%s%d", fallback, i)
	}
	return name
}

func unit3(v [3]float64) [3]float64 {
	l := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
	if l == 0 {
		return v
	}
	return [3]float64{v[0] / l, v[1] / l, v[2] / l}
}

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
		snap(a[0]*x + a[4]*y + a[8]*z + a[12]),
		snap(a[1]*x + a[5]*y + a[9]*z + a[13]),
		snap(a[2]*x + a[6]*y + a[10]*z + a[14]),
	}
}

// snap zeroes what is only rounding noise. A 90° rotation leaves cos(90°) ≈
// 6e-17 where an exact 0 belongs, and a writer would print that as a long
// string of digits (and a diff would call it a change).
func snap(v float64) float64 {
	if math.Abs(v) < 1e-12 {
		return 0
	}
	return v
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
// under non-uniform scale.
func (a mat4) normalMatrix() mat4 {
	cof := func(r0, r1, c0, c1 int) float64 { // 2×2 minor at rows r0,r1 × cols c0,c1
		return a[c0*4+r0]*a[c1*4+r1] - a[c1*4+r0]*a[c0*4+r1]
	}
	n := identity4
	sign := 1.0
	if a.det3() < 0 {
		sign = -1 // the cofactor matrix is det·inverse-transpose: undo its sign
	}
	rows := [3][2]int{{1, 2}, {0, 2}, {0, 1}}
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			s := sign
			if (r+c)%2 == 1 {
				s = -s
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
