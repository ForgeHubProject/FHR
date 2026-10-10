package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// A .blend file is a header, then a sequence of blocks — each a code, a size and
// an element count, a "pointer" (the address the block had in Blender's memory,
// which other blocks refer to) and the bytes — ending with DNA1, the file's own
// description of every struct it holds. Reading is therefore generic: the
// struct layouts are taken from the file, not from Blender's source, and fields
// are found by name.

const (
	maxBlendInflate = 512 << 20
	maxBlocks       = 4_000_000
)

type block struct {
	code  string
	sdna  int
	old   uint64
	count int
	data  []byte
	scope *scope
}

// A pointer in a .blend is only meaningful within the ID it was written under:
// Blender reuses the same "old" addresses for different datablocks' arrays, so
// the DATA blocks that follow an ID block (until the next one) form that ID's
// scope, and pointers inside it resolve there first, then among the IDs.
type scope struct {
	byPtr map[uint64]*block
}

type bfile struct {
	version int // 502 for 5.2
	blocks  []*block
	byPtr   map[uint64]*block // the non-DATA blocks (IDs and the like), by pointer
	dna     *sdna
}

// lookup resolves a pointer seen inside scope sc.
func (f *bfile) lookup(ptr uint64, sc *scope) *block {
	if ptr == 0 {
		return nil
	}
	if sc != nil {
		if b := sc.byPtr[ptr]; b != nil {
			return b
		}
	}
	return f.byPtr[ptr]
}

// decompress unwraps the compression Blender may save with: zstd (the default
// from 3.0) or gzip (older); a plain file is returned as is.
func decompress(b []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(b, []byte{0x28, 0xb5, 0x2f, 0xfd}) || (len(b) >= 4 && b[0]&0xf0 == 0x50 && b[1] == 0x2a && b[2] == 0x4d && b[3] == 0x18):
		dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxBlendInflate), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		out, err := dec.DecodeAll(b, nil)
		if err != nil {
			return nil, fmt.Errorf("decompressing the .blend: %w", err)
		}
		if len(out) > maxBlendInflate {
			return nil, fmt.Errorf("the .blend is larger than %d MiB once decompressed", maxBlendInflate>>20)
		}
		return out, nil
	case len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b:
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		out, err := io.ReadAll(io.LimitReader(zr, maxBlendInflate+1))
		if err != nil {
			return nil, fmt.Errorf("decompressing the .blend: %w", err)
		}
		if len(out) > maxBlendInflate {
			return nil, fmt.Errorf("the .blend is larger than %d MiB once decompressed", maxBlendInflate>>20)
		}
		return out, nil
	}
	return b, nil
}

func readBlend(raw []byte) (*bfile, error) {
	b, err := decompress(raw)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || string(b[:7]) != "BLENDER" {
		return nil, fmt.Errorf("not a .blend file")
	}
	f := &bfile{byPtr: map[uint64]*block{}}
	pos := 12
	large := false
	switch {
	case b[7] == '_' || b[7] == '-': // the classic 12-byte header
		if b[7] == '_' {
			return nil, fmt.Errorf("32-bit .blend files are not supported")
		}
		if b[8] != 'v' {
			return nil, fmt.Errorf("big-endian .blend files are not supported")
		}
		f.version, _ = strconv.Atoi(string(b[9:12]))
	case b[7] >= '0' && b[7] <= '9': // BLENDER17-01v0502: header size, format version, endianness, version
		hs, err := strconv.Atoi(string(b[7:9]))
		if err != nil || hs < 17 || hs > len(b) {
			return nil, fmt.Errorf("bad .blend header size")
		}
		if b[9] != '-' {
			return nil, fmt.Errorf("32-bit .blend files are not supported")
		}
		if fv, _ := strconv.Atoi(string(b[10:12])); fv != 1 {
			return nil, fmt.Errorf("unsupported .blend file format version %d", fv)
		}
		if b[12] != 'v' {
			return nil, fmt.Errorf("big-endian .blend files are not supported")
		}
		f.version, _ = strconv.Atoi(string(b[13:17]))
		pos, large = hs, true
	default:
		return nil, fmt.Errorf("not a .blend file: bad header")
	}

	le := binary.LittleEndian
	var cur *scope
	for {
		var bl block
		var size int
		if large { // code, sdna, old pointer, size, count
			if pos+32 > len(b) {
				return nil, fmt.Errorf("the .blend ends inside a block header")
			}
			bl.code = strings.TrimRight(string(b[pos:pos+4]), "\x00")
			bl.sdna = int(le.Uint32(b[pos+4:]))
			bl.old = le.Uint64(b[pos+8:])
			sz := le.Uint64(b[pos+16:])
			cnt := le.Uint64(b[pos+24:])
			if sz > uint64(len(b)) || cnt > uint64(len(b)) {
				return nil, fmt.Errorf("a .blend block claims more than the file holds")
			}
			size, bl.count = int(sz), int(cnt)
			pos += 32
		} else { // code, size, old pointer, sdna, count
			if pos+24 > len(b) {
				return nil, fmt.Errorf("the .blend ends inside a block header")
			}
			bl.code = strings.TrimRight(string(b[pos:pos+4]), "\x00")
			sz := le.Uint32(b[pos+4:])
			bl.old = le.Uint64(b[pos+8:])
			bl.sdna = int(le.Uint32(b[pos+16:]))
			bl.count = int(le.Uint32(b[pos+20:]))
			size = int(sz)
			pos += 24
		}
		if size < 0 || pos+size > len(b) {
			return nil, fmt.Errorf("a .blend block (%s) runs past the end of the file", bl.code)
		}
		bl.data = b[pos : pos+size]
		pos += size
		if bl.code == "ENDB" {
			break
		}
		f.blocks = append(f.blocks, &bl)
		if len(f.blocks) > maxBlocks {
			return nil, fmt.Errorf("the .blend has more than %d blocks", maxBlocks)
		}
		switch {
		case bl.code == "DNA1":
			d, err := parseSDNA(bl.data)
			if err != nil {
				return nil, err
			}
			f.dna = d
		case bl.code == "DATA":
			if cur == nil { // data before any ID: its own scope
				cur = &scope{byPtr: map[uint64]*block{}}
			}
			bl.scope = cur
			if bl.old != 0 {
				if _, dup := cur.byPtr[bl.old]; !dup {
					cur.byPtr[bl.old] = &bl
				}
			}
		default:
			cur = &scope{byPtr: map[uint64]*block{}}
			bl.scope = cur
			if bl.old != 0 {
				if _, dup := f.byPtr[bl.old]; !dup {
					f.byPtr[bl.old] = &bl
				}
			}
		}
	}
	if f.dna == nil {
		return nil, fmt.Errorf("the .blend has no DNA block")
	}
	return f, nil
}

// ── SDNA ──────────────────────────────────────────────────────────────────────

type field struct {
	name    string // without '*' and array brackets
	typ     string
	typIdx  int
	ptr     bool // a pointer (including an array of pointers)
	funcPtr bool
	dims    []int // array dimensions, outermost first
	offset  int
	size    int // the whole field, array included
}

type structDef struct {
	name   string
	size   int
	fields []field
	byName map[string]*field
}

type sdna struct {
	types   []string
	tlen    []int
	structs []*structDef
	byName  map[string]*structDef
}

func align4(p int) int { return (p + 3) &^ 3 }

func parseSDNA(d []byte) (*sdna, error) {
	if len(d) < 8 || string(d[:4]) != "SDNA" {
		return nil, fmt.Errorf("bad DNA block")
	}
	p := 4
	readU32 := func() (int, error) {
		if p+4 > len(d) {
			return 0, fmt.Errorf("truncated DNA block")
		}
		v := int(binary.LittleEndian.Uint32(d[p:]))
		p += 4
		return v, nil
	}
	tag := func(t string) error {
		if p+4 > len(d) || string(d[p:p+4]) != t {
			return fmt.Errorf("DNA block has no %s section", t)
		}
		p += 4
		return nil
	}
	strs := func(t string) ([]string, error) {
		if err := tag(t); err != nil {
			return nil, err
		}
		n, err := readU32()
		if err != nil {
			return nil, err
		}
		if n < 0 || n > len(d) {
			return nil, fmt.Errorf("bad DNA %s count", t)
		}
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			e := bytes.IndexByte(d[p:], 0)
			if e < 0 {
				return nil, fmt.Errorf("truncated DNA %s", t)
			}
			out = append(out, string(d[p:p+e]))
			p += e + 1
		}
		p = align4(p)
		return out, nil
	}
	names, err := strs("NAME")
	if err != nil {
		return nil, err
	}
	types, err := strs("TYPE")
	if err != nil {
		return nil, err
	}
	if err := tag("TLEN"); err != nil {
		return nil, err
	}
	tlen := make([]int, len(types))
	for i := range tlen {
		if p+2 > len(d) {
			return nil, fmt.Errorf("truncated DNA TLEN")
		}
		tlen[i] = int(binary.LittleEndian.Uint16(d[p:]))
		p += 2
	}
	p = align4(p)
	if err := tag("STRC"); err != nil {
		return nil, err
	}
	ns, err := readU32()
	if err != nil {
		return nil, err
	}
	if ns < 0 || ns > len(d) {
		return nil, fmt.Errorf("bad DNA struct count")
	}
	s := &sdna{types: types, tlen: tlen, byName: map[string]*structDef{}}
	u16 := func() (int, error) {
		if p+2 > len(d) {
			return 0, fmt.Errorf("truncated DNA STRC")
		}
		v := int(binary.LittleEndian.Uint16(d[p:]))
		p += 2
		return v, nil
	}
	for i := 0; i < ns; i++ {
		ti, err := u16()
		if err != nil {
			return nil, err
		}
		nf, err := u16()
		if err != nil {
			return nil, err
		}
		if ti >= len(types) {
			return nil, fmt.Errorf("DNA struct type %d is out of range", ti)
		}
		sd := &structDef{name: types[ti], size: tlen[ti], byName: map[string]*field{}}
		off := 0
		for j := 0; j < nf; j++ {
			ft, err := u16()
			if err != nil {
				return nil, err
			}
			fn, err := u16()
			if err != nil {
				return nil, err
			}
			if ft >= len(types) || fn >= len(names) {
				return nil, fmt.Errorf("DNA field index is out of range")
			}
			f := parseField(names[fn], types[ft], ft, tlen[ft])
			f.offset = off
			off += f.size
			sd.fields = append(sd.fields, f)
		}
		for k := range sd.fields {
			sd.byName[sd.fields[k].name] = &sd.fields[k]
		}
		s.structs = append(s.structs, sd)
		s.byName[sd.name] = sd
	}
	return s, nil
}

// parseField reads a DNA field name such as "*next", "co[3]" or "(*cb)()".
func parseField(raw, typ string, ti, tl int) field {
	f := field{typ: typ, typIdx: ti}
	n := raw
	if strings.HasPrefix(n, "(*") {
		f.funcPtr, f.ptr = true, true
		n = strings.TrimSuffix(strings.TrimPrefix(n, "(*"), ")()")
		f.name, f.size = n, 8
		return f
	}
	for strings.HasPrefix(n, "*") {
		f.ptr = true
		n = n[1:]
	}
	if i := strings.IndexByte(n, '['); i >= 0 {
		for _, part := range strings.Split(strings.TrimSuffix(n[i+1:], "]"), "][") {
			v, _ := strconv.Atoi(part)
			f.dims = append(f.dims, max(v, 1))
		}
		n = n[:i]
	}
	f.name = n
	unit := tl
	if f.ptr {
		unit = 8
	}
	f.size = unit
	for _, d := range f.dims {
		f.size *= d
	}
	return f
}
