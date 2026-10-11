package main

import (
	"sort"
	"strconv"
	"strings"
)

// A writeNode is one object or group to write: its statement and its
// elements, each with the file whose pools it indexes into. A merged file
// mixes both sides — down to single elements, when a group was reshaped on
// one side and recoloured on the other — so every element brings its pools.
type writeNode struct {
	stmt, rawName string
	elems         []placedElem
	children      []*writeNode
}

type placedElem struct {
	src *objFile
	objElem
}

func placed(src *objFile, elems []objElem) []placedElem {
	out := make([]placedElem, len(elems))
	for i, e := range elems {
		out[i] = placedElem{src, e}
	}
	return out
}

// writeOBJ serializes nodes as a Wavefront OBJ.
//
// Each node writes the vertices its own elements use — positions, then
// texture coordinates, then normals, in the order of their source's pools,
// each line copied from the text the parser kept — followed by its elements
// renumbered against what has been written so far. So indices are always
// right however the nodes were combined, and a vertex line is byte-for-byte
// the line it was read from. (A vertex two groups shared in the source is
// written once per group: the geometry is the same, the pools are not.)
//
// usemtl and s are written only when they change, in the order elements need
// them. Comments other than the leading header are not carried.
func writeOBJ(header, mtlLibs []string, roots []*writeNode) []byte {
	var b strings.Builder
	for _, h := range header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	if len(mtlLibs) > 0 {
		b.WriteString("mtllib " + strings.Join(mtlLibs, " ") + "\n")
	}

	w := objWriter{b: &b}
	// The implicit node holds elements that precede every o/g, so it can only
	// be written first: anywhere else its elements would join the group
	// before them.
	sorted := make([]*writeNode, 0, len(roots))
	for _, r := range roots {
		if r.stmt == "" {
			sorted = append(sorted, r)
		}
	}
	for _, r := range roots {
		if r.stmt != "" {
			sorted = append(sorted, r)
		}
	}
	for _, r := range sorted {
		w.node(r)
	}
	return []byte(b.String())
}

type objWriter struct {
	b                *strings.Builder
	nv, nvt, nvn     int
	material, smooth string // state in effect; "" is none / off
}

func (w *objWriter) node(n *writeNode) {
	switch n.stmt {
	case "o", "g":
		w.b.WriteString(n.stmt)
		if n.rawName != "" {
			w.b.WriteString(" " + n.rawName)
		}
		w.b.WriteByte('\n')
	}

	if len(n.elems) > 0 {
		v := w.pool(n, func(r vref) int { return r.v }, func(f *objFile) []string { return f.posText }, "v", &w.nv)
		vt := w.pool(n, func(r vref) int { return r.vt }, func(f *objFile) []string { return f.uvText }, "vt", &w.nvt)
		vn := w.pool(n, func(r vref) int { return r.vn }, func(f *objFile) []string { return f.normalText }, "vn", &w.nvn)

		for _, e := range n.elems {
			if e.material != w.material {
				w.b.WriteString(strings.TrimSpace("usemtl "+e.material) + "\n")
				w.material = e.material
			}
			if e.smooth != w.smooth {
				s := e.smooth
				if s == "" {
					s = "off"
				}
				w.b.WriteString("s " + s + "\n")
				w.smooth = e.smooth
			}
			w.b.WriteString([]string{"f", "l", "p"}[e.kind])
			for _, r := range e.refs {
				w.b.WriteString(" " + strconv.Itoa(v[poolRef{e.src, r.v}]))
				switch {
				case r.vt >= 0 && r.vn >= 0:
					w.b.WriteString("/" + strconv.Itoa(vt[poolRef{e.src, r.vt}]) + "/" + strconv.Itoa(vn[poolRef{e.src, r.vn}]))
				case r.vt >= 0:
					w.b.WriteString("/" + strconv.Itoa(vt[poolRef{e.src, r.vt}]))
				case r.vn >= 0:
					w.b.WriteString("//" + strconv.Itoa(vn[poolRef{e.src, r.vn}]))
				}
			}
			w.b.WriteByte('\n')
		}
	}

	for _, c := range n.children {
		w.node(c)
	}
}

// poolRef is one entry of one source file's pool.
type poolRef struct {
	src *objFile
	i   int
}

// pool writes the entries of one pool (v, vt or vn) that the node's elements
// use — grouped by source in the order sources first appear, each in its own
// pool order — and returns each entry's new 1-based index.
func (w *objWriter) pool(n *writeNode, pick func(vref) int, text func(*objFile) []string, kw string, count *int) map[poolRef]int {
	srcOrder := map[*objFile]int{}
	used := map[poolRef]bool{}
	for _, e := range n.elems {
		if _, ok := srcOrder[e.src]; !ok {
			srcOrder[e.src] = len(srcOrder)
		}
		for _, r := range e.refs {
			if i := pick(r); i >= 0 {
				used[poolRef{e.src, i}] = true
			}
		}
	}
	order := make([]poolRef, 0, len(used))
	for r := range used {
		order = append(order, r)
	}
	sort.Slice(order, func(a, b int) bool {
		if sa, sb := srcOrder[order[a].src], srcOrder[order[b].src]; sa != sb {
			return sa < sb
		}
		return order[a].i < order[b].i
	})
	index := make(map[poolRef]int, len(order))
	for _, r := range order {
		w.b.WriteString(kw + " " + text(r.src)[r.i] + "\n")
		*count++
		index[r] = *count
	}
	return index
}

// asWriteNodes is a parsed file's tree as it would be written — what the
// round-trip tests and a merge that keeps a whole side use.
func asWriteNodes(f *objFile) []*writeNode {
	var conv func(n *objNode) *writeNode
	conv = func(n *objNode) *writeNode {
		w := &writeNode{stmt: n.stmt, rawName: n.rawName, elems: placed(f, n.elems)}
		for _, c := range n.children {
			w.children = append(w.children, conv(c))
		}
		return w
	}
	out := make([]*writeNode, len(f.roots))
	for i, r := range f.roots {
		out[i] = conv(r)
	}
	return out
}
