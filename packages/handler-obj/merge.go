package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
)

// Merge is a semantic three-way merge of OBJ, one object or group at a time.
//
// A text merge of OBJ is not safe, which is why this exists. Faces address
// vertices by their position in the file, so an edit that adds a vertex
// renumbers every face after it. git can report such a merge as clean and
// still leave a face pointing at another object's vertex — nothing in a line
// diff knows what an index means.
//
// So the unit is the object/group (the same nodes the diff reports), compared
// by what its elements actually reference — vertex text, not index — so two
// files that number the same geometry differently agree. Per node:
//
//	changed on one side only        → that side
//	changed identically on both     → either
//	changed differently on both     → keep ours, record a conflict
//	removed on one side, untouched  → removed
//	removed on one side, changed on the other → keep ours' choice, conflict
//	added on one side               → added (on both, identically → once)
//
// mtllib merges the same way. The result is written fresh (write.go), so every
// index is right by construction; when only one side changed, that side is
// returned byte for byte instead.
//
// Files carrying statements the handler does not interpret (free-form curves
// and surfaces, display attributes) are refused: it could not say where they
// belong in a rewritten file, and a merge that drops them is data loss. The
// caller then treats the file as conflicted, which loses nothing.
func (h *Handler) Merge(base, ours, theirs fhr.Blob) (fhr.Blob, *fhr.ConflictInfo, error) {
	switch {
	case bytes.Equal(ours, theirs), bytes.Equal(base, theirs):
		return ours, nil, nil
	case bytes.Equal(base, ours):
		return theirs, nil, nil
	}

	sides := [3]*objFile{}
	for i, blob := range [3]fhr.Blob{base, ours, theirs} {
		f, err := parseOBJ(blob)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", [3]string{"base", "ours", "theirs"}[i], err)
		}
		if kws := uninterpretedKeywords(f); kws != "" {
			return nil, nil, fmt.Errorf("semantic merge is not supported for OBJ files with %s statements", kws)
		}
		sides[i] = f
	}
	b, o, t := indexSides(sides[0]), indexSides(sides[1]), indexSides(sides[2])

	// A side whose change is formatting only (comments, spacing, renumbering)
	// hands the merge to the other side, byte for byte, like an unchanged one.
	switch {
	case o.digest == t.digest, b.digest == t.digest:
		return ours, nil, nil
	case b.digest == o.digest:
		return theirs, nil, nil
	}

	var conflicts []fhr.SemanticConflict
	roots := mergeNodes(b, o, t, &conflicts)
	libs := mergeMtlLibs(b.f.mtlLibs, o.f.mtlLibs, t.f.mtlLibs, &conflicts)

	out := writeOBJ(o.f.header, libs, roots)
	var ci *fhr.ConflictInfo
	if len(conflicts) > 0 {
		ci = &fhr.ConflictInfo{Conflicts: conflicts}
	}
	return out, ci, nil
}

// ── identity and content ──────────────────────────────────────────────────────

// side is one parsed file indexed for merging.
type side struct {
	f      *objFile
	order  []string            // node ids, document order (parents before children)
	nodes  map[string]*objNode // id → node
	parent map[string]string   // id → parent id ("" for a root)
	key    map[string]string   // id → the diff's node key, for conflict paths
	sum    map[string][32]byte // id → content digest of the node's own elements
	digest [32]byte            // the whole file's content, header and comments aside
}

// indexSides gives every node an id that is stable across revisions. A root
// object is its name plus which occurrence of that name it is (`o` repeats are
// separate objects); a root group is its name (`g` reopens); the implicit node
// is one id; a child group is its object's id plus its name. The statement is
// part of the id, so turning an `o` into a `g` is a removal and an addition.
func indexSides(f *objFile) *side {
	s := &side{
		f:      f,
		nodes:  map[string]*objNode{},
		parent: map[string]string{},
		key:    map[string]string{},
		sum:    map[string][32]byte{},
	}
	flat, keys := flatten(f)
	keyOf := make(map[*objNode]string, len(flat))
	for i, n := range flat {
		keyOf[n] = keys[i]
	}

	whole := sha256.New()
	fmt.Fprintf(whole, "mtllib\x00%s\x00", strings.Join(f.mtlLibs, "\x00"))
	occurrence := map[string]int{}
	add := func(id, parent string, n *objNode) {
		s.order = append(s.order, id)
		s.nodes[id] = n
		s.parent[id] = parent
		s.key[id] = keyOf[n]
		s.sum[id] = contentDigest(f, n)
		fmt.Fprintf(whole, "%s\x00%s\x00%s\x00%x\x00", id, n.stmt, n.rawName, s.sum[id])
	}
	for _, r := range f.roots {
		id := r.stmt + "\x00" + r.name
		if r.stmt == "o" {
			id += fmt.Sprintf("\x00%d", occurrence[r.name])
			occurrence[r.name]++
		}
		add(id, "", r)
		for _, c := range r.children {
			add(id+"\x01"+c.name, id, c)
		}
	}
	copy(s.digest[:], whole.Sum(nil))
	return s
}

// contentDigest identifies what a node's own elements draw: kind, material,
// smoothing, and each reference as the text of the vertex, texture coordinate
// and normal it resolves to — never the index, which a renumbering changes
// without changing anything.
func contentDigest(f *objFile, n *objNode) [32]byte {
	h := sha256.New()
	for _, e := range n.elems {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00", e.kind, e.material, e.smooth)
		for _, r := range e.refs {
			h.Write([]byte(f.posText[r.v]))
			h.Write([]byte{1})
			if r.vt >= 0 {
				h.Write([]byte(f.uvText[r.vt]))
			}
			h.Write([]byte{1})
			if r.vn >= 0 {
				h.Write([]byte(f.normalText[r.vn]))
			}
			h.Write([]byte{2})
		}
		h.Write([]byte{3})
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func uninterpretedKeywords(f *objFile) string {
	kws := make([]string, 0, len(f.other))
	for kw := range f.other {
		kws = append(kws, kw)
	}
	sort.Strings(kws)
	return strings.Join(kws, ", ")
}

// ── the node merge ────────────────────────────────────────────────────────────

type pick uint8

const (
	drop pick = iota
	fromOurs
	fromTheirs
	combined // both changed it, in different aspects: see mergeAspects
)

func mergeNodes(b, o, t *side, conflicts *[]fhr.SemanticConflict) []*writeNode {
	ids := mergeOrder(o.order, t.order)

	path := func(id string) string {
		for _, s := range []*side{o, t, b} {
			if k, ok := s.key[id]; ok {
				return "nodes/" + escapeSegment(k)
			}
		}
		return "nodes"
	}
	conflict := func(id, ours, theirs string) {
		*conflicts = append(*conflicts, fhr.SemanticConflict{Path: path(id), Ours: ours, Theirs: theirs})
	}

	decision := make(map[string]pick, len(ids))
	combinedElems := map[string][]placedElem{}
	for _, id := range ids {
		_, inB := b.nodes[id]
		_, inO := o.nodes[id]
		_, inT := t.nodes[id]
		dB, dO, dT := b.sum[id], o.sum[id], t.sum[id]
		switch {
		case inO && inT:
			switch {
			case dO == dT, inB && dT == dB:
				decision[id] = fromOurs
			case inB && dO == dB:
				decision[id] = fromTheirs
			default:
				if inB {
					if elems, ok := mergeAspects(b.f, o.f, t.f, b.nodes[id], o.nodes[id], t.nodes[id]); ok {
						decision[id] = combined
						combinedElems[id] = elems
						break
					}
				}
				decision[id] = fromOurs
				conflict(id, summarize(o.nodes[id]), summarize(t.nodes[id]))
			}
		case inO:
			switch {
			case !inB:
				decision[id] = fromOurs // added here
			case dO == dB:
				decision[id] = drop // removed there, untouched here
			default:
				decision[id] = fromOurs
				conflict(id, summarize(o.nodes[id]), "removed")
			}
		case inT:
			switch {
			case !inB:
				decision[id] = fromTheirs
			case dT == dB:
				decision[id] = drop
			default:
				decision[id] = drop
				gone := "removed"
				if p := t.parent[id]; p != "" && o.nodes[p] == nil {
					gone = "removed with its object"
				}
				conflict(id, gone, summarize(t.nodes[id]))
			}
		}
	}

	// A group kept inside an object that was removed: the removal and the
	// change inside it are one disagreement. Ours decides it — if ours has the
	// object it stays (as ours has it); if ours removed it, the group goes too.
	for _, id := range ids {
		if decision[id] == drop {
			continue
		}
		p := parentOf(id, o, t)
		if p == "" || decision[p] != drop {
			continue
		}
		if _, inO := o.nodes[p]; inO {
			decision[p] = fromOurs
			conflict(p, "kept", "removed")
		} else {
			decision[id] = drop
			conflict(id, "removed with its object", summarize(t.nodes[id]))
		}
	}

	return buildTree(ids, decision, o, t, combinedElems)
}

// buildTree turns per-node decisions into the tree to write: each kept node
// from the side it was decided from (or its combined elements), under its
// parent when the parent was kept too.
func buildTree(ids []string, decision map[string]pick, o, t *side, combinedElems map[string][]placedElem) []*writeNode {
	built := map[string]*writeNode{}
	var roots []*writeNode
	for _, id := range ids {
		var w *writeNode
		switch decision[id] {
		case fromOurs:
			n := o.nodes[id]
			w = &writeNode{stmt: n.stmt, rawName: n.rawName, elems: placed(o.f, n.elems)}
		case fromTheirs:
			n := t.nodes[id]
			w = &writeNode{stmt: n.stmt, rawName: n.rawName, elems: placed(t.f, n.elems)}
		case combined:
			n := o.nodes[id]
			w = &writeNode{stmt: n.stmt, rawName: n.rawName, elems: combinedElems[id]}
		default:
			continue
		}
		built[id] = w
		if p := parentOf(id, o, t); p != "" {
			if pw := built[p]; pw != nil {
				pw.children = append(pw.children, w)
				continue
			}
		}
		roots = append(roots, w)
	}
	return roots
}

// mergeAspects merges a group both sides changed, element by element, when
// its elements still line up — the same count on all three sides, each the
// same kind with the same number of vertices. Each element's geometry (the
// vertices, texture coordinates and normals it resolves to), material and
// smoothing then merge on their own, so reshaping a part on one side and
// recolouring it on the other is not a conflict. ok is false when the
// elements do not line up, or when one aspect of one element changed
// differently on both sides; the caller reports the group as conflicted.
func mergeAspects(fb, fo, ft *objFile, nb, no, nt *objNode) ([]placedElem, bool) {
	if len(nb.elems) != len(no.elems) || len(nb.elems) != len(nt.elems) {
		return nil, false
	}
	out := make([]placedElem, len(nb.elems))
	for i := range nb.elems {
		eb, eo, et := nb.elems[i], no.elems[i], nt.elems[i]
		if eb.kind != eo.kind || eb.kind != et.kind || len(eb.refs) != len(eo.refs) || len(eb.refs) != len(et.refs) {
			return nil, false
		}
		// Geometry decides which side's pools the element indexes into.
		var e placedElem
		switch gB, gO, gT := geometryKey(fb, eb), geometryKey(fo, eo), geometryKey(ft, et); {
		case gO == gT, gT == gB:
			e = placedElem{fo, eo}
		case gO == gB:
			e = placedElem{ft, et}
		default:
			return nil, false
		}
		var ok bool
		if e.material, ok = merge3(eb.material, eo.material, et.material); !ok {
			return nil, false
		}
		if e.smooth, ok = merge3(eb.smooth, eo.smooth, et.smooth); !ok {
			return nil, false
		}
		out[i] = e
	}
	return out, true
}

// geometryKey is what an element draws, independent of indices.
func geometryKey(f *objFile, e objElem) string {
	var b strings.Builder
	for _, r := range e.refs {
		b.WriteString(f.posText[r.v])
		b.WriteByte(1)
		if r.vt >= 0 {
			b.WriteString(f.uvText[r.vt])
		}
		b.WriteByte(1)
		if r.vn >= 0 {
			b.WriteString(f.normalText[r.vn])
		}
		b.WriteByte(2)
	}
	return b.String()
}

// merge3 is a three-way merge of one value; ok is false when both sides
// changed it differently.
func merge3(base, ours, theirs string) (string, bool) {
	switch {
	case ours == theirs, theirs == base:
		return ours, true
	case ours == base:
		return theirs, true
	}
	return ours, false
}

func parentOf(id string, sides ...*side) string {
	for _, s := range sides {
		if p, ok := s.parent[id]; ok {
			return p
		}
	}
	return ""
}

// mergeOrder is ours' order with theirs' additions placed after the node that
// precedes them in theirs, so an added group lands where theirs put it.
func mergeOrder(ours, theirs []string) []string {
	out := append([]string(nil), ours...)
	have := make(map[string]bool, len(out))
	for _, id := range out {
		have[id] = true
	}
	prev := ""
	for _, id := range theirs {
		if !have[id] {
			at := 0
			if prev != "" {
				for i, x := range out {
					if x == prev {
						at = i + 1
						break
					}
				}
			}
			out = append(out[:at], append([]string{id}, out[at:]...)...)
			have[id] = true
		}
		prev = id
	}
	return out
}

func mergeMtlLibs(b, o, t []string, conflicts *[]fhr.SemanticConflict) []string {
	jb, jo, jt := strings.Join(b, " "), strings.Join(o, " "), strings.Join(t, " ")
	switch {
	case jo == jt, jt == jb:
		return o
	case jo == jb:
		return t
	}
	*conflicts = append(*conflicts, fhr.SemanticConflict{Path: "mtllib", Ours: jo, Theirs: jt})
	return o
}

// summarize describes a node's own content for a conflict, e.g.
// "12 faces, 2 lines; usemtl Steel, Brass".
func summarize(n *objNode) string {
	if n == nil {
		return "removed"
	}
	var counts [3]int
	var mats []string
	seen := map[string]bool{}
	for _, e := range n.elems {
		counts[e.kind]++
		if e.material != "" && !seen[e.material] {
			seen[e.material] = true
			mats = append(mats, e.material)
		}
	}
	var parts []string
	for k, noun := range []string{"face", "line", "point"} {
		if counts[k] == 0 {
			continue
		}
		if counts[k] != 1 {
			noun += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], noun))
	}
	s := strings.Join(parts, ", ")
	if s == "" {
		s = "no elements"
	}
	if len(mats) > 0 {
		s += "; usemtl " + strings.Join(mats, ", ")
	}
	return s
}
