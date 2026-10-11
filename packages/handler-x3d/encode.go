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
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}

// defName makes a glTF name a legal, unique X3D DEF: no whitespace, quotes or
// the characters the spec forbids, not starting with a digit, never repeated.
func defName(name, fallback string, i int, used map[string]bool) string {
	var b strings.Builder
	for _, r := range scene.SafeName(name, fallback, i) {
		switch {
		case r <= ' ', r == '"', r == '#', r == '\'', r == ',', r == '.', r == '[', r == ']', r == '\\', r == '{', r == '}', r == '&', r == '<', r == '>', r == 0x7f:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s[0] >= '0' && s[0] <= '9' || s[0] == '+' || s[0] == '-' {
		s = "_" + s
	}
	base := s
	for n := 2; used[s]; n++ {
		s = base + "_" + strconv.Itoa(n)
	}
	used[s] = true
	return s
}

// encode writes the scene as X3D (XML, profile Immersive): one Group per node
// that draws, one Shape per primitive, in world space (so no transforms to
// carry). Triangles are an IndexedFaceSet with Coordinate, and Normal /
// TextureCoordinate / Color when the primitive has them; lines an
// IndexedLineSet; points a PointSet. Materials keep their base colour.
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	var body strings.Builder
	used := map[string]bool{}
	materialDef := map[string]string{} // material key → the DEF it was first written under
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		var shapes strings.Builder
		for _, p := range n.Prims {
			if len(p.Indices) == 0 {
				continue
			}
			var pts strings.Builder
			for i, v := range p.Positions {
				if i > 0 {
					pts.WriteByte(' ')
				}
				pts.WriteString(num(v[0]) + " " + num(v[1]) + " " + num(v[2]))
			}
			var geo string
			switch p.Kind {
			case scene.KindTriangles:
				var idx strings.Builder
				for i := 0; i+2 < len(p.Indices); i += 3 {
					fmt.Fprintf(&idx, "%d %d %d -1 ", p.Indices[i], p.Indices[i+1], p.Indices[i+2])
				}
				attrs := ""
				if p.Normals != nil {
					var s strings.Builder
					for i, v := range p.Normals {
						if i > 0 {
							s.WriteByte(' ')
						}
						s.WriteString(num(v[0]) + " " + num(v[1]) + " " + num(v[2]))
					}
					attrs += fmt.Sprintf("<Normal vector=\"%s\"/>", s.String())
				}
				if p.UVs != nil {
					var s strings.Builder
					for i, v := range p.UVs {
						if i > 0 {
							s.WriteByte(' ')
						}
						s.WriteString(num(v[0]) + " " + num(1-v[1])) // glTF v is down, X3D's up
					}
					attrs += fmt.Sprintf("<TextureCoordinate point=\"%s\"/>", s.String())
				}
				if p.Colors != nil {
					var s strings.Builder
					for i, v := range p.Colors {
						if i > 0 {
							s.WriteByte(' ')
						}
						s.WriteString(num(v[0]) + " " + num(v[1]) + " " + num(v[2]))
					}
					attrs += fmt.Sprintf("<Color color=\"%s\"/>", s.String())
				}
				geo = fmt.Sprintf("<IndexedFaceSet solid=\"false\" coordIndex=\"%s\"><Coordinate point=\"%s\"/>%s</IndexedFaceSet>", strings.TrimSpace(idx.String()), pts.String(), attrs)
			case scene.KindLines:
				var idx strings.Builder
				for i := 0; i+1 < len(p.Indices); i += 2 {
					fmt.Fprintf(&idx, "%d %d -1 ", p.Indices[i], p.Indices[i+1])
				}
				geo = fmt.Sprintf("<IndexedLineSet coordIndex=\"%s\"><Coordinate point=\"%s\"/></IndexedLineSet>", strings.TrimSpace(idx.String()), pts.String())
			case scene.KindPoints:
				geo = fmt.Sprintf("<PointSet><Coordinate point=\"%s\"/></PointSet>", pts.String())
			}
			app := ""
			if p.BaseColor != nil || p.Material != "" {
				c := [4]float64{0.8, 0.8, 0.8, 1}
				if p.BaseColor != nil {
					c = *p.BaseColor
				}
				key := fmt.Sprintf("%s|%v", p.Material, c)
				if d, ok := materialDef[key]; ok { // the same material again: reuse it
					app = fmt.Sprintf("<Appearance><Material USE=\"%s\"/></Appearance>", esc(d))
				} else {
					def := ""
					if p.Material != "" {
						d := defName(p.Material, "material", p.MaterialIndex, used)
						materialDef[key] = d
						def = fmt.Sprintf(" DEF=\"%s\"", esc(d))
					}
					tr := ""
					if c[3] < 1 {
						tr = fmt.Sprintf(" transparency=\"%s\"", num(1-c[3]))
					}
					app = fmt.Sprintf("<Appearance><Material%s diffuseColor=\"%s %s %s\"%s/></Appearance>", def, num(c[0]), num(c[1]), num(c[2]), tr)
				}
			}
			fmt.Fprintf(&shapes, "   <Shape>%s%s</Shape>\n", app, geo)
			count++
		}
		if shapes.Len() > 0 {
			fmt.Fprintf(&body, "  <Group DEF=\"%s\">\n%s  </Group>\n", esc(defName(n.Name, "node", n.Index, used)), shapes.String())
		}
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has nothing to write")
	}
	return []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<X3D profile=\"Immersive\" version=\"3.3\">\n <head><meta name=\"generator\" content=\"FHR x3d handler\"/></head>\n <Scene>\n" + body.String() + " </Scene>\n</X3D>\n"), nil
}
