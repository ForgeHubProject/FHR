// Package main is the VRML97 handler (.wrl, "#VRML V2.0 utf8"): the classic text
// encoding of the web 3D format X3D grew from. Its node and field names are X3D's,
// so the file is parsed into the same tree the X3D XML encoding gives and read
// by the one shared scene-graph reader: Transform (translation, center, rotation,
// scale, scaleOrientation), Group/Switch/LOD, DEF/USE, and Shapes with
// IndexedFaceSet, IndexedLineSet, PointSet or Box, with Coordinate, Normal,
// TextureCoordinate, Color and Material diffuse colour. gzip-compressed files are
// read too.
//
// Refused by name rather than dropped: VRML 1.0 (a different, stateful language),
// PROTO / EXTERNPROTO, Inline (other files), and geometry the reader does not
// tessellate (Sphere, Cone, Cylinder, ElevationGrid, Extrusion, Text). Script
// nodes, routes, lights, viewpoints and sensors are ignored.
//
// VRML97 is Y-up and metres, like glTF, so no axis or unit change is made.
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the VRML97 format.
var Codec = &scene.Codec{
	ID:      "vrml",
	Formats: []string{".wrl"},
	Decode:  decode,
	Encode:  encode,
}

const (
	maxInflate = 256 << 20
	maxDepth   = 64
	maxNodes   = 2_000_000
	maxValues  = 64 << 20
)

func decode(blob []byte) (*scene.Model, error) {
	if len(blob) >= 2 && blob[0] == 0x1f && blob[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			return nil, fmt.Errorf("not a VRML file: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(zr, maxInflate+1))
		if err != nil {
			return nil, fmt.Errorf("decompressing the .wrl: %w", err)
		}
		if len(data) > maxInflate {
			return nil, fmt.Errorf("the .wrl is larger than %d MiB once decompressed", maxInflate>>20)
		}
		blob = data
	}
	head := strings.TrimPrefix(string(blob[:min(len(blob), 64)]), "\xef\xbb\xbf")
	switch {
	case strings.HasPrefix(head, "#VRML V2.0"):
	case strings.HasPrefix(head, "#VRML V1.0"):
		return nil, fmt.Errorf("VRML 1.0 is not supported (it is a different, stateful language): only VRML97 (#VRML V2.0) files can be read")
	default:
		return nil, fmt.Errorf("not a VRML97 file: it must start with \"#VRML V2.0 utf8\"")
	}
	p := &parser{src: string(blob)}
	root := &scene.XMLNode{Name: "Scene", Attr: map[string]string{}}
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind == tEOF {
			break
		}
		n, err := p.statement(0)
		if err != nil {
			return nil, err
		}
		if n != nil {
			root.Kids = append(root.Kids, n)
		}
	}
	return scene.DecodeX3DScene(root)
}

// ── lexer ─────────────────────────────────────────────────────────────────────

type tokKind int

const (
	tEOF tokKind = iota
	tWord
	tNumber
	tString
	tOpen   // {
	tClose  // }
	tOpenB  // [
	tCloseB // ]
)

type token struct {
	kind tokKind
	s    string
	line int
}

type parser struct {
	src    string
	pos    int
	line   int
	tok    token
	peeked bool
	nodes  int
	values int
	// defTypes remembers the node type each DEF names, so a USE reads as a node
	// of that type — the X3D reader finds a Shape's Material by element name, and
	// X3D's XML spells a reuse as <Material USE="…"/>.
	defTypes map[string]string
}

func (p *parser) errf(f string, a ...any) error {
	return fmt.Errorf("line %d: %s", p.line+1, fmt.Sprintf(f, a...))
}

func (p *parser) lex() (token, error) {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '\n':
			p.line++
			p.pos++
		case c == ' ' || c == '\t' || c == '\r' || c == ',' || c == 0xef || c == 0xbb || c == 0xbf:
			p.pos++
		case c == '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			goto tok
		}
	}
	return token{kind: tEOF, line: p.line}, nil
tok:
	c := p.src[p.pos]
	line := p.line
	switch c {
	case '{':
		p.pos++
		return token{tOpen, "{", line}, nil
	case '}':
		p.pos++
		return token{tClose, "}", line}, nil
	case '[':
		p.pos++
		return token{tOpenB, "[", line}, nil
	case ']':
		p.pos++
		return token{tCloseB, "]", line}, nil
	case '"':
		var b strings.Builder
		p.pos++
		for p.pos < len(p.src) {
			ch := p.src[p.pos]
			if ch == '\\' && p.pos+1 < len(p.src) {
				b.WriteByte(p.src[p.pos+1])
				p.pos += 2
				continue
			}
			if ch == '"' {
				p.pos++
				return token{tString, b.String(), line}, nil
			}
			if ch == '\n' {
				p.line++
			}
			b.WriteByte(ch)
			p.pos++
		}
		return token{}, p.errf("unterminated string")
	}
	start := p.pos
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == ',' || ch == '{' || ch == '}' || ch == '[' || ch == ']' || ch == '"' || ch == '#' {
			break
		}
		p.pos++
	}
	w := p.src[start:p.pos]
	if w == "" {
		return token{}, p.errf("unexpected character %q", c)
	}
	if _, err := strconv.ParseFloat(w, 64); err == nil || isHex(w) {
		return token{tNumber, w, line}, nil
	}
	return token{tWord, w, line}, nil
}

func isHex(w string) bool {
	if len(w) > 2 && (w[0] == '0') && (w[1] == 'x' || w[1] == 'X') {
		_, err := strconv.ParseInt(w[2:], 16, 64)
		return err == nil
	}
	return false
}

func (p *parser) next() (token, error) {
	if p.peeked {
		p.peeked = false
		return p.tok, nil
	}
	return p.lex()
}

func (p *parser) peek() (token, error) {
	if !p.peeked {
		t, err := p.lex()
		if err != nil {
			return t, err
		}
		p.tok, p.peeked = t, true
	}
	return p.tok, nil
}

// ── parser ────────────────────────────────────────────────────────────────────

// statement reads one top-level or field-value node: "Type { … }", "DEF name Type
// { … }", "USE name", PROTO-ish constructs (refused) and ROUTEs (skipped).
func (p *parser) statement(depth int) (*scene.XMLNode, error) {
	if depth > maxDepth {
		return nil, p.errf("nodes nest deeper than %d", maxDepth)
	}
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	if t.kind != tWord {
		return nil, p.errf("expected a node, found %q", t.s)
	}
	switch t.s {
	case "PROTO", "EXTERNPROTO":
		return nil, p.errf("%s definitions are not supported", t.s)
	case "ROUTE": // ROUTE a.b TO c.d
		for i := 0; i < 3; i++ {
			if _, err := p.next(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	case "USE":
		n, err := p.next()
		if err != nil || n.kind != tWord {
			return nil, p.errf("USE needs a name")
		}
		typ := p.defTypes[n.s]
		if typ == "" {
			typ = "USE"
		}
		return &scene.XMLNode{Name: typ, Attr: map[string]string{"USE": n.s}}, nil
	case "DEF":
		n, err := p.next()
		if err != nil || n.kind != tWord {
			return nil, p.errf("DEF needs a name")
		}
		node, err := p.statement(depth + 1)
		if err != nil {
			return nil, err
		}
		if node != nil && node.Attr["USE"] == "" {
			node.Attr["DEF"] = n.s
			if p.defTypes == nil {
				p.defTypes = map[string]string{}
			}
			p.defTypes[n.s] = node.Name
		}
		return node, nil
	case "NULL":
		return nil, nil
	}
	return p.node(t.s, depth)
}

func (p *parser) node(typ string, depth int) (*scene.XMLNode, error) {
	p.nodes++
	if p.nodes > maxNodes {
		return nil, p.errf("more than %d nodes", maxNodes)
	}
	if typ == "Inline" {
		return nil, p.errf("Inline files are not supported: a blob cannot bring the files it points to")
	}
	if err := p.expect(tOpen, "{"); err != nil {
		return nil, err
	}
	n := &scene.XMLNode{Name: typ, Attr: map[string]string{}}
	script := typ == "Script"
	for {
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		switch t.kind {
		case tClose:
			if script { // a Script's body is code and declarations: not scene content
				return nil, nil
			}
			return n, nil
		case tEOF:
			return nil, p.errf("file ends inside %s", typ)
		case tWord:
		default:
			return nil, p.errf("expected a field name in %s, found %q", typ, t.s)
		}
		field := t.s
		if script {
			if err := p.skipScriptField(field, depth); err != nil {
				return nil, err
			}
			continue
		}
		if err := p.fieldValue(n, field, depth); err != nil {
			return nil, err
		}
	}
}

func (p *parser) expect(k tokKind, what string) error {
	t, err := p.next()
	if err != nil {
		return err
	}
	if t.kind != k {
		return p.errf("expected %s, found %q", what, t.s)
	}
	return nil
}

// skipScriptField consumes a Script field: "field SFFloat x 0", "eventIn …",
// "url "…"", "directOutput TRUE" — a keyword, then (for declarations) a type and
// a name, then a value.
func (p *parser) skipScriptField(field string, depth int) error {
	switch field {
	case "field", "exposedField":
		for i := 0; i < 2; i++ { // type, name
			if _, err := p.next(); err != nil {
				return err
			}
		}
	case "eventIn", "eventOut":
		for i := 0; i < 2; i++ {
			if _, err := p.next(); err != nil {
				return err
			}
		}
		return nil
	}
	return p.skipValue(depth)
}

func (p *parser) skipValue(depth int) error {
	t, err := p.peek()
	if err != nil {
		return err
	}
	switch t.kind {
	case tOpenB:
		p.peeked = false
		for d := 1; d > 0; {
			n, err := p.next()
			if err != nil {
				return err
			}
			switch n.kind {
			case tOpenB:
				d++
			case tCloseB:
				d--
			case tEOF:
				return p.errf("file ends inside a list")
			}
		}
	case tWord:
		if t.s == "DEF" || t.s == "USE" || t.s == "NULL" {
			_, err := p.statement(depth + 1)
			return err
		}
		p.peeked = false
		nt, err := p.peek()
		if err == nil && nt.kind == tOpen { // a node: Type { … }
			_, err = p.node(t.s, depth+1)
			return err
		}
		return err
	default:
		p.peeked = false
	}
	return nil
}

// fieldValue reads the value after a field name into n: a node (or list of
// nodes) becomes children; numbers, strings and booleans become the attribute.
func (p *parser) fieldValue(n *scene.XMLNode, field string, depth int) error {
	t, err := p.peek()
	if err != nil {
		return err
	}
	switch t.kind {
	case tOpenB:
		p.peeked = false
		var vals []string
		for {
			t, err := p.peek()
			if err != nil {
				return err
			}
			if t.kind == tCloseB {
				p.peeked = false
				break
			}
			if t.kind == tEOF {
				return p.errf("file ends inside a list")
			}
			if t.kind == tWord && !isBool(t.s) { // a node in an MFNode field
				c, err := p.statement(depth + 1)
				if err != nil {
					return err
				}
				if c != nil {
					n.Kids = append(n.Kids, c)
				}
				continue
			}
			p.peeked = false
			vals = append(vals, scalar(t))
			if p.values++; p.values > maxValues {
				return p.errf("too many values")
			}
		}
		if len(vals) > 0 {
			n.Attr[field] = strings.Join(vals, " ")
		}
	case tWord:
		if isBool(t.s) {
			p.peeked = false
			n.Attr[field] = strings.ToLower(t.s)
			return nil
		}
		c, err := p.statement(depth + 1)
		if err != nil {
			return err
		}
		if c != nil {
			n.Kids = append(n.Kids, c)
		}
	case tString:
		p.peeked = false
		n.Attr[field] = t.s
	case tNumber:
		var vals []string
		for {
			t, err := p.peek()
			if err != nil {
				return err
			}
			if t.kind != tNumber {
				break
			}
			p.peeked = false
			vals = append(vals, scalar(t))
			if p.values++; p.values > maxValues {
				return p.errf("too many values")
			}
		}
		n.Attr[field] = strings.Join(vals, " ")
	default:
		return p.errf("unexpected %q after field %s", t.s, field)
	}
	return nil
}

func isBool(s string) bool { return s == "TRUE" || s == "FALSE" }

// scalar is a value token as the text an X3D attribute would hold.
func scalar(t token) string {
	switch {
	case t.kind == tNumber && isHex(t.s):
		v, _ := strconv.ParseInt(t.s[2:], 16, 64)
		return strconv.FormatInt(v, 10)
	case t.kind == tWord && isBool(t.s):
		return strings.ToLower(t.s)
	case t.kind == tNumber:
		if f, err := strconv.ParseFloat(t.s, 64); err == nil && (math.IsNaN(f) || math.IsInf(f, 0)) {
			return "0"
		}
	}
	return t.s
}
