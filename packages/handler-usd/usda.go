package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ── values ────────────────────────────────────────────────────────────────────

// A USD value is one of: float64 (a number), token / str / asset / path (the
// four kinds of text), *numArray (an array of numbers or number tuples, flat),
// []any (an array of anything else), map[string]any (a dictionary), nil (None).
type (
	token string // a bare identifier, or a quoted token
	str   string // a quoted string
	asset string // @path@
	path  string // </Prim/Path>
)

// numArray is a (possibly tuple-valued) number array stored flat: dim is the
// tuple arity (1 for scalars), data holds len(data)/dim elements.
type numArray struct {
	data []float64
	dim  int
}

func (a *numArray) len() int { return len(a.data) / a.dim }

// prim is a scene-description node as written, before any interpretation.
type prim struct {
	spec  string // def, over, class
	typ   string // Mesh, Xform, … ("" for an untyped def)
	name  string
	meta  map[string]any
	props map[string]*property
	order []string // property names in file order
	kids  []*prim
}

type property struct {
	typ         string // "float3[]", "token", … ; "rel" for a relationship
	value       any
	timeSamples []sample // set when written as name.timeSamples = {…}
	meta        map[string]any
}

type sample struct {
	t float64
	v any
}

// layer is a parsed .usda file.
type layer struct {
	meta  map[string]any
	prims []*prim
}

// ── lexer ─────────────────────────────────────────────────────────────────────

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tAsset
	tPath
	tPunct
)

type lexTok struct {
	kind tokKind
	s    string
	num  float64
	line int
}

type lexer struct {
	src  string
	pos  int
	line int
	buf  []lexTok
}

func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == ':' || c == '.'
}

func (l *lexer) errf(f string, a ...any) error {
	return fmt.Errorf("line %d: %s", l.line, fmt.Sprintf(f, a...))
}

func (l *lexer) lex() (lexTok, error) {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n':
			l.line++
			l.pos++
		case c == ' ' || c == '\t' || c == '\r':
			l.pos++
		case c == '#': // comment to end of line
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		default:
			goto token
		}
	}
	return lexTok{kind: tEOF, line: l.line}, nil
token:
	c := l.src[l.pos]
	line := l.line
	switch {
	case c == '"' || c == '\'':
		s, err := l.readString(c)
		return lexTok{kind: tString, s: s, line: line}, err
	case c == '@':
		s, err := l.readAsset()
		return lexTok{kind: tAsset, s: s, line: line}, err
	case c == '<':
		end := strings.IndexByte(l.src[l.pos:], '>')
		if end < 0 {
			return lexTok{}, l.errf("unterminated path")
		}
		s := l.src[l.pos+1 : l.pos+end]
		l.pos += end + 1
		return lexTok{kind: tPath, s: s, line: line}, nil
	case strings.IndexByte("()[]{},=;:", c) >= 0:
		l.pos++
		return lexTok{kind: tPunct, s: string(c), line: line}, nil
	case (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.':
		return l.readNumber()
	case isIdentStart(c):
		start := l.pos
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		return lexTok{kind: tIdent, s: l.src[start:l.pos], line: line}, nil
	}
	return lexTok{}, l.errf("unexpected character %q", c)
}

func (l *lexer) readNumber() (lexTok, error) {
	start, line := l.pos, l.line
	l.pos++
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || ((c == '+' || c == '-') && (l.src[l.pos-1] == 'e' || l.src[l.pos-1] == 'E')) {
			l.pos++
			continue
		}
		break
	}
	// -inf, +inf and -nan are written with letters after the sign.
	if l.pos < len(l.src) && isIdentStart(l.src[l.pos]) {
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
	}
	text := l.src[start:l.pos]
	switch strings.TrimLeft(text, "+-") {
	case "inf":
		if strings.HasPrefix(text, "-") {
			return lexTok{kind: tNumber, num: math.Inf(-1), line: line}, nil
		}
		return lexTok{kind: tNumber, num: math.Inf(1), line: line}, nil
	case "nan":
		return lexTok{kind: tNumber, num: math.NaN(), line: line}, nil
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return lexTok{}, l.errf("bad number %q", text)
	}
	return lexTok{kind: tNumber, num: v, line: line}, nil
}

func (l *lexer) readString(q byte) (string, error) {
	triple := strings.HasPrefix(l.src[l.pos:], strings.Repeat(string(q), 3))
	if triple {
		l.pos += 3
	} else {
		l.pos++
	}
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\\' && l.pos+1 < len(l.src):
			n := l.src[l.pos+1]
			switch n {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(n)
			}
			l.pos += 2
		case triple && strings.HasPrefix(l.src[l.pos:], strings.Repeat(string(q), 3)):
			l.pos += 3
			return b.String(), nil
		case !triple && c == q:
			l.pos++
			return b.String(), nil
		case c == '\n' && !triple:
			return "", l.errf("unterminated string")
		default:
			if c == '\n' {
				l.line++
			}
			b.WriteByte(c)
			l.pos++
		}
	}
	return "", l.errf("unterminated string")
}

func (l *lexer) readAsset() (string, error) {
	if strings.HasPrefix(l.src[l.pos:], "@@@") {
		end := strings.Index(l.src[l.pos+3:], "@@@")
		if end < 0 {
			return "", l.errf("unterminated asset path")
		}
		s := l.src[l.pos+3 : l.pos+3+end]
		l.pos += 6 + end
		return s, nil
	}
	end := strings.IndexByte(l.src[l.pos+1:], '@')
	if end < 0 {
		return "", l.errf("unterminated asset path")
	}
	s := l.src[l.pos+1 : l.pos+1+end]
	l.pos += end + 2
	return s, nil
}

// ── parser ────────────────────────────────────────────────────────────────────

const (
	maxPrims   = 1_000_000
	maxDepthU  = 64
	maxNumbers = 64 << 20
)

type parser struct {
	lx     *lexer
	tok    lexTok
	peeked bool
	prims  int
	nums   int
}

func (p *parser) next() (lexTok, error) {
	if p.peeked {
		p.peeked = false
		return p.tok, nil
	}
	t, err := p.lx.lex()
	p.tok = t
	return t, err
}

func (p *parser) peek() (lexTok, error) {
	if !p.peeked {
		t, err := p.lx.lex()
		if err != nil {
			return t, err
		}
		p.tok, p.peeked = t, true
	}
	return p.tok, nil
}

func (p *parser) punct(s string) (bool, error) {
	t, err := p.peek()
	if err != nil {
		return false, err
	}
	if t.kind == tPunct && t.s == s {
		p.peeked = false
		return true, nil
	}
	return false, nil
}

func (p *parser) expect(s string) error {
	ok, err := p.punct(s)
	if err != nil {
		return err
	}
	if !ok {
		t, _ := p.peek()
		return p.lx.errf("expected %q, found %q", s, t.s)
	}
	return nil
}

func parseUSDA(src string) (*layer, error) {
	if !strings.HasPrefix(src, "#usda") {
		return nil, fmt.Errorf("not a USD text file: missing the #usda header")
	}
	p := &parser{lx: &lexer{src: src, line: 1}}
	l := &layer{meta: map[string]any{}}
	// Layer metadata: an optional ( … ) before the first prim.
	if ok, err := p.punct("("); err != nil {
		return nil, err
	} else if ok {
		m, err := p.metadata()
		if err != nil {
			return nil, err
		}
		l.meta = m
	}
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind == tEOF {
			return l, nil
		}
		pr, err := p.prim(0)
		if err != nil {
			return nil, err
		}
		l.prims = append(l.prims, pr)
	}
}

// metadata reads "key = value" entries (and bare doc strings) up to ')'.
func (p *parser) metadata() (map[string]any, error) {
	m := map[string]any{}
	for {
		if ok, err := p.punct(")"); err != nil {
			return nil, err
		} else if ok {
			return m, nil
		}
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		switch t.kind {
		case tString: // a bare doc string
			m["doc"] = str(t.s)
			continue
		case tIdent:
		default:
			return nil, p.lx.errf("unexpected %q in metadata", t.s)
		}
		key := t.s
		// list-op keywords: prepend apiSchemas = …
		for key == "prepend" || key == "append" || key == "add" || key == "delete" || key == "reorder" {
			n, err := p.next()
			if err != nil || n.kind != tIdent {
				return nil, p.lx.errf("expected a metadata key after %q", key)
			}
			key = n.s
		}
		if err := p.expect("="); err != nil {
			return nil, err
		}
		v, err := p.value(0)
		if err != nil {
			return nil, err
		}
		m[key] = v
		_, _ = p.punct(";")
	}
}

func (p *parser) prim(depth int) (*prim, error) {
	if depth > maxDepthU {
		return nil, p.lx.errf("prims nest deeper than %d", maxDepthU)
	}
	p.prims++
	if p.prims > maxPrims {
		return nil, p.lx.errf("more than %d prims", maxPrims)
	}
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	if t.kind != tIdent || (t.s != "def" && t.s != "over" && t.s != "class") {
		return nil, p.lx.errf("expected def, over or class, found %q", t.s)
	}
	pr := &prim{spec: t.s, meta: map[string]any{}, props: map[string]*property{}}
	n, err := p.next()
	if err != nil {
		return nil, err
	}
	if n.kind == tIdent { // a type name
		pr.typ = n.s
		if n, err = p.next(); err != nil {
			return nil, err
		}
	}
	if n.kind != tString {
		return nil, p.lx.errf("expected a prim name in quotes, found %q", n.s)
	}
	pr.name = n.s
	if ok, err := p.punct("("); err != nil {
		return nil, err
	} else if ok {
		if pr.meta, err = p.metadata(); err != nil {
			return nil, err
		}
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	for {
		if ok, err := p.punct("}"); err != nil {
			return nil, err
		} else if ok {
			return pr, nil
		}
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind == tEOF {
			return nil, p.lx.errf("file ends inside prim %q", pr.name)
		}
		if t.kind == tIdent && (t.s == "def" || t.s == "over" || t.s == "class") {
			k, err := p.prim(depth + 1)
			if err != nil {
				return nil, err
			}
			pr.kids = append(pr.kids, k)
			continue
		}
		if t.kind == tIdent && t.s == "variantSet" {
			return nil, p.lx.errf("variant sets (on %q) are not supported", pr.name)
		}
		if err := p.property(pr); err != nil {
			return nil, err
		}
	}
}

// property reads one attribute or relationship into pr.
func (p *parser) property(pr *prim) error {
	var words []string
	for {
		t, err := p.peek()
		if err != nil {
			return err
		}
		if t.kind != tIdent {
			break
		}
		p.peeked = false
		words = append(words, t.s)
	}
	if len(words) == 0 {
		t, _ := p.peek()
		return p.lx.errf("expected a property declaration, found %q", t.s)
	}
	typ, name := "", ""
	if ok, err := p.punct("["); err != nil { // "type[] name": the brackets follow the type
		return err
	} else if ok {
		if err := p.expect("]"); err != nil {
			return err
		}
		typ = words[len(words)-1] + "[]"
		words = words[:len(words)-1]
		t, err := p.next()
		if err != nil || t.kind != tIdent {
			return p.lx.errf("expected a property name after %q", typ)
		}
		name = t.s
	} else {
		name = words[len(words)-1]
		words = words[:len(words)-1]
	}
	rel := false
	for _, w := range words {
		switch w {
		case "custom", "uniform", "varying", "config", "prepend", "append", "add", "delete", "reorder":
		case "rel":
			rel = true
		default:
			if typ == "" {
				typ = w
			}
		}
	}
	if typ == "" && !rel {
		return p.lx.errf("property %q has no type", name)
	}
	prop := &property{typ: typ, meta: map[string]any{}}
	if rel {
		prop.typ = "rel"
	}
	isSamples := strings.HasSuffix(name, ".timeSamples")
	name = strings.TrimSuffix(name, ".timeSamples")
	if strings.HasSuffix(name, ".connect") {
		name = strings.TrimSuffix(name, ".connect")
		prop.meta["connect"] = true
	}
	if ok, err := p.punct("="); err != nil {
		return err
	} else if ok {
		v, err := p.value(0)
		if err != nil {
			return err
		}
		if single(typ) {
			v = toFloat32(v)
		}
		if isSamples {
			d, ok := v.(map[string]any)
			if !ok {
				return p.lx.errf("timeSamples of %q is not a dictionary", name)
			}
			for k, sv := range d {
				d[k] = toFloat32(sv)
			}
			prop.timeSamples = sortedSamples(d)
		} else {
			prop.value = v
		}
	}
	if ok, err := p.punct("("); err != nil {
		return err
	} else if ok {
		m, err := p.metadata()
		if err != nil {
			return err
		}
		for k, v := range m {
			prop.meta[k] = v
		}
	}
	if _, seen := pr.props[name]; seen && prop.value == nil && prop.timeSamples == nil {
		return nil // a bare re-declaration keeps the opinion already read
	}
	if _, seen := pr.props[name]; !seen {
		pr.order = append(pr.order, name)
	}
	pr.props[name] = prop
	return nil
}

func sortedSamples(d map[string]any) []sample {
	out := make([]sample, 0, len(d))
	for k, v := range d {
		t, err := strconv.ParseFloat(k, 64)
		if err != nil {
			continue
		}
		out = append(out, sample{t, v})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].t < out[j-1].t; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// value parses any value.
func (p *parser) value(depth int) (any, error) {
	if depth > maxDepthU {
		return nil, p.lx.errf("values nest deeper than %d", maxDepthU)
	}
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	switch t.kind {
	case tNumber:
		p.nums++
		return t.num, nil
	case tString:
		return str(t.s), nil
	case tAsset:
		return asset(t.s), nil
	case tPath:
		return path(t.s), nil
	case tIdent:
		if t.s == "None" {
			return nil, nil
		}
		return token(t.s), nil
	case tPunct:
		switch t.s {
		case "[":
			return p.array(depth)
		case "(":
			return p.tuple(depth)
		case "{":
			return p.dict(depth)
		}
	}
	return nil, p.lx.errf("unexpected %q in a value", t.s)
}

// tuple reads "(a, b, c)" — numbers become a one-element numArray row, which
// array() flattens; anything else a []any.
func (p *parser) tuple(depth int) (any, error) {
	var nums []float64
	var gen []any
	allNums := true
	for {
		if ok, err := p.punct(")"); err != nil {
			return nil, err
		} else if ok {
			break
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if f, ok := v.(float64); ok && allNums {
			nums = append(nums, f)
		} else {
			if allNums {
				for _, n := range nums {
					gen = append(gen, n)
				}
				allNums = false
			}
			gen = append(gen, v)
		}
		if _, err := p.punct(","); err != nil {
			return nil, err
		}
	}
	if allNums {
		return &numArray{data: nums, dim: max(len(nums), 1)}, nil
	}
	return gen, nil
}

func (p *parser) array(depth int) (any, error) {
	var flat []float64
	dim := 0
	var gen []any
	general := false
	for {
		if ok, err := p.punct("]"); err != nil {
			return nil, err
		} else if ok {
			break
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if !general {
			switch x := v.(type) {
			case float64:
				if dim == 0 || dim == 1 {
					dim = 1
					flat = append(flat, x)
				} else {
					general = true
				}
			case *numArray:
				if dim == 0 || dim == x.dim {
					dim = x.dim
					flat = append(flat, x.data...)
				} else {
					general = true
				}
			default:
				general = true
			}
			if general {
				gen = unflatten(flat, dim)
				gen = append(gen, v)
				flat = nil
			}
		} else {
			gen = append(gen, v)
		}
		if len(flat)+len(gen) > maxNumbers {
			return nil, p.lx.errf("array is too large")
		}
		if _, err := p.punct(","); err != nil {
			return nil, err
		}
	}
	if general {
		return gen, nil
	}
	if dim == 0 {
		dim = 1
	}
	return &numArray{data: flat, dim: dim}, nil
}

func unflatten(flat []float64, dim int) []any {
	if dim == 0 {
		return nil
	}
	var out []any
	for i := 0; i+dim <= len(flat); i += dim {
		if dim == 1 {
			out = append(out, flat[i])
		} else {
			out = append(out, &numArray{data: append([]float64(nil), flat[i:i+dim]...), dim: dim})
		}
	}
	return out
}

// dict reads "{ type key = value … }" (a dictionary) or "{ 0: v, 1: v }" (time samples).
func (p *parser) dict(depth int) (any, error) {
	m := map[string]any{}
	for {
		if ok, err := p.punct("}"); err != nil {
			return nil, err
		} else if ok {
			return m, nil
		}
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		switch {
		case t.kind == tNumber: // a time sample: <time> : <value>
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			m[strconv.FormatFloat(t.num, 'g', -1, 64)] = v
			_, _ = p.punct(",")
		case t.kind == tIdent || t.kind == tString:
			// dictionary entry: [type[]] key = value
			key := t.s
			if ok, err := p.punct("["); err != nil { // "string[] key = …"
				return nil, err
			} else if ok {
				if err := p.expect("]"); err != nil {
					return nil, err
				}
				k, err := p.next()
				if err != nil || k.kind != tIdent && k.kind != tString {
					return nil, p.lx.errf("expected a dictionary key")
				}
				key = k.s
			} else if n, err := p.peek(); err != nil {
				return nil, err
			} else if n.kind == tIdent || n.kind == tString { // a type came first
				p.peeked = false
				key = n.s
			}
			if err := p.expect("="); err != nil {
				return nil, err
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			m[key] = v
			_, _ = p.punct(";")
		default:
			return nil, p.lx.errf("unexpected %q in a dictionary", t.s)
		}
	}
}

// single reports whether a type is stored as 32-bit floats. Text writes them
// rounded to a few digits; the binary form holds the exact float32. Rounding
// what text says to float32 makes the same layer read the same either way.
func single(typ string) bool {
	switch strings.TrimSuffix(typ, "[]") {
	case "float", "float2", "float3", "float4", "point3f", "normal3f", "vector3f", "color3f", "color4f",
		"texCoord2f", "texCoord3f", "quatf", "half", "half2", "half3", "half4":
		return true
	}
	return false
}

func toFloat32(v any) any {
	switch x := v.(type) {
	case float64:
		return float64(float32(x))
	case *numArray:
		for i, f := range x.data {
			x.data[i] = float64(float32(f))
		}
	case []any:
		for i := range x {
			x[i] = toFloat32(x[i])
		}
	}
	return v
}
