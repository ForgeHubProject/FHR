// Package main is the Blender .blend handler: it reads the scene as saved —
// the objects of the active scene's collections, their parent/child hierarchy
// and transforms, mesh geometry and materials' base colours — into the same
// glTF the rest of the 3D family diffs and previews.
//
// A .blend is read through its own DNA (the struct layouts it embeds), so
// nothing here depends on a Blender version's headers; what does depend on the
// version is *where* a mesh keeps its data. This reader handles files saved by
// Blender 5.0 and later, whose meshes keep their attributes in an
// AttributeStorage (checked against 5.2 saves); older files (CustomData layers
// and, before 3.0, MPoly/MLoop arrays) are refused with the version named
// rather than misread. Files may be zstd- or gzip-compressed.
//
// What is read is the *saved* state: modifiers, shape keys, animation, drivers,
// geometry nodes and curves are not evaluated. An object of a kind this reader
// cannot turn into a mesh (a curve, text, a metaball, grease pencil, a volume,
// a collection instance) is refused by name, so a diff never silently omits it;
// lights, cameras, speakers and armatures are ignored.
//
// Writing .blend files is not supported.
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the Blender format. It has no writer.
var Codec = &scene.Codec{
	ID:      "blend",
	Formats: []string{".blend"},
	Decode:  decode,
}

// Object types (DNA_object_types.h) that matter here.
const (
	obEmpty   = 0
	obMesh    = 1
	obCurve   = 2
	obSurf    = 3
	obFont    = 4
	obMBall   = 5
	obLamp    = 10
	obCamera  = 11
	obSpeaker = 12
	obProbe   = 13
	obLattice = 22
	obArm     = 25
	obGPencil = 26
	obCurves  = 27
	obPoints  = 28
	obVolume  = 29
	obGP      = 30
)

// Attribute types and domains (blender::bke::AttrType / AttrDomain).
const (
	attrBool   = 0
	attrInt32  = 3
	attrInt32x = 4 // int2
	attrFloat2 = 6
	attrFloat3 = 7

	domPoint  = 0
	domEdge   = 1
	domFace   = 2
	domCorner = 3
)

func decode(blob []byte) (*scene.Model, error) {
	f, err := readBlend(blob)
	if err != nil {
		return nil, err
	}
	if f.version < 500 {
		return nil, fmt.Errorf("this .blend was saved by Blender %d.%d: only 5.0 and later files are supported (their meshes keep attributes in an AttributeStorage); re-save it in Blender 5.x, or export glTF", f.version/100, f.version%100)
	}
	scenes := f.ofType("Scene")
	if len(scenes) == 0 {
		return &scene.Model{}, nil
	}
	sc := scenes[0]
	mc := sc.at(sc.ptr("master_collection"), 0)
	if !mc.ok() {
		return &scene.Model{}, nil
	}

	r := &reader{f: f, inScene: map[uint64]bool{}, order: nil}
	if err := r.collect(mc, map[uint64]bool{}, 0); err != nil {
		return nil, err
	}
	m, err := r.model()
	if err != nil {
		return nil, err
	}
	// Blender's units: scale_length metres per unit.
	// Applied only when the scene has a unit system at all (None ignores the scale).
	if unit := sc.sub("unit", 0); unit.u8("system") != 0 {
		if k := unit.f32("scale_length"); k > 0 && k != 1 {
			sm := [16]float64{k, 0, 0, 0, 0, k, 0, 0, 0, 0, k, 0, 0, 0, 0, 1}
			for _, o := range m.Roots {
				o.Matrix = mulMat(&sm, o.Matrix)
			}
		}
	}
	// Blender is Z-up: (x, y, z) → (x, z, -y).
	rot := [16]float64{1, 0, 0, 0, 0, 0, -1, 0, 0, 1, 0, 0, 0, 0, 0, 1}
	for _, o := range m.Roots {
		o.Matrix = mulMat(&rot, o.Matrix)
	}
	return m, nil
}

type reader struct {
	f       *bfile
	inScene map[uint64]bool
	order   []uint64 // object pointers, in collection order
	objs    map[uint64]sobj
}

// collect walks a collection tree, recording its objects.
func (r *reader) collect(c sobj, seen map[uint64]bool, depth int) error {
	if depth > 64 {
		return fmt.Errorf("collections nest deeper than 64")
	}
	if r.objs == nil {
		r.objs = map[uint64]sobj{}
	}
	for _, co := range c.list("gobject") {
		p := co.ptr("ob")
		if p == 0 || r.inScene[p] {
			continue
		}
		ob := c.at(p, 0)
		if !ob.ok() {
			continue
		}
		r.inScene[p] = true
		r.order = append(r.order, p)
		r.objs[p] = ob
	}
	for _, ch := range c.list("children") {
		p := ch.ptr("collection")
		if p == 0 || seen[p] {
			continue
		}
		seen[p] = true
		if sub := c.at(p, 0); sub.ok() {
			if err := r.collect(sub, seen, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func idName(id sobj) string {
	n := id.str("name")
	if len(n) > 2 {
		return n[2:] // the two-letter ID code ("OB", "ME", …) is not part of the name
	}
	return n
}

// model builds the object tree of everything collected.
func (r *reader) model() (*scene.Model, error) {
	children := map[uint64][]uint64{}
	var roots []uint64
	for _, p := range r.order {
		par := r.objs[p].ptr("parent")
		if par != 0 && r.inScene[par] {
			children[par] = append(children[par], p)
		} else {
			roots = append(roots, p)
		}
	}
	m := &scene.Model{}
	var build func(p uint64, depth int) (*scene.Object, error)
	build = func(p uint64, depth int) (*scene.Object, error) {
		if depth > 64 {
			return nil, fmt.Errorf("objects nest deeper than 64 (a parent cycle?)")
		}
		ob := r.objs[p]
		name := idName(ob.sub("id", 0))
		o, err := r.object(ob, name)
		if err != nil || o == nil {
			return nil, err
		}
		for _, c := range children[p] {
			co, err := build(c, depth+1)
			if err != nil {
				return nil, err
			}
			if co != nil {
				o.Children = append(o.Children, co)
			}
		}
		return o, nil
	}
	for _, p := range roots {
		o, err := build(p, 0)
		if err != nil {
			return nil, err
		}
		if o == nil {
			continue
		}
		// A parent outside the scene still places its children: fold its chain in.
		if par := r.objs[p].ptr("parent"); par != 0 {
			chain, err := r.chain(par)
			if err != nil {
				return nil, err
			}
			o.Matrix = mulMat(chain, o.Matrix)
		}
		m.Roots = append(m.Roots, o)
	}
	return m, nil
}

// anyObj finds an Object by pointer among the file's IDs.
func (r *reader) anyObj(p uint64) sobj {
	if ob, ok := r.objs[p]; ok {
		return ob
	}
	if blk := r.f.byPtr[p]; blk != nil {
		if sd := r.f.dna.byName["Object"]; sd != nil && blk.sdna < len(r.f.dna.structs) && r.f.dna.structs[blk.sdna] == sd && len(blk.data) >= sd.size {
			return sobj{r.f, sd, blk.data[:sd.size], blk.scope}
		}
	}
	return sobj{}
}

// chain is the world matrix of an object that is not in the scene's collections.
func (r *reader) chain(p uint64) (*[16]float64, error) {
	var m *[16]float64
	for n := 0; p != 0 && n < 64; n++ {
		ob := r.anyObj(p)
		if !ob.ok() {
			break
		}
		local, err := r.local(ob)
		if err != nil {
			return nil, err
		}
		m = mulMat(local, m)
		p = ob.ptr("parent")
	}
	return m, nil
}

// object converts one Object: a group for empties and ignored kinds, geometry
// for meshes, an error for kinds that carry geometry this reader cannot read.
func (r *reader) object(ob sobj, name string) (*scene.Object, error) {
	typ := ob.i16("type")
	switch typ {
	case obMesh, obEmpty, obLamp, obCamera, obSpeaker, obProbe, obArm, obLattice:
	case obCurve, obSurf, obFont, obMBall, obGPencil, obCurves, obPoints, obVolume, obGP:
		return nil, fmt.Errorf("object %q is a %s, which this reader does not turn into a mesh: convert it to a mesh in Blender first", name, objectKind(typ))
	default:
		return nil, fmt.Errorf("object %q has the unknown type %d", name, typ)
	}
	if typ == obEmpty && ob.ptr("dup_group") != 0 {
		return nil, fmt.Errorf("empty %q instances a collection, which is not supported: make the instance real in Blender first", name)
	}
	if pt := ob.i16("partype"); pt != 0 && ob.ptr("parent") != 0 { // PAROBJECT
		return nil, fmt.Errorf("object %q is parented to a bone, vertex or other target (parent type %d), which is not supported", name, pt)
	}
	o := &scene.Object{Name: name}
	rel, err := r.local(ob)
	if err != nil {
		return nil, fmt.Errorf("object %q: %w", name, err)
	}
	if ob.ptr("parent") != 0 { // world = parent world · parentinv · local
		inv := ob.f32s("parentinv")
		if len(inv) == 16 {
			var pi [16]float64
			copy(pi[:], inv)
			rel = mulMat(&pi, rel)
		}
	}
	o.Matrix = rel
	switch typ {
	case obMesh:
		me := ob.at(ob.ptr("data"), 0)
		if !me.ok() {
			return o, nil
		}
		prims, err := r.mesh(me)
		if err != nil {
			return nil, fmt.Errorf("mesh of object %q: %w", name, err)
		}
		o.Prims = prims
	}
	if len(o.Prims) == 0 && typ != obMesh && typ != obEmpty {
		return nil, nil // lights, cameras, … leave no trace
	}
	return o, nil
}

func objectKind(t int) string {
	return map[int]string{
		obCurve: "curve", obSurf: "surface", obFont: "text object", obMBall: "metaball", obGPencil: "grease pencil object",
		obCurves: "hair curves object", obPoints: "point cloud", obVolume: "volume", obGP: "grease pencil object",
	}[t]
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

func rotAxis(axis int, rad float64) [16]float64 {
	c, s := math.Cos(rad), math.Sin(rad)
	switch axis {
	case 0:
		return [16]float64{1, 0, 0, 0, 0, c, s, 0, 0, -s, c, 0, 0, 0, 0, 1}
	case 1:
		return [16]float64{c, 0, -s, 0, 0, 1, 0, 0, s, 0, c, 0, 0, 0, 0, 1}
	}
	return [16]float64{c, s, 0, 0, -s, c, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

// eulerMatrix applies rotations about x, y, z (radians) in an order's letters,
// first letter first: XYZ is Rz·Ry·Rx.
func eulerMatrix(e []float64, mode int) [16]float64 {
	seq := map[int][3]int{1: {0, 1, 2}, 2: {0, 2, 1}, 3: {1, 0, 2}, 4: {1, 2, 0}, 5: {2, 0, 1}, 6: {2, 1, 0}}[mode]
	if mode < 1 || mode > 6 {
		seq = [3]int{0, 1, 2}
	}
	r := ident
	for _, ax := range seq {
		a := rotAxis(ax, e[ax])
		r = *mulMat(&a, &r)
	}
	return r
}

func quatMatrix(q []float64) [16]float64 {
	w, x, y, z := q[0], q[1], q[2], q[3]
	if l := math.Sqrt(w*w + x*x + y*y + z*z); l > 0 {
		w, x, y, z = w/l, x/l, y/l, z/l
	}
	return [16]float64{
		1 - 2*(y*y+z*z), 2 * (x*y + z*w), 2 * (x*z - y*w), 0,
		2 * (x*y - z*w), 1 - 2*(x*x+z*z), 2 * (y*z + x*w), 0,
		2 * (x*z + y*w), 2 * (y*z - x*w), 1 - 2*(x*x+y*y), 0,
		0, 0, 0, 1,
	}
}

func axisAngleMatrix(axis []float64, a float64) [16]float64 {
	l := math.Sqrt(axis[0]*axis[0] + axis[1]*axis[1] + axis[2]*axis[2])
	if l == 0 {
		return ident
	}
	x, y, z := axis[0]/l, axis[1]/l, axis[2]/l
	c, s := math.Cos(a), math.Sin(a)
	t := 1 - c
	return [16]float64{
		t*x*x + c, t*x*y + s*z, t*x*z - s*y, 0,
		t*x*y - s*z, t*y*y + c, t*y*z + s*x, 0,
		t*x*z + s*y, t*y*z - s*x, t*z*z + c, 0,
		0, 0, 0, 1,
	}
}

// local is an object's own transform: T · R · S, with the delta transforms folded in.
func (r *reader) local(ob sobj) (*[16]float64, error) {
	loc, dloc := ob.f32s("loc"), ob.f32s("dloc")
	size, dscale := ob.f32s("size"), ob.f32s("dscale")
	if len(loc) != 3 || len(dloc) != 3 || len(size) != 3 || len(dscale) != 3 {
		return nil, fmt.Errorf("the object has no transform fields")
	}
	var rot [16]float64
	switch mode := ob.i16("rotmode"); {
	case mode == 0: // quaternion
		rot = quatMatrix(ob.f32s("quat"))
		dq := quatMatrix(ob.f32s("dquat"))
		rot = *mulMat(&dq, &rot)
	case mode == -1: // axis-angle
		rot = axisAngleMatrix(ob.f32s("rotAxis"), ob.f32("rotAngle"))
		d := axisAngleMatrix(ob.f32s("drotAxis"), ob.f32("drotAngle"))
		rot = *mulMat(&d, &rot)
	default:
		rot = eulerMatrix(ob.f32s("rot"), mode)
		d := eulerMatrix(ob.f32s("drot"), mode)
		rot = *mulMat(&d, &rot)
	}
	sc := [3]float64{size[0] * dscale[0], size[1] * dscale[1], size[2] * dscale[2]}
	m := rot
	for c := 0; c < 3; c++ { // R · S scales the columns
		for rr := 0; rr < 3; rr++ {
			m[c*4+rr] *= sc[c]
		}
	}
	m[12], m[13], m[14] = loc[0]+dloc[0], loc[1]+dloc[1], loc[2]+dloc[2]
	if m == ident {
		return nil, nil
	}
	return &m, nil
}

// ── meshes ────────────────────────────────────────────────────────────────────

type attr struct {
	typ, domain int
	data        []byte
	size        int
	single      bool
}

// attrs indexes a mesh's AttributeStorage by name.
func (r *reader) attrs(me sobj) map[string]*attr {
	out := map[string]*attr{}
	as := me.sub("attribute_storage", 0)
	n := as.i32("dna_attributes_num")
	if n < 0 || n > 1<<16 {
		return out
	}
	for i := 0; i < n; i++ {
		a := as.at(as.ptr("dna_attributes"), i)
		if !a.ok() {
			continue
		}
		name := strings.TrimRight(string(a.bytes(a.ptr("name"))), "\x00")
		at := &attr{typ: a.i16("data_type"), domain: a.u8("domain")}
		if a.u8("storage_type") == 0 { // an array
			arr := a.at(a.ptr("data"), 0)
			at.data = arr.bytes(arr.ptr("data"))
			at.size = int(min(arr.i64("size"), 1<<30))
			at.single = arr.u8("is_single") != 0
		} else { // one value for every element
			one := a.at(a.ptr("data"), 0)
			at.data = one.bytes(one.ptr("data"))
			at.single = true
		}
		out[name] = at
	}
	return out
}

func (a *attr) elemSize() int {
	switch a.typ {
	case attrBool:
		return 1
	case attrInt32, 5: // int, float
		return 4
	case attrFloat2, attrInt32x:
		return 8
	case attrFloat3:
		return 12
	}
	return 0
}

// get returns element i's bytes, broadcasting a single-valued attribute.
func (a *attr) get(i int) []byte {
	if a == nil {
		return nil
	}
	sz := a.elemSize()
	if sz == 0 {
		return nil
	}
	if a.single || a.size <= 1 && len(a.data) == sz {
		if len(a.data) < sz {
			return nil
		}
		return a.data[:sz]
	}
	if (i+1)*sz > len(a.data) {
		return nil
	}
	return a.data[i*sz : (i+1)*sz]
}

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func (r *reader) mesh(me sobj) ([]*scene.FlatPrim, error) {
	nv, nf, nl := me.i32("totvert"), me.i32("totpoly"), me.i32("totloop")
	if nv < 0 || nf < 0 || nl < 0 || nv > 1<<28 || nf > 1<<28 || nl > 1<<28 {
		return nil, fmt.Errorf("the mesh counts are out of range")
	}
	if nv == 0 || nf == 0 {
		return nil, nil
	}
	at := r.attrs(me)
	pos, cv := at["position"], at[".corner_vert"]
	if pos == nil || cv == nil || pos.typ != attrFloat3 || pos.domain != domPoint || cv.typ != attrInt32 || cv.domain != domCorner {
		return nil, fmt.Errorf("the mesh has no position / corner-vertex attributes in the layout this reader knows")
	}
	if len(pos.data) < nv*12 || len(cv.data) < nl*4 {
		return nil, fmt.Errorf("the mesh's attribute data is shorter than its counts")
	}
	offs := me.bytes(me.ptr("poly_offset_indices"))
	if len(offs) < (nf+1)*4 {
		return nil, fmt.Errorf("the mesh has no face offsets")
	}
	faceStart := make([]int, nf+1)
	for i := range faceStart {
		faceStart[i] = int(int32(le32(offs[i*4:])))
	}
	if faceStart[0] != 0 || faceStart[nf] != nl {
		return nil, fmt.Errorf("the mesh's face offsets do not span its corners")
	}
	for i := 0; i < nf; i++ {
		if faceStart[i+1] < faceStart[i]+3 {
			return nil, fmt.Errorf("face %d has fewer than 3 corners", i)
		}
	}

	P := make([][3]float64, nv)
	for i := range P {
		for k := 0; k < 3; k++ {
			v := float64(math.Float32frombits(le32(pos.data[i*12+k*4:])))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("vertex %d has a non-finite coordinate", i)
			}
			P[i][k] = v
		}
	}
	corner := make([]int, nl)
	for i := range corner {
		v := int(int32(le32(cv.data[i*4:])))
		if v < 0 || v >= nv {
			return nil, fmt.Errorf("corner %d refers to vertex %d of %d", i, v, nv)
		}
		corner[i] = v
	}

	// Shading: faces are smooth unless "sharp_face" says flat.
	sharp := at["sharp_face"]
	isSharp := func(f int) bool {
		if sharp == nil || sharp.domain != domFace {
			return false
		}
		b := sharp.get(f)
		return len(b) > 0 && b[0] != 0
	}
	faceN := make([][3]float64, nf) // area-weighted: the Newell vector
	for f := 0; f < nf; f++ {
		var n [3]float64
		s, e := faceStart[f], faceStart[f+1]
		for k := s; k < e; k++ {
			a, b := P[corner[k]], P[corner[s+(k-s+1)%(e-s)]]
			n[0] += (a[1] - b[1]) * (a[2] + b[2])
			n[1] += (a[2] - b[2]) * (a[0] + b[0])
			n[2] += (a[0] - b[0]) * (a[1] + b[1])
		}
		faceN[f] = n
	}
	vertN := make([][3]float64, nv)
	for f := 0; f < nf; f++ {
		if isSharp(f) {
			continue
		}
		for k := faceStart[f]; k < faceStart[f+1]; k++ {
			v := corner[k]
			for c := 0; c < 3; c++ {
				vertN[v][c] += faceN[f][c]
			}
		}
	}
	unit := func(v [3]float64) [3]float64 {
		l := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
		if l == 0 {
			return [3]float64{0, 0, 1}
		}
		return [3]float64{v[0] / l, v[1] / l, v[2] / l}
	}

	// UVs: the active map, else the first corner-domain float2 attribute.
	var uv *attr
	if name := strings.TrimRight(string(me.bytes(me.ptr("active_uv_map_attribute"))), "\x00"); name != "" {
		uv = at[name]
	}
	if uv == nil || uv.typ != attrFloat2 || uv.domain != domCorner {
		uv = nil
		for name, a := range at {
			if a.typ == attrFloat2 && a.domain == domCorner && !strings.HasPrefix(name, ".") && (uv == nil || name < nameOf(at, uv)) {
				uv = a
			}
		}
	}
	if uv != nil && len(uv.data) < nl*8 {
		uv = nil
	}

	// Which material each face takes.
	mi := at["material_index"]
	slot := func(f int) int {
		if mi == nil || mi.domain != domFace || mi.typ != attrInt32 {
			return 0
		}
		b := mi.get(f)
		if len(b) < 4 {
			return 0
		}
		return int(int32(le32(b)))
	}
	mats := r.slots(me)

	type key [5]int32
	type group struct {
		fp   *scene.FlatPrim
		seen map[key]uint32
	}
	groups := map[int]*group{}
	var order []int
	for f := 0; f < nf; f++ {
		s := slot(f)
		if s < 0 || s >= len(mats) {
			s = -1
			if len(mats) > 0 {
				s = 0
			}
		}
		g := groups[s]
		if g == nil {
			fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
			if s >= 0 && mats[s] != nil {
				fp.Material = mats[s].name
				fp.BaseColor = mats[s].color
			}
			g = &group{fp, map[key]uint32{}}
			groups[s] = g
			order = append(order, s)
		}
		flat := isSharp(f)
		var poly []uint32
		for c := faceStart[f]; c < faceStart[f+1]; c++ {
			v := corner[c]
			n := unit(vertN[v])
			if flat {
				n = unit(faceN[f])
			}
			var u [2]float32
			if uv != nil {
				u = [2]float32{math.Float32frombits(le32(uv.data[c*8:])), math.Float32frombits(le32(uv.data[c*8+4:]))}
			}
			k := key{int32(v), int32(math.Float32bits(float32(n[0]))), int32(math.Float32bits(float32(n[1]))), int32(math.Float32bits(float32(n[2]))), 0}
			if uv != nil {
				k[4] = int32(math.Float32bits(u[0])) ^ int32(math.Float32bits(u[1]))*31
			}
			id, ok := g.seen[k]
			if !ok {
				fp := g.fp
				fp.Positions = append(fp.Positions, P[v])
				fp.Normals = append(fp.Normals, n)
				if uv != nil {
					fp.UVs = append(fp.UVs, [2]float64{float64(u[0]), 1 - float64(u[1])}) // Blender v is up, glTF's down
				}
				id = uint32(len(fp.Positions) - 1)
				g.seen[k] = id
			}
			poly = append(poly, id)
		}
		for k := 1; k+1 < len(poly); k++ { // fan-triangulate
			g.fp.Indices = append(g.fp.Indices, poly[0], poly[k], poly[k+1])
		}
	}
	var out []*scene.FlatPrim
	for _, s := range order {
		if fp := groups[s].fp; len(fp.Indices) > 0 {
			out = append(out, fp)
		}
	}
	return out, nil
}

func nameOf(at map[string]*attr, a *attr) string {
	for n, x := range at {
		if x == a {
			return n
		}
	}
	return ""
}

type mat struct {
	name  string
	color *[4]float64
}

// slots reads a mesh's material slots.
func (r *reader) slots(me sobj) []*mat {
	n := me.i16("totcol")
	if n <= 0 || n > 4096 {
		return nil
	}
	arr := me.bytes(me.ptr("mat"))
	out := make([]*mat, n)
	for i := 0; i < n && (i+1)*8 <= len(arr); i++ {
		p := binary.LittleEndian.Uint64(arr[i*8:])
		if p == 0 {
			continue
		}
		if ma := me.at(p, 0); ma.ok() {
			out[i] = r.material(ma)
		}
	}
	return out
}

// material is a Material's name and base colour: the Principled BSDF's unlinked
// Base Color when the material uses nodes, else the viewport colour.
func (r *reader) material(ma sobj) *mat {
	m := &mat{name: idName(ma.sub("id", 0))}
	c := [4]float64{ma.f32("r"), ma.f32("g"), ma.f32("b"), 1}
	if ma.u8("use_nodes") != 0 {
		if nt := ma.at(ma.ptr("nodetree"), 0); nt.ok() {
			for _, n := range nt.list("nodes") {
				if n.str("idname") != "ShaderNodeBsdfPrincipled" {
					continue
				}
				for _, s := range n.list("inputs") {
					if s.str("name") != "Base Color" || s.ptr("link") != 0 {
						continue
					}
					if v := s.at(s.ptr("default_value"), 0).f32s("value"); len(v) == 4 {
						c = [4]float64{v[0], v[1], v[2], 1}
					}
				}
				break
			}
		}
	}
	m.color = &c
	return m
}
