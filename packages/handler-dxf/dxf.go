// Package main is the DXF handler: AutoCAD's ASCII drawing exchange format. It
// reads the mesh and line geometry of the model space — 3DFACE, SOLID, polyface
// and polygon-mesh POLYLINEs, the R2000+ MESH entity, LINE, POINT, 3D POLYLINEs,
// LWPOLYLINEs — with INSERTed blocks expanded under their scale, rotation and
// placement, one object per layer. $INSUNITS is applied (a file's numbers become
// glTF's metres) and the Z-up drawing space is rotated to glTF's Y-up.
//
// Not read: ACIS solids and surfaces (3DSOLID, BODY, REGION, SURFACE) — refused by
// name, since they are boundary representations with no mesh in the file — array
// INSERTs, entities in a non-default extrusion direction, and binary DXF. Curves
// (CIRCLE, ARC, ELLIPSE, SPLINE) and annotation (TEXT, MTEXT, DIMENSION, HATCH, …)
// are 2D drafting, not model geometry, and are ignored. Colours are not read.
// DWG is a different, proprietary format.
//
// It writes R12-style ASCII (3DFACE, LINE, POINT per layer) in metres, which every
// CAD package reads.
package main

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the DXF format.
var Codec = &scene.Codec{
	ID:      "dxf",
	Formats: []string{".dxf"},
	Decode:  decode,
	Encode:  encode,
}

const (
	maxPairs    = 40_000_000
	maxEntities = 8_000_000
	maxInsert   = 16
	maxEmitted  = 20_000_000
)

type pair struct {
	code int
	val  string
}

// entity is one DXF entity: its type, layer and group codes, with a POLYLINE's
// VERTEX records folded in.
type entity struct {
	typ   string
	layer string
	g     []pair
	verts []*entity
}

func (e *entity) num(code int, def float64) float64 {
	for _, p := range e.g {
		if p.code == code {
			if f, err := strconv.ParseFloat(strings.TrimSpace(p.val), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f
			}
		}
	}
	return def
}

func (e *entity) str(code int) string {
	for _, p := range e.g {
		if p.code == code {
			return strings.TrimSpace(p.val)
		}
	}
	return ""
}

func (e *entity) point(i int) [3]float64 { // corner i: codes 10+i, 20+i, 30+i
	return [3]float64{e.num(10+i, 0), e.num(20+i, 0), e.num(30+i, 0)}
}

// pairs reads the group-code/value stream.
func readPairs(blob []byte) ([]pair, error) {
	lines := bytes.Split(blob, []byte("\n"))
	if len(lines) > 2*maxPairs {
		return nil, fmt.Errorf("the DXF has more than %d records", maxPairs)
	}
	var out []pair
	for i := 0; i+1 < len(lines); i += 2 {
		c, err := strconv.Atoi(strings.TrimSpace(string(lines[i])))
		if err != nil {
			if strings.TrimSpace(string(lines[i])) == "" && i+2 >= len(lines) {
				break
			}
			return nil, fmt.Errorf("line %d: %q is not a group code", i+1, strings.TrimSpace(string(lines[i])))
		}
		out = append(out, pair{c, strings.TrimRight(string(lines[i+1]), "\r")})
	}
	return out, nil
}

type block struct {
	base [3]float64
	ents []*entity
}

// entities groups pairs into entities until the section ends; polyline vertices
// are folded into their POLYLINE.
func groupEntities(ps []pair, i int, stops ...string) ([]*entity, int, error) {
	var out []*entity
	var poly *entity
	for i < len(ps) {
		p := ps[i]
		if p.code != 0 {
			return nil, i, fmt.Errorf("expected an entity, found group code %d", p.code)
		}
		name := strings.TrimSpace(p.val)
		for _, s := range stops {
			if name == s {
				return out, i, nil
			}
		}
		e := &entity{typ: name}
		i++
		for i < len(ps) && ps[i].code != 0 {
			if ps[i].code == 8 {
				e.layer = strings.TrimSpace(ps[i].val)
			}
			e.g = append(e.g, ps[i])
			i++
		}
		if len(out) > maxEntities {
			return nil, i, fmt.Errorf("the DXF has more than %d entities", maxEntities)
		}
		switch e.typ {
		case "POLYLINE":
			poly = e
			out = append(out, e)
		case "VERTEX":
			if poly == nil {
				return nil, i, fmt.Errorf("a VERTEX outside a POLYLINE")
			}
			poly.verts = append(poly.verts, e)
		case "SEQEND":
			poly = nil
		default:
			poly = nil
			out = append(out, e)
		}
	}
	return out, i, nil
}

// unitMetres maps $INSUNITS to metres per unit (0, unitless, and anything unknown pass through).
var unitMetres = map[int]float64{
	1: 0.0254, 2: 0.3048, 3: 1609.344, 4: 0.001, 5: 0.01, 6: 1, 7: 1000, 8: 2.54e-8, 9: 2.54e-5,
	10: 0.9144, 11: 1e-10, 12: 1e-9, 13: 1e-6, 14: 0.1, 15: 10, 16: 100, 17: 1e9, 18: 1.495978707e11,
}

func decode(blob []byte) (*scene.Model, error) {
	switch {
	case bytes.HasPrefix(blob, []byte("AutoCAD Binary DXF")):
		return nil, fmt.Errorf("binary DXF is not supported: save the drawing as ASCII DXF")
	case len(blob) >= 4 && bytes.HasPrefix(blob, []byte("AC10")):
		return nil, fmt.Errorf("this is a DWG file, not DXF")
	}
	ps, err := readPairs(blob)
	if err != nil {
		return nil, fmt.Errorf("not a DXF file: %w", err)
	}
	if len(ps) == 0 || ps[0].code != 0 {
		return nil, fmt.Errorf("not a DXF file")
	}

	blocks := map[string]*block{}
	var model []*entity
	insunits := 0
	sawSection := false
	for i := 0; i < len(ps); {
		p := ps[i]
		if p.code == 0 && strings.TrimSpace(p.val) == "EOF" {
			break
		}
		if p.code != 0 || strings.TrimSpace(p.val) != "SECTION" {
			return nil, fmt.Errorf("expected SECTION, found %q", strings.TrimSpace(p.val))
		}
		sawSection = true
		if i+1 >= len(ps) || ps[i+1].code != 2 {
			return nil, fmt.Errorf("a SECTION has no name")
		}
		name := strings.TrimSpace(ps[i+1].val)
		i += 2
		switch name {
		case "HEADER":
			for i < len(ps) && !(ps[i].code == 0 && strings.TrimSpace(ps[i].val) == "ENDSEC") {
				if ps[i].code == 9 && strings.TrimSpace(ps[i].val) == "$INSUNITS" && i+1 < len(ps) {
					if v, err := strconv.Atoi(strings.TrimSpace(ps[i+1].val)); err == nil {
						insunits = v
					}
				}
				i++
			}
		case "BLOCKS":
			for i < len(ps) && !(ps[i].code == 0 && strings.TrimSpace(ps[i].val) == "ENDSEC") {
				if ps[i].code != 0 || strings.TrimSpace(ps[i].val) != "BLOCK" {
					return nil, fmt.Errorf("expected BLOCK, found group code %d", ps[i].code)
				}
				hdr := &entity{typ: "BLOCK"}
				i++
				for i < len(ps) && ps[i].code != 0 {
					hdr.g = append(hdr.g, ps[i])
					i++
				}
				ents, ni, err := groupEntities(ps, i, "ENDBLK", "ENDSEC")
				if err != nil {
					return nil, err
				}
				i = ni
				if i < len(ps) && strings.TrimSpace(ps[i].val) == "ENDBLK" {
					i++
					for i < len(ps) && ps[i].code != 0 { // the ENDBLK's own codes
						i++
					}
				}
				blocks[hdr.str(2)] = &block{base: hdr.point(0), ents: ents}
			}
		case "ENTITIES":
			ents, ni, err := groupEntities(ps, i, "ENDSEC")
			if err != nil {
				return nil, err
			}
			i = ni
			model = append(model, ents...)
		default: // TABLES, CLASSES, OBJECTS, THUMBNAILIMAGE: skipped
			for i < len(ps) && !(ps[i].code == 0 && strings.TrimSpace(ps[i].val) == "ENDSEC") {
				i++
			}
		}
		if i < len(ps) && strings.TrimSpace(ps[i].val) == "ENDSEC" {
			i++
		}
	}
	if !sawSection {
		return nil, fmt.Errorf("not a DXF file: no SECTION")
	}

	b := &builder{blocks: blocks, layers: map[string]*layerAcc{}}
	if err := b.emit(model, nil, 0, ""); err != nil {
		return nil, err
	}
	m := &scene.Model{}
	for _, name := range b.order {
		l := b.layers[name]
		o := &scene.Object{Name: name}
		for _, p := range []*scene.FlatPrim{l.tris.prim(scene.KindTriangles), l.lines.prim(scene.KindLines), l.points.prim(scene.KindPoints)} {
			if p != nil {
				o.Prims = append(o.Prims, p)
			}
		}
		if len(o.Prims) > 0 {
			m.Roots = append(m.Roots, o)
		}
	}
	// Units, then Z-up → Y-up: (x, y, z) → (x, z, -y).
	k := 1.0
	if s, ok := unitMetres[insunits]; ok {
		k = s
	}
	root := [16]float64{k, 0, 0, 0, 0, 0, -k, 0, 0, k, 0, 0, 0, 0, 0, 1}
	for _, o := range m.Roots {
		o.Matrix = &root
	}
	return m, nil
}

// acc collects welded vertices and indices for one primitive kind of one layer.
type acc struct {
	pos  [][3]float64
	idx  []uint32
	weld map[[3]float64]uint32
}

func (a *acc) vertex(p [3]float64) uint32 {
	if a.weld == nil {
		a.weld = map[[3]float64]uint32{}
	}
	if i, ok := a.weld[p]; ok {
		return i
	}
	i := uint32(len(a.pos))
	a.pos = append(a.pos, p)
	a.weld[p] = i
	return i
}

func (a *acc) prim(kind scene.PrimKind) *scene.FlatPrim {
	if len(a.idx) == 0 {
		return nil
	}
	return &scene.FlatPrim{Kind: kind, MaterialIndex: -1, Positions: a.pos, Indices: a.idx}
}

type layerAcc struct{ tris, lines, points acc }

type builder struct {
	blocks  map[string]*block
	layers  map[string]*layerAcc
	order   []string
	emitted int
}

func (b *builder) layer(name string) *layerAcc {
	if name == "" {
		name = "0"
	}
	l := b.layers[name]
	if l == nil {
		l = &layerAcc{}
		b.layers[name] = l
		b.order = append(b.order, name)
	}
	return l
}

func (b *builder) count(n int) error {
	b.emitted += n
	if b.emitted > maxEmitted {
		return fmt.Errorf("the drawing expands to more than %d elements", maxEmitted)
	}
	return nil
}

type xform [16]float64

func (m *xform) apply(p [3]float64) [3]float64 {
	if m == nil {
		return p
	}
	return [3]float64{
		m[0]*p[0] + m[4]*p[1] + m[8]*p[2] + m[12],
		m[1]*p[0] + m[5]*p[1] + m[9]*p[2] + m[13],
		m[2]*p[0] + m[6]*p[1] + m[10]*p[2] + m[14],
	}
}

func mul(a, b *xform) *xform {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	var o xform
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

// ocsIsWorld reports whether an entity's extrusion direction is the default +Z,
// i.e. its points are already in world coordinates.
func ocsIsWorld(e *entity) bool {
	return math.Abs(e.num(210, 0)) < 1e-9 && math.Abs(e.num(220, 0)) < 1e-9 && math.Abs(e.num(230, 1)-1) < 1e-9
}

var refusedEntities = map[string]bool{"3DSOLID": true, "BODY": true, "REGION": true, "SURFACE": true, "PLANESURFACE": true, "EXTRUDEDSURFACE": true, "LOFTEDSURFACE": true, "REVOLVEDSURFACE": true, "SWEPTSURFACE": true, "NURBSURFACE": true}

// emit adds the entities' geometry under transform m.
func (b *builder) emit(ents []*entity, m *xform, depth int, inherit string) error {
	for _, e := range ents {
		// Inside a block, layer 0 means "the layer of the INSERT" (the AutoCAD rule).
		name := e.layer
		if inherit != "" && (name == "" || name == "0") {
			name = inherit
		}
		l := b.layer(name)
		switch e.typ {
		case "3DFACE", "SOLID":
			var c [4][3]float64
			for i := range c {
				c[i] = m.apply(e.point(i))
			}
			if e.typ == "SOLID" { // SOLID's corners are in zig-zag order: 1, 2, 4, 3
				c[2], c[3] = c[3], c[2]
			}
			if err := b.count(2); err != nil {
				return err
			}
			l.tris.idx = append(l.tris.idx, l.tris.vertex(c[0]), l.tris.vertex(c[1]), l.tris.vertex(c[2]))
			if c[3] != c[2] && c[3] != c[0] { // a triangle repeats its third corner as the fourth
				l.tris.idx = append(l.tris.idx, l.tris.vertex(c[0]), l.tris.vertex(c[2]), l.tris.vertex(c[3]))
			}
		case "LINE":
			if err := b.count(1); err != nil {
				return err
			}
			l.lines.idx = append(l.lines.idx, l.lines.vertex(m.apply(e.point(0))), l.lines.vertex(m.apply(e.point(1))))
		case "POINT":
			if err := b.count(1); err != nil {
				return err
			}
			l.points.idx = append(l.points.idx, l.points.vertex(m.apply(e.point(0))))
		case "LWPOLYLINE":
			if !ocsIsWorld(e) {
				return fmt.Errorf("an LWPOLYLINE with a non-default extrusion direction is not supported")
			}
			z := e.num(38, 0)
			var pts [][3]float64
			var x float64
			haveX := false
			for _, p := range e.g {
				switch p.code {
				case 10:
					x, _ = strconv.ParseFloat(strings.TrimSpace(p.val), 64)
					haveX = true
				case 20:
					if haveX {
						y, _ := strconv.ParseFloat(strings.TrimSpace(p.val), 64)
						pts = append(pts, m.apply([3]float64{x, y, z}))
						haveX = false
					}
				}
			}
			if err := b.addPolyline(l, pts, int(e.num(70, 0))&1 == 1); err != nil {
				return err
			}
		case "POLYLINE":
			if err := b.polyline(l, e, m); err != nil {
				return err
			}
		case "MESH":
			if err := b.mesh(l, e, m); err != nil {
				return err
			}
		case "INSERT":
			if err := b.insert(e, m, depth, name); err != nil {
				return err
			}
		default:
			if refusedEntities[e.typ] {
				return fmt.Errorf("the drawing holds a %s (an ACIS solid or surface), which has no mesh in the file and cannot be read", e.typ)
			}
			// curves, annotation, attributes, viewports…: not model geometry
		}
	}
	return nil
}

func (b *builder) addPolyline(l *layerAcc, pts [][3]float64, closed bool) error {
	if err := b.count(len(pts)); err != nil {
		return err
	}
	for i := 0; i+1 < len(pts); i++ {
		l.lines.idx = append(l.lines.idx, l.lines.vertex(pts[i]), l.lines.vertex(pts[i+1]))
	}
	if closed && len(pts) > 2 {
		l.lines.idx = append(l.lines.idx, l.lines.vertex(pts[len(pts)-1]), l.lines.vertex(pts[0]))
	}
	return nil
}

// polyline handles the three kinds of POLYLINE: a polyface mesh (flag 64), a
// polygon mesh (flag 16), and a plain 2D/3D polyline (lines).
func (b *builder) polyline(l *layerAcc, e *entity, m *xform) error {
	flags := int(e.num(70, 0))
	switch {
	case flags&64 != 0: // polyface mesh: location vertices (flag 128|64), then face records (flag 128)
		var pts [][3]float64
		for _, v := range e.verts {
			if int(v.num(70, 0))&64 != 0 {
				pts = append(pts, m.apply(v.point(0)))
			}
		}
		for _, v := range e.verts {
			vf := int(v.num(70, 0))
			if vf&128 == 0 || vf&64 != 0 {
				continue
			}
			var f []int
			for _, code := range []int{71, 72, 73, 74} {
				i := int(v.num(code, 0))
				if i < 0 { // a negative index marks an invisible edge
					i = -i
				}
				if i == 0 {
					break
				}
				if i > len(pts) {
					return fmt.Errorf("a polyface face refers to vertex %d of %d", i, len(pts))
				}
				f = append(f, i-1)
			}
			if err := b.count(len(f)); err != nil {
				return err
			}
			for k := 1; k+1 < len(f); k++ {
				l.tris.idx = append(l.tris.idx, l.tris.vertex(pts[f[0]]), l.tris.vertex(pts[f[k]]), l.tris.vertex(pts[f[k+1]]))
			}
		}
	case flags&16 != 0: // polygon mesh: an M×N grid of vertices
		mm, nn := int(e.num(71, 0)), int(e.num(72, 0))
		if mm < 2 || nn < 2 || mm > 1<<14 || nn > 1<<14 || len(e.verts) < mm*nn {
			return fmt.Errorf("a polygon mesh of %d × %d has %d vertices", mm, nn, len(e.verts))
		}
		at := func(i, j int) [3]float64 { return m.apply(e.verts[(i%mm)*nn+(j%nn)].point(0)) }
		mi, nj := mm-1, nn-1
		if flags&1 != 0 {
			mi = mm
		}
		if flags&32 != 0 {
			nj = nn
		}
		if err := b.count(mi * nj * 2); err != nil {
			return err
		}
		for i := 0; i < mi; i++ {
			for j := 0; j < nj; j++ {
				a, bb, c, d := at(i, j), at(i+1, j), at(i+1, j+1), at(i, j+1)
				l.tris.idx = append(l.tris.idx, l.tris.vertex(a), l.tris.vertex(bb), l.tris.vertex(c))
				l.tris.idx = append(l.tris.idx, l.tris.vertex(a), l.tris.vertex(c), l.tris.vertex(d))
			}
		}
	default: // a 2D or 3D polyline: its segments
		z := 0.0
		is3D := flags&8 != 0
		if !is3D {
			if !ocsIsWorld(e) {
				return fmt.Errorf("a 2D POLYLINE with a non-default extrusion direction is not supported")
			}
			z = e.num(30, 0)
		}
		var pts [][3]float64
		for _, v := range e.verts {
			p := v.point(0)
			if !is3D {
				p[2] = z
			}
			pts = append(pts, m.apply(p))
		}
		return b.addPolyline(l, pts, flags&1 != 0)
	}
	return nil
}

// mesh reads the R2000+ MESH entity: a vertex list, then a face list of
// "count, index…" runs. (Edges, creases and subdivision data are skipped.)
func (b *builder) mesh(l *layerAcc, e *entity, m *xform) error {
	var pts [][3]float64
	var faces []int
	state := ""
	nv, nf := 0, 0
	var cur [3]float64
	have := 0
	for _, p := range e.g {
		switch p.code {
		case 92:
			v, _ := strconv.Atoi(strings.TrimSpace(p.val))
			if v < 0 || v > maxEntities {
				return fmt.Errorf("a MESH claims %d vertices", v)
			}
			nv, state = v, "vertices"
		case 93:
			v, _ := strconv.Atoi(strings.TrimSpace(p.val))
			if v < 0 || v > 4*maxEntities {
				return fmt.Errorf("a MESH claims a face list of %d", v)
			}
			nf, state = v, "faces"
		case 94, 95:
			state = "other" // edges and creases follow
		case 10, 20, 30:
			if state != "vertices" || len(pts) >= nv {
				continue
			}
			f, _ := strconv.ParseFloat(strings.TrimSpace(p.val), 64)
			cur[(p.code-10)/10] = f
			have |= 1 << ((p.code - 10) / 10)
			if have == 7 {
				pts = append(pts, m.apply(cur))
				have = 0
			}
		case 90:
			if state == "faces" && len(faces) < nf {
				v, _ := strconv.Atoi(strings.TrimSpace(p.val))
				faces = append(faces, v)
			}
		}
	}
	if len(pts) != nv {
		return fmt.Errorf("a MESH declares %d vertices and holds %d", nv, len(pts))
	}
	for i := 0; i < len(faces); {
		n := faces[i]
		if n < 3 || i+1+n > len(faces) {
			return fmt.Errorf("a MESH face list is malformed")
		}
		var f []int
		for _, v := range faces[i+1 : i+1+n] {
			if v < 0 || v >= len(pts) {
				return fmt.Errorf("a MESH face refers to vertex %d of %d", v, len(pts))
			}
			f = append(f, v)
		}
		if err := b.count(n); err != nil {
			return err
		}
		for k := 1; k+1 < len(f); k++ {
			l.tris.idx = append(l.tris.idx, l.tris.vertex(pts[f[0]]), l.tris.vertex(pts[f[k]]), l.tris.vertex(pts[f[k+1]]))
		}
		i += 1 + n
	}
	return nil
}

// insert expands a block reference: M = T(insertion) · Rz(rotation) · S · T(−base).
func (b *builder) insert(e *entity, m *xform, depth int, layer string) error {
	if depth >= maxInsert {
		return fmt.Errorf("blocks nest deeper than %d (a cycle?)", maxInsert)
	}
	blk := b.blocks[e.str(2)]
	if blk == nil {
		return fmt.Errorf("an INSERT refers to the missing block %q", e.str(2))
	}
	if !ocsIsWorld(e) {
		return fmt.Errorf("an INSERT of %q with a non-default extrusion direction is not supported", e.str(2))
	}
	if e.num(70, 1) > 1 || e.num(71, 1) > 1 {
		return fmt.Errorf("an array INSERT of %q (rows/columns) is not supported", e.str(2))
	}
	a := e.num(50, 0) * math.Pi / 180
	c, s := math.Cos(a), math.Sin(a)
	sx, sy, sz := e.num(41, 1), e.num(42, 1), e.num(43, 1)
	ip := e.point(0)
	bp := blk.base
	// local → world: scale, rotate about z, translate; the block's base point is the origin of the insertion.
	local := &xform{
		c * sx, s * sx, 0, 0,
		-s * sy, c * sy, 0, 0,
		0, 0, sz, 0,
		ip[0], ip[1], ip[2], 1,
	}
	shift := &xform{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, -bp[0], -bp[1], -bp[2], 1}
	return b.emit(blk.ents, mul(m, mul(local, shift)), depth+1, layer)
}
