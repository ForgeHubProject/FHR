package scene

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// XMLNode is one element of a parsed XML document: a small generic tree for the
// formats (Collada, X3D) whose schemas are too loose to describe as structs.
// Names are local names — namespaces are ignored — and Text is the element's
// own character data, concatenated and trimmed.
type XMLNode struct {
	Name string
	Attr map[string]string
	Kids []*XMLNode
	Text string
}

// ParseXML reads a document into a tree. maxDepth and maxNodes bound what a
// hostile file can cost; custom entities are never expanded (Go's decoder
// leaves them alone), so entity bombs do not apply.
func ParseXML(b []byte, maxDepth, maxNodes int) (*XMLNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var root *XMLNode
	var stack []*XMLNode
	var text []*strings.Builder
	nodes := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			nodes++
			if nodes > maxNodes {
				return nil, fmt.Errorf("XML has more than %d elements", maxNodes)
			}
			if len(stack) >= maxDepth {
				return nil, fmt.Errorf("XML nests deeper than %d", maxDepth)
			}
			n := &XMLNode{Name: t.Name.Local, Attr: map[string]string{}}
			for _, a := range t.Attr {
				n.Attr[a.Name.Local] = a.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("XML has more than one root element")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.Kids = append(p.Kids, n)
			}
			stack = append(stack, n)
			text = append(text, &strings.Builder{})
		case xml.CharData:
			if len(text) > 0 {
				text[len(text)-1].Write(t)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("unbalanced XML")
			}
			n := stack[len(stack)-1]
			n.Text = strings.TrimSpace(text[len(text)-1].String())
			stack, text = stack[:len(stack)-1], text[:len(text)-1]
		}
	}
	if root == nil {
		return nil, fmt.Errorf("XML has no root element")
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("XML ends inside <%s>", stack[len(stack)-1].Name)
	}
	return root, nil
}

// Child is the first child element named name, or nil.
func (n *XMLNode) Child(name string) *XMLNode {
	for _, k := range n.Kids {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// Children are the child elements named name, in order.
func (n *XMLNode) Children(name string) []*XMLNode {
	var out []*XMLNode
	for _, k := range n.Kids {
		if k.Name == name {
			out = append(out, k)
		}
	}
	return out
}

// Floats parses whitespace/comma-separated numbers, failing on any that is not.
func Floats(s string, max int) ([]float64, error) {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' })
	if len(f) > max {
		return nil, fmt.Errorf("a number list has more than %d entries", max)
	}
	out := make([]float64, len(f))
	for i, t := range f {
		v, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("bad number %q", t)
		}
		out[i] = v
	}
	return out, nil
}

// Ints parses whitespace-separated non-negative integers.
func Ints(s string, max int) ([]int, error) {
	f := strings.Fields(s)
	if len(f) > max {
		return nil, fmt.Errorf("an index list has more than %d entries", max)
	}
	out := make([]int, len(f))
	for i, t := range f {
		v := 0
		if t == "" || len(t) > 10 {
			return nil, fmt.Errorf("bad index %q", t)
		}
		for _, c := range t {
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("bad index %q", t)
			}
			v = v*10 + int(c-'0')
		}
		out[i] = v
	}
	return out, nil
}
