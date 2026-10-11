package main

import (
	"bytes"
	"encoding/xml"
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

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// encode writes the scene as an uncompressed AMF in metres: one object per node
// that draws triangles (world-space, so no constellation to carry), one volume
// per primitive, and a material per distinct (name, colour). Lines and points
// are dropped.
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	type mat struct {
		id    int
		name  string
		color [4]float64
	}
	var mats []*mat
	matIndex := map[string]*mat{}
	matOf := func(p *scene.FlatPrim) *mat {
		if p.Material == "" && p.BaseColor == nil {
			return nil
		}
		c := [4]float64{0.8, 0.8, 0.8, 1}
		if p.BaseColor != nil {
			c = *p.BaseColor
		}
		k := fmt.Sprintf("%s|%v", p.Material, c)
		if m, ok := matIndex[k]; ok {
			return m
		}
		name := p.Material
		if name == "" {
			name = fmt.Sprintf("color%d", len(mats))
		}
		m := &mat{id: len(mats) + 1, name: name, color: c}
		matIndex[k] = m
		mats = append(mats, m)
		return m
	}

	var objects strings.Builder
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		var vb, volumes strings.Builder
		nv := 0
		for _, p := range n.Prims {
			if p.Kind != scene.KindTriangles || len(p.Indices) < 3 {
				continue
			}
			base := nv
			for _, v := range p.Positions {
				fmt.Fprintf(&vb, "    <vertex><coordinates><x>%s</x><y>%s</y><z>%s</z></coordinates></vertex>\n", num(v[0]), num(v[1]), num(v[2]))
				nv++
			}
			attr := ""
			if m := matOf(p); m != nil {
				attr = fmt.Sprintf(" materialid=\"%d\"", m.id)
			}
			fmt.Fprintf(&volumes, "   <volume%s>\n", attr)
			for i := 0; i+2 < len(p.Indices); i += 3 {
				fmt.Fprintf(&volumes, "    <triangle><v1>%d</v1><v2>%d</v2><v3>%d</v3></triangle>\n", base+int(p.Indices[i]), base+int(p.Indices[i+1]), base+int(p.Indices[i+2]))
			}
			volumes.WriteString("   </volume>\n")
		}
		if nv == 0 {
			return
		}
		fmt.Fprintf(&objects, "  <object id=\"%d\">\n   <metadata type=\"name\">%s</metadata>\n   <mesh>\n    <vertices>\n%s    </vertices>\n%s   </mesh>\n  </object>\n",
			count, esc(scene.SafeName(n.Name, "node", n.Index)), vb.String(), volumes.String())
		count++
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}
	var ms strings.Builder
	for _, m := range mats {
		fmt.Fprintf(&ms, "  <material id=\"%d\">\n   <metadata type=\"name\">%s</metadata>\n   <color><r>%s</r><g>%s</g><b>%s</b><a>%s</a></color>\n  </material>\n",
			m.id, esc(m.name), num(m.color[0]), num(m.color[1]), num(m.color[2]), num(m.color[3]))
	}
	return []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<amf unit=\"meter\" version=\"1.1\">\n <metadata type=\"cad\">FHR amf handler</metadata>\n" + objects.String() + ms.String() + "</amf>\n"), nil
}
