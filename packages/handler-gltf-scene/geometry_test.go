package main

// Tests for issue #43: change detection *inside* a mesh. A mesh used to be
// compared by name and primitive count only, so the commonest edit in a 3D
// review — sculpting vertices without touching topology — produced an empty
// diff, and so did reassigning a primitive to another existing material.
//
// Fixtures here are real GLBs built through the package's own encoder rather
// than JSON with a base64 data URI: the payloads are binary, some of them large,
// and the binary chunk is what real files use. Like the rest of the suite these
// run unchanged against the native and the wasm build.

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/qmuntal/gltf"
)

// ── fixtures ──────────────────────────────────────────────────────────────────

// geometrySpec describes one side of a comparison. The zero value is a valid
// single-primitive mesh; each field switches on one behaviour a test needs.
type geometrySpec struct {
	mesh        string       // mesh name, defaults to "Hull"
	positions   [][3]float32 // POSITION data (required)
	normals     [][3]float32 // nil omits the NORMAL semantic entirely
	uvs         [][2]float32 // nil omits the TEXCOORD_0 semantic entirely
	indices     []uint32     // nil omits the index buffer
	materials   []string     // materials present in the document, in order
	material    *int         // the material the primitive references
	primitives  int          // primitive count, defaults to 1 (repeats the data)
	interleaved bool         // pack POSITION and NORMAL into one strided view
	padAccessor bool         // insert an unused accessor ahead of POSITION
	omitBounds  bool         // leave min/max off the POSITION accessor
}

// geometryGLB encodes a spec as a self-contained GLB.
func geometryGLB(t *testing.T, s geometrySpec) []byte {
	t.Helper()
	name := s.mesh
	if name == "" {
		name = "Hull"
	}
	count := s.primitives
	if count == 0 {
		count = 1
	}
	b := &binWriter{doc: &gltf.Document{Asset: gltf.Asset{Version: "2.0"}}}
	for _, m := range s.materials {
		b.doc.Materials = append(b.doc.Materials, &gltf.Material{Name: m})
	}
	// An accessor nobody references, so that POSITION lands on a different index
	// between two otherwise identical files.
	if s.padAccessor {
		b.vec3(t, [][3]float32{{0, 0, 0}}, false)
	}

	attrs := gltf.PrimitiveAttributes{}
	if s.interleaved {
		if s.normals == nil {
			t.Fatal("interleaved fixture needs normals")
		}
		pos, nrm := b.interleavedVec3(t, s.positions, s.normals, !s.omitBounds)
		attrs[gltf.POSITION], attrs[gltf.NORMAL] = pos, nrm
	} else {
		attrs[gltf.POSITION] = b.vec3(t, s.positions, !s.omitBounds)
		if s.normals != nil {
			attrs[gltf.NORMAL] = b.vec3(t, s.normals, false)
		}
	}
	if s.uvs != nil {
		attrs[gltf.TEXCOORD_0] = b.vec2(s.uvs)
	}
	var indices *int
	if s.indices != nil {
		i := b.scalarU32(s.indices)
		indices = &i
	}

	prims := make([]*gltf.Primitive, count)
	for i := range prims {
		prims[i] = &gltf.Primitive{Attributes: attrs, Indices: indices, Material: s.material}
	}
	b.doc.Meshes = []*gltf.Mesh{{Name: name, Primitives: prims}}
	mesh := 0
	b.doc.Nodes = []*gltf.Node{{Name: "Body", Mesh: &mesh}}
	b.doc.Scenes = []*gltf.Scene{{Name: "Scene", Nodes: []int{0}}}
	scene := 0
	b.doc.Scene = &scene
	b.doc.Buffers = []*gltf.Buffer{{ByteLength: len(b.bin), Data: b.bin}}

	blob, err := encodeBlob(b.doc, true)
	if err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	return blob
}

// binWriter accumulates the binary chunk and the views/accessors addressing it.
type binWriter struct {
	doc *gltf.Document
	bin []byte
}

func (w *binWriter) view(data []byte, stride int) int {
	for len(w.bin)%4 != 0 {
		w.bin = append(w.bin, 0)
	}
	offset := len(w.bin)
	w.bin = append(w.bin, data...)
	w.doc.BufferViews = append(w.doc.BufferViews, &gltf.BufferView{
		Buffer: 0, ByteOffset: offset, ByteLength: len(data), ByteStride: stride,
	})
	return len(w.doc.BufferViews) - 1
}

func (w *binWriter) accessor(a *gltf.Accessor) int {
	w.doc.Accessors = append(w.doc.Accessors, a)
	return len(w.doc.Accessors) - 1
}

func (w *binWriter) vec3(t *testing.T, v [][3]float32, bounds bool) int {
	t.Helper()
	view := w.view(vec3Bytes(v), 0)
	acc := &gltf.Accessor{
		BufferView: &view, ComponentType: gltf.ComponentFloat,
		Type: gltf.AccessorVec3, Count: len(v),
	}
	if bounds {
		acc.Min, acc.Max = vec3Bounds(v)
	}
	return w.accessor(acc)
}

// interleavedVec3 packs two vec3 streams into a single strided buffer view, the
// layout an optimiser emits and the one where a naive count×size byte span
// under-reads the last element.
func (w *binWriter) interleavedVec3(t *testing.T, a, b [][3]float32, bounds bool) (int, int) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("interleaved streams differ in length: %d vs %d", len(a), len(b))
	}
	const stride = 24
	data := make([]byte, 0, len(a)*stride)
	for i := range a {
		data = append(data, vec3Bytes(a[i:i+1])...)
		data = append(data, vec3Bytes(b[i:i+1])...)
	}
	view := w.view(data, stride)
	first := &gltf.Accessor{
		BufferView: &view, ComponentType: gltf.ComponentFloat,
		Type: gltf.AccessorVec3, Count: len(a),
	}
	if bounds {
		first.Min, first.Max = vec3Bounds(a)
	}
	second := &gltf.Accessor{
		BufferView: &view, ByteOffset: 12, ComponentType: gltf.ComponentFloat,
		Type: gltf.AccessorVec3, Count: len(b),
	}
	return w.accessor(first), w.accessor(second)
}

func (w *binWriter) vec2(v [][2]float32) int {
	data := make([]byte, 0, len(v)*8)
	for _, p := range v {
		for _, c := range p {
			bits := math.Float32bits(c)
			data = append(data, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
		}
	}
	view := w.view(data, 0)
	return w.accessor(&gltf.Accessor{
		BufferView: &view, ComponentType: gltf.ComponentFloat,
		Type: gltf.AccessorVec2, Count: len(v),
	})
}

func (w *binWriter) scalarU32(v []uint32) int {
	data := make([]byte, 0, len(v)*4)
	for _, n := range v {
		data = append(data, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	}
	view := w.view(data, 0)
	return w.accessor(&gltf.Accessor{
		BufferView: &view, ComponentType: gltf.ComponentUint,
		Type: gltf.AccessorScalar, Count: len(v),
	})
}

func vec3Bytes(v [][3]float32) []byte {
	out := make([]byte, 0, len(v)*12)
	for _, p := range v {
		for _, c := range p {
			bits := math.Float32bits(c)
			out = append(out, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
		}
	}
	return out
}

func vec3Bounds(v [][3]float32) (min, max []float64) {
	if len(v) == 0 {
		return nil, nil
	}
	min = []float64{float64(v[0][0]), float64(v[0][1]), float64(v[0][2])}
	max = []float64{float64(v[0][0]), float64(v[0][1]), float64(v[0][2])}
	for _, p := range v {
		for i, c := range p {
			min[i] = math.Min(min[i], float64(c))
			max[i] = math.Max(max[i], float64(c))
		}
	}
	return min, max
}

// ramp builds n distinct vertices along a diagonal — deterministic, and no two
// alike, so a byte compare has something to find.
func ramp(n int) [][3]float32 {
	out := make([][3]float32, n)
	for i := range out {
		f := float32(i)
		out[i] = [3]float32{f * 0.01, f * 0.02, f * 0.03}
	}
	return out
}

// sculpt returns a copy of v with one vertex displaced — topology untouched,
// vertex count unchanged: the edit that used to be invisible.
func sculpt(v [][3]float32, index int, dy float32) [][3]float32 {
	out := make([][3]float32, len(v))
	copy(out, v)
	out[index][1] += dy
	return out
}

// sculptAll displaces every vertex, the shape of a global smooth or scale. It is
// what the perf guard compares, because it forces every check to do its full
// work: the byte compare finds a difference, and the centroid decode runs.
func sculptAll(v [][3]float32, dy float32) [][3]float32 {
	out := make([][3]float32, len(v))
	copy(out, v)
	for i := range out {
		out[i][1] += dy
	}
	return out
}

func seq(n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = uint32(i)
	}
	return out
}

func intPtr(i int) *int { return &i }

// valueOf renders a change's before/after for assertions and failure messages.
func valueOf(c *DiffChange) string {
	return fmt.Sprintf("%v → %v", c.Before, c.After)
}

// ── the headline case: a sculpt with no topology change ────────────────────────

func TestDiffGeometrySculptWithUnchangedTopology(t *testing.T) {
	base := ramp(64)
	// Same names, same primitive count, same vertex count — only bytes differ.
	head := sculpt(base, 40, 0.75)

	spec := geometrySpec{mesh: "Hood", positions: base, normals: ramp(64), indices: seq(64)}
	headSpec := spec
	headSpec.positions = head

	d := diffOf(t, geometryGLB(t, spec), geometryGLB(t, headSpec))

	// This is the whole point of the issue: it used to be an empty diff.
	if len(d.Changes) == 0 {
		t.Fatal("sculpting vertices produced an empty diff")
	}
	mustChange(t, d, "meshes/Hood")
	mustChange(t, d, "meshes/Hood/primitives/0")

	geo := mustChange(t, d, "meshes/Hood/primitives/0/geometry")
	if geo.Before != nil || geo.After != nil {
		t.Errorf("comparable geometry should carry no value of its own, got %s", valueOf(geo))
	}
	pos := mustChange(t, d, "meshes/Hood/primitives/0/geometry/POSITION")
	if pos.Before == pos.After {
		t.Errorf("POSITION reported with identical descriptors: %s", valueOf(pos))
	}
	if !strings.Contains(fmt.Sprint(pos.Before), "hash=") {
		t.Errorf("expected a content digest in %v", pos.Before)
	}

	// Untouched streams must stay quiet, or every sculpt reports every stream.
	for _, quiet := range []string{
		"meshes/Hood/primitives/0/geometry/NORMAL",
		"meshes/Hood/primitives/0/geometry/indices",
		"meshes/Hood/primitives/0/vertices",
		"meshes/Hood/primitives/0/material",
	} {
		if c := findChange(d, quiet); c != nil {
			t.Errorf("unchanged %s reported: %s", quiet, valueOf(c))
		}
	}

	// The metrics that make the change legible. Vertex 40 sat at Y=0.80 and the
	// ramp topped out at Y=1.26, so lifting it by 0.75 to 1.55 grows the box by
	// 0.29 — on Blender's Z, which is glTF's Y.
	bounds := mustChange(t, d, "meshes/Hood/primitives/0/bounds")
	if !strings.Contains(fmt.Sprint(bounds.After), "+0.29 Z") {
		t.Errorf("bounds should name the axis that grew, got %s", valueOf(bounds))
	}
	centroid := mustChange(t, d, "meshes/Hood/primitives/0/centroid")
	if !strings.Contains(fmt.Sprint(centroid.After), "moved 0.012") {
		// 0.75 spread over 64 vertices.
		t.Errorf("centroid = %s, want a 0.012 shift", valueOf(centroid))
	}
}

// An identical model must produce nothing at all. A geometry compare that cries
// wolf on every unchanged mesh is worse than no compare.
func TestDiffGeometryIdenticalModelReportsNothing(t *testing.T) {
	spec := geometrySpec{positions: ramp(32), normals: ramp(32), indices: seq(32),
		materials: []string{"Paint"}, material: intPtr(0)}
	blob := geometryGLB(t, spec)
	d := diffOf(t, blob, geometryGLB(t, spec))
	if len(d.Changes) != 0 {
		t.Errorf("identical models diffed as %v", paths(d))
	}
	// And the very same bytes, for good measure.
	if d := diffOf(t, blob, blob); len(d.Changes) != 0 {
		t.Errorf("a model against itself diffed as %v", paths(d))
	}
}

// Content, not indices: rewriting the file so POSITION lands on a different
// accessor index is not a change to the mesh. This is the failure mode that made
// #41 resolve texture references to content, and it applies here too.
func TestDiffGeometryAccessorReindexingIsNotAChange(t *testing.T) {
	spec := geometrySpec{positions: ramp(24), normals: ramp(24), indices: seq(24)}
	shifted := spec
	shifted.padAccessor = true

	d := diffOf(t, geometryGLB(t, spec), geometryGLB(t, shifted))
	if c := findChange(d, "meshes/Hull/primitives/0/geometry"); c != nil {
		t.Errorf("re-indexed accessors reported as a geometry change: %v", c.Children)
	}
}

func TestDiffGeometryVertexCountDelta(t *testing.T) {
	base := geometrySpec{positions: ramp(12480), indices: seq(12480)}
	head := geometrySpec{positions: ramp(13104), indices: seq(13104)}

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))

	c := mustChange(t, d, "meshes/Hull/primitives/0/vertices")
	if got, want := fmt.Sprint(c.Before), "12,480"; got != want {
		t.Errorf("vertices before = %q, want %q (grouped in thousands)", got, want)
	}
	if got, want := fmt.Sprint(c.After), "13,104 (+624)"; got != want {
		t.Errorf("vertices after = %q, want %q", got, want)
	}
	// Losing vertices reports a negative delta, not a bare number.
	back := diffOf(t, geometryGLB(t, head), geometryGLB(t, base))
	if got, want := fmt.Sprint(mustChange(t, back, "meshes/Hull/primitives/0/vertices").After), "12,480 (-624)"; got != want {
		t.Errorf("vertices after = %q, want %q", got, want)
	}
}

// A primitive count change is still reported on its own row, and the overlapping
// prefix is still compared — a mesh that both gained a primitive and had its
// first one sculpted must report both facts.
func TestDiffGeometryPrimitiveCountChangeStillComparesPrefix(t *testing.T) {
	base := geometrySpec{positions: ramp(16), primitives: 1}
	head := geometrySpec{positions: sculpt(ramp(16), 3, 0.5), primitives: 2}

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))

	count := mustChange(t, d, "meshes/Hull/primitives")
	if got, want := valueOf(count), "1 → 2"; got != want {
		t.Errorf("primitive count = %q, want %q", got, want)
	}
	mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")
	// Index 1 exists on one side only, so it is not compared pairwise.
	if c := findChange(d, "meshes/Hull/primitives/1"); c != nil {
		t.Errorf("primitive[1] exists on one side only, should not be compared: %v", c)
	}
}

// ── the material reference, which was never compared either ───────────────────

// Assigning a Blender object a different material that already exists in the
// file rewrites nothing but primitives[i].material, which used to diff as no
// change whatsoever.
func TestDiffPrimitiveMaterialReassignment(t *testing.T) {
	mats := []string{"Paint", "Chrome"}
	base := geometrySpec{positions: ramp(20), materials: mats, material: intPtr(0)}
	head := base
	head.material = intPtr(1)

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))

	c := mustChange(t, d, "meshes/Hull/primitives/0/material")
	if got, want := valueOf(c), "Paint → Chrome"; got != want {
		t.Errorf("material = %q, want %q (names, not indices)", got, want)
	}
	// The geometry is untouched, and must not be dragged in.
	if g := findChange(d, "meshes/Hull/primitives/0/geometry"); g != nil {
		t.Errorf("material swap reported a geometry change: %v", g.Children)
	}
	if n := len(mustChange(t, d, "meshes/Hull/primitives/0").Children); n != 1 {
		t.Errorf("material swap reported %d changes on the primitive, want exactly 1", n)
	}
}

func TestDiffPrimitiveMaterialAssignedAndCleared(t *testing.T) {
	none := geometrySpec{positions: ramp(20), materials: []string{"Paint"}}
	set := none
	set.material = intPtr(0)

	assigned := mustChange(t, diffOf(t, geometryGLB(t, none), geometryGLB(t, set)),
		"meshes/Hull/primitives/0/material")
	if got, want := valueOf(assigned), "<none> → Paint"; got != want {
		t.Errorf("assigning a material = %q, want %q", got, want)
	}
	cleared := mustChange(t, diffOf(t, geometryGLB(t, set), geometryGLB(t, none)),
		"meshes/Hull/primitives/0/material")
	if got, want := valueOf(cleared), "Paint → <none>"; got != want {
		t.Errorf("clearing a material = %q, want %q", got, want)
	}
}

// A material index past the end of the array must degrade the way the texture
// descriptors do, not panic.
func TestDiffPrimitiveDanglingMaterialIsReported(t *testing.T) {
	base := geometrySpec{positions: ramp(8), materials: []string{"Paint"}, material: intPtr(0)}
	head := base
	head.material = intPtr(7)

	c := mustChange(t, diffOf(t, geometryGLB(t, base), geometryGLB(t, head)),
		"meshes/Hull/primitives/0/material")
	if !strings.Contains(fmt.Sprint(c.After), "dangling") {
		t.Errorf("dangling material index = %s, want it named as dangling", valueOf(c))
	}
}

// Renaming a material without reassigning it reports on the material itself and
// on every primitive that points at it — the primitive's value *is* the name, so
// this is the documented cost of not using indices.
func TestDiffPrimitiveMaterialRenameIsVisible(t *testing.T) {
	base := geometrySpec{positions: ramp(8), materials: []string{"Paint"}, material: intPtr(0)}
	head := base
	head.materials = []string{"Lacquer"}

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))
	c := mustChange(t, d, "meshes/Hull/primitives/0/material")
	if got, want := valueOf(c), "Paint → Lacquer"; got != want {
		t.Errorf("material = %q, want %q", got, want)
	}
}

// ── streams gained and lost ───────────────────────────────────────────────────

// A fixed list of a few semantics would ignore an edit to the others. Streams
// are taken from the union of both sides, so adding UVs or dropping normals
// reports even though POSITION is untouched.
func TestDiffGeometryStreamGainedOrLost(t *testing.T) {
	pos := ramp(16)
	with := geometrySpec{positions: pos, normals: ramp(16)}
	without := geometrySpec{positions: pos}

	lost := mustChange(t, diffOf(t, geometryGLB(t, with), geometryGLB(t, without)),
		"meshes/Hull/primitives/0/geometry/NORMAL")
	if !strings.Contains(fmt.Sprint(lost.After), noStream) {
		t.Errorf("dropping NORMAL = %s, want the head side marked absent", valueOf(lost))
	}
	gained := mustChange(t, diffOf(t, geometryGLB(t, without), geometryGLB(t, with)),
		"meshes/Hull/primitives/0/geometry/NORMAL")
	if !strings.Contains(fmt.Sprint(gained.Before), noStream) {
		t.Errorf("adding NORMAL = %s, want the base side marked absent", valueOf(gained))
	}
}

// ── interleaved buffers ───────────────────────────────────────────────────────

// With ByteStride set, the accessor's span is stride×(count−1)+element, not
// count×element. Editing the *last* vertex is the case a naive span misses
// outright, so that is the one tested.
func TestDiffGeometryInterleavedLastVertexIsCompared(t *testing.T) {
	pos, nrm := ramp(48), ramp(48)
	base := geometrySpec{positions: pos, normals: nrm, interleaved: true}
	head := base
	head.positions = sculpt(pos, len(pos)-1, 0.5)

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))
	c := mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")
	if c.Before == c.After {
		t.Errorf("interleaved POSITION reported with identical descriptors: %s", valueOf(c))
	}
	// The centroid decode has to step over the interleaved normals; if it read
	// them as positions the shift would come out wrong.
	centroid := mustChange(t, d, "meshes/Hull/primitives/0/centroid")
	if !strings.Contains(fmt.Sprint(centroid.After), "moved 0.010") {
		t.Errorf("interleaved centroid = %s, want a 0.010 shift (0.5 over 48 vertices)", valueOf(centroid))
	}
}

// An interleaved accessor's byte span overlaps its neighbours': POSITION at
// stride 24 and NORMAL at offset 12 in the same view share every byte except the
// first element's and the last's. Comparing those spans whole reports NORMAL as
// changed whenever a position moves — on an optimiser's output, that is every
// stream of every edited primitive, which would bury the one that really changed.
func TestDiffGeometryInterleavedNeighbourStaysQuiet(t *testing.T) {
	pos, nrm := ramp(48), ramp(48)
	base := geometrySpec{positions: pos, normals: nrm, interleaved: true}
	head := base
	head.positions = sculpt(pos, 10, 0.5)

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))
	mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")
	if c := findChange(d, "meshes/Hull/primitives/0/geometry/NORMAL"); c != nil {
		t.Errorf("untouched interleaved NORMAL reported: %s", valueOf(c))
	}
}

// Comparing element by element also makes the buffer layout irrelevant: the same
// vertices tightly packed in one file and interleaved in another are the same
// geometry, and reporting a change there would fire on every file that has been
// through an optimiser without being edited.
func TestDiffGeometryLayoutChangeAloneIsNotAGeometryChange(t *testing.T) {
	pos, nrm := ramp(48), ramp(48)
	packed := geometrySpec{positions: pos, normals: nrm}
	woven := geometrySpec{positions: pos, normals: nrm, interleaved: true}

	d := diffOf(t, geometryGLB(t, packed), geometryGLB(t, woven))
	if c := findChange(d, "meshes/Hull/primitives/0/geometry"); c != nil {
		t.Errorf("re-packing identical vertices reported as a geometry change: %v", c.Children)
	}
}

// ── honesty: data we cannot read is never "unchanged" ─────────────────────────

// externalBufferDoc is a .gltf whose geometry lives in a .bin next to it. There
// is no filesystem to read it from (always the case under wasm, and in the
// subprocess protocol where only the one blob is passed), so qmuntal/gltf leaves
// Buffer.Data nil with a nil error — the exact shape that must not diff as
// "unchanged".
func externalBufferDoc(t *testing.T, uri string, count int) []byte {
	t.Helper()
	return doc(t, map[string]any{
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": []int{0}}},
		"nodes":       []any{map[string]any{"name": "Body", "mesh": 0}},
		"buffers":     []any{map[string]any{"uri": uri, "byteLength": count * 12}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": count * 12}},
		"accessors": []any{map[string]any{
			"bufferView": 0, "componentType": 5126, "count": count, "type": "VEC3",
			"min": []float64{0, 0, 0}, "max": []float64{1, 1, 1},
		}},
		"meshes": []any{map[string]any{"name": "Hull", "primitives": []any{
			map[string]any{"attributes": map[string]any{"POSITION": 0}},
		}}},
	})
}

func TestDiffGeometryExternalBufferIsNotComparable(t *testing.T) {
	// Same vertex count, same everything the JSON can show: the only difference
	// is in bytes nobody can read. Silence here would be a false "unchanged".
	d := diffOf(t, externalBufferDoc(t, "base.bin", 900), externalBufferDoc(t, "head.bin", 900))

	geo := mustChange(t, d, "meshes/Hull/primitives/0/geometry")
	if !strings.Contains(fmt.Sprint(geo.Before), "not comparable") ||
		!strings.Contains(fmt.Sprint(geo.After), "not comparable") {
		t.Fatalf("unreadable geometry = %s, want it reported as not comparable", valueOf(geo))
	}
	// And it must name which stream, not just wave at the primitive.
	pos := mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")
	if !strings.Contains(fmt.Sprint(pos.Before), "<unreadable>") {
		t.Errorf("POSITION = %s, want the unreadable marker the animation streams use", valueOf(pos))
	}
	// Even byte-identical URIs cannot be claimed as unchanged.
	same := diffOf(t, externalBufferDoc(t, "scene.bin", 900), externalBufferDoc(t, "scene.bin", 900))
	if findChange(same, "meshes/Hull/primitives/0/geometry") == nil {
		t.Error("two unreadable sides reported as unchanged; nothing was actually compared")
	}
}

// A sparse accessor stores only the elements that deviate from a base, so its
// byte span is not the vertex data. Reading it as if it were would silently
// compare the wrong bytes.
func TestDiffGeometrySparseAccessorIsNotComparable(t *testing.T) {
	sparseDoc := func(value float64) []byte {
		return doc(t, map[string]any{
			"scene":   0,
			"scenes":  []any{map[string]any{"nodes": []int{0}}},
			"nodes":   []any{map[string]any{"name": "Body", "mesh": 0}},
			"buffers": []any{map[string]any{"byteLength": 48, "uri": "data:application/octet-stream;base64,AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}},
			"bufferViews": []any{
				map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 24},
				map[string]any{"buffer": 0, "byteOffset": 24, "byteLength": 24},
			},
			"accessors": []any{map[string]any{
				"bufferView": 0, "componentType": 5126, "count": 2, "type": "VEC3",
				"min": []float64{0, 0, 0}, "max": []float64{value, value, value},
				"sparse": map[string]any{
					"count":   1,
					"indices": map[string]any{"bufferView": 1, "componentType": 5123},
					"values":  map[string]any{"bufferView": 1},
				},
			}},
			"meshes": []any{map[string]any{"name": "Hull", "primitives": []any{
				map[string]any{"attributes": map[string]any{"POSITION": 0}},
			}}},
		})
	}
	d := diffOf(t, sparseDoc(1), sparseDoc(1))
	geo := mustChange(t, d, "meshes/Hull/primitives/0/geometry")
	if !strings.Contains(fmt.Sprint(geo.After), "not comparable") {
		t.Errorf("sparse geometry = %s, want it reported as not comparable", valueOf(geo))
	}
}

// A POSITION accessor without the required min/max simply gets no bounds row —
// the geometry row above it already says whether the vertices changed, so
// nothing is claimed that was not measured.
func TestDiffGeometryMissingBoundsSkipsTheBoundsRow(t *testing.T) {
	pos := ramp(16)
	base := geometrySpec{positions: pos, omitBounds: true}
	head := base
	head.positions = sculpt(pos, 2, 0.9)

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))
	mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")
	if c := findChange(d, "meshes/Hull/primitives/0/bounds"); c != nil {
		t.Errorf("bounds reported without min/max to report from: %s", valueOf(c))
	}
	// The centroid is decoded, not read from metadata, so it survives.
	mustChange(t, d, "meshes/Hull/primitives/0/centroid")
}

// ── the payload discipline ────────────────────────────────────────────────────

// A StructuredDiff carrying per-vertex data is a category error: measured at 3.2
// MB of JSON for 400k vertices. The change tree gets booleans and metrics; the
// heatmap is issue #46, computed renderer-side.
func TestDiffGeometryCarriesNoPerVertexPayload(t *testing.T) {
	const verts = 200_000
	pos := ramp(verts)
	base := geometrySpec{positions: pos, normals: ramp(verts), indices: seq(verts)}
	head := base
	head.positions = sculpt(pos, verts/2, 1.5)

	d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))
	mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")

	js, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// Generous by three orders of magnitude against a per-vertex payload, tight
	// enough that one could not slip in unnoticed.
	const budget = 4 << 10
	if len(js) > budget {
		t.Errorf("diff of a %d-vertex sculpt is %d bytes of JSON, budget %d", verts, len(js), budget)
	}
	t.Logf("%d-vertex sculpt → %d bytes of StructuredDiff JSON", verts, len(js))

	// No single value may smuggle one in either.
	walk(d.Changes, func(c *DiffChange, _ int) {
		for _, v := range []any{c.Before, c.After} {
			if s := fmt.Sprint(v); len(s) > 200 {
				t.Errorf("change %q carries a %d-byte value", c.Path, len(s))
			}
		}
	})
}

// ── perf guard ────────────────────────────────────────────────────────────────

// The compare has to stay affordable on a real model under the wasm runtime,
// where this same test runs. The fixture is generated rather than committed: a
// multi-megabyte binary in git to prove a timing is a bad trade.
//
// The budget is deliberately loose — it is guarding against an algorithmic
// regression (a per-vertex decode creeping onto the unchanged path, a hash where
// a byte compare belongs), not measuring the machine. The observed number is
// logged so a regression is visible in CI output well before the budget trips.
func TestDiffGeometryLargeModelStaysInBudget(t *testing.T) {
	const verts = 250_000
	pos := ramp(verts)
	base := geometrySpec{positions: pos, normals: ramp(verts), indices: seq(verts)}
	head := base
	head.positions = sculptAll(pos, 0.01)

	baseBlob, headBlob := geometryGLB(t, base), geometryGLB(t, head)
	t.Logf("fixture: %d vertices, %.1f MB per side", verts, float64(len(baseBlob))/(1<<20))

	start := time.Now()
	d := diffOf(t, baseBlob, headBlob)
	elapsed := time.Since(start)

	mustChange(t, d, "meshes/Hull/primitives/0/geometry/POSITION")
	mustChange(t, d, "meshes/Hull/primitives/0/centroid")
	t.Logf("diff of %d vertices took %v", verts, elapsed)

	const budget = 20 * time.Second
	if elapsed > budget {
		t.Errorf("diff took %v, budget %v", elapsed, budget)
	}
}

// The unchanged path is the one that runs on every file in every review, so it
// is benchmarked separately from the changed one: it must not hash.
func BenchmarkDiffGeometryUnchanged(b *testing.B) {
	benchmarkDiffGeometry(b, false)
}

func BenchmarkDiffGeometrySculpted(b *testing.B) {
	benchmarkDiffGeometry(b, true)
}

func benchmarkDiffGeometry(b *testing.B, changed bool) {
	const verts = 250_000
	t := &testing.T{}
	pos := ramp(verts)
	spec := geometrySpec{positions: pos, normals: ramp(verts), indices: seq(verts)}
	headSpec := spec
	if changed {
		headSpec.positions = sculptAll(pos, 0.01)
	}
	baseBlob, headBlob := geometryGLB(t, spec), geometryGLB(t, headSpec)
	h := &Handler{}
	b.ResetTimer()
	for range b.N {
		if _, err := h.Diff(baseBlob, headBlob); err != nil {
			b.Fatal(err)
		}
	}
}
