// Package main is the FBX handler: Autodesk's interchange format, binary (7.x,
// zlib arrays, 32- and 64-bit node headers) and ASCII. It reads the Model
// hierarchy with FBX's full local-transform formula (translation, rotation
// offset/pivot, pre/post rotation, any Euler order, scaling offset/pivot,
// scale, geometric transform), mesh Geometry (polygons fan-triangulated, with
// ByPolygonVertex / ByVertice / ByPolygon / AllSame layer mapping, Direct and
// IndexToDirect references: normals, UVs, colours, per-polygon materials) and
// Material diffuse colours. A Z-up file is rotated to glTF's Y-up at its roots.
// Not read: skins, blend shapes, animation, cameras, lights, textures, and
// InheritType (parent scale is always composed the standard way).
//
// FBX declares its unit (UnitScaleFactor, centimetres per unit; 1 = cm), and it
// is applied: a file's numbers become glTF's metres. The writer declares
// metres (UnitScaleFactor 100), so a transcode keeps physical size.
package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the FBX format.
var Codec = &scene.Codec{
	ID:      "fbx",
	Formats: []string{".fbx"},
	Decode:  decode,
	Encode:  encode,
}

type material struct {
	name  string
	color *[4]float64
}

type fbxScene struct {
	objects  map[int64]*fnode // Model, Geometry, Material by id
	parent   map[int64]int64
	geoms    map[int64][]int64 // model → geometry ids
	mats     map[int64][]int64 // model → material ids, in slot order
	children map[int64][]int64
	order    []int64 // models in file order
}

// objName is the object's name: "Name\x00\x01Class" in binary, "Class::Name" in text.
func objName(n *fnode) string {
	if len(n.props) < 2 {
		return ""
	}
	s, _ := n.props[1].(string)
	if i := strings.Index(s, "\x00\x01"); i >= 0 {
		return s[:i]
	}
	if i := strings.Index(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}

func objID(n *fnode) (int64, bool) {
	if len(n.props) == 0 {
		return 0, false
	}
	id, ok := n.props[0].(int64)
	return id, ok
}

// props70 maps a node's Properties70 entries to their values (after the four
// name/type/label/flags fields).
func props70(n *fnode) map[string][]any {
	out := map[string][]any{}
	p := n.child("Properties70")
	if p == nil {
		return out
	}
	for _, k := range p.children("P") {
		if len(k.props) < 5 {
			continue
		}
		if name, ok := k.props[0].(string); ok {
			out[name] = k.props[4:]
		}
	}
	return out
}

// asFloats reads a number array; a text file writes whole numbers as integers.
func asFloats(v any) ([]float64, bool) {
	switch x := v.(type) {
	case []float64:
		return x, true
	case []int64:
		out := make([]float64, len(x))
		for i, n := range x {
			out[i] = float64(n)
		}
		return out, true
	}
	return nil, false
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func vec(p map[string][]any, name string, n int, def ...float64) ([]float64, error) {
	v, ok := p[name]
	if !ok {
		return def, nil
	}
	if len(v) < n {
		return nil, fmt.Errorf("property %s has %d values, want %d", name, len(v), n)
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		f, ok := num(v[i])
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("property %s has a bad value", name)
		}
		out[i] = f
	}
	return out, nil
}

func decode(blob []byte) (*scene.Model, error) {
	root, _, err := parseFBX(blob)
	if err != nil {
		return nil, err
	}
	sc := &fbxScene{objects: map[int64]*fnode{}, parent: map[int64]int64{}, geoms: map[int64][]int64{}, mats: map[int64][]int64{}, children: map[int64][]int64{}}
	if objs := root.child("Objects"); objs != nil {
		for _, o := range objs.kids {
			id, ok := objID(o)
			if !ok {
				continue
			}
			switch o.name {
			case "Model", "Geometry", "Material":
				sc.objects[id] = o
				if o.name == "Model" {
					sc.order = append(sc.order, id)
				}
			}
		}
	}
	if conns := root.child("Connections"); conns != nil {
		for _, c := range conns.children("C") {
			if len(c.props) < 3 {
				continue
			}
			kind, _ := c.props[0].(string)
			child, ok1 := c.props[1].(int64)
			par, ok2 := c.props[2].(int64)
			if kind != "OO" || !ok1 || !ok2 {
				continue
			}
			co, po := sc.objects[child], sc.objects[par]
			switch {
			case co == nil:
			case co.name == "Model":
				if po != nil && po.name == "Model" {
					sc.parent[child] = par
					sc.children[par] = append(sc.children[par], child)
				}
			case co.name == "Geometry" && po != nil && po.name == "Model":
				sc.geoms[par] = append(sc.geoms[par], child)
			case co.name == "Material" && po != nil && po.name == "Model":
				sc.mats[par] = append(sc.mats[par], child)
			}
		}
	}

	m := &scene.Model{}
	for _, id := range sc.order {
		if _, nested := sc.parent[id]; nested {
			continue
		}
		o, err := sc.model(id, 0)
		if err != nil {
			return nil, err
		}
		m.Roots = append(m.Roots, o)
	}
	if gs := root.child("GlobalSettings"); gs != nil {
		g := props70(gs)
		up := 1
		if v, ok := g["UpAxis"]; ok && len(v) > 0 {
			if f, ok := num(v[0]); ok {
				up = int(f)
			}
		}
		sign := 1.0
		if v, ok := g["UpAxisSign"]; ok && len(v) > 0 {
			if f, ok := num(v[0]); ok {
				sign = f
			}
		}
		// UnitScaleFactor is centimetres per unit (1 by default): the file's
		// numbers are scaled to glTF's metres at the roots.
		if v, ok := g["UnitScaleFactor"]; ok && len(v) > 0 {
			if f, ok := num(v[0]); ok && f > 0 && f != 100 {
				k := f / 100
				sm := [16]float64{k, 0, 0, 0, 0, k, 0, 0, 0, 0, k, 0, 0, 0, 0, 1}
				for _, r := range m.Roots {
					r.Matrix = mulMat(&sm, r.Matrix)
				}
			}
		}
		switch {
		case up == 1 && sign > 0:
		case up == 2 && sign > 0: // Z up: (x, y, z) → (x, z, -y)
			rot := [16]float64{1, 0, 0, 0, 0, 0, -1, 0, 0, 1, 0, 0, 0, 0, 0, 1}
			for _, r := range m.Roots {
				r.Matrix = mulMat(&rot, r.Matrix)
			}
		default:
			return nil, fmt.Errorf("up axis %d (sign %g) is not supported: only Y-up and Z-up files can be read", up, sign)
		}
	}
	return m, nil
}

func (sc *fbxScene) model(id int64, depth int) (*scene.Object, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("models nest deeper than %d (a cycle?)", maxDepth)
	}
	n := sc.objects[id]
	o := &scene.Object{Name: objName(n)}
	if o.Name == "" {
		o.Name = fmt.Sprintf("model%d", id)
	}
	p := props70(n)
	mat, err := localMatrix(p)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", o.Name, err)
	}
	o.Matrix = mat

	var mats []*material
	for _, mid := range sc.mats[id] {
		mats = append(mats, readMaterial(sc.objects[mid]))
	}
	geom, err := geometricMatrix(p)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", o.Name, err)
	}
	for _, gid := range sc.geoms[id] {
		prims, err := meshPrims(sc.objects[gid], mats)
		if err != nil {
			return nil, fmt.Errorf("geometry of %q: %w", o.Name, err)
		}
		if geom != nil {
			for _, pr := range prims {
				bake(pr, geom)
			}
		}
		o.Prims = append(o.Prims, prims...)
	}
	for _, c := range sc.children[id] {
		co, err := sc.model(c, depth+1)
		if err != nil {
			return nil, err
		}
		o.Children = append(o.Children, co)
	}
	return o, nil
}

func readMaterial(n *fnode) *material {
	m := &material{name: objName(n)}
	p := props70(n)
	if c, err := vec(p, "DiffuseColor", 3); err == nil && c != nil {
		col := [4]float64{c[0], c[1], c[2], 1}
		if f, err := vec(p, "DiffuseFactor", 1); err == nil && f != nil {
			col[0], col[1], col[2] = col[0]*f[0], col[1]*f[0], col[2]*f[0]
		}
		if o, err := vec(p, "Opacity", 1); err == nil && o != nil {
			col[3] = o[0]
		}
		m.color = &col
	}
	return m
}

// ── transforms ────────────────────────────────────────────────────────────────

var ident = [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}

func mulMat(a, b *[16]float64) *[16]float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	var o [16]float64
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			var s float64
			for k := 0; k < 4; k++ {
				s += a[k*4+r] * b[c*4+k]
			}
			o[c*4+r] = s
		}
	}
	return &o
}

func trans(v []float64) [16]float64 {
	return [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, v[0], v[1], v[2], 1}
}

func scaling(v []float64) [16]float64 {
	return [16]float64{v[0], 0, 0, 0, 0, v[1], 0, 0, 0, 0, v[2], 0, 0, 0, 0, 1}
}

func rotAxis(axis int, deg float64) [16]float64 {
	a := deg * math.Pi / 180
	c, s := math.Cos(a), math.Sin(a)
	switch axis {
	case 0:
		return [16]float64{1, 0, 0, 0, 0, c, s, 0, 0, -s, c, 0, 0, 0, 0, 1}
	case 1:
		return [16]float64{c, 0, -s, 0, 0, 1, 0, 0, s, 0, c, 0, 0, 0, 0, 1}
	}
	return [16]float64{c, s, 0, 0, -s, c, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

// euler composes rotations in degrees about x, y, z for an FBX rotation order
// (0 XYZ, 1 XZY, 2 YZX, 3 YXZ, 4 ZXY, 5 ZYX, 6 spheric XYZ). The first letter
// is applied first, so XYZ is Rz·Ry·Rx.
func euler(v []float64, order int) ([16]float64, error) {
	seq := map[int][3]int{0: {0, 1, 2}, 1: {0, 2, 1}, 2: {1, 2, 0}, 3: {1, 0, 2}, 4: {2, 0, 1}, 5: {2, 1, 0}, 6: {0, 1, 2}}
	s, ok := seq[order]
	if !ok {
		return ident, fmt.Errorf("unknown rotation order %d", order)
	}
	r := ident
	for _, ax := range s { // first applied is rightmost
		r = *mulMat(ptr(rotAxis(ax, v[ax])), &r)
	}
	return r, nil
}

func ptr(m [16]float64) *[16]float64 { return &m }

func inverseRigid(m [16]float64) [16]float64 {
	// Rotations and translations only: the transpose of the 3×3 and the
	// negated, rotated translation.
	var o [16]float64
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			o[c*4+r] = m[r*4+c]
		}
	}
	for r := 0; r < 3; r++ {
		o[12+r] = -(o[0*4+r]*m[12] + o[1*4+r]*m[13] + o[2*4+r]*m[14])
	}
	o[15] = 1
	return o
}

// localMatrix is FBX's: T · Roff · Rp · Rpre · R · Rpost⁻¹ · Rp⁻¹ · Soff · Sp · S · Sp⁻¹.
func localMatrix(p map[string][]any) (*[16]float64, error) {
	t, err := vec(p, "Lcl Translation", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	r, err := vec(p, "Lcl Rotation", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	s, err := vec(p, "Lcl Scaling", 3, 1, 1, 1)
	if err != nil {
		return nil, err
	}
	roff, err := vec(p, "RotationOffset", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	rp, err := vec(p, "RotationPivot", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	pre, err := vec(p, "PreRotation", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	post, err := vec(p, "PostRotation", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	soff, err := vec(p, "ScalingOffset", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	sp, err := vec(p, "ScalingPivot", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	order := 0
	if v, ok := p["RotationOrder"]; ok && len(v) > 0 {
		if f, ok := num(v[0]); ok {
			order = int(f)
		}
	}
	rot, err := euler(r, order)
	if err != nil {
		return nil, err
	}
	preM, err := euler(pre, order)
	if err != nil {
		return nil, err
	}
	postM, err := euler(post, order)
	if err != nil {
		return nil, err
	}
	neg := func(v []float64) []float64 { return []float64{-v[0], -v[1], -v[2]} }
	var m *[16]float64
	for _, f := range [][16]float64{
		trans(t), trans(roff), trans(rp), preM, rot, inverseRigid(postM), trans(neg(rp)),
		trans(soff), trans(sp), scaling(s), trans(neg(sp)),
	} {
		f := f
		m = mulMat(m, &f)
	}
	if *m == ident {
		return nil, nil
	}
	return m, nil
}

// geometricMatrix is the transform that applies to a model's own geometry only.
func geometricMatrix(p map[string][]any) (*[16]float64, error) {
	t, err := vec(p, "GeometricTranslation", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	r, err := vec(p, "GeometricRotation", 3, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	s, err := vec(p, "GeometricScaling", 3, 1, 1, 1)
	if err != nil {
		return nil, err
	}
	rot, err := euler(r, 0)
	if err != nil {
		return nil, err
	}
	var m *[16]float64
	for _, f := range [][16]float64{trans(t), rot, scaling(s)} {
		f := f
		m = mulMat(m, &f)
	}
	if *m == ident {
		return nil, nil
	}
	return m, nil
}

// bake applies a transform to a primitive's vertices (and its normals, by the
// rotation part).
func bake(p *scene.FlatPrim, m *[16]float64) {
	for i, v := range p.Positions {
		p.Positions[i] = [3]float64{
			m[0]*v[0] + m[4]*v[1] + m[8]*v[2] + m[12],
			m[1]*v[0] + m[5]*v[1] + m[9]*v[2] + m[13],
			m[2]*v[0] + m[6]*v[1] + m[10]*v[2] + m[14],
		}
	}
	for i, v := range p.Normals {
		n := [3]float64{
			m[0]*v[0] + m[4]*v[1] + m[8]*v[2],
			m[1]*v[0] + m[5]*v[1] + m[9]*v[2],
			m[2]*v[0] + m[6]*v[1] + m[10]*v[2],
		}
		if l := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2]); l > 0 {
			n = [3]float64{n[0] / l, n[1] / l, n[2] / l}
		}
		p.Normals[i] = n
	}
}

// ── geometry ──────────────────────────────────────────────────────────────────

type layer struct {
	mapping string
	ref     string
	data    []float64
	index   []int64
	stride  int
}

// readLayer reads the first LayerElement<kind> of a geometry.
func readLayer(g *fnode, kind, dataName, indexName string, stride int) (*layer, error) {
	n := g.child("LayerElement" + kind)
	if n == nil {
		return nil, nil
	}
	l := &layer{stride: stride}
	if c := n.child("MappingInformationType"); c != nil && len(c.props) > 0 {
		l.mapping, _ = c.props[0].(string)
	}
	if c := n.child("ReferenceInformationType"); c != nil && len(c.props) > 0 {
		l.ref, _ = c.props[0].(string)
	}
	if c := n.child(dataName); c != nil && len(c.props) > 0 {
		d, ok := c.props[0].([]float64)
		if !ok {
			if ii, ok := c.props[0].([]int64); ok { // an ASCII array of whole numbers
				d = make([]float64, len(ii))
				for i, v := range ii {
					d[i] = float64(v)
				}
			}
		}
		l.data = d
	}
	if c := n.child(indexName); c != nil && len(c.props) > 0 {
		l.index, _ = c.props[0].([]int64)
	}
	return l, nil
}

// at returns the direct (data) index for a polygon corner, or -1 when the layer
// has no entry for it.
func (l *layer) at(corner, vert, poly int) (int, error) {
	var i int
	switch l.mapping {
	case "ByPolygonVertex":
		i = corner
	case "ByVertice", "ByVertex":
		i = vert
	case "ByPolygon":
		i = poly
	case "AllSame":
		i = 0
	default:
		return -1, nil // a mapping this handler does not know: the attribute is not carried
	}
	if l.ref == "IndexToDirect" || l.ref == "Index" {
		if i < 0 || i >= len(l.index) {
			return -1, fmt.Errorf("layer index %d is out of range (%d)", i, len(l.index))
		}
		i = int(l.index[i])
	}
	if i < 0 || (i+1)*l.stride > len(l.data) {
		return -1, fmt.Errorf("layer entry %d is out of range", i)
	}
	return i, nil
}

func meshPrims(g *fnode, mats []*material) ([]*scene.FlatPrim, error) {
	vn := g.child("Vertices")
	pn := g.child("PolygonVertexIndex")
	if vn == nil || pn == nil || len(vn.props) == 0 || len(pn.props) == 0 {
		return nil, nil // not a mesh
	}
	verts, ok := asFloats(vn.props[0])
	if !ok {
		return nil, fmt.Errorf("Vertices is not a number array")
	}
	idx, ok := pn.props[0].([]int64)
	if !ok {
		return nil, fmt.Errorf("PolygonVertexIndex is not an integer array")
	}
	if len(verts)%3 != 0 {
		return nil, fmt.Errorf("Vertices has %d numbers, not a whole number of points", len(verts))
	}
	nv := len(verts) / 3

	// Split the corner list into polygons: a negative index ends one (~v).
	type corner struct{ vert int }
	var corners []corner
	var polyStart []int
	polyStart = append(polyStart, 0)
	for _, v := range idx {
		end := v < 0
		if end {
			v = ^v
		}
		if v < 0 || int(v) >= nv {
			return nil, fmt.Errorf("vertex index %d is out of range (%d vertices)", v, nv)
		}
		corners = append(corners, corner{int(v)})
		if end {
			polyStart = append(polyStart, len(corners))
		}
	}
	if polyStart[len(polyStart)-1] != len(corners) {
		return nil, fmt.Errorf("PolygonVertexIndex ends inside a polygon")
	}
	polyStart = polyStart[:len(polyStart)-1]

	nrm, err := readLayer(g, "Normal", "Normals", "NormalsIndex", 3)
	if err != nil {
		return nil, err
	}
	uv, err := readLayer(g, "UV", "UV", "UVIndex", 2)
	if err != nil {
		return nil, err
	}
	col, err := readLayer(g, "Color", "Colors", "ColorIndex", 4)
	if err != nil {
		return nil, err
	}
	matL, err := readLayer(g, "Material", "Materials", "", 1)
	if err != nil {
		return nil, err
	}
	if matL != nil && matL.data == nil && g.child("LayerElementMaterial") != nil {
		// Materials is an integer array, not a float one.
		if c := g.child("LayerElementMaterial").child("Materials"); c != nil && len(c.props) > 0 {
			if ii, ok := c.props[0].([]int64); ok {
				matL.data = make([]float64, len(ii))
				for i, v := range ii {
					matL.data[i] = float64(v)
				}
			}
		}
	}
	// A layer whose mapping we do not understand is dropped.
	for _, l := range []**layer{&nrm, &uv, &col} {
		if *l != nil && (*l).data == nil {
			*l = nil
		}
	}

	type key [4]int32
	type group struct {
		prim *scene.FlatPrim
		seen map[key]uint32
	}
	groups := map[int]*group{}
	var order []int
	for p, start := range polyStart {
		end := len(corners)
		if p+1 < len(polyStart) {
			end = polyStart[p+1]
		}
		slot := -1
		if len(mats) > 0 {
			slot = 0
			if matL != nil && len(matL.data) > 0 {
				i := p
				switch matL.mapping {
				case "AllSame":
					i = 0
				case "ByPolygon":
				default:
					i = 0
				}
				if i < len(matL.data) {
					slot = int(matL.data[i])
				}
			}
			if slot < 0 || slot >= len(mats) {
				slot = 0
			}
		}
		grp := groups[slot]
		if grp == nil {
			fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
			if slot >= 0 {
				fp.Material = mats[slot].name
				fp.BaseColor = mats[slot].color
			}
			grp = &group{prim: fp, seen: map[key]uint32{}}
			groups[slot] = grp
			order = append(order, slot)
		}
		vertex := func(c int) (uint32, error) {
			k := key{int32(corners[c].vert), -1, -1, -1}
			var ni, ui, ci int
			var err error
			if nrm != nil {
				if ni, err = nrm.at(c, corners[c].vert, p); err != nil {
					return 0, err
				}
				k[1] = int32(ni)
			}
			if uv != nil {
				if ui, err = uv.at(c, corners[c].vert, p); err != nil {
					return 0, err
				}
				k[2] = int32(ui)
			}
			if col != nil {
				if ci, err = col.at(c, corners[c].vert, p); err != nil {
					return 0, err
				}
				k[3] = int32(ci)
			}
			if v, ok := grp.seen[k]; ok {
				return v, nil
			}
			fp := grp.prim
			v := corners[c].vert
			fp.Positions = append(fp.Positions, [3]float64{verts[v*3], verts[v*3+1], verts[v*3+2]})
			if k[1] >= 0 {
				d := nrm.data[int(k[1])*3:]
				fp.Normals = append(fp.Normals, [3]float64{d[0], d[1], d[2]})
			}
			if k[2] >= 0 {
				d := uv.data[int(k[2])*2:]
				fp.UVs = append(fp.UVs, [2]float64{d[0], 1 - d[1]}) // FBX v is up, glTF's down
			}
			if k[3] >= 0 {
				d := col.data[int(k[3])*4:]
				fp.Colors = append(fp.Colors, [4]float64{d[0], d[1], d[2], d[3]})
			}
			id := uint32(len(fp.Positions) - 1)
			grp.seen[k] = id
			return id, nil
		}
		var poly []uint32
		for c := start; c < end; c++ {
			v, err := vertex(c)
			if err != nil {
				return nil, err
			}
			poly = append(poly, v)
		}
		for k := 1; k+1 < len(poly); k++ { // fan-triangulate
			grp.prim.Indices = append(grp.prim.Indices, poly[0], poly[k], poly[k+1])
		}
	}
	var out []*scene.FlatPrim
	for _, slot := range order {
		fp := groups[slot].prim
		if len(fp.Indices) == 0 {
			continue
		}
		// An attribute some corners lacked cannot be carried: drop rather than misalign.
		if fp.Normals != nil && len(fp.Normals) != len(fp.Positions) {
			fp.Normals = nil
		}
		if fp.UVs != nil && len(fp.UVs) != len(fp.Positions) {
			fp.UVs = nil
		}
		if fp.Colors != nil && len(fp.Colors) != len(fp.Positions) {
			fp.Colors = nil
		}
		out = append(out, fp)
	}
	return out, nil
}
