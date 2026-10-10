package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// fnode is one FBX node: a name, a property list, and children — the common
// shape of the binary and ASCII encodings. Property values are int64 (Y C I L),
// float64 (F D), string (S R and ASCII words), []float64 (f d arrays) or
// []int64 (l i b arrays).
type fnode struct {
	name  string
	props []any
	kids  []*fnode
}

func (n *fnode) child(name string) *fnode {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *fnode) children(name string) []*fnode {
	var out []*fnode
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

const (
	maxNodes    = 4_000_000
	maxDepth    = 64
	maxArray    = 256 << 20 // bytes an array may inflate to
	binaryMagic = "Kaydara FBX Binary  \x00\x1a\x00"
)

func parseFBX(blob []byte) (root *fnode, version uint32, err error) {
	if bytes.HasPrefix(blob, []byte(binaryMagic)) {
		return parseBinary(blob)
	}
	if bytes.Contains(blob[:min(len(blob), 2048)], []byte("FBXHeaderExtension")) || bytes.HasPrefix(bytes.TrimSpace(blob), []byte("; FBX")) {
		root, err = parseASCII(blob)
		return root, 0, err
	}
	return nil, 0, fmt.Errorf("not an FBX file")
}

type binReader struct {
	b     []byte
	pos   int
	wide  bool // version >= 7500: 64-bit node headers
	nodes int
}

func parseBinary(blob []byte) (*fnode, uint32, error) {
	if len(blob) < len(binaryMagic)+4 {
		return nil, 0, fmt.Errorf("truncated FBX header")
	}
	version := binary.LittleEndian.Uint32(blob[len(binaryMagic):])
	r := &binReader{b: blob, pos: len(binaryMagic) + 4, wide: version >= 7500}
	root := &fnode{}
	for {
		n, err := r.node(0)
		if err != nil {
			return nil, 0, err
		}
		if n == nil { // the null record that ends the top level
			break
		}
		root.kids = append(root.kids, n)
	}
	return root, version, nil
}

func (r *binReader) u(n int) (uint64, error) {
	if r.pos+n > len(r.b) {
		return 0, fmt.Errorf("FBX data ends early")
	}
	var v uint64
	if n == 8 {
		v = binary.LittleEndian.Uint64(r.b[r.pos:])
	} else {
		v = uint64(binary.LittleEndian.Uint32(r.b[r.pos:]))
	}
	r.pos += n
	return v, nil
}

// node reads one record; nil, nil is the null record that closes a scope.
func (r *binReader) node(depth int) (*fnode, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("FBX nodes nest deeper than %d", maxDepth)
	}
	w := 4
	if r.wide {
		w = 8
	}
	start := r.pos
	end, err := r.u(w)
	if err != nil {
		return nil, err
	}
	nprops, err := r.u(w)
	if err != nil {
		return nil, err
	}
	plen, err := r.u(w)
	if err != nil {
		return nil, err
	}
	if r.pos >= len(r.b) {
		return nil, fmt.Errorf("FBX data ends early")
	}
	nameLen := int(r.b[r.pos])
	r.pos++
	if end == 0 && nprops == 0 && plen == 0 && nameLen == 0 {
		return nil, nil
	}
	r.nodes++
	if r.nodes > maxNodes {
		return nil, fmt.Errorf("FBX has more than %d nodes", maxNodes)
	}
	if r.pos+nameLen > len(r.b) {
		return nil, fmt.Errorf("FBX data ends early")
	}
	n := &fnode{name: string(r.b[r.pos : r.pos+nameLen])}
	r.pos += nameLen
	if end > uint64(len(r.b)) || end < uint64(r.pos) {
		return nil, fmt.Errorf("node %q has a bad end offset %d", n.name, end)
	}
	propsEnd := uint64(r.pos) + plen
	if plen > uint64(len(r.b)) || propsEnd > end {
		return nil, fmt.Errorf("node %q has a bad property length", n.name)
	}
	if nprops > plen { // every property is at least one byte (its type)
		return nil, fmt.Errorf("node %q claims %d properties in %d bytes", n.name, nprops, plen)
	}
	for i := uint64(0); i < nprops; i++ {
		v, err := r.prop(propsEnd)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", n.name, err)
		}
		n.props = append(n.props, v)
	}
	if uint64(r.pos) != propsEnd {
		return nil, fmt.Errorf("node %q: property list length mismatch", n.name)
	}
	_ = start
	for uint64(r.pos) < end {
		k, err := r.node(depth + 1)
		if err != nil {
			return nil, err
		}
		if k == nil { // the scope's closing null record
			break
		}
		n.kids = append(n.kids, k)
	}
	if uint64(r.pos) != end {
		return nil, fmt.Errorf("node %q ends at %d, header says %d", n.name, r.pos, end)
	}
	return n, nil
}

func (r *binReader) take(n uint64, limit uint64) ([]byte, error) {
	if n > limit || uint64(r.pos)+n > uint64(len(r.b)) {
		return nil, fmt.Errorf("FBX property runs past its node")
	}
	b := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

func (r *binReader) prop(limit uint64) (any, error) {
	if uint64(r.pos) >= limit {
		return nil, fmt.Errorf("FBX property list ends early")
	}
	t := r.b[r.pos]
	r.pos++
	room := limit - uint64(r.pos)
	switch t {
	case 'Y':
		b, err := r.take(2, room)
		if err != nil {
			return nil, err
		}
		return int64(int16(binary.LittleEndian.Uint16(b))), nil
	case 'C':
		b, err := r.take(1, room)
		if err != nil {
			return nil, err
		}
		return int64(b[0] & 1), nil
	case 'I':
		b, err := r.take(4, room)
		if err != nil {
			return nil, err
		}
		return int64(int32(binary.LittleEndian.Uint32(b))), nil
	case 'F':
		b, err := r.take(4, room)
		if err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), nil
	case 'D':
		b, err := r.take(8, room)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(b)), nil
	case 'L':
		b, err := r.take(8, room)
		if err != nil {
			return nil, err
		}
		return int64(binary.LittleEndian.Uint64(b)), nil
	case 'S', 'R':
		lb, err := r.take(4, room)
		if err != nil {
			return nil, err
		}
		b, err := r.take(uint64(binary.LittleEndian.Uint32(lb)), room-4)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case 'f', 'd', 'l', 'i', 'b':
		hb, err := r.take(12, room)
		if err != nil {
			return nil, err
		}
		count := uint64(binary.LittleEndian.Uint32(hb[0:]))
		enc := binary.LittleEndian.Uint32(hb[4:])
		clen := uint64(binary.LittleEndian.Uint32(hb[8:]))
		size := map[byte]uint64{'f': 4, 'd': 8, 'l': 8, 'i': 4, 'b': 1}[t]
		if count*size > maxArray {
			return nil, fmt.Errorf("FBX array of %d elements is too large", count)
		}
		var raw []byte
		switch enc {
		case 0:
			if clen != count*size {
				return nil, fmt.Errorf("FBX array length mismatch")
			}
			if raw, err = r.take(clen, room-12); err != nil {
				return nil, err
			}
		case 1:
			z, err := r.take(clen, room-12)
			if err != nil {
				return nil, err
			}
			zr, err := zlib.NewReader(bytes.NewReader(z))
			if err != nil {
				return nil, fmt.Errorf("FBX array: %w", err)
			}
			raw, err = io.ReadAll(io.LimitReader(zr, int64(count*size)+1))
			if err != nil {
				return nil, fmt.Errorf("FBX array: %w", err)
			}
			if uint64(len(raw)) != count*size {
				return nil, fmt.Errorf("FBX array inflates to %d bytes, want %d", len(raw), count*size)
			}
		default:
			return nil, fmt.Errorf("unknown FBX array encoding %d", enc)
		}
		switch t {
		case 'f':
			out := make([]float64, count)
			for i := range out {
				out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
			}
			return out, nil
		case 'd':
			out := make([]float64, count)
			for i := range out {
				out[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[i*8:]))
			}
			return out, nil
		case 'l':
			out := make([]int64, count)
			for i := range out {
				out[i] = int64(binary.LittleEndian.Uint64(raw[i*8:]))
			}
			return out, nil
		case 'i':
			out := make([]int64, count)
			for i := range out {
				out[i] = int64(int32(binary.LittleEndian.Uint32(raw[i*4:])))
			}
			return out, nil
		default:
			out := make([]int64, count)
			for i := range out {
				out[i] = int64(raw[i] & 1)
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("unknown FBX property type %q", t)
}

// ── ASCII ─────────────────────────────────────────────────────────────────────

func parseASCII(blob []byte) (*fnode, error) {
	lines := strings.Split(string(blob), "\n")
	p := &asciiParser{lines: lines}
	root := &fnode{}
	if err := p.block(root, 0); err != nil {
		return nil, err
	}
	return root, nil
}

type asciiParser struct {
	lines []string
	i     int
	nodes int
}

// stripComment removes a ';' comment that is not inside a string.
func stripComment(s string) string {
	in := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			in = !in
		case ';':
			if !in {
				return s[:i]
			}
		}
	}
	return s
}

// block reads statements into parent until its closing '}' (or EOF at top level).
func (p *asciiParser) block(parent *fnode, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("FBX nodes nest deeper than %d", maxDepth)
	}
	for p.i < len(p.lines) {
		line := strings.TrimSpace(stripComment(p.lines[p.i]))
		p.i++
		switch {
		case line == "":
			continue
		case line == "}":
			if depth == 0 {
				return fmt.Errorf("unbalanced '}' in FBX text")
			}
			return nil
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return fmt.Errorf("FBX text line %d: expected \"Name:\"", p.i)
		}
		p.nodes++
		if p.nodes > maxNodes {
			return fmt.Errorf("FBX has more than %d nodes", maxNodes)
		}
		n := &fnode{name: strings.TrimSpace(line[:colon])}
		rest := strings.TrimSpace(line[colon+1:])
		opens := strings.HasSuffix(rest, "{")
		if opens {
			rest = strings.TrimSpace(strings.TrimSuffix(rest, "{"))
		}
		if strings.HasPrefix(rest, "*") && opens { // an array: *N { a: v,v,v }
			vals, err := p.arrayBody()
			if err != nil {
				return err
			}
			n.props = []any{vals}
			parent.kids = append(parent.kids, n)
			continue
		}
		props, err := splitProps(rest)
		if err != nil {
			return fmt.Errorf("FBX text line %d: %w", p.i, err)
		}
		n.props = props
		if opens {
			if err := p.block(n, depth+1); err != nil {
				return err
			}
		}
		parent.kids = append(parent.kids, n)
	}
	if depth != 0 {
		return fmt.Errorf("FBX text ends inside a block")
	}
	return nil
}

// arrayBody reads "a: 1,2,3 ... }" and returns the values: []float64 when any
// has a fraction or exponent, otherwise []int64.
func (p *asciiParser) arrayBody() (any, error) {
	var sb strings.Builder
	started := false
	for p.i < len(p.lines) {
		line := strings.TrimSpace(stripComment(p.lines[p.i]))
		p.i++
		if line == "}" {
			break
		}
		if !started {
			if !strings.HasPrefix(line, "a:") {
				return nil, fmt.Errorf("FBX text line %d: expected \"a:\" in an array", p.i)
			}
			line = strings.TrimSpace(line[2:])
			started = true
		}
		sb.WriteString(line)
		sb.WriteByte(',')
		if sb.Len() > maxArray {
			return nil, fmt.Errorf("FBX text array is too large")
		}
	}
	fields := strings.FieldsFunc(sb.String(), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' })
	isFloat := false
	for _, f := range fields {
		if strings.ContainsAny(f, ".eE") {
			isFloat = true
			break
		}
	}
	if isFloat {
		out := make([]float64, len(fields))
		for i, f := range fields {
			v, err := strconv.ParseFloat(f, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("bad number %q in an FBX array", f)
			}
			out[i] = v
		}
		return out, nil
	}
	out := make([]int64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad integer %q in an FBX array", f)
		}
		out[i] = v
	}
	return out, nil
}

// splitProps splits "a, \"b, c\", 1.5, T" into typed values.
func splitProps(s string) ([]any, error) {
	var out []any
	for i := 0; i < len(s); {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '"' {
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string")
			}
			out = append(out, s[i+1:j])
			i = j + 1
			continue
		}
		j := i
		for j < len(s) && s[j] != ',' {
			j++
		}
		tok := strings.TrimSpace(s[i:j])
		i = j
		if v, err := strconv.ParseInt(tok, 10, 64); err == nil {
			out = append(out, v)
		} else if v, err := strconv.ParseFloat(tok, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			out = append(out, v)
		} else {
			out = append(out, tok) // T, Y, C, bare words
		}
	}
	return out, nil
}
