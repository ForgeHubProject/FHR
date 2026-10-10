package main

import (
	"encoding/binary"
	"math"
)

// sobj is one struct instance in the file: its definition and its bytes.
type sobj struct {
	f  *bfile
	sd *structDef
	d  []byte
	sc *scope // where this struct's pointers resolve
}

func (o sobj) ok() bool { return o.sd != nil }

func (o sobj) field(name string) (*field, []byte) {
	if o.sd == nil {
		return nil, nil
	}
	f := o.sd.byName[name]
	if f == nil || f.offset+f.size > len(o.d) {
		return nil, nil
	}
	return f, o.d[f.offset : f.offset+f.size]
}

func (o sobj) has(name string) bool { f, _ := o.field(name); return f != nil }

func (o sobj) i32(name string) int {
	_, b := o.field(name)
	if len(b) < 4 {
		return 0
	}
	return int(int32(binary.LittleEndian.Uint32(b)))
}

func (o sobj) i16(name string) int {
	_, b := o.field(name)
	if len(b) < 2 {
		return 0
	}
	return int(int16(binary.LittleEndian.Uint16(b)))
}

func (o sobj) i64(name string) int64 {
	_, b := o.field(name)
	if len(b) < 8 {
		return 0
	}
	return int64(binary.LittleEndian.Uint64(b))
}

func (o sobj) u8(name string) int {
	_, b := o.field(name)
	if len(b) < 1 {
		return 0
	}
	return int(b[0])
}

func (o sobj) f32(name string) float64 {
	_, b := o.field(name)
	if len(b) < 4 {
		return 0
	}
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
}

// f32s reads a float array field (any rank, flattened).
func (o sobj) f32s(name string) []float64 {
	_, b := o.field(name)
	out := make([]float64, len(b)/4)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:])))
	}
	return out
}

func (o sobj) ptr(name string) uint64 {
	f, b := o.field(name)
	if f == nil || !f.ptr || len(b) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

// str reads a char-array field up to its NUL.
func (o sobj) str(name string) string {
	_, b := o.field(name)
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// sub is an embedded struct field (element i when it is an array of them).
func (o sobj) sub(name string, i int) sobj {
	f, b := o.field(name)
	if f == nil || f.ptr {
		return sobj{}
	}
	sd := o.f.dna.byName[f.typ]
	if sd == nil || sd.size == 0 || (i+1)*sd.size > len(b) {
		return sobj{}
	}
	return sobj{o.f, sd, b[i*sd.size : (i+1)*sd.size], o.sc}
}

// at follows a pointer to an array of structs and returns its element i.
func (o sobj) at(ptr uint64, i int) sobj {
	f := o.f
	b := f.lookup(ptr, o.sc)
	if b == nil || b.sdna < 0 || b.sdna >= len(f.dna.structs) {
		return sobj{}
	}
	sd := f.dna.structs[b.sdna]
	if sd.size == 0 || i < 0 || (i+1)*sd.size > len(b.data) {
		return sobj{}
	}
	return sobj{f, sd, b.data[i*sd.size : (i+1)*sd.size], b.scope}
}

// bytes is the data of the block a pointer names.
func (o sobj) bytes(ptr uint64) []byte {
	if b := o.f.lookup(ptr, o.sc); b != nil {
		return b.data
	}
	return nil
}

// list walks a ListBase field: first → next → …, bounded so a cyclic list ends.
func (o sobj) list(name string) []sobj {
	lb := o.sub(name, 0)
	if !lb.ok() {
		return nil
	}
	var out []sobj
	p := lb.ptr("first")
	for n := 0; p != 0 && n < 1<<20; n++ {
		e := o.at(p, 0)
		if !e.ok() {
			break
		}
		out = append(out, e)
		p = e.ptr("next")
	}
	return out
}

// ofType returns every block-level struct of a type (the ID blocks: Object, Mesh, …).
func (f *bfile) ofType(name string) []sobj {
	sd := f.dna.byName[name]
	if sd == nil {
		return nil
	}
	var out []sobj
	for _, b := range f.blocks {
		if b.sdna >= 0 && b.sdna < len(f.dna.structs) && f.dna.structs[b.sdna] == sd {
			for i := 0; i < b.count && (i+1)*sd.size <= len(b.data); i++ {
				out = append(out, sobj{f, sd, b.data[i*sd.size : (i+1)*sd.size], b.scope})
			}
		}
	}
	return out
}
