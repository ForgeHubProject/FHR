package main

// Stable identity for scene entities: which element of the previous revision is
// which element of the current one (issue #47).
//
// glTF has no identity mechanism. An element is addressed by array index, and
// `name` is optional, non-unique, and routinely destroyed by pipeline tools
// (gltf-transform's dedup() merges same-content elements regardless of name by
// default). Khronos acknowledges the gap — glTF#2337 and #1713 are open, and the
// glTF 2.1 proposal (#2585) lists a "Unique IDs" explainer that has not shipped.
// Matching purely on name means the commonest edit in a 3D review — renaming a
// node — reads as one element removed and an unrelated one added, which is the
// single most misleading thing a diff can say.
//
// Two layers, strongest evidence first:
//
//	1. authored id   extras.fhr_uid, the FHR convention (SPEC.md §7). An opaque
//	                 string written once and never regenerated. Present → it wins
//	                 outright: same id with a different name is a rename, never a
//	                 delete plus an add. Durable but only as good as its adoption.
//	2. name          the diff key, which is uniqueKeys' disambiguated name. This
//	                 is also the glTF 2.1 explainer's own fallback rule — a
//	                 file-unique name *is* a unique id — so a document that writes
//	                 identity-bearing names and no extras still matches exactly.
//	3. content       leftovers only: pair a removed candidate with an added one
//	                 when their content descriptors agree. Works today, on files
//	                 nobody has stamped, which is the whole point.
//
// Tier 3 is deliberately timid. A false rename is worse than a missed one: a
// missed rename shows a reviewer a delete and an add, which is at least two true
// statements, while a false one asserts a relationship between two unrelated
// objects and hides the fact that something was deleted. So a content match is
// made only when it is *mutually unambiguous* — each side's best candidate is the
// other, and each is strictly better than that side's runner-up — and only above
// git's rename threshold (DEFAULT_RENAME_SCORE, 50%). Ties never pair. Elements
// whose descriptor carries no content at all (an empty node at the origin, a
// material at every default) are excluded outright: they are indistinguishable
// from every other empty element, so pairing two of them would be a coin flip
// dressed up as a rename.
//
// The same rule holds one level down, per descriptor field rather than per
// element, and that is what fieldKind is for: two elements that agree by both
// having left something unwritten have agreed on nothing. A node with no mesh and
// a material with no textures are the ordinary cases in a real file — Blender
// empties, armature joints, camera and light nodes, and every untextured material
// — so scoring shared absence as agreement paired arbitrary deleted elements with
// arbitrary added ones.
//
// Dropping a field shrinks the denominator too, though, which promotes whatever
// survives: a meshless node scored over the one or two placement fields it
// happened to write turned `parent=Armature` — a value every joint under that
// armature shares — into half its evidence. So a descriptor may declare a floor
// under its denominator (signature.floor) covering the fields the element has
// whether or not anyone wrote them. A node's placement is five such fields, and
// they were weighted against meshWeight on the assumption that all five are in
// play; the floor is what keeps that true once the mesh drops out.
//
// Unnamed elements never *rename*. They are keyed by array index (node[3]), so
// there is no name for one of them to have changed, and the index cascade an
// insertion causes is issue #42's — not this layer's. Tier 3 leaves them alone
// altogether; tier 1 still pairs them, because an authored id on a file whose
// names a pipeline tool stripped is exactly what the convention is for, but the
// pairing is reported as a modification and never as a rename (isRename).
//
// Every content descriptor is likewise built out of cross-revision *keys* and
// never array indices — a node's mesh, its parent, a primitive's material — for
// the reason meshSide.materialKey gives one level down: an index means "whatever
// is third in this file's array", which an insertion upstream silently redefines.
// An *unnamed* element has no such key: uniqueKeys falls back to `mesh[1]`, which
// is the array index in a wrapper. A descriptor referencing one gets no credit
// for it (fieldKind opaque) rather than pretending the number travels.

import (
	"fmt"
	"strings"

	"github.com/qmuntal/gltf"
)

// uidExtrasKey is the FHR stable-identity convention: an opaque, never-mutated
// string under an element's `extras`. Blender round-trips custom properties
// through glTF extras on both export and import, so an authored id survives the
// standard pipeline with no exporter changes. Documented in SPEC.md §7.
const uidExtrasKey = "fhr_uid"

// renameThreshold is the fraction of a content descriptor two elements must
// share before they may be paired as a rename. 50% is git's DEFAULT_RENAME_SCORE;
// the number is inherited rather than invented because git's is the one rename
// heuristic with two decades of production tuning behind it.
const renameThreshold = 0.5

// extrasUID reads the authored stable id from an element's extras, or "" when
// there isn't one. Anything that is not a non-empty string is not an id: extras
// is free-form author data, and a number or an object under this key is someone
// else's field that happens to collide, not an identity claim.
func extrasUID(extras any) string {
	m, ok := extras.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[uidExtrasKey].(string)
	return s
}

// ── entities and content descriptors ──────────────────────────────────────────

// entity is one element of a diffable collection, reduced to what matching needs.
type entity struct {
	key  string // the diff key: uniqueKeys' disambiguated name
	name string // the raw glTF name; "" for an unnamed element
	uid  string // the authored stable id; "" when absent
	// sig builds the content descriptor. A function because only the leftovers
	// of tiers 1 and 2 ever need one, and a mesh descriptor hashes vertex bytes —
	// a cost the unchanged path must not pay.
	sig func() signature
}

// fieldKind is what a descriptor field is worth as evidence that two elements
// are the same one. Equality is not the same question: two fields can hold the
// identical string and still say nothing about identity.
type fieldKind uint8

const (
	// stated — a value someone wrote, spelled so that it names the same thing in
	// both revisions. The only kind that can score as agreement.
	stated fieldKind = iota
	// unstated — absent, or left at the glTF default. Nobody wrote it, so two
	// sides that are both unstated have told us nothing and the field drops out
	// of the comparison altogether: it neither scores nor counts against. Against
	// a stated value it counts and disagrees, because written and not-written is
	// a real difference. Dropping is per field; whether the denominator may shrink
	// with it is the descriptor's own call (signature.floor).
	//
	// Dropping out rather than scoring is the whole point. Under equality alone a
	// node with no mesh agreed with every other meshless node in the file on the
	// heaviest field a node has (meshWeight, 6 of 11), which put every deleted
	// empty, joint, camera and light node over the threshold against every added
	// one; two untextured materials agreed on all five texture slots and typically
	// three more defaults, 8 of 11, however far apart their colours were.
	unstated
	// opaque — a value that means something only inside its own file: today, any
	// key that fell back to an array index (`mesh[1]`, `node[3]` — what uniqueKeys
	// produces for an unnamed element). Two sides can print the same string and
	// mean different elements, so it never scores as agreement. Unlike unstated
	// its weight still counts, so a pair whose only common ground is a number
	// falls below the threshold instead of being scored on the remainder.
	opaque
)

// unstatedIf classifies a field that is stated unless it holds its glTF default.
func unstatedIf(isDefault bool) fieldKind {
	if isDefault {
		return unstated
	}
	return stated
}

// sigField is one descriptor component. The weight is how much of the element's
// identity that component carries — see nodeSignature for why they are not all
// worth the same — and the kind is whether agreement on it means anything.
type sigField struct {
	value  string
	weight int
	kind   fieldKind
}

// signature is an element's content, as an ordered list of descriptor fields.
// Both sides of a collection build their fields in the same order, so two
// signatures are compared positionally.
type signature struct {
	fields []sigField
	// floor is the least descriptor weight a score may be measured over, whatever
	// dropped out as unstated. It is the weight of the fields the element has
	// regardless of whether anyone wrote them — a node is somewhere, under
	// something, with some number of children — as against the ones it may simply
	// not have, where there is nothing to compare at all. Without it an element
	// that stated almost nothing was scored over almost nothing, and one shared
	// field became the whole comparison. Zero for a descriptor with no such
	// fields; see nodeSignature for the one that has them.
	floor int
	// specific is false for a descriptor that describes nothing — an empty node
	// at the origin, a material at every default. Such an element matches every
	// other empty element perfectly, so it never participates in content matching.
	specific bool
}

// similarity is the share of descriptor weight two signatures agree on, over the
// longer of the two: a primitive added to a mesh lowers the score rather than
// being ignored.
//
// Only a stated field can agree (fieldKind). A field neither side stated is not
// counted at all, so the score is measured over what the two elements actually
// say about themselves rather than over how much they both left blank — but
// never over less than the descriptor's floor, so what survives keeps the share
// of the whole it was weighted for instead of inheriting the dropped fields'.
func similarity(a, b signature) float64 {
	total, same := 0, 0
	for i := range max(len(a.fields), len(b.fields)) {
		switch {
		case i >= len(a.fields):
			total += b.fields[i].weight
		case i >= len(b.fields):
			total += a.fields[i].weight
		default:
			fa, fb := a.fields[i], b.fields[i]
			if fa.kind == unstated && fb.kind == unstated {
				continue
			}
			total += fa.weight
			if fa.kind == stated && fb.kind == stated && fa.value == fb.value {
				same += fa.weight
			}
		}
	}
	total = max(total, a.floor, b.floor)
	if total == 0 {
		return 0
	}
	return float64(same) / float64(total)
}

// meshWeight is what a node's mesh reference is worth against its five
// positional descriptors put together, and then some. A node is a reference plus
// a placement: two nodes drawing the same geometry are the same object moved,
// while two nodes agreeing only on where they sit agree on almost nothing —
// everything nobody has moved sits at the origin with no parent. Weighted this
// way, a node that draws something else cannot clear the threshold no matter how
// exactly its placement matches, which is the intended answer: a swapped mesh is
// a removal and an addition, not a rename.
//
// The weight only ever works in that direction because a mesh nobody assigned is
// unstated rather than a sixth of the score for agreeing to draw nothing, and one
// resolved through an unnamed mesh's index key is opaque rather than a perfect
// match with whatever held that number last revision. Both are nodeIndex.meshField.
const meshWeight = 6

// nodeSignature describes a node by what it draws and where: the mesh it
// instances, its local transform, its place in the hierarchy, and how many
// children hang off it. Names are deliberately absent — the name is the thing
// that changed.
//
// The mesh is nodeIndex.meshKey and the parent nodeIndex.parentKey — keys, never
// array indices. The trade is deliberate and it is the one this package always
// makes: renaming a mesh costs the nodes that draw it their content match, which
// is a missed rename, whereas inserting a mesh upstream renumbers every mesh after
// it and hands a node that draws something else a *perfect* score against whatever
// used to sit at its number, which is a false one at full confidence.
//
// The five placement fields are the descriptor's floor and the mesh is not,
// because a node always has a placement and may genuinely have no mesh. glTF
// gives translation, rotation and scale defaults; every node sits under something
// or at the root; "no children" is a child count. Two nodes both at the origin
// have therefore compared a field and found the value nearly every node in the
// file holds — no agreement (unstated), but not nothing to compare either. An
// absent mesh is the other thing: there is no geometry on either side, so the
// field drops out of the score entirely.
//
// Measuring a meshless node over only the placement it happened to write is what
// the floor exists to stop. Two joints under one armature stated their parent and
// their translation, agreed on the parent — which every sibling shares — and
// landed on exactly renameThreshold, so an ordinary skeleton's deleted joint was
// renamed into an unrelated added one; two group nodes whose only stated fields
// were a shared parent and "2 children" scored a perfect 1.0. Over the whole
// placement those are 1 of 5 and 2 of 5, and both are the removal plus the
// addition they always were.
func nodeSignature(ix *nodeIndex, i int) signature {
	n := ix.nodes[i]
	t, r, s := n.TranslationOrDefault(), n.RotationOrDefault(), n.ScaleOrDefault()
	placement := []sigField{
		{"translation=" + fmtVec3(t), 1, unstatedIf(nearEq3(t, gltf.DefaultTranslation))},
		{"rotation=" + fmtRot(r), 1, unstatedIf(nearEq4(r, gltf.DefaultRotation))},
		{"scale=" + fmtVec3(s), 1, unstatedIf(nearEq3(s, gltf.DefaultScale))},
		ix.parentField(i),
		{fmt.Sprintf("children=%d", len(n.Children)), 1, unstatedIf(len(n.Children) == 0)},
	}
	floor := 0
	for _, f := range placement {
		floor += f.weight
	}
	return signature{
		fields: append([]sigField{ix.meshField(n.Mesh)}, placement...),
		floor:  floor,
		specific: n.Mesh != nil || len(n.Children) > 0 ||
			!nearEq3(t, gltf.DefaultTranslation) || !nearEq4(r, gltf.DefaultRotation) ||
			!nearEq3(s, gltf.DefaultScale),
	}
}

// Material descriptor weights. A material is a bag of independent properties with
// no single one that *is* the material, but they are not therefore worth the
// same, and weighting them equally is decisive at a 50% threshold: two materials
// whose only common ground was `doubleSided: true` — one pure green, one pure red
// — scored exactly 0.5 and were reported as one material renamed.
//
// The currency is the one meshWeight already counts in: how much a field can tell
// one material apart from another. A colour is four numbers a viewer shows; a
// metallic factor is one number artists set to 0, 0.5 or 1; an alpha mode is one
// of four states and double-sidedness one of two, so agreeing on either is nearly
// free.
const (
	colorWeight    = 4 // baseColorFactor, an RGBA tuple
	emissiveWeight = 3 // emissiveFactor, an RGB tuple
	// textureWeight — a resolved image reference: a URI, or a mime type plus a
	// content hash of the pixels, with the sampler that reads it. At least as
	// identifying as the colour it usually stands in for, and the one material
	// property that cannot be arrived at by coincidence.
	textureWeight = colorWeight
	// factorWeight — one number, one enum, one flag.
	factorWeight = 1
)

// materialSignature describes a material by the same descriptors
// diffMaterialProps compares, so "the content is identical" here means exactly
// what "no changes" means there.
//
// Every component is classified against the default material: with a 50%
// threshold, three or four properties nobody touched — an empty texture slot is
// the commonest material property there is — were enough to pair any two
// materials in an untextured scene. A material is scored on what it says about
// itself, not on the defaults it shares with every other material ever exported.
//
// The floor is a colour's weight, for nodeSignature's reason one level up: every
// material has a base colour whether or not anyone wrote one — glTF's default is
// a real value the renderer uses — where it may genuinely have no normal map at
// all. It is the largest thing a material always has, so it is the least a
// material may be scored over. Without it a pair that stated nothing but
// `doubleSided: true` had agreed on everything in play and scored a perfect 1.0,
// which is a coin flip on a boolean dressed up as a rename.
func materialSignature(doc *gltf.Document, m *gltf.Material) signature {
	fields := materialFields(doc, m)
	defaults := materialFields(doc, &gltf.Material{})
	specific := false
	for i := range fields {
		if fields[i].value == defaults[i].value {
			fields[i].kind = unstated
		} else {
			specific = true
		}
	}
	return signature{fields: fields, floor: colorWeight, specific: specific}
}

func materialFields(doc *gltf.Document, m *gltf.Material) []sigField {
	pbr := pbrOrDefault(m)
	fields := []sigField{
		{"baseColorFactor=" + fmtVec4(pbr.BaseColorFactorOrDefault()), colorWeight, stated},
		{"metallicFactor=" + fmtF(pbr.MetallicFactorOrDefault()), factorWeight, stated},
		{"roughnessFactor=" + fmtF(pbr.RoughnessFactorOrDefault()), factorWeight, stated},
		{"emissiveFactor=" + fmtVec3(m.EmissiveFactor), emissiveWeight, stated},
		{"alphaMode=" + m.AlphaMode.String(), factorWeight, stated},
		{fmt.Sprintf("doubleSided=%v", m.DoubleSided), factorWeight, stated},
	}
	for _, slot := range textureSlots {
		fields = append(fields, sigField{slot.label + "=" + slot.describe(doc, m), textureWeight, stated})
	}
	return fields
}

// meshSignature describes a mesh by its primitives: what each one is made of
// (the same stream descriptors the geometry compare reports, digest included) and
// which material it uses. This is the one descriptor that reads buffer bytes,
// which is why signatures are built lazily.
//
// The primitive *count* is deliberately not a field of its own. It is already
// implied by the field list's length, and stating it separately would hand every
// pair of single-primitive meshes half a point of agreement before their contents
// were even looked at — half being exactly the threshold.
func meshSignature(s meshSide, m *gltf.Mesh) signature {
	fields := make([]sigField, 0, len(m.Primitives))
	for _, p := range m.Primitives {
		parts := []string{"material=" + s.materialKey(p.Material)}
		for _, stream := range primitiveStreams(p, p) {
			parts = append(parts, stream+"="+readStream(s.doc, p, stream).describe(s.doc))
		}
		fields = append(fields, sigField{strings.Join(parts, " "), 1, stated})
	}
	return signature{fields: fields, specific: len(m.Primitives) > 0}
}

// ── matching ──────────────────────────────────────────────────────────────────

// How an element of one revision was tied to an element of the other.
const (
	byUID     = "fhr_uid"
	byName    = "name"
	byContent = "content"
)

// matchEvidence records why two elements were paired, so a rename can say so.
// Issue #42's rule: never a silent re-pair.
type matchEvidence struct {
	by string
	// similarity is the content score, 0 for the exact-evidence tiers.
	similarity float64
	// duplicateUID marks an id that is not unique on one of the sides. The first
	// occurrence was used; the rest fell through to the weaker tiers.
	duplicateUID bool
}

// pairing is one collection's cross-revision matching: which base element is
// which head element, and on what evidence.
type pairing struct {
	headOf map[int]int           // base index → head index
	baseOf map[int]int           // head index → base index
	how    map[int]matchEvidence // base index → evidence
}

func (p pairing) pair(base, head int, ev matchEvidence) {
	p.headOf[base] = head
	p.baseOf[head] = base
	p.how[base] = ev
}

// sameEntity reports whether a base index and a head index are the same element.
// Callers compare *identity*, never keys: after a rename the two keys differ by
// construction, so a key comparison would report every child of a renamed node as
// re-parented.
func (p pairing) sameEntity(base, head int) bool {
	if base < 0 || head < 0 {
		return base == head // both at the root of the hierarchy
	}
	h, ok := p.headOf[base]
	return ok && h == head
}

// matchEntities runs the cascade over one collection and returns the pairing.
func matchEntities(base, head []entity) pairing {
	p := pairing{
		headOf: make(map[int]int, len(base)),
		baseOf: make(map[int]int, len(head)),
		how:    make(map[int]matchEvidence, len(base)),
	}

	// Tier 1 — authored ids. A duplicated id is untrusted beyond its first
	// occurrence, which is uniqueKeys' rule for duplicated names applied to
	// identity: the first claimant keeps it, the rest degrade to the weaker tiers,
	// and any rename the id produced says that it was duplicated.
	baseUID, baseDup := uidIndex(base)
	headUID, headDup := uidIndex(head)
	for i, e := range base {
		if e.uid == "" || baseUID[e.uid] != i {
			continue
		}
		j, ok := headUID[e.uid]
		if !ok {
			continue
		}
		p.pair(i, j, matchEvidence{by: byUID, duplicateUID: baseDup[e.uid] || headDup[e.uid]})
	}

	// Tier 2 — names, via the diff key. An element already claimed by an id match
	// is not up for grabs: the id is the stronger evidence, so a head element that
	// merely inherited the old name reads as new, which is what it is.
	headByKey := make(map[string]int, len(head))
	for j, e := range head {
		headByKey[e.key] = j
	}
	for i, e := range base {
		if _, done := p.headOf[i]; done {
			continue
		}
		j, ok := headByKey[e.key]
		if !ok {
			continue
		}
		if _, taken := p.baseOf[j]; taken {
			continue
		}
		p.pair(i, j, matchEvidence{by: byName})
	}

	matchByContent(p, base, head)
	return p
}

// uidIndex maps each id to its first occurrence, and reports which ids occur more
// than once on this side.
func uidIndex(items []entity) (first map[string]int, duplicated map[string]bool) {
	first = make(map[string]int, len(items))
	duplicated = make(map[string]bool)
	for i, e := range items {
		if e.uid == "" {
			continue
		}
		if _, seen := first[e.uid]; seen {
			duplicated[e.uid] = true
			continue
		}
		first[e.uid] = i
	}
	return first, duplicated
}

// renameLimit caps tier 3's search, in candidates per side.
//
// Content matching compares every leftover base element against every leftover
// head one, and nothing upstream bounds a document's element count. The leftovers
// are usually a handful, but the case that makes *every* element one is not
// exotic: any pipeline step that rewrites all the names at once — an FBX round
// trip, a namespace prefix on import, a re-export that suffixes — clears tiers 1
// and 2 completely. At 8000 nodes that took four minutes and a gigabyte of score
// matrix, in a package that is also compiled to wasm and run in a browser tab
// (wasm.go), where a single-threaded block that long is a hung page and the
// allocation is an OOM rather than a slow diff.
//
// The number and the shape of the test are git's — diff.renameLimit, default
// 1000, applied as sources × destinations against limit², after which git skips
// inexact rename detection outright — for renameThreshold's reason: git's rename
// detection is the one with two decades of production tuning behind it. Over the
// limit the leftovers are reported as the removals and additions they are, which
// is the same answer this layer already gives whenever the evidence is not good
// enough, and the safe direction to fail in.
const renameLimit = 1000

// matchByContent pairs what tiers 1 and 2 left over, by content descriptor.
//
// Only named elements participate: an unnamed element's key is its array index,
// so pairing two of them would report a "rename" between two synthetic keys and
// would wander into the index cascade that is issue #42's to fix.
func matchByContent(p pairing, base, head []entity) {
	var leftBase, leftHead []int
	for i, e := range base {
		if _, done := p.headOf[i]; !done && e.name != "" {
			leftBase = append(leftBase, i)
		}
	}
	for j, e := range head {
		if _, done := p.baseOf[j]; !done && e.name != "" {
			leftHead = append(leftHead, j)
		}
	}
	if len(leftBase) == 0 || len(leftHead) == 0 {
		return
	}
	// Divided rather than multiplied: the product of two element counts is not
	// bounded by anything and this runs on documents nobody validated.
	if len(leftBase) > renameLimit*renameLimit/len(leftHead) {
		return
	}

	// Descriptors are built here and not before the cap, because a mesh descriptor
	// hashes vertex bytes: over the limit not one of them is read.
	baseSig := signaturesOf(base, leftBase)
	headSig := signaturesOf(head, leftHead)

	// The score matrix, thresholded on the way in so everything above zero is a
	// candidate. Row-major and dense rather than a map keyed by index pair: at the
	// cap this is one 8 MB allocation whatever the input does, where the map was
	// an entry per surviving pair and the hashing that went with it.
	score := make([]float64, len(leftBase)*len(leftHead))
	for bi, si := range baseSig {
		if !si.specific {
			continue
		}
		row := score[bi*len(leftHead) : (bi+1)*len(leftHead)]
		for hj, sj := range headSig {
			if !sj.specific {
				continue
			}
			if s := similarity(si, sj); s >= renameThreshold {
				row[hj] = s
			}
		}
	}

	// Strict mutual best. Each side's winner must beat its own runner-up outright,
	// so two equally plausible candidates leave the pair unmatched — reported
	// honestly as a removal and an addition rather than guessed at.
	for bi := range leftBase {
		row := score[bi*len(leftHead) : (bi+1)*len(leftHead)]
		hj, s, ok := strictBest(len(leftHead), func(k int) float64 { return row[k] })
		if !ok {
			continue
		}
		back, _, ok := strictBest(len(leftBase), func(k int) float64 { return score[k*len(leftHead)+hj] })
		if !ok || back != bi {
			continue
		}
		p.pair(leftBase[bi], leftHead[hj], matchEvidence{by: byContent, similarity: s})
	}
}

// signaturesOf builds the content descriptor of the elements at `which`. The
// descriptors are what tier 3 costs — a mesh's reads buffer bytes — which is why
// entity.sig is a function and why this is called on the leftovers alone.
func signaturesOf(items []entity, which []int) []signature {
	out := make([]signature, len(which))
	for k, i := range which {
		out[k] = items[i].sig()
	}
	return out
}

// strictBest returns the position of the highest-scoring counterpart among `n`
// candidates scored by `at`, and whether it is a strict winner. A score below the
// threshold is not a candidate at all: the matrix stores zero there.
func strictBest(n int, at func(int) float64) (int, float64, bool) {
	best, bestScore, runnerUp, found := -1, 0.0, 0.0, false
	for k := range n {
		s := at(k)
		if s < renameThreshold {
			continue
		}
		switch {
		case !found || s > bestScore:
			runnerUp = bestScore
			best, bestScore, found = k, s, true
		case s > runnerUp:
			runnerUp = s
		}
	}
	return best, bestScore, found && bestScore > runnerUp
}

// ── reporting ─────────────────────────────────────────────────────────────────

// isRename reports whether the key change between two paired elements is a name
// edit, and so reportable as a rename at all.
//
// The name and not the key decides it. A key is uniqueKeys' *disambiguated* name
// — Wheel, Wheel#1, Wheel#2 — so two elements whose names are identical can still
// hold different keys, and in the file this feature is aimed at they routinely
// do: duplicate names are the normal case uniqueKeys exists for, an exporter
// reordering the array swaps which one gets the bare name, and an authored id
// pairs them correctly across the swap. Nothing was renamed there. Reporting one
// would put `Wheel#1` in `before`, and SPEC.md defines `before` as the bare old
// *name* — a consumer that resolves it against the previous revision finds
// nothing under that string, because the previous revision has two elements
// named `Wheel` and none named `Wheel#1`.
//
// The unnamed case is the same rule read the other way. An unnamed element is
// keyed by its array index (node[3]), so when neither side has a name both names
// are "" and the keys differ only because something upstream was inserted or
// removed — the index cascade issue #42 owns, and another synthetic `before`.
//
// The pairing itself stands in both. An authored id is still the strongest
// evidence there is that two array slots hold the same element — it is what makes
// the properties get compared against the right one, which is the whole point of
// stamping a file whose names a pipeline tool dropped. Only the rename label is
// withheld.
//
// A name on one side only is a real edit — a name was added or removed — and the
// unnamed side's key is the only thing that element is called anywhere else in
// the diff, so that pair is still reported as the rename it is.
func isRename(a, b entity) bool {
	return a.key != b.key && a.name != b.name
}

// bareName is what a rename puts in `before` and `after`: the element's own name,
// never uniqueKeys' disambiguated key.
//
// SPEC.md §7 defines both fields as the bare name, and the key is not one. The
// case is the same one isRename turns away for a different reason — duplicate
// names, the ordinary case uniqueKeys exists for, paired across the swap by an
// authored id. When the name *also* changed the pairing really is a rename and is
// reported, but `Wheel#1` in `before` still sends a consumer looking for a node
// the previous revision does not have: it has two called `Wheel` and none called
// `Wheel#1`. The renderer's own base-file lookup (node-index.ts) is keyed on raw
// names and misses outright, taking the removed ghost and the motion vector with
// it. `path` and `label` keep the key — they are what this diff addresses the
// element by, and they have to stay unique.
//
// An unnamed element falls back to its key. It has no bare name to report, and
// `node[3]` is the only thing it is called anywhere: this is the one-sided case
// isRename keeps, where a name was added or removed outright.
func bareName(e entity) string {
	if e.name != "" {
		return e.name
	}
	return e.key
}

// renameAfter renders a rename's `after` value: the new name, plus the evidence
// that tied it to the old one in the parenthetical form the rest of this handler
// uses for a measured value ("40 mm (moved 12 mm)").
//
// The label stays the bare new name and `before` the bare old one, so a consumer
// reads names off those two fields and never has to parse this string.
func renameAfter(newKey string, ev matchEvidence) string {
	var note string
	switch ev.by {
	case byUID:
		note = "matched by " + uidExtrasKey
		if ev.duplicateUID {
			// The id was claimed by more than one element on one of the sides, so
			// this pairing is the first-occurrence rule's guess, not a fact.
			note += ", duplicated — first occurrence used"
		}
	case byContent:
		note = "matched by content"
		if ev.similarity < 1 {
			// Approximate by construction: the score is a fraction of descriptor
			// fields, not a measurement of the geometry.
			note += fmt.Sprintf(", ~%d%% similar", int(ev.similarity*100+0.5))
		}
	default:
		return newKey
	}
	return newKey + " (" + note + ")"
}
