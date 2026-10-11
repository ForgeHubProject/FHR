package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func num(v float64) string {
	f := float32(v)
	if f == 0 {
		f = 0 // not "-0"
	}
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

// layerName makes a glTF name a legal DXF layer name: none of < > / \ " : ; ? * | = `
// or control characters, and unique.
func layerName(name, fallback string, i int, used map[string]bool) string {
	var b strings.Builder
	for _, r := range scene.SafeName(name, fallback, i) {
		switch {
		case strings.ContainsRune("<>/\\\":;?*|=`", r), r < ' ', r == 0x7f:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 200 {
		s = s[:200]
	}
	base := s
	for n := 2; used[strings.ToUpper(s)]; n++ {
		s = base + "_" + strconv.Itoa(n)
	}
	used[strings.ToUpper(s)] = true
	return s
}

// encode writes the scene as R12-style ASCII DXF, in metres ($INSUNITS 6): a
// 3DFACE per triangle, LINE per segment and POINT per point, on a layer per node.
// glTF's Y-up is rotated to DXF's Z-up: (x, y, z) → (x, −z, y).
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	var ents strings.Builder
	var layers []string
	used := map[string]bool{"0": true}
	grp := func(code int, val string) { fmt.Fprintf(&ents, "%3d\n%s\n", code, val) }
	pt := func(base int, p [3]float64) {
		// The Y-up → Z-up rotation.
		grp(base, num(p[0]))
		grp(base+10, num(-p[2]))
		grp(base+20, num(p[1]))
	}
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		var any bool
		for _, p := range n.Prims {
			if len(p.Indices) > 0 {
				any = true
			}
		}
		if !any {
			return
		}
		layer := layerName(n.Name, "node", n.Index, used)
		layers = append(layers, layer)
		for _, p := range n.Prims {
			switch p.Kind {
			case scene.KindTriangles:
				for i := 0; i+2 < len(p.Indices); i += 3 {
					a, b, c := p.Positions[p.Indices[i]], p.Positions[p.Indices[i+1]], p.Positions[p.Indices[i+2]]
					grp(0, "3DFACE")
					grp(8, layer)
					pt(10, a)
					pt(11, b)
					pt(12, c)
					pt(13, c) // a triangle repeats its third corner
					count++
				}
			case scene.KindLines:
				for i := 0; i+1 < len(p.Indices); i += 2 {
					grp(0, "LINE")
					grp(8, layer)
					pt(10, p.Positions[p.Indices[i]])
					pt(11, p.Positions[p.Indices[i+1]])
					count++
				}
			case scene.KindPoints:
				for _, i := range p.Indices {
					grp(0, "POINT")
					grp(8, layer)
					pt(10, p.Positions[i])
					count++
				}
			}
		}
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has nothing to write")
	}
	var b strings.Builder
	w := func(code int, val string) { fmt.Fprintf(&b, "%3d\n%s\n", code, val) }
	w(0, "SECTION")
	w(2, "HEADER")
	w(9, "$ACADVER")
	w(1, "AC1009")
	w(9, "$INSUNITS")
	w(70, "6") // metres
	w(0, "ENDSEC")
	w(0, "SECTION")
	w(2, "TABLES")
	w(0, "TABLE")
	w(2, "LTYPE")
	w(70, "1")
	w(0, "LTYPE")
	w(2, "CONTINUOUS")
	w(70, "0")
	w(3, "Solid line")
	w(72, "65")
	w(73, "0")
	w(40, "0.0")
	w(0, "ENDTAB")
	w(0, "TABLE")
	w(2, "LAYER")
	w(70, strconv.Itoa(len(layers)+1))
	for _, l := range append([]string{"0"}, layers...) {
		w(0, "LAYER")
		w(2, l)
		w(70, "0")
		w(62, "7")
		w(6, "CONTINUOUS")
	}
	w(0, "ENDTAB")
	w(0, "ENDSEC")
	w(0, "SECTION")
	w(2, "ENTITIES")
	b.WriteString(ents.String())
	w(0, "ENDSEC")
	w(0, "EOF")
	return []byte(b.String()), nil
}
