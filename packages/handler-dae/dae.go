// Package main is the Collada (.dae) handler: the XML interchange format of
// DCC tools. It reads geometry (triangles, polylist, polygons, strips, fans,
// lines) with positions, normals, texture coordinates and vertex colours, the
// visual-scene node hierarchy with its transforms, instance_node reuse, and
// materials' diffuse colours. Animation, skinning, cameras, lights and physics
// are not read. It writes a Y-up, metre document with one geometry per drawn
// node, in world space.
//
// Coordinates are not rescaled: a document's <unit> is not applied, as with
// STL and 3MF. A Z_UP document is rotated to glTF's Y-up at its roots.
package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the Collada format.
var Codec = &scene.Codec{
	ID:      "dae",
	Formats: []string{".dae"},
	Decode:  decode,
	Encode:  encode,
}

const (
	maxDepth   = 64
	maxNodes   = 2_000_000
	maxNumbers = 64 << 20
)

type source struct {
	data   []float64
	stride int
}

type input struct {
	semantic string
	source   string // id, without '#'
	offset   int
	set      int
}

type geometry struct {
	name    string
	sources map[string]*source
	// vertices id → the POSITION source it names.
	vertices map[string]string
	prims    []*primGroup
}

type primGroup struct {
	kind     string // triangles, polylist, polygons, tristrips, trifans, lines, linestrips
	material string // the symbol a bind_material maps
	inputs   []input
	vcount   []int
	p        [][]int // one index list per <p>
}

type material struct {
	name  string
	color *[4]float64
}

type doc struct {
	geometries map[string]*geometry
	materials  map[string]*material // by id
	effects    map[string]*[4]float64
	nodes      map[string]*scene.XMLNode
	zUp        bool
}

func decode(blob []byte) (*scene.Model, error) {
	root, err := scene.ParseXML(blob, maxDepth, maxNodes)
	if err != nil {
		return nil, err
	}
	if root.Name != "COLLADA" {
		return nil, fmt.Errorf("not a Collada document: root element is <%s>", root.Name)
	}
	d := &doc{geometries: map[string]*geometry{}, materials: map[string]*material{}, effects: map[string]*[4]float64{}, nodes: map[string]*scene.XMLNode{}}
	if a := root.Child("asset"); a != nil {
		if u := a.Child("up_axis"); u != nil && strings.EqualFold(u.Text, "Z_UP") {
			d.zUp = true
		}
	}
	if lib := root.Child("library_effects"); lib != nil {
		for _, e := range lib.Children("effect") {
			if c := effectColor(e); c != nil {
				d.effects[e.Attr["id"]] = c
			}
		}
	}
	if lib := root.Child("library_materials"); lib != nil {
		for _, m := range lib.Children("material") {
			mat := &material{name: m.Attr["name"]}
			if mat.name == "" {
				mat.name = m.Attr["id"]
			}
			if ie := m.Child("instance_effect"); ie != nil {
				mat.color = d.effects[strings.TrimPrefix(ie.Attr["url"], "#")]
			}
			d.materials[m.Attr["id"]] = mat
		}
	}
	if lib := root.Child("library_geometries"); lib != nil {
		for _, g := range lib.Children("geometry") {
			geo, err := parseGeometry(g)
			if err != nil {
				return nil, fmt.Errorf("geometry %q: %w", g.Attr["id"], err)
			}
			if geo != nil {
				d.geometries[g.Attr["id"]] = geo
			}
		}
	}
	var collect func(n *scene.XMLNode)
	collect = func(n *scene.XMLNode) {
		if id := n.Attr["id"]; id != "" && n.Name == "node" {
			d.nodes[id] = n
		}
		for _, k := range n.Kids {
			collect(k)
		}
	}
	if lib := root.Child("library_nodes"); lib != nil {
		collect(lib)
	}

	var sceneNodes []*scene.XMLNode
	if vs := root.Child("library_visual_scenes"); vs != nil {
		var pick *scene.XMLNode
		if sc := root.Child("scene"); sc != nil {
			if iv := sc.Child("instance_visual_scene"); iv != nil {
				want := strings.TrimPrefix(iv.Attr["url"], "#")
				for _, v := range vs.Children("visual_scene") {
					if v.Attr["id"] == want {
						pick = v
					}
				}
			}
		}
		if pick == nil && len(vs.Children("visual_scene")) > 0 {
			pick = vs.Children("visual_scene")[0]
		}
		if pick != nil {
			collect(pick)
			sceneNodes = pick.Children("node")
		}
	}

	m := &scene.Model{}
	if len(sceneNodes) == 0 { // no scene: every geometry, as is
		for _, id := range sortedKeys(d.geometries) {
			g := d.geometries[id]
			prims, err := d.prims(g, nil)
			if err != nil {
				return nil, err
			}
			if len(prims) > 0 {
				m.Roots = append(m.Roots, &scene.Object{Name: g.name, Prims: prims})
			}
		}
	}
	for _, n := range sceneNodes {
		o, err := d.node(n, 0)
		if err != nil {
			return nil, err
		}
		m.Roots = append(m.Roots, o)
	}
	if d.zUp { // (x, y, z) → (x, z, -y): glTF is Y-up
		rot := [16]float64{1, 0, 0, 0, 0, 0, -1, 0, 0, 1, 0, 0, 0, 0, 0, 1}
		for _, r := range m.Roots {
			r.Matrix = mulMat(&rot, r.Matrix)
		}
	}
	return m, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort: tiny maps, and no import for it
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// effectColor finds an effect's surface colour: the first <color> under a
// diffuse (or, failing that, constant/emission) shading parameter.
func effectColor(e *scene.XMLNode) *[4]float64 {
	var find func(n *scene.XMLNode, want string) *scene.XMLNode
	find = func(n *scene.XMLNode, want string) *scene.XMLNode {
		if n.Name == want {
			return n
		}
		for _, k := range n.Kids {
			if f := find(k, want); f != nil {
				return f
			}
		}
		return nil
	}
	for _, want := range []string{"diffuse", "constant"} {
		if d := find(e, want); d != nil {
			if c := d.Child("color"); c != nil {
				v, err := scene.Floats(c.Text, 8)
				if err == nil && len(v) >= 3 {
					out := [4]float64{v[0], v[1], v[2], 1}
					if len(v) >= 4 {
						out[3] = v[3]
					}
					return &out
				}
			}
		}
	}
	return nil
}

func parseGeometry(g *scene.XMLNode) (*geometry, error) {
	mesh := g.Child("mesh")
	if mesh == nil {
		return nil, nil // splines, convex_mesh, … are not meshes
	}
	geo := &geometry{name: g.Attr["name"], sources: map[string]*source{}, vertices: map[string]string{}}
	if geo.name == "" {
		geo.name = g.Attr["id"]
	}
	for _, s := range mesh.Children("source") {
		fa := s.Child("float_array")
		if fa == nil {
			continue
		}
		data, err := scene.Floats(fa.Text, maxNumbers)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", s.Attr["id"], err)
		}
		stride := 3
		if tc := s.Child("technique_common"); tc != nil {
			if acc := tc.Child("accessor"); acc != nil {
				if v, err := strconv.Atoi(acc.Attr["stride"]); err == nil && v > 0 {
					stride = v
				}
			}
		}
		geo.sources[s.Attr["id"]] = &source{data: data, stride: stride}
	}
	for _, v := range mesh.Children("vertices") {
		for _, in := range v.Children("input") {
			if in.Attr["semantic"] == "POSITION" {
				geo.vertices[v.Attr["id"]] = strings.TrimPrefix(in.Attr["source"], "#")
			}
		}
	}
	for _, k := range mesh.Kids {
		switch k.Name {
		case "triangles", "polylist", "polygons", "tristrips", "trifans", "lines", "linestrips":
		default:
			continue
		}
		pg := &primGroup{kind: k.Name, material: k.Attr["material"]}
		for _, in := range k.Children("input") {
			off, _ := strconv.Atoi(in.Attr["offset"])
			set, _ := strconv.Atoi(in.Attr["set"])
			pg.inputs = append(pg.inputs, input{in.Attr["semantic"], strings.TrimPrefix(in.Attr["source"], "#"), off, set})
		}
		if vc := k.Child("vcount"); vc != nil {
			v, err := scene.Ints(vc.Text, maxNumbers)
			if err != nil {
				return nil, fmt.Errorf("vcount: %w", err)
			}
			pg.vcount = v
		}
		for _, p := range k.Children("p") {
			v, err := scene.Ints(p.Text, maxNumbers)
			if err != nil {
				return nil, fmt.Errorf("<p>: %w", err)
			}
			pg.p = append(pg.p, v)
		}
		geo.prims = append(geo.prims, pg)
	}
	return geo, nil
}

// prims builds a geometry's primitives, naming materials through the
// instance's bind_material map (symbol → material id).
func (d *doc) prims(g *geometry, bind map[string]string) ([]*scene.FlatPrim, error) {
	var out []*scene.FlatPrim
	for _, pg := range g.prims {
		p, err := d.prim(g, pg)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		if pg.material != "" {
			target := bind[pg.material]
			if m, ok := d.materials[target]; ok {
				p.Material = m.name
				p.BaseColor = m.color
			} else {
				p.Material = pg.material
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func (d *doc) prim(g *geometry, pg *primGroup) (*scene.FlatPrim, error) {
	stride := 0
	var vertexIn, normalIn, uvIn, colorIn *input
	for i := range pg.inputs {
		in := &pg.inputs[i]
		if in.offset+1 > stride {
			stride = in.offset + 1
		}
		switch in.semantic {
		case "VERTEX":
			vertexIn = in
		case "NORMAL":
			if normalIn == nil {
				normalIn = in
			}
		case "TEXCOORD":
			if uvIn == nil || in.set < uvIn.set {
				uvIn = in
			}
		case "COLOR":
			if colorIn == nil {
				colorIn = in
			}
		}
	}
	if vertexIn == nil || stride == 0 {
		return nil, nil
	}
	if stride > 8 {
		return nil, fmt.Errorf("a primitive with %d input offsets is not supported", stride)
	}
	posSrc := g.sources[g.vertices[vertexIn.source]]
	if posSrc == nil {
		return nil, fmt.Errorf("VERTEX input %q names no POSITION source", vertexIn.source)
	}
	srcOf := func(in *input) (*source, error) {
		if in == nil {
			return nil, nil
		}
		s := g.sources[in.source]
		if s == nil {
			return nil, fmt.Errorf("input %s names the missing source %q", in.semantic, in.source)
		}
		return s, nil
	}
	nSrc, err := srcOf(normalIn)
	if err != nil {
		return nil, err
	}
	uvSrc, err := srcOf(uvIn)
	if err != nil {
		return nil, err
	}
	cSrc, err := srcOf(colorIn)
	if err != nil {
		return nil, err
	}

	// Each distinct index tuple is one glTF vertex.
	type tuple [8]int32
	seen := map[tuple]uint32{}
	fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
	vertex := func(idx []int, at int) (uint32, error) {
		var key tuple
		for k := 0; k < stride; k++ {
			key[k] = int32(idx[at+k])
		}
		if v, ok := seen[key]; ok {
			return v, nil
		}
		get := func(in *input, s *source, n int) ([]float64, error) {
			i := int(key[in.offset]) * s.stride
			if i < 0 || i+n > len(s.data) {
				return nil, fmt.Errorf("%s index %d is out of range", in.semantic, key[in.offset])
			}
			return s.data[i : i+n], nil
		}
		if posSrc.stride < 3 {
			return 0, fmt.Errorf("POSITION source has stride %d", posSrc.stride)
		}
		pi := int(key[vertexIn.offset]) * posSrc.stride
		if pi < 0 || pi+3 > len(posSrc.data) {
			return 0, fmt.Errorf("vertex index %d is out of range", key[vertexIn.offset])
		}
		fp.Positions = append(fp.Positions, [3]float64{posSrc.data[pi], posSrc.data[pi+1], posSrc.data[pi+2]})
		if nSrc != nil && nSrc.stride >= 3 {
			v, err := get(normalIn, nSrc, 3)
			if err != nil {
				return 0, err
			}
			fp.Normals = append(fp.Normals, [3]float64{v[0], v[1], v[2]})
		}
		if uvSrc != nil && uvSrc.stride >= 2 {
			v, err := get(uvIn, uvSrc, 2)
			if err != nil {
				return 0, err
			}
			fp.UVs = append(fp.UVs, [2]float64{v[0], 1 - v[1]}) // Collada v is up, glTF's is down
		}
		if cSrc != nil && cSrc.stride >= 3 {
			v, err := get(colorIn, cSrc, cSrc.stride)
			if err != nil {
				return 0, err
			}
			c := [4]float64{v[0], v[1], v[2], 1}
			if cSrc.stride >= 4 {
				c[3] = v[3]
			}
			fp.Colors = append(fp.Colors, c)
		}
		id := uint32(len(fp.Positions) - 1)
		seen[key] = id
		return id, nil
	}

	// poly lists one polygon's vertices; emit turns it into elements.
	poly := func(idx []int, n int, at int) ([]uint32, error) {
		if at+n*stride > len(idx) {
			return nil, fmt.Errorf("a %s runs past its index list", pg.kind)
		}
		vs := make([]uint32, n)
		for i := 0; i < n; i++ {
			v, err := vertex(idx, at+i*stride)
			if err != nil {
				return nil, err
			}
			vs[i] = v
		}
		return vs, nil
	}
	fan := func(vs []uint32) {
		for k := 1; k+1 < len(vs); k++ {
			fp.Indices = append(fp.Indices, vs[0], vs[k], vs[k+1])
		}
	}

	switch pg.kind {
	case "triangles":
		for _, idx := range pg.p {
			if len(idx)%(3*stride) != 0 {
				return nil, fmt.Errorf("triangles index list is not a whole number of triangles")
			}
			for at := 0; at < len(idx); at += 3 * stride {
				vs, err := poly(idx, 3, at)
				if err != nil {
					return nil, err
				}
				fp.Indices = append(fp.Indices, vs...)
			}
		}
	case "polylist":
		if len(pg.p) != 1 {
			return nil, fmt.Errorf("polylist needs exactly one <p>")
		}
		at := 0
		for _, n := range pg.vcount {
			vs, err := poly(pg.p[0], n, at)
			if err != nil {
				return nil, err
			}
			fan(vs)
			at += n * stride
		}
	case "polygons":
		for _, idx := range pg.p {
			vs, err := poly(idx, len(idx)/stride, 0)
			if err != nil {
				return nil, err
			}
			fan(vs)
		}
	case "trifans":
		for _, idx := range pg.p {
			vs, err := poly(idx, len(idx)/stride, 0)
			if err != nil {
				return nil, err
			}
			fan(vs)
		}
	case "tristrips":
		for _, idx := range pg.p {
			vs, err := poly(idx, len(idx)/stride, 0)
			if err != nil {
				return nil, err
			}
			for k := 0; k+2 < len(vs); k++ {
				if k%2 == 0 {
					fp.Indices = append(fp.Indices, vs[k], vs[k+1], vs[k+2])
				} else {
					fp.Indices = append(fp.Indices, vs[k+1], vs[k], vs[k+2])
				}
			}
		}
	case "lines", "linestrips":
		fp.Kind = scene.KindLines
		for _, idx := range pg.p {
			vs, err := poly(idx, len(idx)/stride, 0)
			if err != nil {
				return nil, err
			}
			if pg.kind == "lines" {
				for k := 0; k+1 < len(vs); k += 2 {
					fp.Indices = append(fp.Indices, vs[k], vs[k+1])
				}
			} else {
				for k := 0; k+1 < len(vs); k++ {
					fp.Indices = append(fp.Indices, vs[k], vs[k+1])
				}
			}
		}
	}
	if len(fp.Indices) == 0 {
		return nil, nil
	}
	return fp, nil
}

// node converts a visual-scene node (and its instances) to an Object.
func (d *doc) node(n *scene.XMLNode, depth int) (*scene.Object, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nodes nest deeper than %d (a cycle?)", maxDepth)
	}
	name := n.Attr["name"]
	if name == "" {
		name = n.Attr["id"]
	}
	o := &scene.Object{Name: name}
	m, err := nodeMatrix(n)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", name, err)
	}
	o.Matrix = m
	for _, ig := range n.Children("instance_geometry") {
		g := d.geometries[strings.TrimPrefix(ig.Attr["url"], "#")]
		if g == nil {
			continue
		}
		bind := map[string]string{}
		if bm := ig.Child("bind_material"); bm != nil {
			if tc := bm.Child("technique_common"); tc != nil {
				for _, im := range tc.Children("instance_material") {
					bind[im.Attr["symbol"]] = strings.TrimPrefix(im.Attr["target"], "#")
				}
			}
		}
		prims, err := d.prims(g, bind)
		if err != nil {
			return nil, err
		}
		o.Prims = append(o.Prims, prims...)
	}
	for _, k := range n.Kids {
		switch k.Name {
		case "node":
			c, err := d.node(k, depth+1)
			if err != nil {
				return nil, err
			}
			o.Children = append(o.Children, c)
		case "instance_node":
			target := d.nodes[strings.TrimPrefix(k.Attr["url"], "#")]
			if target == nil {
				continue
			}
			c, err := d.node(target, depth+1)
			if err != nil {
				return nil, err
			}
			o.Children = append(o.Children, c)
		}
	}
	return o, nil
}

// nodeMatrix composes a node's transform elements in document order, the way
// Collada defines them: M = T1 · T2 · … applied to column vectors.
func nodeMatrix(n *scene.XMLNode) (*[16]float64, error) {
	var m *[16]float64
	for _, k := range n.Kids {
		var t [16]float64
		switch k.Name {
		case "matrix": // 16 numbers, row-major
			v, err := scene.Floats(k.Text, 16)
			if err != nil || len(v) != 16 {
				return nil, fmt.Errorf("bad <matrix>")
			}
			for r := 0; r < 4; r++ {
				for c := 0; c < 4; c++ {
					t[c*4+r] = v[r*4+c]
				}
			}
		case "translate":
			v, err := scene.Floats(k.Text, 3)
			if err != nil || len(v) != 3 {
				return nil, fmt.Errorf("bad <translate>")
			}
			t = [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, v[0], v[1], v[2], 1}
		case "scale":
			v, err := scene.Floats(k.Text, 3)
			if err != nil || len(v) != 3 {
				return nil, fmt.Errorf("bad <scale>")
			}
			t = [16]float64{v[0], 0, 0, 0, 0, v[1], 0, 0, 0, 0, v[2], 0, 0, 0, 0, 1}
		case "rotate": // axis x y z, angle in degrees
			v, err := scene.Floats(k.Text, 4)
			if err != nil || len(v) != 4 {
				return nil, fmt.Errorf("bad <rotate>")
			}
			t = axisAngle(v[0], v[1], v[2], v[3]*math.Pi/180)
		case "skew", "lookat":
			return nil, fmt.Errorf("<%s> transforms are not supported", k.Name)
		default:
			continue
		}
		m = mulMat(m, &t)
	}
	return m, nil
}

func axisAngle(x, y, z, a float64) [16]float64 {
	l := math.Sqrt(x*x + y*y + z*z)
	if l == 0 {
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

// mulMat is a·b for column-major matrices; nil is the identity.
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
