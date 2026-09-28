package main

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// defaultNode receives elements that appear before any o/g statement.
const defaultNode = "(default)"

// objFile is a parsed Wavefront OBJ: the global vertex pools every element
// indexes into, and the element hierarchy the file declares.
//
// Hierarchy: each `o` statement starts a new root node, even when the name
// repeats — exporters write one `o` per object, so a repeated name is two
// objects that share it (three.js's OBJExporter does exactly that for two
// meshes of one name), and Blender's importer and three.js's OBJLoader both
// read it that way. Each `g` is a child of the current `o`, or a root when no
// `o` is open, and reopening a group name reopens the same node: OBJ groups are
// sets, not blocks, so `g Wheel` twice under one object is one Wheel — but a
// Wheel under another object is a different node.
type objFile struct {
	positions [][3]float64
	colors    [][3]float64 // parallel to positions; hasColor says which are real
	hasColor  []bool
	uvs       [][2]float64
	normals   [][3]float64
	// The statements' own text after the keyword, fields single-spaced, per
	// pool entry — what the merge writes back, so a vertex it carries over is
	// the same line it read rather than a reformatted float.
	posText, uvText, normalText []string

	// Comment lines before the first statement: the exporter banner. A merged
	// file keeps its current side's.
	header []string

	roots   []*objNode
	mtlLibs []string // mtllib references, in order of first appearance

	// Statements this handler does not interpret (free-form geometry, display
	// attributes, …). They are not diffed semantically, but a change to them
	// must not read as "no change" — see diffUninterpreted — and a merge
	// refuses files that carry them, since it could not place them.
	other     map[string]int // keyword → count
	otherHash uint64         // hash of the uninterpreted lines, in order
}

// objNode is one object or group and the elements assigned to it.
type objNode struct {
	name string
	// stmt is the statement that opened it — "o", "g", or "" for the implicit
	// node holding elements that precede any o/g — and rawName its argument
	// text, so a merge writes the same statement back (a bare `g` stays bare).
	stmt, rawName string
	elems         []objElem
	children      []*objNode
	byName        map[string]*objNode // children by name
}

type elemKind uint8

const (
	elemFace elemKind = iota
	elemLine
	elemPoint
)

// objElem is one f/l/p statement with its references resolved to 0-based
// indices into the pools (-1: that component is absent).
type objElem struct {
	kind     elemKind
	material string
	smooth   string // smoothing group in effect; "" is off (`s off`, `s 0`, or none)
	refs     []vref
}

type vref struct{ v, vt, vn int }

// newChild appends a child node without making it reachable by name.
func (n *objNode) newChild(name, stmt, rawName string) *objNode {
	c := &objNode{name: name, stmt: stmt, rawName: rawName, byName: map[string]*objNode{}}
	n.children = append(n.children, c)
	return c
}

// child returns the named child, creating it on first use.
func (n *objNode) child(name, stmt, rawName string) *objNode {
	if c, ok := n.byName[name]; ok {
		return c
	}
	c := n.newChild(name, stmt, rawName)
	n.byName[name] = c
	return c
}

// parseOBJ reads a blob into an objFile. An empty blob is an empty file (the
// added/deleted-file side), not an error. Malformed numbers and references are
// errors with a line number; keywords outside the interpreted set are counted
// into other.
func parseOBJ(blob []byte) (*objFile, error) {
	f := &objFile{other: map[string]int{}}
	if len(blob) == 0 {
		return f, nil
	}
	if !utf8.Valid(blob) || strings.ContainsRune(string(blob), 0) {
		return nil, fmt.Errorf("parsing OBJ: not valid text")
	}

	top := &objNode{byName: map[string]*objNode{}} // holds the roots
	var object *objNode                            // current `o`, nil before the first
	var target *objNode                            // node receiving elements
	material := ""
	smooth := ""
	statements := false // seen anything but comments yet
	h := fnv.New64a()

	lines := strings.Split(string(blob), "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimSpace(lines[i])
		// A trailing backslash continues the statement on the next line.
		for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, "\\") + " " + strings.TrimSpace(lines[i])
		}
		if line == "" || line[0] == '#' {
			if !statements && line != "" {
				f.header = append(f.header, line)
			}
			continue
		}
		statements = true
		fields := strings.Fields(line)
		kw, args := fields[0], fields[1:]
		fail := func(format string, a ...any) error {
			return fmt.Errorf("parsing OBJ: line %d: %s", lineNo, fmt.Sprintf(format, a...))
		}

		switch kw {
		case "v":
			if len(args) < 3 {
				return nil, fail("vertex needs 3 coordinates, got %d", len(args))
			}
			p, err := floats(args[:3])
			if err != nil {
				return nil, fail("%v", err)
			}
			f.positions = append(f.positions, [3]float64{p[0], p[1], p[2]})
			f.posText = append(f.posText, strings.Join(args, " "))
			// `v x y z r g b` is the de-facto vertex-color extension; a 4th
			// value alone is the rational weight w, which meshes do not use.
			var c [3]float64
			hasColor := len(args) == 6
			if hasColor {
				rgb, err := floats(args[3:6])
				if err != nil {
					return nil, fail("%v", err)
				}
				c = [3]float64{rgb[0], rgb[1], rgb[2]}
			}
			f.colors = append(f.colors, c)
			f.hasColor = append(f.hasColor, hasColor)

		case "vt":
			if len(args) < 1 {
				return nil, fail("texture coordinate needs at least u")
			}
			n := min(len(args), 2)
			uv, err := floats(args[:n])
			if err != nil {
				return nil, fail("%v", err)
			}
			if n == 1 {
				uv = append(uv, 0)
			}
			f.uvs = append(f.uvs, [2]float64{uv[0], uv[1]})
			f.uvText = append(f.uvText, strings.Join(args, " "))

		case "vn":
			if len(args) < 3 {
				return nil, fail("normal needs 3 components, got %d", len(args))
			}
			nv, err := floats(args[:3])
			if err != nil {
				return nil, fail("%v", err)
			}
			f.normals = append(f.normals, [3]float64{nv[0], nv[1], nv[2]})
			f.normalText = append(f.normalText, strings.Join(args, " "))

		case "o":
			object = top.newChild(nameOf(args), "o", strings.Join(args, " "))
			target = object

		case "g":
			// `g` with several names puts the elements in several groups; OBJ
			// viewers (three.js's OBJLoader among them) treat the whole list as
			// one name, and so does this handler.
			raw := strings.Join(args, " ")
			if object != nil {
				target = object.child(nameOf(args), "g", raw)
			} else {
				target = top.child(nameOf(args), "g", raw)
			}

		case "usemtl":
			material = strings.Join(args, " ")

		case "s":
			// Smoothing groups apply to the faces that follow. "off" and "0" are
			// the same state as no statement at all.
			smooth = strings.Join(args, " ")
			if smooth == "off" || smooth == "0" {
				smooth = ""
			}

		case "mtllib":
			for _, lib := range args {
				if !contains(f.mtlLibs, lib) {
					f.mtlLibs = append(f.mtlLibs, lib)
				}
			}

		case "f", "l", "p":
			e := objElem{material: material, smooth: smooth}
			minRefs := 3
			switch kw {
			case "l":
				e.kind, minRefs = elemLine, 2
			case "p":
				e.kind, minRefs = elemPoint, 1
			}
			if len(args) < minRefs {
				return nil, fail("%q needs at least %d vertex references, got %d", kw, minRefs, len(args))
			}
			for _, ref := range args {
				r, err := f.resolve(ref)
				if err != nil {
					return nil, fail("%v", err)
				}
				e.refs = append(e.refs, r)
			}
			if target == nil {
				target = top.child(defaultNode, "", "")
			}
			target.elems = append(target.elems, e)

		default:
			f.other[kw]++
			h.Write([]byte(line))
			h.Write([]byte{'\n'})
		}
	}

	f.roots = top.children
	f.otherHash = h.Sum64()
	return f, f.checkRefs()
}

func nameOf(args []string) string {
	if n := strings.Join(args, " "); n != "" {
		return n
	}
	return defaultNode
}

func floats(ss []string) ([]float64, error) {
	out := make([]float64, len(ss))
	for i, s := range ss {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", s)
		}
		out[i] = v
	}
	return out, nil
}

// resolve turns one "v", "v/vt", "v//vn" or "v/vt/vn" reference into 0-based
// pool indices. Negative indices count back from the vertices defined so far,
// which is why this runs as each statement is read.
func (f *objFile) resolve(ref string) (vref, error) {
	parts := strings.Split(ref, "/")
	if len(parts) > 3 || parts[0] == "" {
		return vref{}, fmt.Errorf("invalid vertex reference %q", ref)
	}
	r := vref{v: -1, vt: -1, vn: -1}
	pools := []int{len(f.positions), len(f.uvs), len(f.normals)}
	out := []*int{&r.v, &r.vt, &r.vn}
	for i, part := range parts {
		if part == "" {
			continue // the v//vn form
		}
		n, err := strconv.Atoi(part)
		if err != nil || n == 0 {
			return vref{}, fmt.Errorf("invalid vertex reference %q", ref)
		}
		if n < 0 {
			n = pools[i] + n + 1
			if n < 1 {
				return vref{}, fmt.Errorf("vertex reference %q points before the first vertex", ref)
			}
		}
		*out[i] = n - 1
	}
	return r, nil
}

// checkRefs validates every reference against the final pool sizes. Positive
// references are checked here rather than while reading, because some writers
// emit an element before the vertices it uses.
func (f *objFile) checkRefs() error {
	var walk func(n *objNode) error
	walk = func(n *objNode) error {
		for _, e := range n.elems {
			for _, r := range e.refs {
				switch {
				case r.v >= len(f.positions):
					return fmt.Errorf("parsing OBJ: %q references vertex %d, the file has %d", n.name, r.v+1, len(f.positions))
				case r.vt >= len(f.uvs):
					return fmt.Errorf("parsing OBJ: %q references texture coordinate %d, the file has %d", n.name, r.vt+1, len(f.uvs))
				case r.vn >= len(f.normals):
					return fmt.Errorf("parsing OBJ: %q references normal %d, the file has %d", n.name, r.vn+1, len(f.normals))
				}
			}
		}
		for _, c := range n.children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range f.roots {
		if err := walk(r); err != nil {
			return err
		}
	}
	return nil
}

// otherSummary renders the uninterpreted-statement counts, e.g. "curv ×2, s ×14".
func (f *objFile) otherSummary() string {
	if len(f.other) == 0 {
		return ""
	}
	kws := make([]string, 0, len(f.other))
	for kw := range f.other {
		kws = append(kws, kw)
	}
	sort.Strings(kws)
	parts := make([]string, len(kws))
	for i, kw := range kws {
		parts[i] = fmt.Sprintf("%s ×%d", kw, f.other[kw])
	}
	return strings.Join(parts, ", ")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
