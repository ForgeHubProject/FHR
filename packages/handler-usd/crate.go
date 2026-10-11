package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// The binary USD "crate" (.usdc): a header, a table of contents, and sections —
// TOKENS, STRINGS, FIELDS, FIELDSETS, PATHS, SPECS — from which a layer is
// rebuilt. Values live in the file body and are decoded lazily, only for the
// fields this handler reads. Arrays and the index sections are compressed
// (LZ4 blocks and USD's delta-coded integer scheme); every length is checked
// against the file before anything is allocated for it.

const (
	crateHeader   = 88
	maxCrateItems = 8 << 20
	maxInflate    = 256 << 20
)

type crate struct {
	b       []byte
	major   byte
	minor   byte
	tokens  []string
	strings []uint32 // string index → token index
	fields  []crateField
	sets    []uint32 // the field sets, flat: each is a run of field indices ended by 0xffffffff
	paths   []string
	specs   []crateSpec
}

type crateField struct {
	token uint32
	rep   valueRep
}

type crateSpec struct {
	path int
	set  int
	typ  uint32
}

const (
	specAttribute    = 1
	specPrim         = 6
	specPseudoRoot   = 7
	specRelationship = 8
)

type valueRep uint64

func (r valueRep) isArray() bool      { return r>>63&1 == 1 }
func (r valueRep) isInlined() bool    { return r>>62&1 == 1 }
func (r valueRep) isCompressed() bool { return r>>61&1 == 1 }
func (r valueRep) typ() int           { return int(r >> 48 & 0xff) }
func (r valueRep) payload() uint64    { return uint64(r) & (1<<48 - 1) }

// crate value types (the TypeEnum of the file format).
const (
	ctBool = 1 + iota
	ctUChar
	ctInt
	ctUInt
	ctInt64
	ctUInt64
	ctHalf
	ctFloat
	ctDouble
	ctString
	ctToken
	ctAssetPath
	ctMatrix2d
	ctMatrix3d
	ctMatrix4d
	ctQuatd
	ctQuatf
	ctQuath
	ctVec2d
	ctVec2f
	ctVec2h
	ctVec2i
	ctVec3d
	ctVec3f
	ctVec3h
	ctVec3i
	ctVec4d
	ctVec4f
	ctVec4h
	ctVec4i
	ctDictionary
	ctTokenListOp
	ctStringListOp
	ctPathListOp
	ctReferenceListOp
	ctIntListOp
	ctInt64ListOp
	ctUIntListOp
	ctUInt64ListOp
	ctPathVector
	ctTokenVector
	ctSpecifier
	ctPermission
	ctVariability
	ctVariantSelectionMap
	ctTimeSamples
	ctPayload
	ctDoubleVector
	ctLayerOffsetVector
	ctStringVector
	ctValueBlock
)

// ── low-level readers ─────────────────────────────────────────────────────────

type cursor struct {
	b   []byte
	pos int
	err error
}

func (c *cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || c.pos < 0 || c.pos+n > len(c.b) {
		c.err = fmt.Errorf("USD crate data ends early")
		return nil
	}
	s := c.b[c.pos : c.pos+n]
	c.pos += n
	return s
}

func (c *cursor) u64() uint64 {
	b := c.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func (c *cursor) u32() uint32 {
	b := c.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

// count reads a uint64 element count and bounds it.
func (c *cursor) count(what string) int {
	n := c.u64()
	if c.err == nil && n > maxCrateItems {
		c.err = fmt.Errorf("USD crate %s count %d is too large", what, n)
		return 0
	}
	return int(n)
}

// lz4Block decompresses one LZ4 block into exactly want bytes.
func lz4Block(src []byte, want int) ([]byte, error) {
	if want < 0 || want > maxInflate {
		return nil, fmt.Errorf("LZ4 output of %d bytes is too large", want)
	}
	out := make([]byte, 0, want)
	i := 0
	for i < len(src) {
		tok := src[i]
		i++
		lit := int(tok >> 4)
		if lit == 15 {
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("corrupt LZ4 block")
				}
				b := src[i]
				i++
				lit += int(b)
				if b != 255 {
					break
				}
			}
		}
		if i+lit > len(src) || len(out)+lit > want {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		out = append(out, src[i:i+lit]...)
		i += lit
		if i >= len(src) { // the last sequence has only literals
			break
		}
		if i+2 > len(src) {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		if off == 0 || off > len(out) {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		ml := int(tok & 15)
		if ml == 15 {
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("corrupt LZ4 block")
				}
				b := src[i]
				i++
				ml += int(b)
				if b != 255 {
					break
				}
			}
		}
		ml += 4
		if len(out)+ml > want {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		for k := 0; k < ml; k++ { // byte by byte: matches may overlap their own output
			out = append(out, out[len(out)-off])
		}
	}
	if len(out) != want {
		return nil, fmt.Errorf("LZ4 block inflates to %d bytes, want %d", len(out), want)
	}
	return out, nil
}

// fastDecompress is USD's TfFastCompression: a chunk count byte (0 for one
// block), then the block — or, for large buffers, (size, block) pairs.
func fastDecompress(src []byte, want int) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("empty compressed buffer")
	}
	chunks := int(src[0])
	if chunks == 0 {
		return lz4Block(src[1:], want)
	}
	out := make([]byte, 0, min(want, maxInflate))
	p := 1
	for i := 0; i < chunks; i++ {
		if p+4 > len(src) {
			return nil, fmt.Errorf("corrupt chunked buffer")
		}
		n := int(binary.LittleEndian.Uint32(src[p:]))
		p += 4
		if n < 0 || p+n > len(src) {
			return nil, fmt.Errorf("corrupt chunked buffer")
		}
		// Every chunk but the last inflates to 127 MB less a header; the last
		// takes the remainder.
		left := want - len(out)
		const chunkMax = 127 * 1000 * 1000
		w := min(left, chunkMax)
		b, err := lz4Block(src[p:p+n], w)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
		p += n
	}
	if len(out) != want {
		return nil, fmt.Errorf("chunked buffer inflates to %d bytes, want %d", len(out), want)
	}
	return out, nil
}

// readInts decodes USD's compressed integer array: compressedSize, then a
// fast-compressed buffer of [common delta][2-bit codes][small deltas].
func (c *cursor) readInts(n int, size int, signed bool) []int64 {
	if c.err != nil || n == 0 {
		return nil
	}
	csize := c.count("compressed size")
	raw := c.take(csize)
	if c.err != nil {
		return nil
	}
	codesLen := (n*2 + 7) / 8
	encoded := size + codesLen + n*size // an upper bound on the encoded size
	buf, err := fastDecompressAtMost(raw, encoded)
	if err != nil {
		c.err = err
		return nil
	}
	if len(buf) < size+codesLen {
		c.err = fmt.Errorf("compressed integers are truncated")
		return nil
	}
	read := func(p, w int) (int64, bool) {
		if p+w > len(buf) {
			return 0, false
		}
		switch w {
		case 1:
			return int64(int8(buf[p])), true
		case 2:
			return int64(int16(binary.LittleEndian.Uint16(buf[p:]))), true
		case 4:
			return int64(int32(binary.LittleEndian.Uint32(buf[p:]))), true
		}
		return int64(binary.LittleEndian.Uint64(buf[p:])), true
	}
	common, _ := read(0, size)
	codes := buf[size : size+codesLen]
	p := size + codesLen
	out := make([]int64, n)
	var prev int64
	for i := 0; i < n; i++ {
		var delta int64
		var ok bool
		switch (codes[i/4] >> (uint(i%4) * 2)) & 3 {
		case 0:
			delta = common
			ok = true
		case 1:
			delta, ok = read(p, 1)
			p++
		case 2:
			delta, ok = read(p, 2)
			p += 2
		default:
			delta, ok = read(p, size)
			p += size
		}
		if !ok {
			c.err = fmt.Errorf("compressed integers are truncated")
			return nil
		}
		prev += delta
		out[i] = prev
	}
	return out
}

// fastDecompressAtMost inflates a fast-compressed buffer whose exact size the
// caller does not know (only a bound): the true size is whatever the LZ4 block
// holds, so it tries sizes by decoding with a generous cap.
func fastDecompressAtMost(src []byte, maxSize int) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("empty compressed buffer")
	}
	if src[0] != 0 {
		return nil, fmt.Errorf("chunked compressed integers are not supported")
	}
	return lz4Unbounded(src[1:], maxSize)
}

// lz4Unbounded decodes an LZ4 block of unknown output size, up to max bytes.
func lz4Unbounded(src []byte, max int) ([]byte, error) {
	if max > maxInflate {
		return nil, fmt.Errorf("LZ4 output is too large")
	}
	out := make([]byte, 0, min(max, 1<<20))
	i := 0
	for i < len(src) {
		tok := src[i]
		i++
		lit := int(tok >> 4)
		if lit == 15 {
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("corrupt LZ4 block")
				}
				b := src[i]
				i++
				lit += int(b)
				if b != 255 {
					break
				}
			}
		}
		if i+lit > len(src) || len(out)+lit > max {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		out = append(out, src[i:i+lit]...)
		i += lit
		if i >= len(src) {
			break
		}
		if i+2 > len(src) {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		if off == 0 || off > len(out) {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		ml := int(tok & 15)
		if ml == 15 {
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("corrupt LZ4 block")
				}
				b := src[i]
				i++
				ml += int(b)
				if b != 255 {
					break
				}
			}
		}
		ml += 4
		if len(out)+ml > max {
			return nil, fmt.Errorf("corrupt LZ4 block")
		}
		for k := 0; k < ml; k++ {
			out = append(out, out[len(out)-off])
		}
	}
	return out, nil
}

// ── file structure ────────────────────────────────────────────────────────────

func parseCrate(blob []byte) (*layer, error) {
	c, err := readCrate(blob)
	if err != nil {
		return nil, err
	}
	return c.layer()
}

func readCrate(blob []byte) (*crate, error) {
	if len(blob) < crateHeader {
		return nil, fmt.Errorf("truncated USD crate header")
	}
	cr := &crate{b: blob, major: blob[8], minor: blob[9]}
	if cr.major != 0 || cr.minor < 4 {
		return nil, fmt.Errorf("USD crate version %d.%d is not supported (0.4 or later)", cr.major, cr.minor)
	}
	toc := binary.LittleEndian.Uint64(blob[16:])
	if toc < crateHeader || toc+8 > uint64(len(blob)) {
		return nil, fmt.Errorf("USD crate table of contents is out of range")
	}
	c := &cursor{b: blob, pos: int(toc)}
	nsec := c.count("section")
	type section struct {
		name        string
		start, size uint64
	}
	secs := map[string]section{}
	for i := 0; i < nsec && c.err == nil; i++ {
		name := string(c.take(16))
		name = strings.TrimRight(name, "\x00")
		start, size := c.u64(), c.u64()
		secs[name] = section{name, start, size}
	}
	if c.err != nil {
		return nil, c.err
	}
	sec := func(name string) (*cursor, error) {
		s, ok := secs[name]
		if !ok {
			return nil, fmt.Errorf("USD crate has no %s section", name)
		}
		if s.start > uint64(len(blob)) || s.size > uint64(len(blob)) || s.start+s.size > uint64(len(blob)) {
			return nil, fmt.Errorf("USD crate section %s is out of range", name)
		}
		return &cursor{b: blob[:s.start+s.size], pos: int(s.start)}, nil
	}

	// TOKENS: a count, the sizes, and one LZ4 block of NUL-terminated strings.
	tc, err := sec("TOKENS")
	if err != nil {
		return nil, err
	}
	nTok := tc.count("token")
	usize := int(tc.u64())
	csize := int(tc.u64())
	if tc.err == nil && (usize < 0 || usize > maxInflate) {
		return nil, fmt.Errorf("USD crate token data is too large")
	}
	raw := tc.take(csize)
	if tc.err != nil {
		return nil, tc.err
	}
	data, err := fastDecompress(raw, usize)
	if err != nil {
		return nil, fmt.Errorf("USD crate tokens: %w", err)
	}
	for start := 0; len(cr.tokens) < nTok; {
		end := start
		for end < len(data) && data[end] != 0 {
			end++
		}
		if end >= len(data) && len(cr.tokens) < nTok-0 && end == start && start >= len(data) {
			return nil, fmt.Errorf("USD crate has fewer tokens than it declares")
		}
		cr.tokens = append(cr.tokens, string(data[start:end]))
		start = end + 1
	}

	// STRINGS: a count of token indices.
	if sc, err := sec("STRINGS"); err == nil {
		n := sc.count("string")
		for i := 0; i < n && sc.err == nil; i++ {
			cr.strings = append(cr.strings, sc.u32())
		}
		if sc.err != nil {
			return nil, sc.err
		}
	}

	// FIELDS: compressed token indices, then compressed value reps.
	fc, err := sec("FIELDS")
	if err != nil {
		return nil, err
	}
	nf := fc.count("field")
	toks := fc.readInts(nf, 4, false)
	repSize := fc.count("field value")
	repRaw := fc.take(repSize)
	if fc.err != nil {
		return nil, fc.err
	}
	reps, err := fastDecompress(repRaw, nf*8)
	if err != nil {
		return nil, fmt.Errorf("USD crate fields: %w", err)
	}
	if len(toks) != nf {
		return nil, fmt.Errorf("USD crate fields are inconsistent")
	}
	for i := 0; i < nf; i++ {
		cr.fields = append(cr.fields, crateField{uint32(toks[i]), valueRep(binary.LittleEndian.Uint64(reps[i*8:]))})
	}

	// FIELDSETS: runs of field indices ended by -1 (0xffffffff).
	sc, err := sec("FIELDSETS")
	if err != nil {
		return nil, err
	}
	nfs := sc.count("field set")
	flat := sc.readInts(nfs, 4, false)
	if sc.err != nil {
		return nil, sc.err
	}
	for _, v := range flat {
		cr.sets = append(cr.sets, uint32(v))
	}

	// PATHS: the tree, flattened with jump offsets.
	pc, err := sec("PATHS")
	if err != nil {
		return nil, err
	}
	nPaths := pc.count("path")
	nEnc := pc.count("encoded path")
	pathIdx := pc.readInts(nEnc, 4, false)
	elemIdx := pc.readInts(nEnc, 4, true)
	jumps := pc.readInts(nEnc, 4, true)
	if pc.err != nil {
		return nil, pc.err
	}
	cr.paths = make([]string, nPaths)
	if err := cr.buildPaths(pathIdx, elemIdx, jumps); err != nil {
		return nil, err
	}

	// SPECS.
	spc, err := sec("SPECS")
	if err != nil {
		return nil, err
	}
	ns := spc.count("spec")
	pi := spc.readInts(ns, 4, false)
	fsi := spc.readInts(ns, 4, false)
	st := spc.readInts(ns, 4, false)
	if spc.err != nil {
		return nil, spc.err
	}
	for i := 0; i < ns; i++ {
		cr.specs = append(cr.specs, crateSpec{int(pi[i]), int(fsi[i]), uint32(st[i])})
	}
	return cr, nil
}

// buildPaths rebuilds path strings from the flattened tree. Entries are in
// depth-first order: a node's first child is the entry right after it. Its jump
// says what else it has: −1 children and no sibling, 0 a sibling (the next
// entry) and no children, a positive jump both (the sibling that many entries
// on), and anything else neither.
func (cr *crate) buildPaths(pathIdx, elemIdx, jumps []int64) error {
	type frame struct {
		at     int
		parent string
	}
	stack := []frame{{0, ""}}
	steps := 0
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		cur, parent := f.at, f.parent
		for {
			steps++
			if cur < 0 || cur >= len(pathIdx) || steps > len(pathIdx)+1 {
				return fmt.Errorf("USD crate path table is corrupt")
			}
			idx := int(pathIdx[cur])
			if idx < 0 || idx >= len(cr.paths) {
				return fmt.Errorf("USD crate path index %d is out of range", idx)
			}
			var p string
			if parent == "" {
				p = "/"
			} else {
				t := int(elemIdx[cur])
				prop := t < 0
				if prop {
					t = -t
				}
				if t < 0 || t >= len(cr.tokens) {
					return fmt.Errorf("USD crate path token %d is out of range", t)
				}
				name := cr.tokens[t]
				switch {
				case prop:
					p = parent + "." + name
				case parent == "/":
					p = "/" + name
				default:
					p = parent + "/" + name
				}
			}
			cr.paths[idx] = p
			j := jumps[cur]
			hasChild := j > 0 || j == -1
			hasSibling := j >= 0
			if hasChild && hasSibling {
				stack = append(stack, frame{cur + int(j), parent}) // the sibling, later
			}
			if hasChild {
				parent = p
			}
			if !hasChild && !hasSibling {
				break
			}
			cur++ // the entry after this one: its first child, or (no child) its next sibling
		}
	}
	return nil
}

// ── values ────────────────────────────────────────────────────────────────────

func (cr *crate) token(i uint32) string {
	if int(i) < len(cr.tokens) {
		return cr.tokens[i]
	}
	return ""
}

func (cr *crate) str(i uint32) string {
	if int(i) < len(cr.strings) {
		return cr.token(cr.strings[i])
	}
	return ""
}

// unsupportedValue stands in for an opinion this handler cannot read (an
// animated attribute); interpretation refuses the prim that carries it.
type unsupportedValue struct{ what string }

var vecDim = map[int]int{
	ctVec2d: 2, ctVec2f: 2, ctVec2h: 2, ctVec2i: 2, ctVec3d: 3, ctVec3f: 3, ctVec3h: 3, ctVec3i: 3,
	ctVec4d: 4, ctVec4f: 4, ctVec4h: 4, ctVec4i: 4, ctQuatd: 4, ctQuatf: 4, ctQuath: 4,
}

func half(h uint16) float64 {
	sign := 1.0
	if h&0x8000 != 0 {
		sign = -1
	}
	e := int(h >> 10 & 0x1f)
	m := float64(h & 0x3ff)
	switch e {
	case 0:
		return sign * m / 1024 * math.Pow(2, -14)
	case 31:
		if m == 0 {
			return sign * math.Inf(1)
		}
		return math.NaN()
	}
	return sign * (1 + m/1024) * math.Pow(2, float64(e-15))
}

// scalarSize is the byte width of one component of a numeric type.
func scalarSize(t int) int {
	switch t {
	case ctBool, ctUChar:
		return 1
	case ctHalf, ctVec2h, ctVec3h, ctVec4h, ctQuath:
		return 2
	case ctInt, ctUInt, ctFloat, ctVec2f, ctVec3f, ctVec4f, ctVec2i, ctVec3i, ctVec4i, ctQuatf:
		return 4
	}
	return 8
}

// readNum reads one component of type t (the type or its vector's element).
func readNum(c *cursor, t int) float64 {
	switch t {
	case ctBool, ctUChar:
		b := c.take(1)
		if b == nil {
			return 0
		}
		return float64(b[0])
	case ctHalf, ctVec2h, ctVec3h, ctVec4h, ctQuath:
		b := c.take(2)
		if b == nil {
			return 0
		}
		return half(binary.LittleEndian.Uint16(b))
	case ctInt, ctVec2i, ctVec3i, ctVec4i:
		return float64(int32(c.u32()))
	case ctUInt:
		return float64(c.u32())
	case ctFloat, ctVec2f, ctVec3f, ctVec4f, ctQuatf:
		return float64(math.Float32frombits(c.u32()))
	case ctInt64:
		return float64(int64(c.u64()))
	case ctUInt64:
		return float64(c.u64())
	}
	return math.Float64frombits(c.u64())
}

// value decodes a ValueRep. Types this handler never needs decode to nil
// without reading their bytes.
func (cr *crate) value(r valueRep) (any, error) {
	t := r.typ()
	if r.isArray() {
		return cr.array(r)
	}
	if r.isInlined() {
		p := uint32(r.payload())
		switch t {
		case ctBool, ctUChar, ctInt, ctUInt, ctSpecifier, ctPermission, ctVariability:
			if t == ctInt {
				return float64(int32(p)), nil
			}
			return float64(p), nil
		case ctFloat:
			return float64(math.Float32frombits(p)), nil
		case ctDouble:
			return float64(math.Float32frombits(p)), nil // inlined doubles are stored as floats
		case ctToken:
			return token(cr.token(p)), nil
		case ctString:
			return str(cr.str(p)), nil
		case ctAssetPath:
			return asset(cr.token(p)), nil
		case ctMatrix4d: // inlined: the diagonal, as four int8s
			m := make([]any, 4)
			for i := 0; i < 4; i++ {
				row := make([]float64, 4)
				row[i] = float64(int8(p >> (8 * uint(i))))
				m[i] = &numArray{data: row, dim: 4}
			}
			return m, nil
		}
		if d, ok := vecDim[t]; ok { // small vectors, one int8 per component
			v := make([]float64, d)
			for i := range v {
				v[i] = float64(int8(p >> (8 * uint(i))))
			}
			return &numArray{data: v, dim: d}, nil
		}
		return nil, nil
	}
	c := &cursor{b: cr.b, pos: int(r.payload())}
	if r.payload() >= uint64(len(cr.b)) {
		return nil, fmt.Errorf("USD crate value offset is out of range")
	}
	switch t {
	case ctBool, ctUChar, ctInt, ctUInt, ctInt64, ctUInt64, ctHalf, ctFloat, ctDouble:
		v := readNum(c, t)
		return v, c.err
	case ctMatrix4d:
		m := make([]any, 4)
		for i := range m {
			row := make([]float64, 4)
			for j := range row {
				row[j] = readNum(c, ctDouble)
			}
			m[i] = &numArray{data: row, dim: 4}
		}
		return m, c.err
	case ctTokenVector:
		n := c.count("token")
		out := make([]any, 0, min(n, 1024))
		for i := 0; i < n && c.err == nil; i++ {
			out = append(out, token(cr.token(c.u32())))
		}
		return out, c.err
	case ctPathVector:
		n := c.count("path")
		out := make([]any, 0, min(n, 1024))
		for i := 0; i < n && c.err == nil; i++ {
			idx := c.u32()
			if int(idx) < len(cr.paths) {
				out = append(out, path(cr.paths[idx]))
			}
		}
		return out, c.err
	case ctPathListOp:
		return cr.pathListOp(c)
	case ctTimeSamples:
		return unsupportedValue{"timeSamples"}, nil
	}
	if d, ok := vecDim[t]; ok {
		v := make([]float64, d)
		for i := range v {
			v[i] = readNum(c, t)
		}
		return &numArray{data: v, dim: d}, c.err
	}
	return nil, nil
}

// pathListOp reads a PathListOp (a relationship's targets): a flags byte, then
// a counted list of path indices for each list that is present. The first
// non-empty of explicit, prepended, appended, added wins.
func (cr *crate) pathListOp(c *cursor) (any, error) {
	b := c.take(1)
	if b == nil {
		return nil, c.err
	}
	flags := b[0]
	var lists [][]any
	for bit := 1; bit <= 6; bit++ { // explicit, added, prepended, appended, deleted, ordered
		if flags&(1<<uint(bit)) == 0 {
			continue
		}
		n := c.count("path")
		var l []any
		for i := 0; i < n && c.err == nil; i++ {
			idx := c.u32()
			if int(idx) < len(cr.paths) {
				l = append(l, path(cr.paths[idx]))
			}
		}
		if bit <= 4 { // not the deleted/ordered lists
			lists = append(lists, l)
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	for _, l := range lists {
		if len(l) > 0 {
			return l, nil
		}
	}
	return nil, nil
}

// array decodes an array ValueRep: numbers and number vectors become a
// numArray; token arrays a []any.
func (cr *crate) array(r valueRep) (any, error) {
	t := r.typ()
	if r.payload() >= uint64(len(cr.b)) {
		return nil, fmt.Errorf("USD crate array offset is out of range")
	}
	if r.isInlined() { // an empty array
		return &numArray{dim: max(vecDim[t], 1)}, nil
	}
	c := &cursor{b: cr.b, pos: int(r.payload())}
	var n int
	if cr.minor >= 7 {
		n = c.count("array element")
	} else {
		n = int(c.u32())
	}
	if c.err != nil {
		return nil, c.err
	}
	if n == 0 {
		return &numArray{dim: max(vecDim[t], 1)}, nil
	}
	dim := max(vecDim[t], 1)
	if n*dim > maxCrateItems {
		return nil, fmt.Errorf("USD crate array of %d elements is too large", n)
	}
	switch t {
	case ctToken, ctString, ctAssetPath:
		out := make([]any, 0, min(n, 1024))
		for i := 0; i < n && c.err == nil; i++ {
			idx := c.u32()
			switch t {
			case ctToken:
				out = append(out, token(cr.token(idx)))
			case ctString:
				out = append(out, str(cr.str(idx)))
			default:
				out = append(out, asset(cr.token(idx)))
			}
		}
		return out, c.err
	case ctInt, ctUInt, ctInt64, ctUInt64:
		if r.isCompressed() && n >= 16 {
			w := 4
			if t == ctInt64 || t == ctUInt64 {
				w = 8
			}
			ints := c.readInts(n, w, t == ctInt || t == ctInt64)
			if c.err != nil {
				return nil, c.err
			}
			out := make([]float64, len(ints))
			for i, v := range ints {
				out[i] = float64(v)
			}
			return &numArray{data: out, dim: 1}, nil
		}
	case ctFloat, ctDouble:
		if r.isCompressed() && n >= 16 {
			return cr.compressedFloats(c, n, t)
		}
	}
	if _, ok := vecDim[t]; !ok {
		switch t {
		case ctBool, ctUChar, ctHalf, ctInt, ctUInt, ctInt64, ctUInt64, ctFloat, ctDouble:
		default:
			return nil, nil // an array type this handler never reads
		}
	}
	if int64(n)*int64(dim)*int64(scalarSize(t)) > int64(len(cr.b)) {
		return nil, fmt.Errorf("USD crate array is larger than the file")
	}
	data := make([]float64, n*dim)
	for i := range data {
		data[i] = readNum(c, t)
	}
	return &numArray{data: data, dim: dim}, c.err
}

// compressedFloats decodes the two encodings of a float array: 'i' — the values
// are integers, stored as compressed ints — and 't' — a table of distinct
// values plus compressed indices into it.
func (cr *crate) compressedFloats(c *cursor, n, t int) (any, error) {
	code := c.take(1)
	if code == nil {
		return nil, c.err
	}
	switch code[0] {
	case 'i':
		ints := c.readInts(n, 4, true)
		if c.err != nil {
			return nil, c.err
		}
		out := make([]float64, len(ints))
		for i, v := range ints {
			out[i] = float64(v)
		}
		return &numArray{data: out, dim: 1}, nil
	case 't':
		nl := int(c.u32())
		if nl < 0 || nl > maxCrateItems {
			return nil, fmt.Errorf("USD crate float table is too large")
		}
		table := make([]float64, nl)
		for i := range table {
			table[i] = readNum(c, t)
		}
		idx := c.readInts(n, 4, false)
		if c.err != nil {
			return nil, c.err
		}
		out := make([]float64, n)
		for i, v := range idx {
			if v < 0 || int(v) >= nl {
				return nil, fmt.Errorf("USD crate float table index %d is out of range", v)
			}
			out[i] = table[v]
		}
		return &numArray{data: out, dim: 1}, nil
	}
	return nil, fmt.Errorf("unknown compressed float encoding %q", code[0])
}

// ── layer ─────────────────────────────────────────────────────────────────────

// layer rebuilds the prim tree the text parser would have made.
func (cr *crate) layer() (*layer, error) {
	prims := map[string]*prim{}
	l := &layer{meta: map[string]any{}}
	fieldsOf := func(s crateSpec) ([]crateField, error) {
		if s.set < 0 || s.set >= len(cr.sets) {
			return nil, fmt.Errorf("USD crate field set %d is out of range", s.set)
		}
		var out []crateField
		for i := s.set; i < len(cr.sets) && cr.sets[i] != 0xffffffff; i++ { // a spec names where its set starts
			fi := cr.sets[i]
			if int(fi) >= len(cr.fields) {
				return nil, fmt.Errorf("USD crate field %d is out of range", fi)
			}
			out = append(out, cr.fields[fi])
		}
		return out, nil
	}
	pathOf := func(s crateSpec) (string, error) {
		if s.path < 0 || s.path >= len(cr.paths) {
			return "", fmt.Errorf("USD crate spec path %d is out of range", s.path)
		}
		return cr.paths[s.path], nil
	}

	// Pass 1: prims, so properties can find their owners.
	childOrder := map[string][]string{}
	for _, s := range cr.specs {
		p, err := pathOf(s)
		if err != nil {
			return nil, err
		}
		fields, err := fieldsOf(s)
		if err != nil {
			return nil, err
		}
		switch s.typ {
		case specPseudoRoot:
			for _, f := range fields {
				name := cr.token(f.token)
				v, err := cr.value(f.rep)
				if err != nil {
					return nil, fmt.Errorf("layer field %s: %w", name, err)
				}
				if name == "primChildren" {
					for _, k := range asList(v) {
						childOrder["/"] = append(childOrder["/"], textOf(k))
					}
					continue
				}
				l.meta[name] = v
			}
		case specPrim:
			pr := &prim{spec: "def", meta: map[string]any{}, props: map[string]*property{}}
			pr.name = p[strings.LastIndexByte(p, '/')+1:]
			for _, f := range fields {
				name := cr.token(f.token)
				switch name {
				case "specifier":
					v, _ := cr.value(f.rep)
					if x, ok := v.(float64); ok {
						pr.spec = []string{"def", "over", "class"}[min(int(x), 2)]
					}
				case "typeName":
					v, _ := cr.value(f.rep)
					pr.typ = textOf(v)
				case "primChildren":
					v, err := cr.value(f.rep)
					if err != nil {
						return nil, err
					}
					for _, k := range asList(v) {
						childOrder[p] = append(childOrder[p], textOf(k))
					}
				case "properties":
				default:
					v, err := cr.value(f.rep)
					if err != nil {
						return nil, fmt.Errorf("prim %s field %s: %w", p, name, err)
					}
					pr.meta[name] = v
				}
			}
			prims[p] = pr
		}
	}

	// Pass 2: properties.
	for _, s := range cr.specs {
		if s.typ != specAttribute && s.typ != specRelationship {
			continue
		}
		p, err := pathOf(s)
		if err != nil {
			return nil, err
		}
		dot := strings.LastIndexByte(p, '.')
		if dot < 0 {
			continue
		}
		owner, ok := prims[p[:dot]]
		if !ok {
			continue
		}
		fields, err := fieldsOf(s)
		if err != nil {
			return nil, err
		}
		prop := &property{meta: map[string]any{}}
		if s.typ == specRelationship {
			prop.typ = "rel"
		}
		for _, f := range fields {
			name := cr.token(f.token)
			switch name {
			case "typeName":
				v, _ := cr.value(f.rep)
				if prop.typ == "" {
					prop.typ = textOf(v)
				}
			case "default", "targetPaths":
				v, err := cr.value(f.rep)
				if err != nil {
					return nil, fmt.Errorf("property %s: %w", p, err)
				}
				if prop.value == nil {
					prop.value = v
				}
			case "timeSamples":
				if prop.value == nil {
					prop.value = unsupportedValue{"timeSamples"}
				}
			case "interpolation":
				v, _ := cr.value(f.rep)
				prop.meta["interpolation"] = v
			}
		}
		pname := p[dot+1:]
		if _, seen := owner.props[pname]; !seen {
			owner.order = append(owner.order, pname)
		}
		owner.props[pname] = prop
	}

	// Link the tree.
	var attach func(parentPath string, parent *prim)
	attach = func(parentPath string, parent *prim) {
		for _, name := range childOrder[parentPath] {
			p := parentPath + "/" + name
			if parentPath == "/" {
				p = "/" + name
			}
			child, ok := prims[p]
			if !ok {
				continue
			}
			if parent != nil {
				parent.kids = append(parent.kids, child)
			} else {
				l.prims = append(l.prims, child)
			}
			attach(p, child)
		}
	}
	attach("/", nil)
	return l, nil
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}
