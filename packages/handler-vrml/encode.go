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

// defName makes a glTF name a legal, unique VRML identifier: none of the
// characters the grammar reserves (whitespace, quotes, '#', ',', '.', brackets,
// braces, backslash, '&', control characters), not starting with a digit or a
// sign, never repeated.
func defName(name, fallback string, i int, used map[string]bool) string {
	var b strings.Builder
	for _, r := range scene.SafeName(name, fallback, i) {
		switch {
		case r <= ' ', r == '"', r == '#', r == '\'', r == ',', r == '.', r == '[', r == ']', r == '\\', r == '{', r == '}', r == '&', r == '<', r == '>', r == 0x7f, r > 0x7e:
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

func vec3s(v [][3]float64) string {
	var b strings.Builder
	for i, p := range v {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(num(p[0]) + " " + num(p[1]) + " " + num(p[2]))
	}
	return b.String()
}

// encode writes the scene as VRML97: one Group per node that draws, one Shape per
// primitive, in world space (so no transforms to carry). Triangles are an
// IndexedFaceSet with Coordinate, and Normal / TextureCoordinate / Color when the
// primitive has them; lines an IndexedLineSet; points a PointSet. Materials keep
// their base colour; a repeated material is a USE of its first DEF.
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	var body strings.Builder
	used := map[string]bool{}
	materialDef := map[string]string{}
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		var shapes strings.Builder
		for _, p := range n.Prims {
			if len(p.Indices) == 0 {
				continue
			}
			var geo string
			switch p.Kind {
			case scene.KindTriangles:
				var idx strings.Builder
				for i := 0; i+2 < len(p.Indices); i += 3 {
					fmt.Fprintf(&idx, "%d, %d, %d, -1, ", p.Indices[i], p.Indices[i+1], p.Indices[i+2])
				}
				attrs := ""
				if p.Normals != nil {
					attrs += fmt.Sprintf("\n          normal Normal { vector [ %s ] }", vec3s(p.Normals))
				}
				if p.UVs != nil {
					var s strings.Builder
					for i, uv := range p.UVs {
						if i > 0 {
							s.WriteString(", ")
						}
						s.WriteString(num(uv[0]) + " " + num(1-uv[1])) // glTF v is down, VRML's up
					}
					attrs += fmt.Sprintf("\n          texCoord TextureCoordinate { point [ %s ] }", s.String())
				}
				if p.Colors != nil {
					cols := make([][3]float64, len(p.Colors))
					for i, c := range p.Colors {
						cols[i] = [3]float64{c[0], c[1], c[2]}
					}
					attrs += fmt.Sprintf("\n          color Color { color [ %s ] }", vec3s(cols))
				}
				geo = fmt.Sprintf("IndexedFaceSet {\n          solid FALSE\n          coord Coordinate { point [ %s ] }\n          coordIndex [ %s ]%s\n        }", vec3s(p.Positions), strings.TrimSuffix(idx.String(), ", "), attrs)
			case scene.KindLines:
				var idx strings.Builder
				for i := 0; i+1 < len(p.Indices); i += 2 {
					fmt.Fprintf(&idx, "%d, %d, -1, ", p.Indices[i], p.Indices[i+1])
				}
				geo = fmt.Sprintf("IndexedLineSet {\n          coord Coordinate { point [ %s ] }\n          coordIndex [ %s ]\n        }", vec3s(p.Positions), strings.TrimSuffix(idx.String(), ", "))
			case scene.KindPoints:
				geo = fmt.Sprintf("PointSet {\n          coord Coordinate { point [ %s ] }\n        }", vec3s(p.Positions))
			}
			app := ""
			if p.BaseColor != nil || p.Material != "" {
				c := [4]float64{0.8, 0.8, 0.8, 1}
				if p.BaseColor != nil {
					c = *p.BaseColor
				}
				key := fmt.Sprintf("%s|%v", p.Material, c)
				if d, ok := materialDef[key]; ok {
					app = fmt.Sprintf("\n        appearance Appearance { material USE %s }", d)
				} else {
					def := ""
					if p.Material != "" {
						d := defName(p.Material, "material", p.MaterialIndex, used)
						materialDef[key] = d
						def = "DEF " + d + " "
					}
					tr := ""
					if c[3] < 1 {
						tr = " transparency " + num(1-c[3])
					}
					app = fmt.Sprintf("\n        appearance Appearance { material %sMaterial { diffuseColor %s %s %s%s } }", def, num(c[0]), num(c[1]), num(c[2]), tr)
				}
			}
			fmt.Fprintf(&shapes, "      Shape {%s\n        geometry %s\n      }\n", app, geo)
			count++
		}
		if shapes.Len() > 0 {
			fmt.Fprintf(&body, "  DEF %s Group {\n    children [\n%s    ]\n  }\n", defName(n.Name, "node", n.Index, used), shapes.String())
		}
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has nothing to write")
	}
	return []byte("#VRML V2.0 utf8\n# Exported by FHR vrml handler\n" + body.String()), nil
}
