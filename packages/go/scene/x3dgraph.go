package scene

import (
	"fmt"
	"math"
	"strings"
)

// The X3D scene-graph reader. It works on an XMLNode tree whose element names are
// X3D node names and whose attributes are field values as text — which is what
// the X3D XML encoding parses to directly, and what the VRML97 reader builds from
// VRML's own syntax, since the two share node and field names. One
// interpretation of Transform/Group/Switch/DEF/USE and Shape geometry therefore
// serves both formats.

const (
	x3dMaxDepth   = 64
	x3dMaxNodes   = 2_000_000
	x3dMaxNumbers = 64 << 20
)

type x3dReader struct {
	defs  map[string]*XMLNode
	nodes int
}

// DecodeX3DScene reads the children of an X3D <Scene> (or a VRML file's top
// level) into a Model: Transform (translation/center/rotation/scale/
// scaleOrientation), Group/Switch/LOD, DEF/USE, and Shapes whose geometry is
// IndexedFaceSet, IndexedTriangleSet, TriangleSet, IndexedLineSet, PointSet or
// Box, with Coordinate, Normal, TextureCoordinate, Color and Material diffuse
// colour/transparency. Geometry it cannot read is refused by name.
func DecodeX3DScene(sc *XMLNode) (*Model, error) {
	r := &x3dReader{defs: map[string]*XMLNode{}}
	r.collectDefs(sc)
	m := &Model{}
	for _, k := range sc.Kids {
		o, err := r.node(k, 0)
		if err != nil {
			return nil, err
		}
		if o != nil {
			m.Roots = append(m.Roots, o)
		}
	}
	return m, nil
}

func (r *x3dReader) collectDefs(n *XMLNode) {
	if d := n.Attr["DEF"]; d != "" {
		r.defs[d] = n
	}
	for _, k := range n.Kids {
		r.collectDefs(k)
	}
}

// resolve follows USE to the DEF node it names.
func (r *x3dReader) resolve(n *XMLNode) (*XMLNode, error) {
	if u := n.Attr["USE"]; u != "" {
		d, ok := r.defs[u]
		if !ok {
			return nil, fmt.Errorf("USE %q has no matching DEF", u)
		}
		return d, nil
	}
	return n, nil
}

// x3dGrouping nodes are walked as plain containers (a Switch shows its chosen
// child only; LOD its first, the highest detail).
var x3dGrouping = map[string]bool{"Group": true, "Transform": true, "Anchor": true, "Billboard": true, "Collision": true, "StaticGroup": true, "Switch": true, "LOD": true}

func (r *x3dReader) node(n *XMLNode, depth int) (*Object, error) {
	if depth > x3dMaxDepth {
		return nil, fmt.Errorf("nodes nest deeper than %d (a cycle?)", x3dMaxDepth)
	}
	r.nodes++
	if r.nodes > x3dMaxNodes {
		return nil, fmt.Errorf("X3D expands to more than %d nodes", x3dMaxNodes)
	}
	n, err := r.resolve(n)
	if err != nil {
		return nil, err
	}
	name := n.Attr["DEF"]
	switch {
	case n.Name == "Shape":
		o := &Object{Name: name}
		if o.Name == "" {
			o.Name = "shape"
		}
		p, err := r.shape(n)
		if err != nil {
			return nil, err
		}
		if p != nil {
			o.Prims = []*FlatPrim{p}
		}
		return o, nil
	case x3dGrouping[n.Name]:
		o := &Object{Name: name}
		if o.Name == "" {
			o.Name = strings.ToLower(n.Name)
		}
		if n.Name == "Transform" {
			m, err := x3dTransformMatrix(n)
			if err != nil {
				return nil, err
			}
			o.Matrix = m
		}
		kids := n.Kids
		switch n.Name {
		case "Switch":
			kids = nil
			choice := 0
			if s, ok := n.Attr["whichChoice"]; ok {
				if v, err := Ints(s, 1); err == nil && len(v) == 1 {
					choice = v[0]
				} else {
					choice = -1 // negative: nothing is shown
				}
			}
			var shown []*XMLNode
			for _, k := range n.Kids {
				if k.Name == "Shape" || x3dGrouping[k.Name] || k.Attr["USE"] != "" {
					shown = append(shown, k)
				}
			}
			if choice >= 0 && choice < len(shown) {
				kids = shown[choice : choice+1]
			}
		case "LOD":
			if len(n.Kids) > 0 {
				kids = n.Kids[:1]
			}
		}
		for _, k := range kids {
			// An anonymous Shape belongs to its group: the group is the named
			// thing and its shapes are what it draws (how export writes them).
			if k.Name == "Shape" && k.Attr["DEF"] == "" && k.Attr["USE"] == "" {
				p, err := r.shape(k)
				if err != nil {
					return nil, err
				}
				if p != nil {
					o.Prims = append(o.Prims, p)
				}
				continue
			}
			c, err := r.node(k, depth+1)
			if err != nil {
				return nil, err
			}
			if c != nil {
				o.Children = append(o.Children, c)
			}
		}
		return o, nil
	}
	return nil, nil // lights, viewpoints, sensors, metadata…: not geometry
}

func x3dFloatsAttr(n *XMLNode, attr string, want int) ([]float64, bool, error) {
	s, ok := n.Attr[attr]
	if !ok {
		return nil, false, nil
	}
	v, err := Floats(s, x3dMaxNumbers)
	if err != nil {
		return nil, true, fmt.Errorf("%s.%s: %w", n.Name, attr, err)
	}
	if want > 0 && len(v) != want {
		return nil, true, fmt.Errorf("%s.%s has %d numbers, want %d", n.Name, attr, len(v), want)
	}
	return v, true, nil
}

// x3dTransformMatrix is T · C · R · SR · S · SR⁻¹ · C⁻¹, X3D's definition.
func x3dTransformMatrix(n *XMLNode) (*[16]float64, error) {
	var m *[16]float64
	mul := func(t [16]float64) { m = x3dMulMat(m, &t) }
	get := func(attr string, def []float64, want int) ([]float64, error) {
		v, ok, err := x3dFloatsAttr(n, attr, want)
		if err != nil {
			return nil, err
		}
		if !ok {
			return def, nil
		}
		return v, nil
	}
	tr, err := get("translation", []float64{0, 0, 0}, 3)
	if err != nil {
		return nil, err
	}
	c, err := get("center", []float64{0, 0, 0}, 3)
	if err != nil {
		return nil, err
	}
	rot, err := get("rotation", []float64{0, 0, 1, 0}, 4)
	if err != nil {
		return nil, err
	}
	sc, err := get("scale", []float64{1, 1, 1}, 3)
	if err != nil {
		return nil, err
	}
	so, err := get("scaleOrientation", []float64{0, 0, 1, 0}, 4)
	if err != nil {
		return nil, err
	}
	mul(x3dTranslate(tr[0], tr[1], tr[2]))
	mul(x3dTranslate(c[0], c[1], c[2]))
	mul(x3dAxisAngle(rot[0], rot[1], rot[2], rot[3]))
	mul(x3dAxisAngle(so[0], so[1], so[2], so[3]))
	mul([16]float64{sc[0], 0, 0, 0, 0, sc[1], 0, 0, 0, 0, sc[2], 0, 0, 0, 0, 1})
	mul(x3dAxisAngle(so[0], so[1], so[2], -so[3]))
	mul(x3dTranslate(-c[0], -c[1], -c[2]))
	return m, nil
}

func x3dTranslate(x, y, z float64) [16]float64 {
	return [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, x, y, z, 1}
}

// x3dAxisAngle is the rotation about (x,y,z) by a radians (X3D's unit).
func x3dAxisAngle(x, y, z, a float64) [16]float64 {
	l := math.Sqrt(x*x + y*y + z*z)
	if l == 0 || a == 0 {
		return [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	}
	x, y, z = x/l, y/l, z/l
	c, s := math.Cos(a), math.Sin(a)
	t := 1 - c
	return [16]float64{
		t*x*x + c, t*x*y + s*z, t*x*z - s*y, 0,
		t*x*y - s*z, t*y*y + c, t*y*z + s*x, 0,
		t*x*z + s*y, t*y*z - s*x, t*z*z + c, 0,
		0, 0, 0, 1,
	}
}

func x3dMulMat(a, b *[16]float64) *[16]float64 {
	if a == nil {
		return b
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

func (r *x3dReader) shape(n *XMLNode) (*FlatPrim, error) {
	var geo *XMLNode
	var color *[4]float64
	material := ""
	for _, k := range n.Kids {
		k, err := r.resolve(k)
		if err != nil {
			return nil, err
		}
		switch k.Name {
		case "Appearance":
			if m := k.Child("Material"); m != nil {
				m, err := r.resolve(m)
				if err != nil {
					return nil, err
				}
				c := [4]float64{0.8, 0.8, 0.8, 1}
				if v, ok, err := x3dFloatsAttr(m, "diffuseColor", 3); err != nil {
					return nil, err
				} else if ok {
					c[0], c[1], c[2] = v[0], v[1], v[2]
				}
				if v, ok, err := x3dFloatsAttr(m, "transparency", 1); err != nil {
					return nil, err
				} else if ok {
					c[3] = 1 - v[0]
				}
				color = &c
				material = m.Attr["DEF"]
			}
		case "IndexedFaceSet", "IndexedTriangleSet", "TriangleSet", "IndexedLineSet", "PointSet", "Box":
			geo = k
		case "Sphere", "Cone", "Cylinder", "Extrusion", "ElevationGrid", "IndexedTriangleStripSet", "IndexedTriangleFanSet", "TriangleStripSet", "TriangleFanSet", "QuadSet", "IndexedQuadSet", "NurbsPatchSurface", "Text":
			return nil, fmt.Errorf("geometry <%s> is not supported", k.Name)
		}
	}
	if geo == nil {
		return nil, nil
	}
	p, err := r.geometry(geo)
	if err != nil || p == nil {
		return nil, err
	}
	p.BaseColor = color
	p.Material = material
	return p, nil
}

// attr resolves a geometry's child (Coordinate, Normal, …), following USE.
func (r *x3dReader) child(geo *XMLNode, name string) (*XMLNode, error) {
	c := geo.Child(name)
	if c == nil {
		return nil, nil
	}
	return r.resolve(c)
}

func (r *x3dReader) vec(geo *XMLNode, node, attr string, dim int) ([][]float64, error) {
	c, err := r.child(geo, node)
	if err != nil || c == nil {
		return nil, err
	}
	v, _, err := x3dFloatsAttr(c, attr, 0)
	if err != nil {
		return nil, err
	}
	if len(v)%dim != 0 {
		return nil, fmt.Errorf("%s.%s: %d numbers is not a whole number of %d-vectors", node, attr, len(v), dim)
	}
	out := make([][]float64, len(v)/dim)
	for i := range out {
		out[i] = v[i*dim : i*dim+dim]
	}
	return out, nil
}

func (r *x3dReader) geometry(geo *XMLNode) (*FlatPrim, error) {
	if geo.Name == "Box" {
		s := []float64{2, 2, 2}
		if v, ok, err := x3dFloatsAttr(geo, "size", 3); err != nil {
			return nil, err
		} else if ok {
			s = v
		}
		return x3dBox(s[0]/2, s[1]/2, s[2]/2), nil
	}
	pts, err := r.vec(geo, "Coordinate", "point", 3)
	if err != nil {
		return nil, err
	}
	if pts == nil {
		return nil, nil
	}
	fp := &FlatPrim{Kind: KindTriangles, MaterialIndex: -1}
	nrm, err := r.vec(geo, "Normal", "vector", 3)
	if err != nil {
		return nil, err
	}
	uvs, err := r.vec(geo, "TextureCoordinate", "point", 2)
	if err != nil {
		return nil, err
	}
	cols, err := r.vec(geo, "Color", "color", 3)
	if err != nil {
		return nil, err
	}
	if cols == nil {
		if cols, err = r.vec(geo, "ColorRGBA", "color", 4); err != nil {
			return nil, err
		}
	}

	switch geo.Name {
	case "PointSet":
		fp.Kind = KindPoints
		for i := range pts {
			fp.Indices = append(fp.Indices, uint32(i))
		}
	case "TriangleSet":
		if len(pts)%3 != 0 {
			return nil, fmt.Errorf("TriangleSet has %d points, not a whole number of triangles", len(pts))
		}
		for i := range pts {
			fp.Indices = append(fp.Indices, uint32(i))
		}
	}

	// The vertex stream: positions always; the optional attributes ride along
	// only when they are per-vertex and line up with the points.
	perVertex := func(a [][]float64) bool { return a != nil && len(a) == len(pts) }
	useN, useUV, useC := perVertex(nrm), perVertex(uvs), perVertex(cols)
	if geo.Name == "IndexedFaceSet" {
		// Separate index lists are honoured below; per-face attributes are not carried.
		useN = nrm != nil && geo.Attr["normalPerVertex"] != "false"
		useC = cols != nil && geo.Attr["colorPerVertex"] != "false"
		useUV = uvs != nil
	}
	if geo.Name == "IndexedFaceSet" || geo.Name == "IndexedTriangleSet" || geo.Name == "IndexedLineSet" {
		isLine := geo.Name == "IndexedLineSet"
		idxAttr := "coordIndex"
		if geo.Name == "IndexedTriangleSet" {
			idxAttr = "index"
		}
		coord, err := x3dIndexList(geo, idxAttr)
		if err != nil {
			return nil, err
		}
		nIdx, _ := x3dIndexListOpt(geo, "normalIndex")
		tIdx, _ := x3dIndexListOpt(geo, "texCoordIndex")
		cIdx, _ := x3dIndexListOpt(geo, "colorIndex")
		// An attribute with no index list of its own is indexed by coordIndex,
		// so it needs one entry per point; one that has fewer (per-face
		// normals and colours, which this handler does not carry) is dropped
		// rather than misread.
		useN = useN && (nIdx != nil || len(nrm) == len(pts))
		useUV = useUV && (tIdx != nil || len(uvs) == len(pts))
		useC = useC && (cIdx != nil || len(cols) == len(pts))
		type key [4]int
		seen := map[key]uint32{}
		vertexOf := func(pos int) (uint32, error) {
			ci := coord[pos]
			if ci < 0 || ci >= len(pts) {
				return 0, fmt.Errorf("coordinate index %d is out of range (%d points)", ci, len(pts))
			}
			k := key{ci, -1, -1, -1}
			pick := func(list []int, attr [][]float64, ok bool, slot int) error {
				if !ok {
					return nil
				}
				i := ci
				if list != nil {
					if pos >= len(list) {
						return fmt.Errorf("an index list is shorter than coordIndex")
					}
					i = list[pos]
				}
				if i < 0 || i >= len(attr) {
					return fmt.Errorf("attribute index %d is out of range (%d entries)", i, len(attr))
				}
				k[slot] = i
				return nil
			}
			if err := pick(nIdx, nrm, useN, 1); err != nil {
				return 0, err
			}
			if err := pick(tIdx, uvs, useUV, 2); err != nil {
				return 0, err
			}
			if err := pick(cIdx, cols, useC, 3); err != nil {
				return 0, err
			}
			if v, ok := seen[k]; ok {
				return v, nil
			}
			fp.Positions = append(fp.Positions, [3]float64{pts[ci][0], pts[ci][1], pts[ci][2]})
			if k[1] >= 0 {
				fp.Normals = append(fp.Normals, [3]float64{nrm[k[1]][0], nrm[k[1]][1], nrm[k[1]][2]})
			}
			if k[2] >= 0 {
				fp.UVs = append(fp.UVs, [2]float64{uvs[k[2]][0], 1 - uvs[k[2]][1]}) // X3D v is up
			}
			if k[3] >= 0 {
				c := cols[k[3]]
				a := 1.0
				if len(c) == 4 {
					a = c[3]
				}
				fp.Colors = append(fp.Colors, [4]float64{c[0], c[1], c[2], a})
			}
			id := uint32(len(fp.Positions) - 1)
			seen[k] = id
			return id, nil
		}
		ccw := geo.Attr["ccw"] != "false"
		var poly []uint32
		flush := func() {
			if isLine {
				for i := 0; i+1 < len(poly); i++ {
					fp.Indices = append(fp.Indices, poly[i], poly[i+1])
				}
			} else {
				if !ccw {
					for i, j := 0, len(poly)-1; i < j; i, j = i+1, j-1 {
						poly[i], poly[j] = poly[j], poly[i]
					}
				}
				for k := 1; k+1 < len(poly); k++ {
					fp.Indices = append(fp.Indices, poly[0], poly[k], poly[k+1])
				}
			}
			poly = poly[:0]
		}
		if isLine {
			fp.Kind = KindLines
		}
		for pos, ci := range coord {
			if ci == -1 {
				flush()
				continue
			}
			v, err := vertexOf(pos)
			if err != nil {
				return nil, err
			}
			poly = append(poly, v)
			if geo.Name == "IndexedTriangleSet" && len(poly) == 3 {
				flush()
			}
		}
		flush()
		if (fp.Normals != nil && len(fp.Normals) != len(fp.Positions)) || (fp.UVs != nil && len(fp.UVs) != len(fp.Positions)) || (fp.Colors != nil && len(fp.Colors) != len(fp.Positions)) {
			// Some corners had the attribute and some did not: drop it rather than misalign.
			fp.Normals, fp.UVs, fp.Colors = nil, nil, nil
		}
	} else { // TriangleSet, PointSet: positions are the stream
		for _, p := range pts {
			fp.Positions = append(fp.Positions, [3]float64{p[0], p[1], p[2]})
		}
		if useN {
			for _, v := range nrm {
				fp.Normals = append(fp.Normals, [3]float64{v[0], v[1], v[2]})
			}
		}
		if useUV {
			for _, v := range uvs {
				fp.UVs = append(fp.UVs, [2]float64{v[0], 1 - v[1]})
			}
		}
		if useC {
			for _, c := range cols {
				a := 1.0
				if len(c) == 4 {
					a = c[3]
				}
				fp.Colors = append(fp.Colors, [4]float64{c[0], c[1], c[2], a})
			}
		}
	}
	if len(fp.Indices) == 0 {
		return nil, nil
	}
	return fp, nil
}

func x3dIndexList(geo *XMLNode, attr string) ([]int, error) {
	s, ok := geo.Attr[attr]
	if !ok {
		return nil, nil
	}
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\t' || r == '\r' })
	if len(f) > x3dMaxNumbers {
		return nil, fmt.Errorf("%s has too many entries", attr)
	}
	out := make([]int, len(f))
	for i, t := range f {
		neg := strings.HasPrefix(t, "-")
		t = strings.TrimPrefix(t, "-")
		v := 0
		if t == "" || len(t) > 10 {
			return nil, fmt.Errorf("bad index in %s", attr)
		}
		for _, c := range t {
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("bad index in %s", attr)
			}
			v = v*10 + int(c-'0')
		}
		if neg {
			if v != 1 {
				return nil, fmt.Errorf("bad index in %s", attr)
			}
			v = -1
		}
		out[i] = v
	}
	return out, nil
}

// x3dIndexListOpt is an optional index list; a malformed one is treated as absent
// (the attribute is then indexed by coordIndex, as the spec says).
func x3dIndexListOpt(geo *XMLNode, attr string) ([]int, bool) {
	v, err := x3dIndexList(geo, attr)
	return v, err == nil && v != nil
}

// x3dBox is X3D's Box: an axis-aligned cuboid centred on the origin, with
// outward faces, per-face normals and the standard 0..1 texture mapping.
func x3dBox(hx, hy, hz float64) *FlatPrim {
	fp := &FlatPrim{Kind: KindTriangles, MaterialIndex: -1}
	faces := []struct {
		n    [3]float64
		a, b [3]float64 // the face's two edge directions, counter-clockwise seen from outside
	}{
		{[3]float64{0, 0, 1}, [3]float64{1, 0, 0}, [3]float64{0, 1, 0}},
		{[3]float64{0, 0, -1}, [3]float64{-1, 0, 0}, [3]float64{0, 1, 0}},
		{[3]float64{1, 0, 0}, [3]float64{0, 0, -1}, [3]float64{0, 1, 0}},
		{[3]float64{-1, 0, 0}, [3]float64{0, 0, 1}, [3]float64{0, 1, 0}},
		{[3]float64{0, 1, 0}, [3]float64{1, 0, 0}, [3]float64{0, 0, -1}},
		{[3]float64{0, -1, 0}, [3]float64{1, 0, 0}, [3]float64{0, 0, 1}},
	}
	h := [3]float64{hx, hy, hz}
	for _, f := range faces {
		base := uint32(len(fp.Positions))
		for _, c := range [][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
			var p [3]float64
			for i := 0; i < 3; i++ {
				p[i] = (f.n[i] + c[0]*f.a[i] + c[1]*f.b[i]) * h[i]
			}
			fp.Positions = append(fp.Positions, p)
			fp.Normals = append(fp.Normals, f.n)
			fp.UVs = append(fp.UVs, [2]float64{(c[0] + 1) / 2, 1 - (c[1]+1)/2})
		}
		fp.Indices = append(fp.Indices, base, base+1, base+2, base, base+2, base+3)
	}
	return fp
}
