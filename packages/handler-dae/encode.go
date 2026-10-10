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

// encode writes the scene as a Collada 1.4.1 document: Y-up, metres, one
// geometry per node that draws triangles (world-space, so no transforms to
// carry), one triangles group per material, and an effect + material for each
// distinct (name, colour). Normals and texture coordinates are written when
// every primitive of the node has them. Lines and points are dropped.
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	type mat struct {
		name  string
		color [4]float64
		has   bool
	}
	var mats []mat
	matIndex := map[string]int{}
	matOf := func(p *scene.FlatPrim) int {
		if p.Material == "" && p.BaseColor == nil {
			return -1
		}
		c := [4]float64{0.8, 0.8, 0.8, 1}
		if p.BaseColor != nil {
			c = *p.BaseColor
		}
		k := fmt.Sprintf("%s|%v", p.Material, c)
		i, ok := matIndex[k]
		if !ok {
			i = len(mats)
			matIndex[k] = i
			name := p.Material
			if name == "" {
				name = "color" + strconv.Itoa(i)
			}
			mats = append(mats, mat{name, c, p.BaseColor != nil})
		}
		return i
	}

	var geoms, nodes bytes.Buffer
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		var tris []*scene.FlatPrim
		for _, p := range n.Prims {
			if p.Kind == scene.KindTriangles && len(p.Indices) >= 3 {
				tris = append(tris, p)
			}
		}
		if len(tris) == 0 {
			return
		}
		id := count
		count++
		wantN, wantUV := true, true
		for _, p := range tris {
			wantN = wantN && p.Normals != nil
			wantUV = wantUV && p.UVs != nil
		}
		var pos, nrm, uv strings.Builder
		nv := 0
		type group struct {
			mat int
			idx []int
		}
		var groups []*group
		groupOf := map[int]*group{}
		for _, p := range tris {
			base := nv
			for i, v := range p.Positions {
				pos.WriteString(num(v[0]) + " " + num(v[1]) + " " + num(v[2]) + " ")
				if wantN {
					nrm.WriteString(num(p.Normals[i][0]) + " " + num(p.Normals[i][1]) + " " + num(p.Normals[i][2]) + " ")
				}
				if wantUV {
					uv.WriteString(num(p.UVs[i][0]) + " " + num(1-p.UVs[i][1]) + " ") // glTF v is down, Collada's up
				}
				nv++
			}
			mi := matOf(p)
			g := groupOf[mi]
			if g == nil {
				g = &group{mat: mi}
				groupOf[mi] = g
				groups = append(groups, g)
			}
			for _, i := range p.Indices {
				g.idx = append(g.idx, base+int(i))
			}
		}

		fmt.Fprintf(&geoms, "  <geometry id=\"geom-%d\" name=\"%s\">\n   <mesh>\n", id, esc(scene.SafeName(n.Name, "node", n.Index)))
		fmt.Fprintf(&geoms, "    <source id=\"geom-%d-pos\"><float_array id=\"geom-%d-pos-array\" count=\"%d\">%s</float_array><technique_common><accessor source=\"#geom-%d-pos-array\" count=\"%d\" stride=\"3\"><param name=\"X\" type=\"float\"/><param name=\"Y\" type=\"float\"/><param name=\"Z\" type=\"float\"/></accessor></technique_common></source>\n", id, id, nv*3, strings.TrimSpace(pos.String()), id, nv)
		inputs := fmt.Sprintf("     <input semantic=\"VERTEX\" source=\"#geom-%d-vtx\" offset=\"0\"/>\n", id)
		if wantN {
			fmt.Fprintf(&geoms, "    <source id=\"geom-%d-nrm\"><float_array id=\"geom-%d-nrm-array\" count=\"%d\">%s</float_array><technique_common><accessor source=\"#geom-%d-nrm-array\" count=\"%d\" stride=\"3\"><param name=\"X\" type=\"float\"/><param name=\"Y\" type=\"float\"/><param name=\"Z\" type=\"float\"/></accessor></technique_common></source>\n", id, id, nv*3, strings.TrimSpace(nrm.String()), id, nv)
			inputs += fmt.Sprintf("     <input semantic=\"NORMAL\" source=\"#geom-%d-nrm\" offset=\"0\"/>\n", id)
		}
		if wantUV {
			fmt.Fprintf(&geoms, "    <source id=\"geom-%d-uv\"><float_array id=\"geom-%d-uv-array\" count=\"%d\">%s</float_array><technique_common><accessor source=\"#geom-%d-uv-array\" count=\"%d\" stride=\"2\"><param name=\"S\" type=\"float\"/><param name=\"T\" type=\"float\"/></accessor></technique_common></source>\n", id, id, nv*2, strings.TrimSpace(uv.String()), id, nv)
			inputs += fmt.Sprintf("     <input semantic=\"TEXCOORD\" source=\"#geom-%d-uv\" offset=\"0\" set=\"0\"/>\n", id)
		}
		fmt.Fprintf(&geoms, "    <vertices id=\"geom-%d-vtx\"><input semantic=\"POSITION\" source=\"#geom-%d-pos\"/></vertices>\n", id, id)
		var bind strings.Builder
		for _, g := range groups {
			sym := ""
			if g.mat >= 0 {
				sym = fmt.Sprintf(" material=\"sym-%d\"", g.mat)
				fmt.Fprintf(&bind, "<instance_material symbol=\"sym-%d\" target=\"#mat-%d\"/>", g.mat, g.mat)
			}
			var p strings.Builder
			for i, v := range g.idx {
				if i > 0 {
					p.WriteByte(' ')
				}
				p.WriteString(strconv.Itoa(v))
			}
			fmt.Fprintf(&geoms, "    <triangles%s count=\"%d\">\n%s     <p>%s</p>\n    </triangles>\n", sym, len(g.idx)/3, inputs, p.String())
		}
		geoms.WriteString("   </mesh>\n  </geometry>\n")

		bm := ""
		if bind.Len() > 0 {
			bm = "<bind_material><technique_common>" + bind.String() + "</technique_common></bind_material>"
		}
		fmt.Fprintf(&nodes, "   <node id=\"node-%d\" name=\"%s\"><instance_geometry url=\"#geom-%d\">%s</instance_geometry></node>\n", id, esc(scene.SafeName(n.Name, "node", n.Index)), id, bm)
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}

	var effects, materials strings.Builder
	for i, m := range mats {
		fmt.Fprintf(&effects, "  <effect id=\"fx-%d\"><profile_COMMON><technique sid=\"common\"><phong><diffuse><color>%s %s %s %s</color></diffuse></phong></technique></profile_COMMON></effect>\n", i, num(m.color[0]), num(m.color[1]), num(m.color[2]), num(m.color[3]))
		fmt.Fprintf(&materials, "  <material id=\"mat-%d\" name=\"%s\"><instance_effect url=\"#fx-%d\"/></material>\n", i, esc(m.name), i)
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<COLLADA xmlns=\"http://www.collada.org/2005/11/COLLADASchema\" version=\"1.4.1\">\n <asset><contributor><authoring_tool>FHR dae handler</authoring_tool></contributor><unit name=\"meter\" meter=\"1\"/><up_axis>Y_UP</up_axis></asset>\n")
	if len(mats) > 0 {
		b.WriteString(" <library_effects>\n" + effects.String() + " </library_effects>\n <library_materials>\n" + materials.String() + " </library_materials>\n")
	}
	b.WriteString(" <library_geometries>\n" + geoms.String() + " </library_geometries>\n <library_visual_scenes>\n  <visual_scene id=\"scene\">\n" + nodes.String() + "  </visual_scene>\n </library_visual_scenes>\n <scene><instance_visual_scene url=\"#scene\"/></scene>\n</COLLADA>\n")
	return []byte(b.String()), nil
}
