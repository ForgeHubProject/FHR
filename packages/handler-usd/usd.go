// Package main is the USD handler: Pixar's Universal Scene Description, as
// text (.usda), binary crate (.usdc) and zipped package (.usdz); a .usd file
// is whichever of the first two its bytes say.
//
// It reads one layer: the Xform/Scope hierarchy with xformOps (translate,
// scale, rotateX/Y/Z and the six rotateXYZ orders, orient, transform, and
// !invert!), Mesh prims with points, face counts/indices, normals, texture
// coordinates and display colours in any interpolation, GeomSubsets, hole
// indices and orientation, the Cube gprim, Points, and UsdPreviewSurface
// materials' diffuse colour and opacity bound through material:binding
// (inherited down the tree). metersPerUnit and upAxis are applied, so a file's
// numbers become glTF's Y-up metres.
//
// Composition is not performed: references, payloads, inherits, variant sets
// and sublayers are refused by name, because evaluating a scene that points
// into other files cannot be done from one blob. The other implicit gprims
// (Sphere, Cone, Cylinder, Capsule, Plane), curves and point instancers are
// refused too, rather than left out of the diff without a word.
//
// It writes text USD (.usd/.usda) or a .usdz of one; writing the binary crate
// is not supported.
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the USD format.
var Codec = &scene.Codec{
	ID:      "usd",
	Formats: []string{".usd", ".usda", ".usdc", ".usdz"},
	Decode:  decode,
	Encode:  encode,
}

const maxPackage = 256 << 20

func decode(blob []byte) (*scene.Model, error) {
	switch {
	case bytes.HasPrefix(blob, []byte("PK")):
		inner, err := firstLayer(blob)
		if err != nil {
			return nil, err
		}
		return decode(inner)
	case bytes.HasPrefix(blob, []byte("PXR-USDC")):
		l, err := parseCrate(blob)
		if err != nil {
			return nil, err
		}
		return interpret(l)
	case bytes.HasPrefix(blob, []byte("#usda")):
		l, err := parseUSDA(string(blob))
		if err != nil {
			return nil, err
		}
		return interpret(l)
	}
	return nil, fmt.Errorf("not a USD file: expected #usda text, a PXR-USDC crate or a .usdz package")
}

// firstLayer is a usdz package's default layer: its first file.
func firstLayer(blob []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, fmt.Errorf("not a usdz package: %w", err)
	}
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if !(strings.HasSuffix(name, ".usd") || strings.HasSuffix(name, ".usda") || strings.HasSuffix(name, ".usdc")) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxPackage+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxPackage {
			return nil, fmt.Errorf("%s is larger than %d MiB once unpacked", f.Name, maxPackage>>20)
		}
		return data, nil
	}
	return nil, fmt.Errorf("the usdz package holds no USD layer")
}

// ── interpretation ────────────────────────────────────────────────────────────

type interp struct {
	materials map[string]*material // by absolute prim path
}

type material struct {
	name  string
	color [4]float64
	has   bool
}

var refused = map[string]bool{
	"Sphere": true, "Capsule": true, "Cone": true, "Cylinder": true, "Plane": true,
	"BasisCurves": true, "NurbsCurves": true, "NurbsPatch": true, "PointInstancer": true, "TetMesh": true,
}

func interpret(l *layer) (*scene.Model, error) {
	for _, k := range []string{"subLayers"} {
		if _, ok := l.meta[k]; ok {
			return nil, fmt.Errorf("sublayers are not supported: a layer that composes other files cannot be read from one blob")
		}
	}
	in := &interp{materials: map[string]*material{}}
	for _, p := range l.prims {
		in.collectMaterials(p, "/"+p.name)
	}
	m := &scene.Model{}
	for _, p := range l.prims {
		o, err := in.object(p, "/"+p.name, "", 0)
		if err != nil {
			return nil, err
		}
		if o != nil {
			m.Roots = append(m.Roots, o)
		}
	}

	if s, ok := metaNumber(l.meta, "metersPerUnit"); ok && s > 0 && s != 1 {
		sm := [16]float64{s, 0, 0, 0, 0, s, 0, 0, 0, 0, s, 0, 0, 0, 0, 1}
		for _, r := range m.Roots {
			r.Matrix = mulMat(&sm, r.Matrix)
		}
	} else if !ok { // the fallback when a stage says nothing: centimetres
		sm := [16]float64{0.01, 0, 0, 0, 0, 0.01, 0, 0, 0, 0, 0.01, 0, 0, 0, 0, 1}
		for _, r := range m.Roots {
			r.Matrix = mulMat(&sm, r.Matrix)
		}
	}
	up := "Y"
	if v, ok := l.meta["upAxis"]; ok {
		up = strings.ToUpper(textOf(v))
	}
	switch up {
	case "Y":
	case "Z": // (x, y, z) → (x, z, -y)
		rot := [16]float64{1, 0, 0, 0, 0, 0, -1, 0, 0, 1, 0, 0, 0, 0, 0, 1}
		for _, r := range m.Roots {
			r.Matrix = mulMat(&rot, r.Matrix)
		}
	default:
		return nil, fmt.Errorf("upAxis %q is not supported (want Y or Z)", up)
	}
	return m, nil
}

func textOf(v any) string {
	switch x := v.(type) {
	case str:
		return string(x)
	case token:
		return string(x)
	case asset:
		return string(x)
	case path:
		return string(x)
	}
	return ""
}

func metaNumber(m map[string]any, k string) (float64, bool) {
	f, ok := m[k].(float64)
	return f, ok
}

// value is a property's value: its authored one, else its first time sample.
func (p *property) val() any {
	if p.value != nil {
		return p.value
	}
	if len(p.timeSamples) > 0 {
		return p.timeSamples[0].v
	}
	return nil
}

func (pr *prim) prop(name string) any {
	if p, ok := pr.props[name]; ok {
		return p.val()
	}
	return nil
}

func isActive(pr *prim) bool {
	if a, ok := pr.meta["active"]; ok {
		if t, ok := a.(token); ok && (t == "false" || t == "0") {
			return false
		}
		if f, ok := a.(float64); ok && f == 0 {
			return false
		}
	}
	return true
}

func (in *interp) collectMaterials(pr *prim, p string) {
	if pr.typ == "Material" {
		mat := &material{name: pr.name}
		var find func(k *prim)
		find = func(k *prim) {
			if k.typ == "Shader" && !mat.has {
				if c, ok := k.prop("inputs:diffuseColor").(*numArray); ok && c.dim == 3 && len(c.data) == 3 {
					mat.color = [4]float64{c.data[0], c.data[1], c.data[2], 1}
					mat.has = true
					if o, ok := k.prop("inputs:opacity").(float64); ok {
						mat.color[3] = o
					}
				}
			}
			for _, c := range k.kids {
				find(c)
			}
		}
		find(pr)
		in.materials[p] = mat
	}
	for _, k := range pr.kids {
		in.collectMaterials(k, p+"/"+k.name)
	}
}

// object converts a prim and its descendants. binding is the nearest
// ancestor's material binding.
func (in *interp) object(pr *prim, p, binding string, depth int) (*scene.Object, error) {
	if depth > maxDepthU {
		return nil, fmt.Errorf("prims nest deeper than %d", maxDepthU)
	}
	if pr.spec != "def" || !isActive(pr) || pr.typ == "Material" || pr.typ == "Shader" || pr.typ == "GeomSubset" {
		return nil, nil
	}
	for _, k := range []string{"references", "payload", "inherits", "specializes"} {
		if v, ok := pr.meta[k]; ok && v != nil {
			return nil, fmt.Errorf("prim %s uses %s: composition arcs are not supported", p, k)
		}
	}
	if _, ok := pr.meta["variantSets"]; ok {
		return nil, fmt.Errorf("prim %s has variant sets, which are not supported", p)
	}
	for _, name := range pr.order {
		if u, ok := pr.props[name].val().(unsupportedValue); ok {
			return nil, fmt.Errorf("prim %s: %s on %q is animated (%s), which is not supported", p, pr.typ, name, u.what)
		}
	}
	if refused[pr.typ] {
		return nil, fmt.Errorf("prim %s is a %s, a geometry type this handler does not read", p, pr.typ)
	}

	o := &scene.Object{Name: pr.name}
	mat, err := xformMatrix(pr)
	if err != nil {
		return nil, fmt.Errorf("prim %s: %w", p, err)
	}
	o.Matrix = mat
	if b := bindingOf(pr); b != "" {
		binding = b
	}
	switch pr.typ {
	case "Mesh":
		prims, err := in.mesh(pr, p, binding)
		if err != nil {
			return nil, fmt.Errorf("mesh %s: %w", p, err)
		}
		o.Prims = prims
	case "Cube":
		size := 2.0
		if s, ok := pr.prop("size").(float64); ok {
			size = s
		}
		fp := cube(size / 2)
		in.applyMaterial(fp, binding, nil)
		o.Prims = []*scene.FlatPrim{fp}
	case "Points":
		pts, ok := pr.prop("points").(*numArray)
		if ok && pts.dim == 3 && pts.len() > 0 {
			fp := &scene.FlatPrim{Kind: scene.KindPoints, MaterialIndex: -1}
			for i := 0; i < pts.len(); i++ {
				fp.Positions = append(fp.Positions, [3]float64{pts.data[i*3], pts.data[i*3+1], pts.data[i*3+2]})
				fp.Indices = append(fp.Indices, uint32(i))
			}
			o.Prims = []*scene.FlatPrim{fp}
		}
	}
	for _, k := range pr.kids {
		c, err := in.object(k, p+"/"+k.name, binding, depth+1)
		if err != nil {
			return nil, err
		}
		if c != nil {
			o.Children = append(o.Children, c)
		}
	}
	// Prims that only held materials, lights, cameras… leave no trace.
	if len(o.Prims) == 0 && len(o.Children) == 0 && pr.typ != "Xform" {
		return nil, nil
	}
	return o, nil
}

func bindingOf(pr *prim) string {
	v := pr.prop("material:binding")
	switch x := v.(type) {
	case path:
		return string(x)
	case []any:
		if len(x) > 0 {
			if p, ok := x[0].(path); ok {
				return string(p)
			}
		}
	}
	return ""
}

func (in *interp) applyMaterial(fp *scene.FlatPrim, binding string, display *[4]float64) {
	if m, ok := in.materials[binding]; ok {
		fp.Material = m.name
		if m.has {
			c := m.color
			fp.BaseColor = &c
		}
		return
	}
	if display != nil {
		fp.BaseColor = display
	}
}

func cube(h float64) *scene.FlatPrim {
	fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
	faces := []struct{ n, a, b [3]float64 }{
		{[3]float64{0, 0, 1}, [3]float64{1, 0, 0}, [3]float64{0, 1, 0}},
		{[3]float64{0, 0, -1}, [3]float64{-1, 0, 0}, [3]float64{0, 1, 0}},
		{[3]float64{1, 0, 0}, [3]float64{0, 0, -1}, [3]float64{0, 1, 0}},
		{[3]float64{-1, 0, 0}, [3]float64{0, 0, 1}, [3]float64{0, 1, 0}},
		{[3]float64{0, 1, 0}, [3]float64{1, 0, 0}, [3]float64{0, 0, -1}},
		{[3]float64{0, -1, 0}, [3]float64{1, 0, 0}, [3]float64{0, 0, 1}},
	}
	for _, f := range faces {
		base := uint32(len(fp.Positions))
		for _, c := range [][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
			var p [3]float64
			for i := 0; i < 3; i++ {
				p[i] = (f.n[i] + c[0]*f.a[i] + c[1]*f.b[i]) * h
			}
			fp.Positions = append(fp.Positions, p)
			fp.Normals = append(fp.Normals, f.n)
		}
		fp.Indices = append(fp.Indices, base, base+1, base+2, base, base+2, base+3)
	}
	return fp
}

// ── meshes ────────────────────────────────────────────────────────────────────

func intsOf(v any) ([]int, bool) {
	a, ok := v.(*numArray)
	if !ok || a.dim != 1 {
		return nil, false
	}
	out := make([]int, len(a.data))
	for i, f := range a.data {
		if f != math.Trunc(f) || f < math.MinInt32 || f > math.MaxInt32 {
			return nil, false
		}
		out[i] = int(f)
	}
	return out, true
}

type primvar struct {
	data    *numArray
	indices []int
	interp  string // constant, uniform, varying, vertex, faceVarying
}

func readPrimvar(pr *prim, name string, dim int) (*primvar, error) {
	p, ok := pr.props[name]
	if !ok {
		return nil, nil
	}
	a, ok := p.val().(*numArray)
	if !ok || a.dim != dim {
		return nil, nil
	}
	pv := &primvar{data: a, interp: "constant"}
	if s, ok := p.meta["interpolation"]; ok {
		pv.interp = textOf(s)
	}
	if ip, ok := pr.props[name+":indices"]; ok {
		idx, ok := intsOf(ip.val())
		if !ok {
			return nil, fmt.Errorf("%s:indices is not an integer array", name)
		}
		pv.indices = idx
	}
	return pv, nil
}

// at is the data index a primvar gives a corner / vertex / face.
func (pv *primvar) at(corner, vert, face int) (int, error) {
	var i int
	switch pv.interp {
	case "faceVarying":
		i = corner
	case "vertex", "varying":
		i = vert
	case "uniform":
		i = face
	default:
		i = 0
	}
	if pv.indices != nil {
		if i < 0 || i >= len(pv.indices) {
			return 0, fmt.Errorf("primvar index %d is out of range (%d)", i, len(pv.indices))
		}
		i = pv.indices[i]
	}
	if i < 0 || i >= pv.data.len() {
		return 0, fmt.Errorf("primvar entry %d is out of range (%d)", i, pv.data.len())
	}
	return i, nil
}

func (in *interp) mesh(pr *prim, p, binding string) ([]*scene.FlatPrim, error) {
	pts, ok := pr.prop("points").(*numArray)
	if !ok || pts.dim != 3 {
		return nil, nil // a mesh with no points draws nothing
	}
	counts, ok1 := intsOf(pr.prop("faceVertexCounts"))
	idx, ok2 := intsOf(pr.prop("faceVertexIndices"))
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("faceVertexCounts / faceVertexIndices are missing or not integer arrays")
	}
	nv := pts.len()
	total := 0
	for _, c := range counts {
		if c < 3 {
			return nil, fmt.Errorf("a face has %d vertices", c)
		}
		total += c
	}
	if total != len(idx) {
		return nil, fmt.Errorf("faceVertexCounts sum to %d but there are %d indices", total, len(idx))
	}
	for _, v := range idx {
		if v < 0 || v >= nv {
			return nil, fmt.Errorf("vertex index %d is out of range (%d points)", v, nv)
		}
	}
	nrm, err := readPrimvar(pr, "normals", 3)
	if err != nil {
		return nil, err
	}
	if nrm == nil {
		if nrm, err = readPrimvar(pr, "primvars:normals", 3); err != nil {
			return nil, err
		}
	}
	var uv *primvar
	for _, n := range []string{"primvars:st", "primvars:st0", "primvars:UVMap", "primvars:uv"} {
		if uv, err = readPrimvar(pr, n, 2); err != nil || uv != nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	col, err := readPrimvar(pr, "primvars:displayColor", 3)
	if err != nil {
		return nil, err
	}
	var display *[4]float64
	if col != nil && col.interp == "constant" && col.data.len() >= 1 {
		d := [4]float64{col.data.data[0], col.data.data[1], col.data.data[2], 1}
		display = &d
		col = nil
	}
	if col != nil && col.interp != "vertex" && col.interp != "faceVarying" && col.interp != "varying" {
		col = nil // per-face colours are not carried
	}
	leftHanded := textOf(pr.prop("orientation")) == "leftHanded"
	holes := map[int]bool{}
	if h, ok := intsOf(pr.prop("holeIndices")); ok {
		for _, f := range h {
			holes[f] = true
		}
	}

	// Which material each face takes: the mesh's binding, overridden per
	// GeomSubset (face-element subsets only).
	faceBinding := make([]string, len(counts))
	for i := range faceBinding {
		faceBinding[i] = binding
	}
	for _, k := range pr.kids {
		if k.typ != "GeomSubset" || !isActive(k) {
			continue
		}
		b := bindingOf(k)
		if b == "" {
			continue
		}
		fs, ok := intsOf(k.prop("indices"))
		if !ok {
			return nil, fmt.Errorf("GeomSubset %q has no integer indices", k.name)
		}
		for _, f := range fs {
			if f < 0 || f >= len(faceBinding) {
				return nil, fmt.Errorf("GeomSubset %q refers to face %d of %d", k.name, f, len(faceBinding))
			}
			faceBinding[f] = b
		}
	}

	type key [4]int32
	type group struct {
		fp   *scene.FlatPrim
		seen map[key]uint32
	}
	groups := map[string]*group{}
	var order []string
	corner := 0
	for f, c := range counts {
		start := corner
		corner += c
		if holes[f] {
			continue
		}
		bnd := faceBinding[f]
		g := groups[bnd]
		if g == nil {
			fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
			in.applyMaterial(fp, bnd, display)
			g = &group{fp, map[key]uint32{}}
			groups[bnd] = g
			order = append(order, bnd)
		}
		var poly []uint32
		for k := 0; k < c; k++ {
			ci := start + k
			if leftHanded { // reverse the winding
				ci = start + c - 1 - k
			}
			v := idx[ci]
			kk := key{int32(v), -1, -1, -1}
			var ni, ui, cj int
			if nrm != nil {
				if ni, err = nrm.at(ci, v, f); err != nil {
					return nil, err
				}
				kk[1] = int32(ni)
			}
			if uv != nil {
				if ui, err = uv.at(ci, v, f); err != nil {
					return nil, err
				}
				kk[2] = int32(ui)
			}
			if col != nil {
				if cj, err = col.at(ci, v, f); err != nil {
					return nil, err
				}
				kk[3] = int32(cj)
			}
			id, ok := g.seen[kk]
			if !ok {
				fp := g.fp
				fp.Positions = append(fp.Positions, [3]float64{pts.data[v*3], pts.data[v*3+1], pts.data[v*3+2]})
				if kk[1] >= 0 {
					d := nrm.data.data[int(kk[1])*3:]
					fp.Normals = append(fp.Normals, [3]float64{d[0], d[1], d[2]})
				}
				if kk[2] >= 0 {
					d := uv.data.data[int(kk[2])*2:]
					fp.UVs = append(fp.UVs, [2]float64{d[0], 1 - d[1]}) // USD v is up, glTF's down
				}
				if kk[3] >= 0 {
					d := col.data.data[int(kk[3])*3:]
					fp.Colors = append(fp.Colors, [4]float64{d[0], d[1], d[2], 1})
				}
				id = uint32(len(fp.Positions) - 1)
				g.seen[kk] = id
			}
			poly = append(poly, id)
		}
		for k := 1; k+1 < len(poly); k++ { // fan-triangulate
			g.fp.Indices = append(g.fp.Indices, poly[0], poly[k], poly[k+1])
		}
	}
	var out []*scene.FlatPrim
	for _, b := range order {
		fp := groups[b].fp
		if len(fp.Indices) == 0 {
			continue
		}
		out = append(out, fp)
	}
	return out, nil
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

func rotAxis(axis byte, deg float64) [16]float64 {
	a := deg * math.Pi / 180
	c, s := math.Cos(a), math.Sin(a)
	switch axis {
	case 'X':
		return [16]float64{1, 0, 0, 0, 0, c, s, 0, 0, -s, c, 0, 0, 0, 0, 1}
	case 'Y':
		return [16]float64{c, 0, -s, 0, 0, 1, 0, 0, s, 0, c, 0, 0, 0, 0, 1}
	}
	return [16]float64{c, s, 0, 0, -s, c, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

// inverse4 inverts a general 4×4 (Gauss-Jordan); ok is false when singular.
func inverse4(m [16]float64) ([16]float64, bool) {
	var a [4][8]float64
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			a[r][c] = m[c*4+r]
		}
		a[r][4+r] = 1
	}
	for col := 0; col < 4; col++ {
		piv := col
		for r := col + 1; r < 4; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[piv][col]) {
				piv = r
			}
		}
		if math.Abs(a[piv][col]) < 1e-12 {
			return ident, false
		}
		a[col], a[piv] = a[piv], a[col]
		d := a[col][col]
		for c := 0; c < 8; c++ {
			a[col][c] /= d
		}
		for r := 0; r < 4; r++ {
			if r == col {
				continue
			}
			f := a[r][col]
			for c := 0; c < 8; c++ {
				a[r][c] -= f * a[col][c]
			}
		}
	}
	var o [16]float64
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			o[c*4+r] = a[r][4+c]
		}
	}
	return o, true
}

func vec3Of(v any, want int) ([]float64, bool) {
	a, ok := v.(*numArray)
	if ok && len(a.data) == want {
		return a.data, true
	}
	if f, ok := v.(float64); ok && want == 1 {
		return []float64{f}, true
	}
	return nil, false
}

// xformMatrix composes a prim's xformOps: the first one listed is the
// outermost, so M = op1 · op2 · … acting on column vectors.
func xformMatrix(pr *prim) (*[16]float64, error) {
	order, _ := pr.prop("xformOpOrder").([]any)
	var m *[16]float64
	for _, o := range order {
		name := textOf(o)
		inv := false
		if strings.HasPrefix(name, "!invert!") {
			inv, name = true, strings.TrimPrefix(name, "!invert!")
		}
		if name == "!resetXformStack!" {
			return nil, fmt.Errorf("!resetXformStack! is not supported")
		}
		if !strings.HasPrefix(name, "xformOp:") {
			return nil, fmt.Errorf("unknown xformOp %q", name)
		}
		kind := strings.SplitN(strings.TrimPrefix(name, "xformOp:"), ":", 2)[0]
		val := pr.prop(name)
		if val == nil {
			return nil, fmt.Errorf("xformOp %q has no value", name)
		}
		var t [16]float64
		switch {
		case kind == "translate":
			v, ok := vec3Of(val, 3)
			if !ok {
				return nil, fmt.Errorf("%s is not a 3-vector", name)
			}
			t = [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, v[0], v[1], v[2], 1}
		case kind == "scale":
			v, ok := vec3Of(val, 3)
			if !ok {
				return nil, fmt.Errorf("%s is not a 3-vector", name)
			}
			t = [16]float64{v[0], 0, 0, 0, 0, v[1], 0, 0, 0, 0, v[2], 0, 0, 0, 0, 1}
		case kind == "rotateX" || kind == "rotateY" || kind == "rotateZ":
			v, ok := vec3Of(val, 1)
			if !ok {
				return nil, fmt.Errorf("%s is not a number", name)
			}
			t = rotAxis(kind[6], v[0])
		case strings.HasPrefix(kind, "rotate") && len(kind) == 9:
			v, ok := vec3Of(val, 3)
			if !ok {
				return nil, fmt.Errorf("%s is not a 3-vector", name)
			}
			t = ident
			for i := 0; i < 3; i++ { // the first letter is applied first: R = Rc·Rb·Ra
				ax := kind[6+i]
				if ax != 'X' && ax != 'Y' && ax != 'Z' {
					return nil, fmt.Errorf("unknown rotation order in %q", name)
				}
				r := rotAxis(ax, v[map[byte]int{'X': 0, 'Y': 1, 'Z': 2}[ax]])
				t = *mulMat(&r, &t)
			}
		case kind == "orient":
			q, ok := vec3Of(val, 4)
			if !ok {
				return nil, fmt.Errorf("%s is not a quaternion", name)
			}
			w, x, y, z := q[0], q[1], q[2], q[3] // USD stores (real, i, j, k)
			if l := math.Sqrt(w*w + x*x + y*y + z*z); l > 0 {
				w, x, y, z = w/l, x/l, y/l, z/l
			}
			t = [16]float64{
				1 - 2*(y*y+z*z), 2 * (x*y + z*w), 2 * (x*z - y*w), 0,
				2 * (x*y - z*w), 1 - 2*(x*x+z*z), 2 * (y*z + x*w), 0,
				2 * (x*z + y*w), 2 * (y*z - x*w), 1 - 2*(x*x+y*y), 0,
				0, 0, 0, 1,
			}
		case kind == "transform": // 4 rows of 4, row-vector convention = glTF's column order
			rows, ok := val.([]any)
			if !ok || len(rows) != 4 {
				return nil, fmt.Errorf("%s is not a 4×4 matrix", name)
			}
			for r := 0; r < 4; r++ {
				row, ok := rows[r].(*numArray)
				if !ok || len(row.data) != 4 {
					return nil, fmt.Errorf("%s is not a 4×4 matrix", name)
				}
				copy(t[r*4:], row.data)
			}
		default:
			return nil, fmt.Errorf("unknown xformOp kind %q", kind)
		}
		if inv {
			i, ok := inverse4(t)
			if !ok {
				return nil, fmt.Errorf("%s cannot be inverted", name)
			}
			t = i
		}
		m = mulMat(m, &t)
	}
	if m == nil || *m == ident {
		return nil, nil
	}
	return m, nil
}
