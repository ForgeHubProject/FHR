package main

// Quantized, canonically-ordered geometry digests — for MATCHING only (#42).
//
// The scope fence, stated once and load-bearing: nothing in this file touches
// what a diff *asserts*. diffPrimitiveGeometry, equalStreams and accessorLabel —
// the wire rows that say "geometry changed" and the exact FNV hashes they carry —
// are byte-for-byte untouched, so a re-exported file with quantization-level
// jitter still REPORTS its streams as changed. Whether sub-epsilon jitter should
// be reported at all is a product judgment deliberately not taken in this slice;
// matching-only is the conservative half that cannot change what a diff says
// about a file. Signature field values never reach the wire (only the similarity
// percentage does), so the quantized digest is invisible to consumers.
//
// What it buys: a mesh renamed AND re-exported — vertex order shuffled by an
// optimiser, floats perturbed at the exporter's precision — still pairs with
// itself, because the digest is taken over a canonical form of the geometry
// rather than over the bytes:
//
//	1. Each float component is quantized to a per-semantic decimal precision
//	   (trimesh's constants, as the issue directs): POSITION 8 decimals,
//	   NORMAL/TANGENT 2, TEXCOORD_* 4, COLOR_*/WEIGHTS_* 4, unknown float
//	   semantics 8. Integer streams (indices, JOINTS_*, a ubyte COLOR_*) are
//	   never quantized: their components pass through exactly, so a skinned or
//	   byte-colored mesh canonicalizes like any other instead of losing this
//	   tier to its one integer attribute. −0 normalizes to 0 through the
//	   rounding itself.
//	2. Vertices are put in a canonical order: one record per vertex — the
//	   quantized components of every attribute stream, in sorted semantic
//	   order — sorted lexicographically and deduplicated, so a permutation of
//	   the vertex array (with its index buffer remapped) cancels out.
//	3. Each attribute stream's digest is FNV-1a over its components visited in
//	   that sorted-unique order. The index stream is remapped to sorted-unique
//	   ranks (so duplicate-vertex permutations cancel too); for triangle
//	   topologies each triangle is rotated so its smallest rank leads —
//	   rotation only, winding preserved, so a mirrored triangle list stays a
//	   different mesh — and the triangle list is sorted. Non-triangle modes
//	   digest the remapped sequence as-is (a stated limitation, not a guess).
//
// Refuse-don't-guess, mirroring primitiveCentroid: only plain little-endian
// data reachable through accessorSpan is decoded. A sparse accessor, an
// unreadable buffer, a NaN, a float whose quantum does not fit an int64, a
// count mismatch between streams — any of them makes the whole primitive fall
// back to today's exact-hash-or-<unreadable> descriptors, because a canonical
// form built from bytes we cannot fully read could digest two different meshes
// equal.
//
// Cost discipline: canonicalization is O(bytes + V·log V) per primitive and runs
// at most once per primitive per diff — memoized in meshSide.canon — and only
// ever for tier-3 leftovers (entity.sig stays lazy). The memo is keyed by the
// primitive rather than the accessor index the plan sketched, because the
// canonical order is a property of the primitive's whole vertex record (the
// sorted-semantics concatenation), not of any one accessor; one accessor shared
// by two primitives with different attribute sets has two canonical orders.
// Buckets never trust a digest alone: the exact-content tier re-compares the
// canonical component sequences (canonPrim.seqs) before pairing, so the 64-bit
// FNV is an accelerator, never the proof.
//
// Node placement fields are untouched: they already quantize through fmtF's two
// decimals. Pure Go stdlib (sort, math, hash/fnv, encoding/binary), wasm-clean,
// single-threaded; peak allocation is one flat record table plus the index
// permutation (~8 bytes per vertex-component and per index).

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/qmuntal/gltf"
)

// semanticDecimals is the quantization precision for one float attribute
// semantic, in decimal digits. The constants are trimesh's merge-tolerance
// defaults, which issue #42 names as the shipped baseline for "the same
// geometry after a re-export".
func semanticDecimals(semantic string) int {
	switch {
	case semantic == gltf.POSITION:
		return 8
	case semantic == gltf.NORMAL || semantic == gltf.TANGENT:
		return 2
	case strings.HasPrefix(semantic, "TEXCOORD_"):
		return 4
	case strings.HasPrefix(semantic, "COLOR_") || strings.HasPrefix(semantic, "WEIGHTS_"):
		return 4
	default:
		return 8
	}
}

// quantumLimit is the first magnitude a quantum may NOT hold: 2^63, exactly
// representable as a float64, and one past what an int64 can.
const quantumLimit = float64(1 << 63)

// quantize maps one float component to its quantized integer at the given
// decimal precision. Rounding takes −0 to 0 on its own. ok is false for a NaN,
// an infinity, or a finite value whose quantum overflows int64 — for any of
// them there is no honest quantum, so the primitive is disqualified from
// canonical digesting outright. The overflow refusal is load-bearing, not
// pedantry: Go defines an out-of-range float→int conversion as
// implementation-dependent (amd64 saturates to minInt64), so without it every
// |v·pow10| ≥ 2^63 — a POSITION beyond ~9.2e10, which garbage or corrupted
// exports reach — collapsed to ONE quantum and genuinely different
// out-of-range geometries canonicalized equal, the exact false pair the NaN
// refusal above exists to prevent.
func quantize(v float64, pow10 float64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	r := math.Round(v * pow10)
	if r >= quantumLimit || r <= -quantumLimit {
		return 0, false
	}
	return int64(r), true
}

// canonPrim is one primitive's canonical form: a quantized descriptor per
// stream, plus the canonical component sequences themselves so a caller that
// must not trust a 64-bit hash can compare the actual values.
type canonPrim struct {
	ok bool
	// desc maps a stream name (attribute semantics plus indicesStream) to its
	// quantized descriptor: "count=N type=VEC3 component=FLOAT qhash=…".
	desc map[string]string
	// seqs holds the canonical component sequence behind each digest, in the
	// exact order the digest hashed.
	seqs map[string][]int64
}

// canonical returns the primitive's canonical form, memoized so each primitive
// is canonicalized at most once per diff however many candidate pairs look at
// it.
func (s meshSide) canonical(p *gltf.Primitive) *canonPrim {
	if c, ok := s.canon[p]; ok {
		return c
	}
	c := buildCanonPrim(s.doc, p)
	s.canon[p] = c
	return c
}

// streamDescriptor renders one stream of a primitive for a content signature:
// the canonical quantized digest when the primitive is canonicalizable, and
// today's exact descriptor (hash of the raw bytes, or <unreadable>) when it is
// not. Signature values only — the wire's geometry rows never come through here.
func (s meshSide) streamDescriptor(p *gltf.Primitive, stream string) string {
	if c := s.canonical(p); c.ok {
		if d, ok := c.desc[stream]; ok {
			return d
		}
	}
	return readStream(s.doc, p, stream).describe(s.doc)
}

// buildCanonPrim computes a primitive's canonical form, or {ok: false} when any
// part of it cannot be decoded honestly.
func buildCanonPrim(doc *gltf.Document, p *gltf.Primitive) *canonPrim {
	fail := &canonPrim{}
	semantics := make([]string, 0, len(p.Attributes))
	for name := range p.Attributes {
		semantics = append(semantics, name)
	}
	sort.Strings(semantics)
	if len(semantics) == 0 {
		return fail
	}

	// Decode every attribute stream to integer components: float streams
	// quantized at their semantic's precision, integer streams (JOINTS_*, a
	// byte-encoded COLOR_*) exactly as written — never quantized, per the
	// header. All streams of one primitive must agree on the vertex count, or
	// the per-vertex record below would be built from misaligned data.
	type streamData struct {
		acc   *gltf.Accessor
		comps []int64 // count × components(), quantized (floats) or exact (ints)
		width int
	}
	streams := make(map[string]streamData, len(semantics))
	count := -1
	for _, sem := range semantics {
		idx := p.Attributes[sem]
		if idx < 0 || idx >= len(doc.Accessors) {
			return fail
		}
		acc := doc.Accessors[idx]
		var comps []int64
		var ok bool
		if acc.ComponentType == gltf.ComponentFloat {
			comps, ok = decodeQuantized(doc, acc, semanticDecimals(sem))
		} else {
			comps, ok = decodeIntComponents(doc, acc)
		}
		if !ok {
			return fail
		}
		if count == -1 {
			count = acc.Count
		} else if count != acc.Count {
			return fail
		}
		streams[sem] = streamData{acc: acc, comps: comps, width: acc.Type.Components()}
	}
	if count == 0 {
		return fail
	}

	// One flat record per vertex: the concatenation of every stream's quantized
	// components in sorted semantic order.
	recordLen := 0
	for _, sem := range semantics {
		recordLen += streams[sem].width
	}
	records := make([]int64, count*recordLen)
	for i := range count {
		at := i * recordLen
		for _, sem := range semantics {
			sd := streams[sem]
			at += copy(records[at:], sd.comps[i*sd.width:(i+1)*sd.width])
		}
	}

	// Canonical order: sort vertex indices by record, lexicographically, then
	// deduplicate equal records. rank[v] is vertex v's position in the
	// sorted-unique list; reps holds one representative vertex per unique record,
	// in rank order.
	order := make([]int, count)
	for i := range order {
		order[i] = i
	}
	rec := func(i int) []int64 { return records[i*recordLen : (i+1)*recordLen] }
	sort.Slice(order, func(a, b int) bool {
		return slices.Compare(rec(order[a]), rec(order[b])) < 0
	})
	rank := make([]int64, count)
	reps := make([]int, 0, count)
	for k, v := range order {
		if k > 0 && slices.Equal(rec(v), rec(order[k-1])) {
			rank[v] = rank[order[k-1]]
			continue
		}
		rank[v] = int64(len(reps))
		reps = append(reps, v)
	}

	c := &canonPrim{
		ok:   true,
		desc: make(map[string]string, len(semantics)+1),
		seqs: make(map[string][]int64, len(semantics)+1),
	}
	for _, sem := range semantics {
		sd := streams[sem]
		seq := make([]int64, 0, len(reps)*sd.width)
		for _, v := range reps {
			seq = append(seq, sd.comps[v*sd.width:(v+1)*sd.width]...)
		}
		c.seqs[sem] = seq
		c.desc[sem] = quantizedLabel(sd.acc, seq)
	}

	if p.Indices != nil {
		seq, acc, ok := canonicalIndices(doc, p, rank)
		if !ok {
			return fail
		}
		c.seqs[indicesStream] = seq
		c.desc[indicesStream] = quantizedLabel(acc, seq)
	}
	return c
}

// canonicalIndices remaps a primitive's index buffer to canonical vertex ranks
// and, for triangle topology, rotates each triangle so its smallest rank leads
// (rotation only — winding and orientation preserved) and sorts the triangle
// list. Other modes keep the remapped sequence as-is: order may be meaningful
// there (strips, fans, lines), so reordering it would be a guess.
func canonicalIndices(doc *gltf.Document, p *gltf.Primitive, rank []int64) ([]int64, *gltf.Accessor, bool) {
	idx := *p.Indices
	if idx < 0 || idx >= len(doc.Accessors) {
		return nil, nil, false
	}
	acc := doc.Accessors[idx]
	raw, ok := decodeInts(doc, acc)
	if !ok {
		return nil, nil, false
	}
	seq := make([]int64, len(raw))
	for i, v := range raw {
		if v < 0 || v >= int64(len(rank)) {
			return nil, nil, false
		}
		seq[i] = rank[v]
	}
	if p.Mode == gltf.PrimitiveTriangles && len(seq)%3 == 0 {
		for i := 0; i < len(seq); i += 3 {
			rotateTriangle(seq[i : i+3])
		}
		tris := make([][3]int64, len(seq)/3)
		for i := range tris {
			tris[i] = [3]int64{seq[i*3], seq[i*3+1], seq[i*3+2]}
		}
		sort.Slice(tris, func(a, b int) bool {
			return slices.Compare(tris[a][:], tris[b][:]) < 0
		})
		for i, t := range tris {
			seq[i*3], seq[i*3+1], seq[i*3+2] = t[0], t[1], t[2]
		}
	}
	return seq, acc, true
}

// rotateTriangle rotates a triangle in place so its smallest vertex rank comes
// first. Cyclic rotation only: [b a c] and [a c b] stay different triangles,
// which is what keeps a mirrored mesh from digesting equal.
func rotateTriangle(t []int64) {
	lead := 0
	for i := 1; i < 3; i++ {
		if t[i] < t[lead] {
			lead = i
		}
	}
	switch lead {
	case 1:
		t[0], t[1], t[2] = t[1], t[2], t[0]
	case 2:
		t[0], t[1], t[2] = t[2], t[0], t[1]
	}
}

// quantizedLabel renders a canonical stream descriptor. Shape mirrors
// accessorLabel so the two read in one voice, with `qhash` marking that the
// digest is over the canonical quantized form rather than the raw bytes. The
// normalized flag is part of the label because a normalized integer stream
// holds the same raw components as an unnormalized one while meaning different
// geometry (255 as a color channel vs 1.0's encoding) — the descriptor must
// keep them from ever comparing equal.
func quantizedLabel(acc *gltf.Accessor, seq []int64) string {
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range seq {
		binary.LittleEndian.PutUint64(buf[:], uint64(v))
		_, _ = h.Write(buf[:])
	}
	norm := ""
	if acc.Normalized {
		norm = " normalized"
	}
	return fmt.Sprintf("count=%d type=%v component=%v%s qhash=%016x", acc.Count, acc.Type, acc.ComponentType, norm, h.Sum64())
}

// decodeQuantized reads one float accessor's components as quantized integers.
// ok is false for anything that is not plain, readable, little-endian float32
// data — the caller then keeps the exact descriptor instead of guessing.
func decodeQuantized(doc *gltf.Document, acc *gltf.Accessor, decimals int) ([]int64, bool) {
	if acc.ComponentType != gltf.ComponentFloat {
		return nil, false
	}
	data, stride, ok := accessorSpan(doc, acc)
	if !ok {
		return nil, false
	}
	width := acc.Type.Components()
	elem := 4 * width
	pow10 := math.Pow(10, float64(decimals))
	out := make([]int64, 0, acc.Count*width)
	for i := range acc.Count {
		v := data[i*stride:]
		if len(v) < elem {
			return nil, false
		}
		for cmp := range width {
			f := float64(math.Float32frombits(binary.LittleEndian.Uint32(v[cmp*4 : cmp*4+4])))
			q, ok := quantize(f, pow10)
			if !ok {
				return nil, false
			}
			out = append(out, q)
		}
	}
	return out, true
}

// decodeIntComponents reads one integer accessor's components exactly — the
// attribute-stream counterpart of decodeInts, for JOINTS_* and byte-encoded
// COLOR_* streams, which the header promises are never quantized. Only the
// integer component types readComponentInt knows are accepted; anything else
// refuses.
func decodeIntComponents(doc *gltf.Document, acc *gltf.Accessor) ([]int64, bool) {
	data, stride, ok := accessorSpan(doc, acc)
	if !ok {
		return nil, false
	}
	width := acc.Type.Components()
	size := acc.ComponentType.ByteSize()
	if size == 0 {
		return nil, false
	}
	elem := size * width
	out := make([]int64, 0, acc.Count*width)
	for i := range acc.Count {
		v := data[i*stride:]
		if len(v) < elem {
			return nil, false
		}
		for cmp := range width {
			n, ok := readComponentInt(v[cmp*size:], acc.ComponentType)
			if !ok {
				return nil, false
			}
			out = append(out, n)
		}
	}
	return out, true
}

// decodeInts reads one integer accessor's scalar values. Used for the index
// buffer; JOINTS_* attributes go through the same integer reading below.
func decodeInts(doc *gltf.Document, acc *gltf.Accessor) ([]int64, bool) {
	if acc.Type != gltf.AccessorScalar {
		return nil, false
	}
	data, stride, ok := accessorSpan(doc, acc)
	if !ok {
		return nil, false
	}
	size := acc.ComponentType.ByteSize()
	out := make([]int64, 0, acc.Count)
	for i := range acc.Count {
		v := data[i*stride:]
		if len(v) < size {
			return nil, false
		}
		n, ok := readComponentInt(v, acc.ComponentType)
		if !ok {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// readComponentInt decodes one integer component. Floats are rejected — an
// index buffer or a JOINTS stream is integer by spec, and a float here means a
// file this code should refuse rather than reinterpret.
func readComponentInt(v []byte, t gltf.ComponentType) (int64, bool) {
	switch t {
	case gltf.ComponentUbyte:
		return int64(v[0]), true
	case gltf.ComponentByte:
		return int64(int8(v[0])), true
	case gltf.ComponentUshort:
		return int64(binary.LittleEndian.Uint16(v)), true
	case gltf.ComponentShort:
		return int64(int16(binary.LittleEndian.Uint16(v))), true
	case gltf.ComponentUint:
		return int64(binary.LittleEndian.Uint32(v)), true
	default:
		return 0, false
	}
}

// canonMeshesEqual reports whether two meshes' geometry is identical under the
// canonical quantized form, comparing the actual component sequences rather
// than their 64-bit digests. This is the exact-content tier's deep verification:
// hash equality accelerates the search, byte-level canonical comparison decides
// it. Anything that cannot be canonically compared on both sides falls back to
// a raw byte compare of the streams; anything unreadable refuses.
func canonMeshesEqual(a, b meshSide, am, bm *gltf.Mesh) bool {
	if len(am.Primitives) != len(bm.Primitives) {
		return false
	}
	for i := range am.Primitives {
		ap, bp := am.Primitives[i], bm.Primitives[i]
		ca, cb := a.canonical(ap), b.canonical(bp)
		switch {
		case ca.ok && cb.ok:
			if len(ca.seqs) != len(cb.seqs) {
				return false
			}
			for name, sa := range ca.seqs {
				sb, ok := cb.seqs[name]
				// The descriptors are compared as well as the sequences: two
				// streams can hold identical integers and mean different data —
				// a normalized ubyte color against an unnormalized one, or a
				// quantized float that happens to land on an integer stream's
				// values. The label carries type, component and normalization,
				// so equality here is equality of meaning, not just of numbers.
				if !ok || ca.desc[name] != cb.desc[name] || !slices.Equal(sa, sb) {
					return false
				}
			}
		case !ca.ok && !cb.ok:
			// Neither side canonicalizes; the honest fallback is the raw bytes.
			for _, stream := range primitiveStreams(ap, bp) {
				as, bs := readStream(a.doc, ap, stream), readStream(b.doc, bp, stream)
				if as.present != bs.present {
					return false
				}
				if !as.present {
					continue
				}
				if !as.readable || !bs.readable || !equalStreams(as, bs) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
