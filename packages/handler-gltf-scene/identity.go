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
// Unnamed elements are out of scope here. They are keyed by array index
// (node[3]), so "renaming" one is not a thing that can happen, and the index
// cascade an insertion causes is issue #42's — not this layer's.

import (
	"fmt"
	"slices"
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

// sigField is one descriptor component. The weight is how much of the element's
// identity that component carries — see nodeSignature for why they are not all
// worth the same.
type sigField struct {
	value  string
	weight int
}

// signature is an element's content, as an ordered list of descriptor fields.
// Both sides of a collection build their fields in the same order, so two
// signatures are compared positionally.
type signature struct {
	fields []sigField
	// specific is false for a descriptor that describes nothing — an empty node
	// at the origin, a material at every default. Such an element matches every
	// other empty element perfectly, so it never participates in content matching.
	specific bool
}

// similarity is the share of descriptor weight two signatures agree on, over the
// longer of the two: a primitive added to a mesh lowers the score rather than
// being ignored.
func similarity(a, b signature) float64 {
	total, same := 0, 0
	for i := range max(len(a.fields), len(b.fields)) {
		switch {
		case i >= len(a.fields):
			total += b.fields[i].weight
		case i >= len(b.fields):
			total += a.fields[i].weight
		default:
			total += a.fields[i].weight
			if a.fields[i].value == b.fields[i].value {
				same += a.fields[i].weight
			}
		}
	}
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
const meshWeight = 6

// nodeSignature describes a node by what it draws and where: the mesh it
// instances, its local transform, its place in the hierarchy, and how many
// children hang off it. Names are deliberately absent — the name is the thing
// that changed.
func nodeSignature(ix *nodeIndex, i int) signature {
	n := ix.nodes[i]
	t, r, s := n.TranslationOrDefault(), n.RotationOrDefault(), n.ScaleOrDefault()
	return signature{
		fields: []sigField{
			{"mesh=" + ptrLabel(n.Mesh, "mesh"), meshWeight},
			{"translation=" + fmtVec3(t), 1},
			{"rotation=" + fmtRot(r), 1},
			{"scale=" + fmtVec3(s), 1},
			{"parent=" + ix.parentKey(i), 1},
			{fmt.Sprintf("children=%d", len(n.Children)), 1},
		},
		specific: n.Mesh != nil || len(n.Children) > 0 ||
			!nearEq3(t, gltf.DefaultTranslation) || !nearEq4(r, gltf.DefaultRotation) ||
			!nearEq3(s, gltf.DefaultScale),
	}
}

// materialSignature describes a material by the same descriptors
// diffMaterialProps compares, so "the content is identical" here means exactly
// what "no changes" means there. Every component is worth the same: a material is
// a bag of independent properties with no single one that *is* the material.
func materialSignature(doc *gltf.Document, m *gltf.Material) signature {
	fields := materialFields(doc, m)
	defaults := materialFields(doc, &gltf.Material{})
	return signature{fields: fields, specific: !slices.Equal(fields, defaults)}
}

func materialFields(doc *gltf.Document, m *gltf.Material) []sigField {
	pbr := pbrOrDefault(m)
	fields := []sigField{
		{"baseColorFactor=" + fmtVec4(pbr.BaseColorFactorOrDefault()), 1},
		{"metallicFactor=" + fmtF(pbr.MetallicFactorOrDefault()), 1},
		{"roughnessFactor=" + fmtF(pbr.RoughnessFactorOrDefault()), 1},
		{"emissiveFactor=" + fmtVec3(m.EmissiveFactor), 1},
		{"alphaMode=" + m.AlphaMode.String(), 1},
		{fmt.Sprintf("doubleSided=%v", m.DoubleSided), 1},
	}
	for _, slot := range textureSlots {
		fields = append(fields, sigField{slot.label + "=" + slot.describe(doc, m), 1})
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
		fields = append(fields, sigField{strings.Join(parts, " "), 1})
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

	sigs := make(map[*entity]signature, len(leftBase)+len(leftHead))
	sigOf := func(e *entity) signature {
		if s, ok := sigs[e]; ok {
			return s
		}
		s := e.sig()
		sigs[e] = s
		return s
	}

	// The score matrix, thresholded on the way in so everything below is a
	// candidate. Quadratic in the leftovers only, which are the handful of
	// elements neither an id nor a name accounted for.
	score := make(map[[2]int]float64)
	for _, i := range leftBase {
		si := sigOf(&base[i])
		if !si.specific {
			continue
		}
		for _, j := range leftHead {
			sj := sigOf(&head[j])
			if !sj.specific {
				continue
			}
			if s := similarity(si, sj); s >= renameThreshold {
				score[[2]int{i, j}] = s
			}
		}
	}

	// Strict mutual best. Each side's winner must beat its own runner-up outright,
	// so two equally plausible candidates leave the pair unmatched — reported
	// honestly as a removal and an addition rather than guessed at.
	for _, i := range leftBase {
		j, s, ok := strictBest(i, leftHead, score, false)
		if !ok {
			continue
		}
		if back, _, ok := strictBest(j, leftBase, score, true); !ok || back != i {
			continue
		}
		p.pair(i, j, matchEvidence{by: byContent, similarity: s})
	}
}

// strictBest returns the highest-scoring counterpart of `from` among `others`,
// and whether it is a strict winner. transposed selects the matrix direction:
// false scores [from][other], true scores [other][from].
func strictBest(from int, others []int, score map[[2]int]float64, transposed bool) (int, float64, bool) {
	best, bestScore, runnerUp, found := -1, 0.0, 0.0, false
	for _, other := range others {
		key := [2]int{from, other}
		if transposed {
			key = [2]int{other, from}
		}
		s, ok := score[key]
		if !ok {
			continue
		}
		switch {
		case !found || s > bestScore:
			runnerUp = bestScore
			best, bestScore, found = other, s, true
		case s > runnerUp:
			runnerUp = s
		}
	}
	return best, bestScore, found && bestScore > runnerUp
}

// ── reporting ─────────────────────────────────────────────────────────────────

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
