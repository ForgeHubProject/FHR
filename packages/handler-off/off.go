// Package main is the OFF (Object File Format) handler: the plain-text polygon
// format of the Princeton Shape Benchmark and Geomview, with its colour
// (COFF), normal (NOFF), texture (STOFF) and 4D (4OFF) variants. OFF has no
// named objects, so a file is one object called "mesh"; the semantic diff is
// over the glTF it becomes.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the OFF format.
var Codec = &scene.Codec{
	ID:      "off",
	Formats: []string{".off"},
	Decode:  decode,
	Encode:  encode,
}

// tokens yields whitespace-separated tokens, skipping '#' comments, so counts
// and records may break across lines as some exporters do.
type tokens struct {
	sc  *bufio.Scanner
	buf []string
	err error
}

func newTokens(b []byte) *tokens {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	return &tokens{sc: sc}
}

func (t *tokens) next() (string, bool) {
	for len(t.buf) == 0 {
		if !t.sc.Scan() {
			t.err = t.sc.Err()
			return "", false
		}
		line := t.sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		t.buf = strings.Fields(line)
	}
	tok := t.buf[0]
	t.buf = t.buf[1:]
	return tok, true
}

// restOfLine returns what is left of the current line without consuming the
// next, for the optional trailing colour of a face.
func (t *tokens) restOfLine() []string {
	r := t.buf
	t.buf = nil
	return r
}

func (t *tokens) float() (float64, error) {
	s, ok := t.next()
	if !ok {
		return 0, fmt.Errorf("OFF file ends early")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("bad number %q", s)
	}
	return v, nil
}

func (t *tokens) count(what string) (int, error) {
	v, err := t.float()
	if err != nil || v < 0 || v != math.Trunc(v) {
		return 0, fmt.Errorf("bad %s count", what)
	}
	return int(v), nil
}

func decode(blob []byte) (*scene.Model, error) {
	t := newTokens(blob)
	magic, ok := t.next()
	if !ok || !strings.HasSuffix(magic, "OFF") {
		return nil, fmt.Errorf("not an OFF file: missing OFF header")
	}
	prefix := strings.TrimSuffix(magic, "OFF")
	var hasC, hasN, hasST bool
	dim := 3
	for _, c := range prefix {
		switch c {
		case 'C':
			hasC = true
		case 'N':
			hasN = true
		case 'S', 'T':
			hasST = true
		case '4':
			dim = 4
		case 'n':
			// "nOFF": the dimension follows on the next token.
		default:
			return nil, fmt.Errorf("unknown OFF variant %q", magic)
		}
	}
	if strings.Contains(prefix, "n") {
		d, err := t.count("dimension")
		if err != nil || d < 3 {
			return nil, fmt.Errorf("bad OFF dimension")
		}
		dim = d
	}
	nv, err := t.count("vertex")
	if err != nil {
		return nil, err
	}
	nf, err := t.count("face")
	if err != nil {
		return nil, err
	}
	if _, err := t.count("edge"); err != nil {
		return nil, err
	}
	// Every vertex and face costs at least a token's bytes, so a count the file
	// cannot hold is rejected before anything is allocated for it.
	if nv > len(blob) || nf > len(blob) {
		return nil, fmt.Errorf("OFF counts (%d vertices, %d faces) exceed the file's size", nv, nf)
	}

	p := &scene.FlatPrim{MaterialIndex: -1, Kind: scene.KindTriangles}
	var normals [][3]float64
	var uvs [][2]float64
	var colors [][4]float64
	for i := 0; i < nv; i++ {
		var xyz [3]float64
		for k := 0; k < dim; k++ {
			v, err := t.float()
			if err != nil {
				return nil, fmt.Errorf("vertex %d: %w", i, err)
			}
			if k < 3 { // 4OFF's homogeneous coordinate (and any higher dimension) is dropped
				xyz[k] = v
			}
		}
		p.Positions = append(p.Positions, xyz)
		if hasN {
			var n [3]float64
			for k := range n {
				if n[k], err = t.float(); err != nil {
					return nil, fmt.Errorf("vertex %d normal: %w", i, err)
				}
			}
			normals = append(normals, n)
		}
		if hasC {
			rest := t.restOfLine()
			c, err := parseColor(rest)
			if err != nil {
				return nil, fmt.Errorf("vertex %d colour: %w", i, err)
			}
			colors = append(colors, c)
		}
		if hasST {
			var uv [2]float64
			for k := range uv {
				if uv[k], err = t.float(); err != nil {
					return nil, fmt.Errorf("vertex %d texture coordinate: %w", i, err)
				}
			}
			uvs = append(uvs, [2]float64{uv[0], 1 - uv[1]})
		}
	}
	if hasN {
		p.Normals = normals
	}
	if hasC {
		p.Colors = colors
	}
	if hasST {
		p.UVs = uvs
	}

	for i := 0; i < nf; i++ {
		n, err := t.count("face vertex")
		if err != nil {
			return nil, fmt.Errorf("face %d: %w", i, err)
		}
		if n > nv && nv > 0 && n > 1<<16 {
			return nil, fmt.Errorf("face %d claims %d vertices", i, n)
		}
		f := make([]uint32, 0, min(n, 64))
		for k := 0; k < n; k++ {
			v, err := t.count("vertex index")
			if err != nil {
				return nil, fmt.Errorf("face %d: %w", i, err)
			}
			if v >= nv {
				return nil, fmt.Errorf("face %d refers to vertex %d of %d", i, v, nv)
			}
			f = append(f, uint32(v))
		}
		t.restOfLine()                  // an optional face colour: not carried
		for k := 1; k+1 < len(f); k++ { // fan-triangulate polygons
			p.Indices = append(p.Indices, f[0], f[k], f[k+1])
		}
	}
	if t.err != nil {
		return nil, t.err
	}
	if nv == 0 {
		return &scene.Model{}, nil
	}
	if len(p.Indices) == 0 { // vertices only: a point cloud
		p.Kind = scene.KindPoints
		for i := range p.Positions {
			p.Indices = append(p.Indices, uint32(i))
		}
	}
	return &scene.Model{Roots: []*scene.Object{{Name: "mesh", Prims: []*scene.FlatPrim{p}}}}, nil
}

// parseColor reads a vertex's trailing colour: three or four components, 0..255
// integers or 0..1 floats (OFF writers use both), as RGBA in 0..1.
func parseColor(f []string) ([4]float64, error) {
	if len(f) != 3 && len(f) != 4 {
		return [4]float64{}, fmt.Errorf("want 3 or 4 components, got %d", len(f))
	}
	var v [4]float64
	v[3] = 1
	isInt := true
	for i, s := range f {
		x, err := strconv.ParseFloat(s, 64)
		if err != nil || x < 0 {
			return [4]float64{}, fmt.Errorf("bad component %q", s)
		}
		v[i] = x
		if strings.ContainsAny(s, ".eE") {
			isInt = false
		}
	}
	if isInt {
		for i := range f {
			v[i] /= 255
		}
	}
	for i := range v {
		v[i] = math.Min(1, v[i])
	}
	return v, nil
}

func num(v float64) string {
	f := float32(v)
	if f == 0 {
		f = 0 // not "-0"
	}
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

// encode writes every triangle and point in the scene as one OFF file — COFF
// when every primitive carries vertex colours. Lines are dropped (OFF has
// none); normals and UVs are not written (the plain/colour variants are the
// ones every reader understands).
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	var prims []*scene.FlatPrim
	scene.Walk(roots, func(n *scene.FlatNode, _ int) { prims = append(prims, n.Prims...) })
	wantC := len(prims) > 0
	for _, p := range prims {
		wantC = wantC && p.Colors != nil
	}
	var verts, faces strings.Builder
	nv, nf := 0, 0
	for _, p := range prims {
		if p.Kind == scene.KindLines {
			continue
		}
		base := nv
		for i, v := range p.Positions {
			verts.WriteString(num(v[0]) + " " + num(v[1]) + " " + num(v[2]))
			if wantC {
				for _, c := range p.Colors[i] {
					fmt.Fprintf(&verts, " %d", int(math.Round(math.Max(0, math.Min(1, c))*255)))
				}
			}
			verts.WriteByte('\n')
			nv++
		}
		if p.Kind == scene.KindTriangles {
			for i := 0; i+2 < len(p.Indices); i += 3 {
				fmt.Fprintf(&faces, "3 %d %d %d\n", base+int(p.Indices[i]), base+int(p.Indices[i+1]), base+int(p.Indices[i+2]))
				nf++
			}
		}
	}
	if nv == 0 {
		return nil, fmt.Errorf("the scene has nothing to write")
	}
	magic := "OFF"
	if wantC {
		magic = "COFF"
	}
	return []byte(fmt.Sprintf("%s\n# Exported by FHR off handler\n%d %d 0\n%s%s", magic, nv, nf, verts.String(), faces.String())), nil
}
