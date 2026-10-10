// Package main is the 3MF (3D Manufacturing Format) handler: the ZIP-of-XML
// package 3D printers exchange. It reads meshes, component assemblies, build
// item placements and base-material / colour-group colours; and writes a
// single-model package with one object per node of the scene.
//
// 3MF carries a unit (millimetre by default) that glTF does not. Coordinates
// pass through unchanged in both directions, as they do for STL, so a file's
// numbers survive a transcode; the unit is the user's to know.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the 3MF format.
var Codec = &scene.Codec{
	ID:      "3mf",
	Formats: []string{".3mf"},
	Decode:  decode,
	Encode:  encode,
}

const (
	modelPath = "3D/3dmodel.model"
	// maxXML bounds what one package part may inflate to, so a zip bomb cannot
	// exhaust memory.
	maxXML = 256 << 20
	// maxDepth bounds component nesting (3MF forbids cycles; this enforces it).
	maxDepth = 32
)

type model struct {
	Unit      string `xml:"unit,attr"`
	Resources struct {
		Objects     []object        `xml:"object"`
		BaseGroups  []baseMaterials `xml:"basematerials"`
		ColorGroups []colorGroup    `xml:"colorgroup"`
	} `xml:"resources"`
	Build struct {
		Items []item `xml:"item"`
	} `xml:"build"`
}

type object struct {
	ID         int         `xml:"id,attr"`
	Name       string      `xml:"name,attr"`
	PID        *int        `xml:"pid,attr"`
	PIndex     *int        `xml:"pindex,attr"`
	Vertices   []vertex    `xml:"mesh>vertices>vertex"`
	Triangles  []triangle  `xml:"mesh>triangles>triangle"`
	Components []component `xml:"components>component"`
}

type vertex struct {
	X float64 `xml:"x,attr"`
	Y float64 `xml:"y,attr"`
	Z float64 `xml:"z,attr"`
}

type triangle struct {
	V1  int  `xml:"v1,attr"`
	V2  int  `xml:"v2,attr"`
	V3  int  `xml:"v3,attr"`
	PID *int `xml:"pid,attr"`
	P1  *int `xml:"p1,attr"`
}

type component struct {
	ObjectID  int    `xml:"objectid,attr"`
	Transform string `xml:"transform,attr"`
	Path      string `xml:"path,attr"` // production extension: another part of the package
}

type item struct {
	ObjectID  int    `xml:"objectid,attr"`
	Transform string `xml:"transform,attr"`
	Path      string `xml:"path,attr"`
}

type baseMaterials struct {
	ID    int `xml:"id,attr"`
	Bases []struct {
		Name  string `xml:"name,attr"`
		Color string `xml:"displaycolor,attr"`
	} `xml:"base"`
}

type colorGroup struct {
	ID     int `xml:"id,attr"`
	Colors []struct {
		Color string `xml:"color,attr"`
	} `xml:"color"`
}

func readPart(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxXML+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxXML {
		return nil, fmt.Errorf("%s is larger than %d MiB once unpacked", f.Name, maxXML>>20)
	}
	return data, nil
}

// modelFile finds the package's root model part: the target of the
// 3dmodel relationship, else the conventional path.
func modelFile(zr *zip.Reader) (*zip.File, error) {
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[strings.TrimPrefix(f.Name, "/")] = f
	}
	if rels := byName["_rels/.rels"]; rels != nil {
		if data, err := readPart(rels); err == nil {
			var r struct {
				Rels []struct {
					Target string `xml:"Target,attr"`
					Type   string `xml:"Type,attr"`
				} `xml:"Relationship"`
			}
			if xml.Unmarshal(data, &r) == nil {
				for _, rel := range r.Rels {
					if strings.HasSuffix(rel.Type, "/3dmodel") {
						if f := byName[strings.TrimPrefix(rel.Target, "/")]; f != nil {
							return f, nil
						}
					}
				}
			}
		}
	}
	if f := byName[modelPath]; f != nil {
		return f, nil
	}
	return nil, fmt.Errorf("not a 3MF package: no 3D model part")
}

func decode(blob []byte) (*scene.Model, error) {
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, fmt.Errorf("not a 3MF package: %w", err)
	}
	mf, err := modelFile(zr)
	if err != nil {
		return nil, err
	}
	data, err := readPart(mf)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", mf.Name, err)
	}
	var m model
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", mf.Name, err)
	}

	d := &decoder{objects: map[int]*object{}, groups: map[int][]matDef{}}
	for i := range m.Resources.Objects {
		o := &m.Resources.Objects[i]
		d.objects[o.ID] = o
	}
	for _, g := range m.Resources.BaseGroups {
		for _, b := range g.Bases {
			c, err := parseColor(b.Color)
			if err != nil {
				return nil, fmt.Errorf("material %q: %w", b.Name, err)
			}
			d.groups[g.ID] = append(d.groups[g.ID], matDef{name: b.Name, color: c})
		}
	}
	for _, g := range m.Resources.ColorGroups {
		for _, c := range g.Colors {
			col, err := parseColor(c.Color)
			if err != nil {
				return nil, err
			}
			d.groups[g.ID] = append(d.groups[g.ID], matDef{color: col})
		}
	}

	out := &scene.Model{}
	if len(m.Build.Items) > 0 {
		for _, it := range m.Build.Items {
			if it.Path != "" {
				return nil, fmt.Errorf("build item %d lives in another package part (%s): multi-part 3MF is not supported", it.ObjectID, it.Path)
			}
			o, err := d.object(it.ObjectID, it.Transform, 0)
			if err != nil {
				return nil, err
			}
			out.Roots = append(out.Roots, o)
		}
		return out, nil
	}
	// No build section: every object nothing else uses as a component.
	used := map[int]bool{}
	for _, o := range d.objects {
		for _, c := range o.Components {
			used[c.ObjectID] = true
		}
	}
	for _, o := range m.Resources.Objects {
		if used[o.ID] {
			continue
		}
		obj, err := d.object(o.ID, "", 0)
		if err != nil {
			return nil, err
		}
		out.Roots = append(out.Roots, obj)
	}
	return out, nil
}

type matDef struct {
	name  string
	color [4]float64 // linear RGBA
}

type decoder struct {
	objects map[int]*object
	groups  map[int][]matDef
}

func (d *decoder) object(id int, transform string, depth int) (*scene.Object, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("components nest deeper than %d (a cycle?)", maxDepth)
	}
	o, ok := d.objects[id]
	if !ok {
		return nil, fmt.Errorf("object %d is not defined", id)
	}
	mat, err := parseTransform(transform)
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", id, err)
	}
	out := &scene.Object{Name: o.Name, Matrix: mat}
	if out.Name == "" {
		out.Name = "object" + strconv.Itoa(id)
	}
	if len(o.Triangles) > 0 {
		prims, err := d.prims(o)
		if err != nil {
			return nil, fmt.Errorf("object %d: %w", id, err)
		}
		out.Prims = prims
	}
	for _, c := range o.Components {
		if c.Path != "" {
			return nil, fmt.Errorf("object %d: component in another package part (%s) is not supported", id, c.Path)
		}
		child, err := d.object(c.ObjectID, c.Transform, depth+1)
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, child)
	}
	return out, nil
}

// prims splits an object's triangles by material — one primitive per distinct
// (group, index), in order of first use — each with only the vertices it uses.
func (d *decoder) prims(o *object) ([]*scene.FlatPrim, error) {
	type key struct{ pid, idx int }
	var order []key
	tris := map[key][]triangle{}
	for i, t := range o.Triangles {
		for _, v := range []int{t.V1, t.V2, t.V3} {
			if v < 0 || v >= len(o.Vertices) {
				return nil, fmt.Errorf("triangle %d refers to vertex %d of %d", i, v, len(o.Vertices))
			}
		}
		k := key{-1, -1}
		pid, idx := o.PID, o.PIndex
		if t.PID != nil { // a triangle's own group overrides the object's
			pid, idx = t.PID, nil
		}
		if t.P1 != nil {
			idx = t.P1
		}
		if pid != nil {
			k.pid, k.idx = *pid, 0
			if idx != nil {
				k.idx = *idx
			}
		}
		if _, seen := tris[k]; !seen {
			order = append(order, k)
		}
		tris[k] = append(tris[k], t)
	}
	var out []*scene.FlatPrim
	for _, k := range order {
		p := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
		if k.pid >= 0 {
			g := d.groups[k.pid]
			if k.idx < 0 || k.idx >= len(g) {
				return nil, fmt.Errorf("material %d/%d is not defined", k.pid, k.idx)
			}
			p.Material = g[k.idx].name
			c := g[k.idx].color
			p.BaseColor = &c
		}
		remap := map[int]uint32{}
		for _, t := range tris[k] {
			for _, v := range []int{t.V1, t.V2, t.V3} {
				i, ok := remap[v]
				if !ok {
					i = uint32(len(p.Positions))
					remap[v] = i
					vt := o.Vertices[v]
					p.Positions = append(p.Positions, [3]float64{vt.X, vt.Y, vt.Z})
				}
				p.Indices = append(p.Indices, i)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// parseTransform reads 3MF's twelve-number affine (row-vector convention:
// x' = m00·x + m10·y + m20·z + m30) as the column-major matrix glTF uses —
// the same twelve numbers, in the same order, with the last row filled in.
func parseTransform(s string) (*[16]float64, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil, nil
	}
	if len(f) != 12 {
		return nil, fmt.Errorf("transform has %d numbers, want 12", len(f))
	}
	var v [12]float64
	for i, t := range f {
		x, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("bad transform number %q", t)
		}
		v[i] = x
	}
	return &[16]float64{
		v[0], v[1], v[2], 0,
		v[3], v[4], v[5], 0,
		v[6], v[7], v[8], 0,
		v[9], v[10], v[11], 1,
	}, nil
}

// parseColor reads #RRGGBB or #RRGGBBAA (sRGB) as linear RGBA, the space glTF's
// base colour factor is in.
func parseColor(s string) ([4]float64, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) != 6 && len(h) != 8 {
		return [4]float64{}, fmt.Errorf("bad colour %q (want #RRGGBB or #RRGGBBAA)", s)
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return [4]float64{}, fmt.Errorf("bad colour %q", s)
	}
	c := [4]float64{1, 1, 1, 1}
	if len(h) == 6 {
		n = n<<8 | 0xff
	}
	for i := 0; i < 4; i++ {
		c[i] = float64((n>>(8*(3-i)))&0xff) / 255
		if i < 3 {
			c[i] = srgbToLinear(c[i])
		}
	}
	return c, nil
}

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSRGB(c float64) float64 {
	c = math.Max(0, math.Min(1, c))
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

func hexColor(c [4]float64) string {
	b := func(v float64) int { return int(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	s := fmt.Sprintf("#%02X%02X%02X", b(linearToSRGB(c[0])), b(linearToSRGB(c[1])), b(linearToSRGB(c[2])))
	if a := b(c[3]); a != 255 {
		s += fmt.Sprintf("%02X", a)
	}
	return s
}

// encode writes the scene as a 3MF package: one object per node that draws
// triangles (world-space geometry, so no transforms to carry), one build item
// each, and a basematerials group for the surfaces that name a material or
// state a colour. Lines and points have no 3MF form and are dropped.
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	type mat struct {
		name  string
		color [4]float64
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
			mats = append(mats, mat{name, c})
		}
		return i
	}

	const matsID = 1
	var objs bytes.Buffer
	var build bytes.Buffer
	next := matsID + 1
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		var vb, tb bytes.Buffer
		nv, nt := 0, 0
		for _, p := range n.Prims {
			if p.Kind != scene.KindTriangles || len(p.Indices) < 3 {
				continue
			}
			mi := matOf(p)
			base := nv
			for _, v := range p.Positions {
				fmt.Fprintf(&vb, "     <vertex x=\"%s\" y=\"%s\" z=\"%s\"/>\n", num(v[0]), num(v[1]), num(v[2]))
				nv++
			}
			for i := 0; i+2 < len(p.Indices); i += 3 {
				fmt.Fprintf(&tb, "     <triangle v1=\"%d\" v2=\"%d\" v3=\"%d\"", base+int(p.Indices[i]), base+int(p.Indices[i+1]), base+int(p.Indices[i+2]))
				if mi >= 0 {
					fmt.Fprintf(&tb, " pid=\"%d\" p1=\"%d\"", matsID, mi)
				}
				tb.WriteString("/>\n")
				nt++
			}
		}
		if nt == 0 {
			return
		}
		id := next
		next++
		fmt.Fprintf(&objs, "  <object id=\"%d\" type=\"model\"%s>\n   <mesh>\n    <vertices>\n%s    </vertices>\n    <triangles>\n%s    </triangles>\n   </mesh>\n  </object>\n",
			id, nameAttr(n.Name), vb.String(), tb.String())
		fmt.Fprintf(&build, "  <item objectid=\"%d\"/>\n", id)
	})
	if next == matsID+1 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}

	var res bytes.Buffer
	if len(mats) > 0 {
		fmt.Fprintf(&res, "  <basematerials id=\"%d\">\n", matsID)
		for _, m := range mats {
			fmt.Fprintf(&res, "   <base name=\"%s\" displaycolor=\"%s\"/>\n", xmlEscape(m.name), hexColor(m.color))
		}
		res.WriteString("  </basematerials>\n")
	}
	doc := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<model unit=\"millimeter\" xml:lang=\"en-US\" xmlns=\"http://schemas.microsoft.com/3dmanufacturing/core/2015/02\">\n <metadata name=\"Application\">FHR 3mf handler</metadata>\n <resources>\n%s%s </resources>\n <build>\n%s </build>\n</model>\n", res.String(), objs.String(), build.String())
	return zipParts([]part{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/` + modelPath + `" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`},
		{modelPath, doc},
	})
}

type part struct{ name, body string }

// zipParts writes a deterministic ZIP: fixed (zero) timestamps, so the same
// scene always exports to the same bytes.
func zipParts(parts []part) ([]byte, error) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, p := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, p.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func nameAttr(n string) string {
	if n == "" {
		return ""
	}
	return ` name="` + xmlEscape(n) + `"`
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}

func num(v float64) string {
	f := float32(v)
	if f == 0 {
		f = 0
	}
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}
