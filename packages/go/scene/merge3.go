package scene

// The three-way glTF merge, assembled.
//
// glTF elements point at each other by array index — a node at its mesh and
// children, a primitive at its material and accessors, a texture at its image.
// An index is only meaningful in the document it came from, so a merge cannot
// splice elements from two documents into one list and hope the numbers still
// line up. (The merge used to try, and it lost data: meshes and animations were
// always ours, so a geometry edit made only on theirs was silently dropped, and
// a node theirs added kept theirs' indices, pointing at the wrong mesh.)
//
// Instead:
//
//  1. Every element is identified by its key — its name, disambiguated
//     (uniqueKeys) — and compared through a fingerprint that never mentions an
//     index: an accessor is its bytes, a mesh is its accessors' bytes and its
//     material's key, a node is its placement and the keys it refers to. A side
//     that only renumbered things has not changed them.
//  2. Each key is decided three ways, the way git decides a line: unchanged on
//     one side takes the other's; changed alike is either; changed differently
//     keeps ours and is a conflict. A removal against an untouched element is a
//     clean removal; against an edited one, a conflict.
//  3. The merged document is assembled from scratch: each chosen element is
//     copied out of its own side together with what it depends on (materials,
//     textures, images, accessors, the buffer bytes behind them), and every
//     reference is rewritten to the element's place in the result. A reference
//     to something the merge removed brings it back (a mesh still drawn by a
//     node), except where a missing target only means "not any more" (a child,
//     a scene root, an animation channel), which is dropped.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/qmuntal/gltf"
)

type mside int

const (
	sideBase mside = iota
	sideOurs
	sideTheirs
)

// mdoc is one side of a merge: its document, and each collection's keys.
type mdoc struct {
	doc  *gltf.Document
	keys map[string][]string       // collection → key per index
	idx  map[string]map[string]int // collection → key → index
	acc  map[int]string            // accessor fingerprints, memoised
	tex  map[int]string            // texture fingerprints, memoised
}

// The collections a merge decides element by element. Textures, images,
// samplers and accessors are not merged on their own: they come along with the
// material, mesh, skin or animation that uses them.
var mergeCollections = []string{"materials", "meshes", "cameras", "nodes", "skins", "animations", "scenes"}

func newMdoc(doc *gltf.Document) *mdoc {
	if doc == nil {
		doc = &gltf.Document{}
	}
	d := &mdoc{doc: doc, keys: map[string][]string{}, idx: map[string]map[string]int{}, acc: map[int]string{}, tex: map[int]string{}}
	set := func(coll string, keys []string) {
		d.keys[coll] = keys
		m := make(map[string]int, len(keys))
		for i, k := range keys {
			m[k] = i
		}
		d.idx[coll] = m
	}
	set("nodes", uniqueKeys(doc.Nodes, nodeName))
	set("meshes", uniqueKeys(doc.Meshes, meshName))
	set("materials", uniqueKeys(doc.Materials, materialName))
	set("animations", uniqueKeys(doc.Animations, animName))
	set("skins", uniqueKeys(doc.Skins, func(s *gltf.Skin, i int) string { return orIndexed(s.Name, "skin", i) }))
	set("cameras", uniqueKeys(doc.Cameras, func(c *gltf.Camera, i int) string { return orIndexed(c.Name, "camera", i) }))
	set("scenes", uniqueKeys(doc.Scenes, func(s *gltf.Scene, i int) string { return orIndexed(s.Name, "scene", i) }))
	return d
}

func orIndexed(name, kind string, i int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s[%d]", kind, i)
}

func (d *mdoc) has(coll, key string) bool { _, ok := d.idx[coll][key]; return ok }

// keyOf is the key of index i in a collection, "" when out of range.
func (d *mdoc) keyOf(coll string, i *int) string {
	if i == nil || *i < 0 || *i >= len(d.keys[coll]) {
		return ""
	}
	return d.keys[coll][*i]
}

// ── fingerprints ──────────────────────────────────────────────────────────────

func hashOf(parts ...any) string {
	b, _ := json.Marshal(parts)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:12])
}

// bufferViewBytes is a buffer view's slice of its buffer, or an error when the
// bytes aren't there (an external .bin this package was never given).
func (d *mdoc) bufferViewBytes(i int) ([]byte, error) {
	if i < 0 || i >= len(d.doc.BufferViews) {
		return nil, fmt.Errorf("buffer view %d does not exist", i)
	}
	bv := d.doc.BufferViews[i]
	if bv.Buffer < 0 || bv.Buffer >= len(d.doc.Buffers) {
		return nil, fmt.Errorf("buffer view %d names buffer %d, which does not exist", i, bv.Buffer)
	}
	data := d.doc.Buffers[bv.Buffer].Data
	if bv.ByteOffset < 0 || bv.ByteLength < 0 || bv.ByteOffset+bv.ByteLength > len(data) {
		return nil, fmt.Errorf("buffer view %d reaches past its buffer's bytes", i)
	}
	return data[bv.ByteOffset : bv.ByteOffset+bv.ByteLength], nil
}

// accessorFP is an accessor's content: its shape and the bytes of its elements
// (not the layout around them), so the same data reads alike in either file.
func (d *mdoc) accessorFP(i int) string {
	if fp, ok := d.acc[i]; ok {
		return fp
	}
	fp := "missing"
	if i >= 0 && i < len(d.doc.Accessors) {
		a := d.doc.Accessors[i]
		h := sha256.New()
		fmt.Fprintf(h, "%d|%d|%d|%v|%v|%v|", a.ComponentType, a.Type, a.Count, a.Normalized, a.Min, a.Max)
		if a.BufferView != nil {
			if data, err := d.bufferViewBytes(*a.BufferView); err == nil {
				elem := a.ComponentType.ByteSize() * a.Type.Components()
				stride := d.doc.BufferViews[*a.BufferView].ByteStride
				if stride == 0 {
					stride = elem
				}
				for e := 0; e < a.Count; e++ {
					at := a.ByteOffset + e*stride
					if at+elem > len(data) {
						break
					}
					h.Write(data[at : at+elem])
				}
			}
		}
		if a.Sparse != nil {
			fmt.Fprintf(h, "sparse|%d|", a.Sparse.Count)
			if b, err := d.bufferViewBytes(a.Sparse.Indices.BufferView); err == nil {
				h.Write(b)
			}
			if b, err := d.bufferViewBytes(a.Sparse.Values.BufferView); err == nil {
				h.Write(b)
			}
		}
		fp = hex.EncodeToString(h.Sum(nil)[:12])
	}
	d.acc[i] = fp
	return fp
}

// textureFP is a texture's image (bytes or URI) and sampler.
func (d *mdoc) textureFP(i int) string {
	if fp, ok := d.tex[i]; ok {
		return fp
	}
	fp := "missing"
	if i >= 0 && i < len(d.doc.Textures) {
		t := d.doc.Textures[i]
		var img any
		if t.Source != nil && *t.Source >= 0 && *t.Source < len(d.doc.Images) {
			im := d.doc.Images[*t.Source]
			if im.BufferView != nil {
				b, _ := d.bufferViewBytes(*im.BufferView)
				s := sha256.Sum256(b)
				img = []any{im.MimeType, hex.EncodeToString(s[:12])}
			} else {
				img = im.URI
			}
		}
		var smp any
		if t.Sampler != nil && *t.Sampler >= 0 && *t.Sampler < len(d.doc.Samplers) {
			smp = d.doc.Samplers[*t.Sampler]
		}
		fp = hashOf(img, smp, t.Extensions)
	}
	d.tex[i] = fp
	return fp
}

// materialTextureRefs is a material's texture references, by slot.
func materialTextureRefs(m *gltf.Material) map[string]*int {
	slots := map[string]*int{}
	if p := m.PBRMetallicRoughness; p != nil {
		if p.BaseColorTexture != nil {
			slots["baseColor"] = &p.BaseColorTexture.Index
		}
		if p.MetallicRoughnessTexture != nil {
			slots["metallicRoughness"] = &p.MetallicRoughnessTexture.Index
		}
	}
	if m.NormalTexture != nil && m.NormalTexture.Index != nil {
		slots["normal"] = m.NormalTexture.Index
	}
	if m.OcclusionTexture != nil && m.OcclusionTexture.Index != nil {
		slots["occlusion"] = m.OcclusionTexture.Index
	}
	if m.EmissiveTexture != nil {
		slots["emissive"] = &m.EmissiveTexture.Index
	}
	return slots
}

func (d *mdoc) materialTexturesFP(m *gltf.Material) string {
	slots := materialTextureRefs(m)
	out := map[string]string{}
	for k, i := range slots {
		out[k] = d.textureFP(*i)
	}
	return hashOf(out)
}

// fp is an element's index-free fingerprint.
func (d *mdoc) fp(coll, key string) string {
	i, ok := d.idx[coll][key]
	if !ok {
		return ""
	}
	switch coll {
	case "materials":
		m := cloneMaterialDeep(d.doc.Materials[i])
		tex := d.materialTexturesFP(m)
		for _, slot := range materialTextureRefs(m) {
			*slot = 0
		}
		return hashOf(m, tex)
	case "meshes":
		m := d.doc.Meshes[i]
		type prim struct {
			Mode       gltf.PrimitiveMode
			Attributes map[string]string
			Indices    string
			Material   string
			Targets    []map[string]string
			Extensions gltf.Extensions
			Extras     any
		}
		attrs := func(a gltf.PrimitiveAttributes) map[string]string {
			out := make(map[string]string, len(a))
			for k, v := range a {
				out[k] = d.accessorFP(v)
			}
			return out
		}
		prims := make([]prim, len(m.Primitives))
		for p, pr := range m.Primitives {
			prims[p] = prim{Mode: pr.Mode, Attributes: attrs(pr.Attributes), Material: d.keyOf("materials", pr.Material), Extensions: pr.Extensions, Extras: pr.Extras}
			if pr.Indices != nil {
				prims[p].Indices = d.accessorFP(*pr.Indices)
			}
			for _, t := range pr.Targets {
				prims[p].Targets = append(prims[p].Targets, attrs(t))
			}
		}
		return hashOf(prims, m.Weights, m.Extensions, m.Extras)
	case "nodes":
		return hashOf(d.nodeSpec(key))
	case "skins":
		s := d.doc.Skins[i]
		joints := make([]string, len(s.Joints))
		for j, n := range s.Joints {
			joints[j] = d.keyOf("nodes", &n)
		}
		ibm := ""
		if s.InverseBindMatrices != nil {
			ibm = d.accessorFP(*s.InverseBindMatrices)
		}
		return hashOf(joints, d.keyOf("nodes", s.Skeleton), ibm, s.Extensions, s.Extras)
	case "cameras":
		return hashOf(d.doc.Cameras[i])
	case "animations":
		a := d.doc.Animations[i]
		type ch struct {
			Node, Path, Interp, In, Out string
		}
		var chans []ch
		for _, c := range a.Channels {
			x := ch{Node: d.keyOf("nodes", c.Target.Node), Path: c.Target.Path.String()}
			if c.Sampler >= 0 && c.Sampler < len(a.Samplers) {
				s := a.Samplers[c.Sampler]
				x.Interp, x.In, x.Out = s.Interpolation.String(), d.accessorFP(s.Input), d.accessorFP(s.Output)
			}
			chans = append(chans, x)
		}
		return hashOf(chans, a.Extensions, a.Extras)
	case "scenes":
		s := d.doc.Scenes[i]
		roots := make([]string, len(s.Nodes))
		for r, n := range s.Nodes {
			roots[r] = d.keyOf("nodes", &n)
		}
		return hashOf(roots, s.Extensions, s.Extras)
	}
	return ""
}

// ── nodes and scenes: merged field by field ──────────────────────────────────

// keyRef is a reference by key, with the side whose collection it names — the
// side an element is brought back from when the merge removed it.
type keyRef struct {
	Key  string
	Side mside `json:"-"`
}

// nodeSpec is a node with its references as keys: what the merge decides, and
// what the assembler turns back into a node.
type nodeSpec struct {
	From        mside `json:"-"` // whose name, extensions and extras it carries
	Translation [3]float64
	Rotation    [4]float64
	Scale       [3]float64
	Matrix      [16]float64
	Weights     []float64
	Mesh        keyRef
	Skin        keyRef
	Camera      keyRef
	Children    []string
}

func (d *mdoc) nodeSpecSide(key string, side mside) *nodeSpec {
	i, ok := d.idx["nodes"][key]
	if !ok {
		return nil
	}
	n := d.doc.Nodes[i]
	s := &nodeSpec{
		From:        side,
		Translation: n.TranslationOrDefault(),
		Rotation:    n.RotationOrDefault(),
		Scale:       n.ScaleOrDefault(),
		Matrix:      n.MatrixOrDefault(),
		Weights:     n.Weights,
		Mesh:        keyRef{d.keyOf("meshes", n.Mesh), side},
		Skin:        keyRef{d.keyOf("skins", n.Skin), side},
		Camera:      keyRef{d.keyOf("cameras", n.Camera), side},
	}
	for _, c := range n.Children {
		if k := d.keyOf("nodes", &c); k != "" {
			s.Children = append(s.Children, k)
		}
	}
	return s
}

func (d *mdoc) nodeSpec(key string) *nodeSpec { return d.nodeSpecSide(key, sideOurs) }

// mergeKeySet merges two edits of a set of keys against their base: ours, plus
// what theirs added, minus what theirs removed. Order follows ours.
func mergeKeySet(base, ours, theirs []string) []string {
	out := slices.Clone(ours)
	for _, k := range theirs {
		if !slices.Contains(base, k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, k := range base {
		if !slices.Contains(theirs, k) {
			out = slices.DeleteFunc(out, func(x string) bool { return x == k })
		}
	}
	return out
}

// mergeNodeSpecs merges a node both sides changed, field by field.
func mergeNodeSpecs(key string, b, o, t *nodeSpec, conflicts *[]SemanticConflict) *nodeSpec {
	out := *o
	out.Children = mergeKeySet(b.Children, o.Children, t.Children)
	field := func(name string, bv, ov, tv any, take func(), show func(any) string) {
		bj, oj, tj := hashOf(bv), hashOf(ov), hashOf(tv)
		switch {
		case oj == tj, tj == bj:
		case oj == bj:
			take()
		default:
			*conflicts = append(*conflicts, SemanticConflict{Path: joinPath("nodes", key, name), Ours: show(ov), Theirs: show(tv)})
		}
	}
	vec3 := func(f func([3]float64) [3]float64, fm func([3]float64) string) func(any) string {
		return func(v any) string { return fm(f(v.([3]float64))) }
	}
	field("translation", b.Translation, o.Translation, t.Translation, func() { out.Translation = t.Translation }, vec3(blenderTranslation, fmtLenVec3))
	field("rotation", b.Rotation, o.Rotation, t.Rotation, func() { out.Rotation = t.Rotation }, func(v any) string { return fmtRot(v.([4]float64)) })
	field("scale", b.Scale, o.Scale, t.Scale, func() { out.Scale = t.Scale }, vec3(blenderScale, fmtVec3))
	field("matrix", b.Matrix, o.Matrix, t.Matrix, func() { out.Matrix = t.Matrix }, func(v any) string { return fmtMat(v.([16]float64)) })
	field("weights", b.Weights, o.Weights, t.Weights, func() { out.Weights = t.Weights }, func(v any) string { return fmt.Sprint(v) })
	ref := func(v any) string {
		if k := v.(keyRef).Key; k != "" {
			return k
		}
		return "<none>"
	}
	field("mesh", b.Mesh, o.Mesh, t.Mesh, func() { out.Mesh = t.Mesh }, ref)
	field("skin", b.Skin, o.Skin, t.Skin, func() { out.Skin = t.Skin }, ref)
	field("camera", b.Camera, o.Camera, t.Camera, func() { out.Camera = t.Camera }, ref)
	return &out
}

// sceneSpec is a scene's roots as keys.
type sceneSpec struct {
	From  mside
	Roots []string
}

func (d *mdoc) sceneSpec(key string, side mside) *sceneSpec {
	i, ok := d.idx["scenes"][key]
	if !ok {
		return nil
	}
	s := &sceneSpec{From: side}
	for _, n := range d.doc.Scenes[i].Nodes {
		if k := d.keyOf("nodes", &n); k != "" {
			s.Roots = append(s.Roots, k)
		}
	}
	return s
}

// ── the plan ──────────────────────────────────────────────────────────────────

// choice is one element of the result: the side it is copied from, or — for a
// node, scene or material merged field by field — the merged element itself.
type choice struct {
	Key      string
	Side     mside
	Node     *nodeSpec
	Scene    *sceneSpec
	Material *gltf.Material // copied from Side, with merged scalar properties
}

type mergePlan struct {
	sides map[mside]*mdoc
	order map[string][]*choice
}

func (p *mergePlan) find(coll, key string) *choice {
	for _, c := range p.order[coll] {
		if c.Key == key {
			return c
		}
	}
	return nil
}

// decide makes the three-way call for every key of every collection.
func decide(base, ours, theirs *mdoc, conflicts *[]SemanticConflict) *mergePlan {
	p := &mergePlan{sides: map[mside]*mdoc{sideBase: base, sideOurs: ours, sideTheirs: theirs}, order: map[string][]*choice{}}
	for _, coll := range mergeCollections {
		for _, key := range mergeKeyOrder(ours.keys[coll], theirs.keys[coll]) {
			inB, inO, inT := base.has(coll, key), ours.has(coll, key), theirs.has(coll, key)
			fb, fo, ft := base.fp(coll, key), ours.fp(coll, key), theirs.fp(coll, key)
			var c *choice
			switch {
			case inO && inT:
				switch {
				case fo == ft || (inB && ft == fb):
					c = &choice{Key: key, Side: sideOurs}
				case inB && fo == fb:
					c = &choice{Key: key, Side: sideTheirs}
				default:
					c = mergeBothChanged(coll, key, inB, base, ours, theirs, conflicts)
				}
			case inO:
				if !inB {
					c = &choice{Key: key, Side: sideOurs} // ours added it
				} else if fo != fb {
					c = &choice{Key: key, Side: sideOurs}
					*conflicts = append(*conflicts, SemanticConflict{Path: joinPath(coll, key), Ours: "kept", Theirs: "removed"})
				} // else: theirs removed what ours left alone — gone
			case inT:
				if !inB {
					c = &choice{Key: key, Side: sideTheirs} // theirs added it
				} else if ft != fb {
					*conflicts = append(*conflicts, SemanticConflict{Path: joinPath(coll, key), Ours: "removed", Theirs: "kept"})
				} // else: ours removed what theirs left alone — gone
			}
			if c != nil {
				p.order[coll] = append(p.order[coll], c)
			}
		}
	}
	return p
}

// mergeBothChanged is a key both sides changed, differently.
func mergeBothChanged(coll, key string, inB bool, base, ours, theirs *mdoc, conflicts *[]SemanticConflict) *choice {
	if inB {
		switch coll {
		case "nodes":
			n := mergeNodeSpecs(key, base.nodeSpecSide(key, sideBase), ours.nodeSpecSide(key, sideOurs), theirs.nodeSpecSide(key, sideTheirs), conflicts)
			return &choice{Key: key, Side: sideOurs, Node: n}
		case "scenes":
			b, o, t := base.sceneSpec(key, sideBase), ours.sceneSpec(key, sideOurs), theirs.sceneSpec(key, sideTheirs)
			return &choice{Key: key, Side: sideOurs, Scene: &sceneSpec{From: sideOurs, Roots: mergeKeySet(b.Roots, o.Roots, t.Roots)}}
		case "materials":
			bm := base.doc.Materials[base.idx[coll][key]]
			om := ours.doc.Materials[ours.idx[coll][key]]
			tm := theirs.doc.Materials[theirs.idx[coll][key]]
			// Scalar properties merge one by one; textures only when theirs left
			// them as they were (else the textures would come from two files).
			if theirs.materialTexturesFP(tm) == base.materialTexturesFP(bm) {
				return &choice{Key: key, Side: sideOurs, Material: merge3Material(bm, om, tm, key, conflicts)}
			}
		}
	}
	*conflicts = append(*conflicts, SemanticConflict{Path: joinPath(coll, key), Ours: summary(ours, coll, key), Theirs: summary(theirs, coll, key)})
	return &choice{Key: key, Side: sideOurs}
}

func summary(d *mdoc, coll, key string) string {
	i := d.idx[coll][key]
	switch coll {
	case "meshes":
		return fmt.Sprintf("%d primitives", len(d.doc.Meshes[i].Primitives))
	case "animations":
		return fmt.Sprintf("%d channels", len(d.doc.Animations[i].Channels))
	case "skins":
		return fmt.Sprintf("%d joints", len(d.doc.Skins[i].Joints))
	}
	return "changed"
}

// close brings back what a chosen element still refers to: a mesh a node draws,
// a material a mesh uses, a joint a skin moves. What a missing reference only
// means "not any more" — a child, a scene root, an animation target — is not.
func (p *mergePlan) close() {
	for changed := true; changed; {
		changed = false
		need := func(coll string, r keyRef) {
			if r.Key == "" || p.find(coll, r.Key) != nil || !p.sides[r.Side].has(coll, r.Key) {
				return
			}
			p.order[coll] = append(p.order[coll], &choice{Key: r.Key, Side: r.Side})
			changed = true
		}
		for _, c := range slices.Clone(p.order["nodes"]) {
			n := c.Node
			if n == nil {
				n = p.sides[c.Side].nodeSpecSide(c.Key, c.Side)
			}
			need("meshes", n.Mesh)
			need("skins", n.Skin)
			need("cameras", n.Camera)
		}
		for _, c := range slices.Clone(p.order["meshes"]) {
			d := p.sides[c.Side]
			for _, pr := range d.doc.Meshes[d.idx["meshes"][c.Key]].Primitives {
				need("materials", keyRef{d.keyOf("materials", pr.Material), c.Side})
			}
		}
		for _, c := range slices.Clone(p.order["skins"]) {
			d := p.sides[c.Side]
			s := d.doc.Skins[d.idx["skins"][c.Key]]
			for _, j := range s.Joints {
				need("nodes", keyRef{d.keyOf("nodes", &j), c.Side})
			}
			need("nodes", keyRef{d.keyOf("nodes", s.Skeleton), c.Side})
		}
	}
}

// ── assembly ──────────────────────────────────────────────────────────────────

type assembler struct {
	p      *mergePlan
	out    *gltf.Document
	bin    bytes.Buffer
	index  map[string]map[string]int // collection → key → index in out
	bv     map[mside]map[int]int
	acc    map[mside]map[int]int
	img    map[mside]map[int]int
	smp    map[mside]map[int]int
	tex    map[mside]map[int]int
	failed error
}

func (a *assembler) memo(m map[mside]map[int]int, s mside, i int, build func() int) int {
	if m[s] == nil {
		m[s] = map[int]int{}
	}
	if j, ok := m[s][i]; ok {
		return j
	}
	j := build()
	m[s][i] = j
	return j
}

func (a *assembler) bufferView(s mside, i int) int {
	return a.memo(a.bv, s, i, func() int {
		d := a.p.sides[s]
		data, err := d.bufferViewBytes(i)
		if err != nil && a.failed == nil {
			a.failed = err
		}
		for a.bin.Len()%4 != 0 {
			a.bin.WriteByte(0)
		}
		src := d.doc.BufferViews[i]
		bv := *src
		bv.Buffer, bv.ByteOffset, bv.ByteLength = 0, a.bin.Len(), len(data)
		a.bin.Write(data)
		a.out.BufferViews = append(a.out.BufferViews, &bv)
		return len(a.out.BufferViews) - 1
	})
}

func (a *assembler) accessor(s mside, i int) int {
	return a.memo(a.acc, s, i, func() int {
		d := a.p.sides[s]
		if i < 0 || i >= len(d.doc.Accessors) {
			if a.failed == nil {
				a.failed = fmt.Errorf("accessor %d does not exist", i)
			}
			a.out.Accessors = append(a.out.Accessors, &gltf.Accessor{})
			return len(a.out.Accessors) - 1
		}
		acc := *d.doc.Accessors[i]
		if acc.BufferView != nil {
			acc.BufferView = gltf.Index(a.bufferView(s, *acc.BufferView))
		}
		if acc.Sparse != nil {
			sp := *acc.Sparse
			sp.Indices.BufferView = a.bufferView(s, sp.Indices.BufferView)
			sp.Values.BufferView = a.bufferView(s, sp.Values.BufferView)
			acc.Sparse = &sp
		}
		a.out.Accessors = append(a.out.Accessors, &acc)
		return len(a.out.Accessors) - 1
	})
}

func (a *assembler) texture(s mside, i int) int {
	return a.memo(a.tex, s, i, func() int {
		d := a.p.sides[s]
		if i < 0 || i >= len(d.doc.Textures) {
			if a.failed == nil {
				a.failed = fmt.Errorf("texture %d does not exist", i)
			}
			a.out.Textures = append(a.out.Textures, &gltf.Texture{})
			return len(a.out.Textures) - 1
		}
		t := *d.doc.Textures[i]
		if t.Source != nil && *t.Source >= 0 && *t.Source < len(d.doc.Images) {
			src := *t.Source
			t.Source = gltf.Index(a.memo(a.img, s, src, func() int {
				im := *d.doc.Images[src]
				if im.BufferView != nil {
					im.BufferView = gltf.Index(a.bufferView(s, *im.BufferView))
				}
				a.out.Images = append(a.out.Images, &im)
				return len(a.out.Images) - 1
			}))
		}
		if t.Sampler != nil && *t.Sampler >= 0 && *t.Sampler < len(d.doc.Samplers) {
			src := *t.Sampler
			t.Sampler = gltf.Index(a.memo(a.smp, s, src, func() int {
				sm := *d.doc.Samplers[src]
				a.out.Samplers = append(a.out.Samplers, &sm)
				return len(a.out.Samplers) - 1
			}))
		}
		a.out.Textures = append(a.out.Textures, &t)
		return len(a.out.Textures) - 1
	})
}

// ref turns a key into its index in the result, nil when it isn't there.
func (a *assembler) ref(coll, key string) *int {
	if key == "" {
		return nil
	}
	if i, ok := a.index[coll][key]; ok {
		return gltf.Index(i)
	}
	return nil
}

func cloneMaterialDeep(m *gltf.Material) *gltf.Material {
	b, _ := json.Marshal(m)
	var c gltf.Material
	_ = json.Unmarshal(b, &c)
	if c.Name == "" {
		c.Name = m.Name
	}
	return &c
}

// assemble builds the merged document from the plan. `like` is the side whose
// document-level settings (asset, extensions, extras) the result keeps.
func (p *mergePlan) assemble(like mside) (*gltf.Document, error) {
	src := p.sides[like].doc
	a := &assembler{
		p: p,
		out: &gltf.Document{
			Asset:      src.Asset,
			Extensions: src.Extensions,
			Extras:     src.Extras,
		},
		index: map[string]map[string]int{},
		bv:    map[mside]map[int]int{}, acc: map[mside]map[int]int{}, img: map[mside]map[int]int{},
		smp: map[mside]map[int]int{}, tex: map[mside]map[int]int{},
	}
	for _, s := range []mside{sideOurs, sideTheirs} {
		for _, e := range p.sides[s].doc.ExtensionsUsed {
			if !slices.Contains(a.out.ExtensionsUsed, e) {
				a.out.ExtensionsUsed = append(a.out.ExtensionsUsed, e)
			}
		}
		for _, e := range p.sides[s].doc.ExtensionsRequired {
			if !slices.Contains(a.out.ExtensionsRequired, e) {
				a.out.ExtensionsRequired = append(a.out.ExtensionsRequired, e)
			}
		}
	}
	for _, coll := range mergeCollections {
		a.index[coll] = map[string]int{}
		for i, c := range p.order[coll] {
			a.index[coll][c.Key] = i
		}
	}

	for _, c := range p.order["materials"] {
		d := p.sides[c.Side]
		m := c.Material
		if m == nil {
			m = d.doc.Materials[d.idx["materials"][c.Key]]
		}
		m = cloneMaterialDeep(m)
		for _, slot := range materialTextureRefs(m) {
			*slot = a.texture(c.Side, *slot)
		}
		a.out.Materials = append(a.out.Materials, m)
	}
	for _, c := range p.order["meshes"] {
		d := p.sides[c.Side]
		src := d.doc.Meshes[d.idx["meshes"][c.Key]]
		m := *src
		m.Primitives = nil
		for _, sp := range src.Primitives {
			pr := *sp
			pr.Attributes = make(gltf.PrimitiveAttributes, len(sp.Attributes))
			for k, v := range sp.Attributes {
				pr.Attributes[k] = a.accessor(c.Side, v)
			}
			if sp.Indices != nil {
				pr.Indices = gltf.Index(a.accessor(c.Side, *sp.Indices))
			}
			pr.Material = a.ref("materials", d.keyOf("materials", sp.Material))
			pr.Targets = nil
			for _, t := range sp.Targets {
				nt := make(gltf.PrimitiveAttributes, len(t))
				for k, v := range t {
					nt[k] = a.accessor(c.Side, v)
				}
				pr.Targets = append(pr.Targets, nt)
			}
			m.Primitives = append(m.Primitives, &pr)
		}
		a.out.Meshes = append(a.out.Meshes, &m)
	}
	for _, c := range p.order["cameras"] {
		d := p.sides[c.Side]
		cam := *d.doc.Cameras[d.idx["cameras"][c.Key]]
		a.out.Cameras = append(a.out.Cameras, &cam)
	}
	for _, c := range p.order["nodes"] {
		spec := c.Node
		if spec == nil {
			spec = p.sides[c.Side].nodeSpecSide(c.Key, c.Side)
		}
		d := p.sides[spec.From]
		n := &gltf.Node{}
		if i, ok := d.idx["nodes"][c.Key]; ok {
			src := d.doc.Nodes[i]
			n.Name, n.Extensions, n.Extras = src.Name, src.Extensions, src.Extras
		}
		if spec.Matrix != [16]float64(identity4) {
			n.Matrix = spec.Matrix
		} else {
			n.Translation, n.Rotation, n.Scale = spec.Translation, spec.Rotation, spec.Scale
		}
		n.Weights = spec.Weights
		n.Mesh = a.ref("meshes", spec.Mesh.Key)
		n.Skin = a.ref("skins", spec.Skin.Key)
		n.Camera = a.ref("cameras", spec.Camera.Key)
		for _, k := range spec.Children {
			if i := a.ref("nodes", k); i != nil {
				n.Children = append(n.Children, *i)
			}
		}
		a.out.Nodes = append(a.out.Nodes, n)
	}
	for _, c := range p.order["skins"] {
		d := p.sides[c.Side]
		s := *d.doc.Skins[d.idx["skins"][c.Key]]
		joints := s.Joints
		s.Joints = nil
		for _, j := range joints {
			if i := a.ref("nodes", d.keyOf("nodes", &j)); i != nil {
				s.Joints = append(s.Joints, *i)
			}
		}
		s.Skeleton = a.ref("nodes", d.keyOf("nodes", s.Skeleton))
		if s.InverseBindMatrices != nil {
			s.InverseBindMatrices = gltf.Index(a.accessor(c.Side, *s.InverseBindMatrices))
		}
		a.out.Skins = append(a.out.Skins, &s)
	}
	for _, c := range p.order["animations"] {
		d := p.sides[c.Side]
		src := d.doc.Animations[d.idx["animations"][c.Key]]
		an := *src
		an.Samplers, an.Channels = nil, nil
		samplerAt := map[int]int{}
		for _, ch := range src.Channels {
			target := a.ref("nodes", d.keyOf("nodes", ch.Target.Node))
			if target == nil || ch.Sampler < 0 || ch.Sampler >= len(src.Samplers) {
				continue // its node is gone: the channel goes with it
			}
			si, ok := samplerAt[ch.Sampler]
			if !ok {
				s := *src.Samplers[ch.Sampler]
				s.Input, s.Output = a.accessor(c.Side, s.Input), a.accessor(c.Side, s.Output)
				an.Samplers = append(an.Samplers, &s)
				si = len(an.Samplers) - 1
				samplerAt[ch.Sampler] = si
			}
			nc := *ch
			nc.Sampler = si
			nc.Target.Node = target
			an.Channels = append(an.Channels, &nc)
		}
		if len(an.Channels) > 0 {
			a.out.Animations = append(a.out.Animations, &an)
		}
	}
	for _, c := range p.order["scenes"] {
		spec := c.Scene
		if spec == nil {
			spec = p.sides[c.Side].sceneSpec(c.Key, c.Side)
		}
		d := p.sides[spec.From]
		sc := &gltf.Scene{}
		if i, ok := d.idx["scenes"][c.Key]; ok {
			src := d.doc.Scenes[i]
			sc.Name, sc.Extensions, sc.Extras = src.Name, src.Extensions, src.Extras
		}
		for _, k := range spec.Roots {
			if i := a.ref("nodes", k); i != nil {
				sc.Nodes = append(sc.Nodes, *i)
			}
		}
		a.out.Scenes = append(a.out.Scenes, sc)
	}
	if src.Scene != nil {
		a.out.Scene = a.ref("scenes", p.sides[like].keyOf("scenes", src.Scene))
	}
	if a.out.Scene == nil && len(a.out.Scenes) > 0 {
		a.out.Scene = gltf.Index(0)
	}
	if a.bin.Len() > 0 {
		for a.bin.Len()%4 != 0 {
			a.bin.WriteByte(0)
		}
		a.out.Buffers = []*gltf.Buffer{{ByteLength: a.bin.Len(), Data: a.bin.Bytes()}}
	}
	return a.out, a.failed
}

// docFP is a whole document's index-free fingerprint: two files that differ only
// in how they number things read alike.
func docFP(d *mdoc) string {
	var parts []string
	for _, coll := range mergeCollections {
		for _, k := range d.keys[coll] {
			parts = append(parts, coll+"/"+k+"="+d.fp(coll, k))
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(parts)))
	return hex.EncodeToString(append(sum[:], n[:]...))
}

// ── hierarchy ─────────────────────────────────────────────────────────────────

// parentOf is a node's parent key in a side, "" for a root or orphan.
func (d *mdoc) parentOf(key string) string {
	i, ok := d.idx["nodes"][key]
	if !ok {
		return ""
	}
	for p, n := range d.doc.Nodes {
		if slices.Contains(n.Children, i) {
			return d.keys["nodes"][p]
		}
	}
	return ""
}

// isRoot reports whether a side has the node at the root of one of its scenes,
// and which scene.
func (d *mdoc) rootScene(key string) string {
	i, ok := d.idx["nodes"][key]
	if !ok {
		return ""
	}
	for s, sc := range d.doc.Scenes {
		if slices.Contains(sc.Nodes, i) {
			return d.keys["scenes"][s]
		}
	}
	return ""
}

// fixHierarchy keeps the merged scene graph a tree. Every node gets at most one
// parent (both sides moving a node to different parents keeps ours and is a
// conflict), and a node kept against the other side's deletion of it — whose
// parent, merged, no longer lists it — goes back where its own side had it.
func (p *mergePlan) fixHierarchy(conflicts *[]SemanticConflict) {
	for _, c := range p.order["nodes"] {
		if c.Node == nil {
			c.Node = p.sides[c.Side].nodeSpecSide(c.Key, c.Side)
		}
	}
	for _, c := range p.order["scenes"] {
		if c.Scene == nil {
			c.Scene = p.sides[c.Side].sceneSpec(c.Key, c.Side)
		}
	}
	ours, theirs := p.sides[sideOurs], p.sides[sideTheirs]
	for _, c := range p.order["nodes"] {
		k := c.Key
		var parents []*choice
		for _, pc := range p.order["nodes"] {
			if slices.Contains(pc.Node.Children, k) {
				parents = append(parents, pc)
			}
		}
		if len(parents) > 1 {
			keep := parents[0]
			if want := ours.parentOf(k); want != "" {
				for _, pc := range parents {
					if pc.Key == want {
						keep = pc
					}
				}
			}
			for _, pc := range parents {
				if pc != keep {
					pc.Node.Children = slices.DeleteFunc(slices.Clone(pc.Node.Children), func(x string) bool { return x == k })
				}
			}
			if conflicts != nil {
				show := func(s string) string {
					if s == "" {
						return rootParentLabel
					}
					return s
				}
				*conflicts = append(*conflicts, SemanticConflict{Path: joinPath("nodes", k, "parent"), Ours: show(ours.parentOf(k)), Theirs: show(theirs.parentOf(k))})
			}
			continue
		}
		if len(parents) == 1 {
			continue
		}
		rooted := false
		for _, sc := range p.order["scenes"] {
			if slices.Contains(sc.Scene.Roots, k) {
				rooted = true
			}
		}
		// Only a node one side deleted outright is put back: there, the other
		// side's parent no longer listing it says nothing about where it goes. A
		// node both sides have and one of them detached stays detached.
		if rooted || (ours.has("nodes", k) && theirs.has("nodes", k)) {
			continue
		}
		src := p.sides[c.Node.From]
		if parent := src.parentOf(k); parent != "" {
			if pc := p.find("nodes", parent); pc != nil {
				pc.Node.Children = append(slices.Clone(pc.Node.Children), k)
			}
		} else if scene := src.rootScene(k); scene != "" {
			if sc := p.find("scenes", scene); sc != nil {
				sc.Scene.Roots = append(slices.Clone(sc.Scene.Roots), k)
			}
		}
	}
}

// ── entry points ──────────────────────────────────────────────────────────────

// Merge performs a 3-way semantic merge of glTF/GLB blobs, element by element
// (see the top of this file): what only one side changed is taken from it, what
// both changed alike is either, what both changed differently keeps ours and is
// a conflict — a node's translation, rotation, scale, mesh or parent, a
// material's factors, or a whole mesh, animation, skin or camera.
func (h *Handler) Merge(base, ours, theirs Blob) (Blob, *ConflictInfo, error) {
	if len(base) == 0 {
		ci := &ConflictInfo{
			Conflicts: []SemanticConflict{{
				Path: "file", Ours: "created", Theirs: "created",
			}},
		}
		return ours, ci, nil
	}
	docBase, err := parseDoc(base)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing base: %w", err)
	}
	docOurs, err := parseDoc(ours)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing ours: %w", err)
	}
	docTheirs, err := parseDoc(theirs)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing theirs: %w", err)
	}
	// One form for every placement, so a side that moved a matrix-placed node is
	// merged like one that moved a TRS node.
	normalizeTransforms(docBase)
	normalizeTransforms(docOurs)
	normalizeTransforms(docTheirs)
	b, o, t := newMdoc(docBase), newMdoc(docOurs), newMdoc(docTheirs)

	// One side changed nothing: the other is the merge, byte for byte.
	switch fb := docFP(b); fb {
	case docFP(t):
		return ours, nil, nil
	case docFP(o):
		return theirs, nil, nil
	}

	var conflicts []SemanticConflict
	plan := decide(b, o, t, &conflicts)
	plan.close()
	plan.fixHierarchy(&conflicts)
	doc, err := plan.assemble(sideOurs)
	if err != nil {
		return nil, nil, fmt.Errorf("assembling the merge: %w", err)
	}
	result, err := encodeBlob(doc, isGLB(ours))
	if err != nil {
		return nil, nil, fmt.Errorf("encoding merged glTF: %w", err)
	}
	var ci *ConflictInfo
	if len(conflicts) > 0 {
		ci = &ConflictInfo{Conflicts: conflicts}
	}
	return result, ci, nil
}

// takeableNodeFields are the node properties a choice can take from theirs.
var takeableNodeFields = []string{"translation", "rotation", "scale", "matrix", "weights", "mesh", "skin", "camera", "children", "parent"}

// takeableMaterialFields are the material properties a choice can take from theirs.
var takeableMaterialFields = []string{"baseColorFactor", "metallicFactor", "roughnessFactor", "alphaMode", "doubleSided"}

// ApplyChoices takes theirs for each conflict path in takePaths, on top of the
// merged document (ours at every conflict) — what `forge mergetool` calls once
// the reviewer has picked per conflict.
//
// A path is a whole element ("nodes/Wheel", "meshes/Body", "materials/Paint",
// "animations/Spin", "skins/…", "cameras/…") — taken from theirs, or removed
// when theirs has none — or one property of a node (its translation, rotation,
// scale, matrix, weights, mesh, skin, camera, children or parent) or of a
// material (its factors, alpha mode, double-sidedness). The result is assembled
// like a merge, so whatever a taken element refers to comes with it. Any other
// path is refused with an error naming it: a choice is never silently skipped.
func (h *Handler) ApplyChoices(merged, theirs Blob, takePaths []string) (Blob, error) {
	if len(takePaths) == 0 {
		return merged, nil
	}
	for _, p := range takePaths {
		if p == "file" {
			return theirs, nil
		}
	}
	docM, err := parseDoc(merged)
	if err != nil {
		return nil, fmt.Errorf("parsing merged: %w", err)
	}
	docT, err := parseDoc(theirs)
	if err != nil {
		return nil, fmt.Errorf("parsing theirs: %w", err)
	}
	normalizeTransforms(docM)
	normalizeTransforms(docT)
	m, t := newMdoc(docM), newMdoc(docT)
	plan := &mergePlan{sides: map[mside]*mdoc{sideBase: newMdoc(nil), sideOurs: m, sideTheirs: t}, order: map[string][]*choice{}}
	for _, coll := range mergeCollections {
		for _, k := range m.keys[coll] {
			plan.order[coll] = append(plan.order[coll], &choice{Key: k, Side: sideOurs})
		}
	}
	plan.fixHierarchy(nil) // specs for every node and scene, so fields can be set

	for _, path := range takePaths {
		if err := plan.take(path); err != nil {
			return nil, err
		}
	}
	plan.close()
	plan.fixHierarchy(nil)
	doc, err := plan.assemble(sideOurs)
	if err != nil {
		return nil, fmt.Errorf("assembling the choices: %w", err)
	}
	return encodeBlob(doc, isGLB(merged))
}

// take applies one choice to the plan.
func (p *mergePlan) take(path string) error {
	parts := splitPath(path)
	ours, theirs := p.sides[sideOurs], p.sides[sideTheirs]
	if len(parts) < 2 || !slices.Contains(mergeCollections, parts[0]) || parts[0] == "scenes" {
		return fmt.Errorf("unknown conflict path %q", path)
	}
	coll, key := parts[0], parts[1]
	if len(parts) == 2 {
		c := p.find(coll, key)
		if !theirs.has(coll, key) {
			p.order[coll] = slices.DeleteFunc(p.order[coll], func(x *choice) bool { return x.Key == key })
			return nil
		}
		taken := &choice{Key: key, Side: sideTheirs}
		if coll == "nodes" {
			taken.Node = theirs.nodeSpecSide(key, sideTheirs)
		}
		if c != nil {
			*c = *taken
		} else {
			p.order[coll] = append(p.order[coll], taken)
		}
		return nil
	}
	field := parts[2]
	if len(parts) != 3 || !ours.has(coll, key) || !theirs.has(coll, key) {
		return fmt.Errorf("conflict path %q names an element missing from one side", path)
	}
	c := p.find(coll, key)
	if c == nil {
		return fmt.Errorf("conflict path %q names an element the merge does not hold", path)
	}
	switch coll {
	case "nodes":
		if !slices.Contains(takeableNodeFields, field) {
			return fmt.Errorf("taking %s from theirs is not supported — resolve it in your editor", path)
		}
		ts := theirs.nodeSpecSide(key, sideTheirs)
		n := c.Node
		switch field {
		case "translation":
			n.Translation = ts.Translation
		case "rotation":
			n.Rotation = ts.Rotation
		case "scale":
			n.Scale = ts.Scale
		case "matrix":
			n.Matrix = ts.Matrix
		case "weights":
			n.Weights = ts.Weights
		case "mesh":
			n.Mesh = ts.Mesh
		case "skin":
			n.Skin = ts.Skin
		case "camera":
			n.Camera = ts.Camera
		case "children":
			n.Children = ts.Children
		case "parent":
			for _, pc := range p.order["nodes"] {
				pc.Node.Children = slices.DeleteFunc(slices.Clone(pc.Node.Children), func(x string) bool { return x == key })
			}
			for _, sc := range p.order["scenes"] {
				sc.Scene.Roots = slices.DeleteFunc(slices.Clone(sc.Scene.Roots), func(x string) bool { return x == key })
			}
			if parent := theirs.parentOf(key); parent != "" {
				if pc := p.find("nodes", parent); pc != nil {
					pc.Node.Children = append(pc.Node.Children, key)
				}
			} else if scene := theirs.rootScene(key); scene != "" {
				if sc := p.find("scenes", scene); sc != nil {
					sc.Scene.Roots = append(sc.Scene.Roots, key)
				}
			}
		}
		return nil
	case "materials":
		if !slices.Contains(takeableMaterialFields, field) || c.Side != sideOurs {
			return fmt.Errorf("taking %s from theirs is not supported — resolve it in your editor", path)
		}
		mm := c.Material
		if mm == nil {
			mm = cloneMaterialDeep(ours.doc.Materials[ours.idx[coll][key]])
		}
		tm := theirs.doc.Materials[theirs.idx[coll][key]]
		tPBR := pbrOrDefault(tm)
		switch field {
		case "baseColorFactor":
			setBaseColor(mm, tPBR.BaseColorFactorOrDefault())
		case "metallicFactor":
			setMetallic(mm, tPBR.MetallicFactorOrDefault())
		case "roughnessFactor":
			setRoughness(mm, tPBR.RoughnessFactorOrDefault())
		case "alphaMode":
			mm.AlphaMode = tm.AlphaMode
		case "doubleSided":
			mm.DoubleSided = tm.DoubleSided
		}
		c.Material = mm
		return nil
	}
	return fmt.Errorf("taking %s from theirs is not supported — resolve it in your editor", path)
}
