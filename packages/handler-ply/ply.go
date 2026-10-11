// Package main is the PLY (Polygon File Format / Stanford Triangle Format)
// handler: ASCII and binary (little- and big-endian) files, with positions,
// normals, texture coordinates, vertex colours, polygon faces, edges, and bare
// point clouds. A PLY has no named objects, so a file is one object called
// "mesh"; the semantic diff is over the glTF that object becomes.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the PLY format.
var Codec = &scene.Codec{
	ID:      "ply",
	Formats: []string{".ply"},
	Decode:  decode,
	Encode:  encode,
}

type prop struct {
	name      string
	typ       string // scalar type, or the element type of a list
	list      bool
	countType string // list length type
}

type element struct {
	name  string
	count int
	props []prop
}

type header struct {
	format   string // ascii | binary_little_endian | binary_big_endian
	elements []*element
}

var typeSize = map[string]int{
	"char": 1, "uchar": 1, "short": 2, "ushort": 2, "int": 4, "uint": 4, "float": 4, "double": 8,
	"int8": 1, "uint8": 1, "int16": 2, "uint16": 2, "int32": 4, "uint32": 4, "float32": 4, "float64": 8,
}

func parseHeader(blob []byte) (*header, []byte, error) {
	if !bytes.HasPrefix(blob, []byte("ply")) {
		return nil, nil, fmt.Errorf("not a PLY file: missing \"ply\" magic")
	}
	h := &header{}
	rest := blob
	var cur *element
	for first := true; ; first = false {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return nil, nil, fmt.Errorf("PLY header has no end_header")
		}
		line := strings.TrimSpace(string(rest[:nl]))
		rest = rest[nl+1:]
		f := strings.Fields(line)
		if first {
			if line != "ply" {
				return nil, nil, fmt.Errorf("not a PLY file: bad magic line %q", line)
			}
			continue
		}
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "format":
			if len(f) < 2 {
				return nil, nil, fmt.Errorf("bad format line %q", line)
			}
			h.format = f[1]
			if h.format != "ascii" && h.format != "binary_little_endian" && h.format != "binary_big_endian" {
				return nil, nil, fmt.Errorf("unknown PLY format %q", h.format)
			}
		case "comment", "obj_info":
		case "element":
			if len(f) != 3 {
				return nil, nil, fmt.Errorf("bad element line %q", line)
			}
			n, err := strconv.Atoi(f[2])
			if err != nil || n < 0 {
				return nil, nil, fmt.Errorf("bad element count in %q", line)
			}
			cur = &element{name: f[1], count: n}
			h.elements = append(h.elements, cur)
		case "property":
			if cur == nil {
				return nil, nil, fmt.Errorf("property before any element: %q", line)
			}
			p := prop{}
			switch {
			case len(f) == 5 && f[1] == "list":
				p = prop{name: f[4], typ: f[3], list: true, countType: f[2]}
				if _, ok := typeSize[p.countType]; !ok {
					return nil, nil, fmt.Errorf("unknown type %q", p.countType)
				}
			case len(f) == 3:
				p = prop{name: f[2], typ: f[1]}
			default:
				return nil, nil, fmt.Errorf("bad property line %q", line)
			}
			if _, ok := typeSize[p.typ]; !ok {
				return nil, nil, fmt.Errorf("unknown type %q", p.typ)
			}
			cur.props = append(cur.props, p)
		case "end_header":
			if h.format == "" {
				return nil, nil, fmt.Errorf("PLY header has no format line")
			}
			return h, rest, nil
		default:
			return nil, nil, fmt.Errorf("unknown header keyword %q", f[0])
		}
	}
}

// reader yields successive numbers from either the ASCII or binary body.
type reader struct {
	ascii []string // remaining ASCII tokens
	bin   []byte
	order binary.ByteOrder
	err   error
}

func newReader(h *header, body []byte) *reader {
	r := &reader{}
	switch h.format {
	case "ascii":
		r.ascii = strings.Fields(string(body))
	case "binary_little_endian":
		r.bin, r.order = body, binary.LittleEndian
	default:
		r.bin, r.order = body, binary.BigEndian
	}
	return r
}

func (r *reader) next(typ string) float64 {
	if r.err != nil {
		return 0
	}
	if r.bin == nil && r.order == nil { // ASCII
		if len(r.ascii) == 0 {
			r.err = fmt.Errorf("PLY body ends early")
			return 0
		}
		t := r.ascii[0]
		r.ascii = r.ascii[1:]
		v, err := strconv.ParseFloat(t, 64)
		if err != nil {
			r.err = fmt.Errorf("bad number %q in PLY body", t)
		}
		return v
	}
	n := typeSize[typ]
	if len(r.bin) < n {
		r.err = fmt.Errorf("PLY body ends early")
		return 0
	}
	b := r.bin[:n]
	r.bin = r.bin[n:]
	switch typ {
	case "char", "int8":
		return float64(int8(b[0]))
	case "uchar", "uint8":
		return float64(b[0])
	case "short", "int16":
		return float64(int16(r.order.Uint16(b)))
	case "ushort", "uint16":
		return float64(r.order.Uint16(b))
	case "int", "int32":
		return float64(int32(r.order.Uint32(b)))
	case "uint", "uint32":
		return float64(r.order.Uint32(b))
	case "float", "float32":
		return float64(math.Float32frombits(r.order.Uint32(b)))
	default: // double, float64
		return math.Float64frombits(r.order.Uint64(b))
	}
}

// remaining is how many more values the body can still hold at the very most:
// the cap that keeps a hostile element count from allocating gigabytes.
func (r *reader) remaining() int {
	if r.bin == nil && r.order == nil {
		return len(r.ascii)
	}
	return len(r.bin)
}

func decode(blob []byte) (*scene.Model, error) {
	h, body, err := parseHeader(blob)
	if err != nil {
		return nil, err
	}
	r := newReader(h, body)
	var (
		pos               [][3]float64
		nrm               [][3]float64
		uvs               [][2]float64
		cols              [][4]float64
		hasN, hasUV, hasC bool
		faces             [][]uint32
		edges             [][2]uint32
	)
	for _, e := range h.elements {
		if e.count > r.remaining() {
			return nil, fmt.Errorf("element %q claims %d entries but the body is too short", e.name, e.count)
		}
		switch e.name {
		case "vertex":
			col := map[string]int{}
			for i, p := range e.props {
				if !p.list {
					col[p.name] = i
				}
			}
			has := func(names ...string) bool {
				for _, n := range names {
					if _, ok := col[n]; !ok {
						return false
					}
				}
				return true
			}
			if !has("x", "y", "z") {
				return nil, fmt.Errorf("vertex element lacks x/y/z")
			}
			hasN = has("nx", "ny", "nz")
			uName, vName := "", ""
			for _, pair := range [][2]string{{"s", "t"}, {"u", "v"}, {"texture_u", "texture_v"}, {"texture_s", "texture_t"}} {
				if has(pair[0], pair[1]) {
					uName, vName, hasUV = pair[0], pair[1], true
					break
				}
			}
			hasC = has("red", "green", "blue")
			hasA := has("alpha")
			colorScale := 1.0 / 255 // uchar colours; float colours are already 0..1
			if hasC && strings.HasPrefix(e.props[col["red"]].typ, "float") {
				colorScale = 1
			}
			vals := make([]float64, len(e.props))
			for i := 0; i < e.count; i++ {
				for j, p := range e.props {
					if p.list { // a list on a vertex: read and discard
						n := int(r.next(p.countType))
						for k := 0; k < n && r.err == nil; k++ {
							r.next(p.typ)
						}
						continue
					}
					vals[j] = r.next(p.typ)
				}
				if r.err != nil {
					return nil, r.err
				}
				pos = append(pos, [3]float64{vals[col["x"]], vals[col["y"]], vals[col["z"]]})
				if hasN {
					nrm = append(nrm, [3]float64{vals[col["nx"]], vals[col["ny"]], vals[col["nz"]]})
				}
				if hasUV {
					uvs = append(uvs, [2]float64{vals[col[uName]], 1 - vals[col[vName]]})
				}
				if hasC {
					c := [4]float64{vals[col["red"]] * colorScale, vals[col["green"]] * colorScale, vals[col["blue"]] * colorScale, 1}
					if hasA {
						c[3] = vals[col["alpha"]] * colorScale
					}
					cols = append(cols, c)
				}
			}
		case "face":
			listIdx := -1
			for i, p := range e.props {
				if p.list && (p.name == "vertex_indices" || p.name == "vertex_index") {
					listIdx = i
				}
			}
			if listIdx < 0 {
				return nil, fmt.Errorf("face element has no vertex_indices list")
			}
			for i := 0; i < e.count; i++ {
				for j, p := range e.props {
					if !p.list {
						r.next(p.typ)
						continue
					}
					n := int(r.next(p.countType))
					if n < 0 || n > r.remaining() {
						return nil, fmt.Errorf("face %d has an impossible vertex count %d", i, n)
					}
					var f []uint32
					for k := 0; k < n; k++ {
						f = append(f, uint32(r.next(p.typ)))
					}
					if j == listIdx {
						faces = append(faces, f)
					}
				}
				if r.err != nil {
					return nil, r.err
				}
			}
		case "edge":
			c1, c2 := -1, -1
			for i, p := range e.props {
				switch p.name {
				case "vertex1":
					c1 = i
				case "vertex2":
					c2 = i
				}
			}
			for i := 0; i < e.count; i++ {
				var a, b float64
				for j, p := range e.props {
					v := r.next(p.typ)
					if j == c1 {
						a = v
					}
					if j == c2 {
						b = v
					}
				}
				if r.err != nil {
					return nil, r.err
				}
				if c1 >= 0 && c2 >= 0 {
					edges = append(edges, [2]uint32{uint32(a), uint32(b)})
				}
			}
		default: // an element this handler does not interpret: read past it
			for i := 0; i < e.count; i++ {
				for _, p := range e.props {
					if !p.list {
						r.next(p.typ)
						continue
					}
					n := int(r.next(p.countType))
					if n < 0 || n > r.remaining() {
						return nil, fmt.Errorf("element %q has an impossible list length %d", e.name, n)
					}
					for k := 0; k < n; k++ {
						r.next(p.typ)
					}
				}
				if r.err != nil {
					return nil, r.err
				}
			}
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(pos) == 0 {
		return &scene.Model{}, nil
	}

	base := scene.FlatPrim{MaterialIndex: -1, Positions: pos}
	if hasN {
		base.Normals = nrm
	}
	if hasUV {
		base.UVs = uvs
	}
	if hasC {
		base.Colors = cols
	}
	obj := &scene.Object{Name: "mesh"}
	add := func(kind scene.PrimKind, idx []uint32) {
		p := base
		p.Kind, p.Indices = kind, idx
		obj.Prims = append(obj.Prims, &p)
	}
	var tri, line, point []uint32
	for fi, f := range faces {
		for _, i := range f {
			if int(i) >= len(pos) {
				return nil, fmt.Errorf("face %d refers to vertex %d of %d", fi, i, len(pos))
			}
		}
		for k := 1; k+1 < len(f); k++ { // fan-triangulate polygons
			tri = append(tri, f[0], f[k], f[k+1])
		}
	}
	for _, e := range edges {
		if int(e[0]) >= len(pos) || int(e[1]) >= len(pos) {
			return nil, fmt.Errorf("edge refers to a vertex out of range")
		}
		line = append(line, e[0], e[1])
	}
	if len(faces) == 0 && len(edges) == 0 { // a point cloud
		for i := range pos {
			point = append(point, uint32(i))
		}
	}
	if len(tri) > 0 {
		add(scene.KindTriangles, tri)
	}
	if len(line) > 0 {
		add(scene.KindLines, line)
	}
	if len(point) > 0 {
		add(scene.KindPoints, point)
	}
	return &scene.Model{Roots: []*scene.Object{obj}}, nil
}

// encode writes every primitive in the scene as one little-endian binary PLY:
// a shared vertex list, triangle faces, line edges, and — when nothing else is
// drawn — the points. Normals, UVs and colours are written only when every
// primitive has them (a PLY vertex has the same properties throughout).
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	var prims []*scene.FlatPrim
	scene.Walk(roots, func(n *scene.FlatNode, _ int) { prims = append(prims, n.Prims...) })
	if len(prims) == 0 {
		return nil, fmt.Errorf("the scene has nothing to write")
	}
	all := func(has func(*scene.FlatPrim) bool) bool {
		for _, p := range prims {
			if !has(p) {
				return false
			}
		}
		return true
	}
	wantN := all(func(p *scene.FlatPrim) bool { return p.Normals != nil })
	wantUV := all(func(p *scene.FlatPrim) bool { return p.UVs != nil })
	wantC := all(func(p *scene.FlatPrim) bool { return p.Colors != nil })

	type vtx struct {
		p, n [3]float32
		uv   [2]float32
		c    [4]uint8
	}
	var vs []vtx
	var faces, edges [][]uint32
	var points int
	for _, p := range prims {
		base := uint32(len(vs))
		for i, v := range p.Positions {
			x := vtx{p: [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}}
			if wantN {
				n := p.Normals[i]
				x.n = [3]float32{float32(n[0]), float32(n[1]), float32(n[2])}
			}
			if wantUV {
				x.uv = [2]float32{float32(p.UVs[i][0]), float32(1 - p.UVs[i][1])}
			}
			if wantC {
				for k, c := range p.Colors[i] {
					x.c[k] = uint8(math.Round(math.Max(0, math.Min(1, c)) * 255))
				}
			}
			vs = append(vs, x)
		}
		switch p.Kind {
		case scene.KindTriangles:
			for i := 0; i+2 < len(p.Indices); i += 3 {
				faces = append(faces, []uint32{base + p.Indices[i], base + p.Indices[i+1], base + p.Indices[i+2]})
			}
		case scene.KindLines:
			for i := 0; i+1 < len(p.Indices); i += 2 {
				edges = append(edges, []uint32{base + p.Indices[i], base + p.Indices[i+1]})
			}
		case scene.KindPoints:
			points += len(p.Indices)
		}
	}

	var b bytes.Buffer
	b.WriteString("ply\nformat binary_little_endian 1.0\ncomment Exported by FHR ply handler\n")
	fmt.Fprintf(&b, "element vertex %d\nproperty float x\nproperty float y\nproperty float z\n", len(vs))
	if wantN {
		b.WriteString("property float nx\nproperty float ny\nproperty float nz\n")
	}
	if wantUV {
		b.WriteString("property float s\nproperty float t\n")
	}
	if wantC {
		b.WriteString("property uchar red\nproperty uchar green\nproperty uchar blue\nproperty uchar alpha\n")
	}
	if len(faces) > 0 {
		fmt.Fprintf(&b, "element face %d\nproperty list uchar int vertex_indices\n", len(faces))
	}
	if len(edges) > 0 {
		fmt.Fprintf(&b, "element edge %d\nproperty int vertex1\nproperty int vertex2\n", len(edges))
	}
	b.WriteString("end_header\n")
	le := binary.LittleEndian
	for _, v := range vs {
		_ = binary.Write(&b, le, v.p)
		if wantN {
			_ = binary.Write(&b, le, v.n)
		}
		if wantUV {
			_ = binary.Write(&b, le, v.uv)
		}
		if wantC {
			b.Write(v.c[:])
		}
	}
	for _, f := range faces {
		b.WriteByte(3)
		for _, i := range f {
			_ = binary.Write(&b, le, int32(i))
		}
	}
	for _, e := range edges {
		_ = binary.Write(&b, le, int32(e[0]))
		_ = binary.Write(&b, le, int32(e[1]))
	}
	return b.Bytes(), nil
}
