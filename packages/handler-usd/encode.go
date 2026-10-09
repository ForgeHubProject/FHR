package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
	"time"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

func num(v float64) string {
	f := float32(v)
	if f == 0 {
		f = 0 // not "-0"
	}
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

// primName makes a name USD accepts for a prim: letters, digits and
// underscores, not starting with a digit, and not repeated among its siblings.
func primName(name, fallback string, i int, used map[string]bool) string {
	var b strings.Builder
	for _, r := range scene.SafeName(name, fallback, i) {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s[0] >= '0' && s[0] <= '9' {
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
		b.WriteString("(" + num(p[0]) + ", " + num(p[1]) + ", " + num(p[2]) + ")")
	}
	return b.String()
}

func ints(v []uint32) string {
	var b strings.Builder
	for i, x := range v {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.FormatUint(uint64(x), 10))
	}
	return b.String()
}

// encode writes the scene as text USD (.usd / .usda) or a .usdz holding one:
// Y-up, metres, a World Xform with one Xform per node that draws, a Mesh per
// primitive in world space (so no xformOps to carry), and a UsdPreviewSurface
// Material per distinct (name, colour). Triangles only — lines and points are
// dropped. The binary crate is not written.
func encode(roots []*scene.FlatNode, format string) ([]byte, error) {
	if format == ".usdc" {
		return nil, fmt.Errorf("writing binary USD (.usdc) is not supported: export .usda or .usd, which every USD tool reads")
	}
	type mat struct {
		name  string
		color [4]float64
		path  string
	}
	var mats []*mat
	matIndex := map[string]*mat{}
	matNames := map[string]bool{}
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
		name := primName(p.Material, "material", len(mats), matNames)
		m := &mat{name: name, color: c, path: "/World/Materials/" + name}
		matIndex[k] = m
		mats = append(mats, m)
		return m
	}

	var body strings.Builder
	names := map[string]bool{"Materials": true} // the scope that holds the materials
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
			if p.Kind != scene.KindTriangles || len(p.Indices) < 3 {
				continue
			}
			m := matOf(p)
			// Geometry is in world space, so a Mesh needs no Xform around it: it
			// carries the node's name, and a node with several primitives writes
			// several sibling meshes (name, name_2, …).
			name := primName(n.Name, "node", n.Index, names)
			nf := len(p.Indices) / 3
			counts := make([]string, nf)
			for i := range counts {
				counts[i] = "3"
			}
			if m != nil {
				fmt.Fprintf(&body, "    def Mesh \"%s\" (\n        prepend apiSchemas = [\"MaterialBindingAPI\"]\n    )\n    {\n", name)
			} else {
				fmt.Fprintf(&body, "    def Mesh \"%s\"\n    {\n", name)
			}
			fmt.Fprintf(&body, "        int[] faceVertexCounts = [%s]\n", strings.Join(counts, ", "))
			fmt.Fprintf(&body, "        int[] faceVertexIndices = [%s]\n", ints(p.Indices))
			if m != nil {
				fmt.Fprintf(&body, "        rel material:binding = <%s>\n", m.path)
			}
			if p.Normals != nil {
				fmt.Fprintf(&body, "        normal3f[] normals = [%s] (\n                interpolation = \"vertex\"\n            )\n", vec3s(p.Normals))
			}
			fmt.Fprintf(&body, "        point3f[] points = [%s]\n", vec3s(p.Positions))
			if p.UVs != nil {
				var b strings.Builder
				for i, uv := range p.UVs {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString("(" + num(uv[0]) + ", " + num(1-uv[1]) + ")") // glTF v is down, USD's up
				}
				fmt.Fprintf(&body, "        texCoord2f[] primvars:st = [%s] (\n                interpolation = \"vertex\"\n            )\n", b.String())
			}
			body.WriteString("        uniform token subdivisionScheme = \"none\"\n    }\n")
			count++
		}
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}

	var matsText strings.Builder
	if len(mats) > 0 {
		matsText.WriteString("    def Scope \"Materials\"\n    {\n")
		for _, m := range mats {
			fmt.Fprintf(&matsText, "        def Material \"%s\"\n        {\n            token outputs:surface.connect = <%s/PreviewSurface.outputs:surface>\n\n            def Shader \"PreviewSurface\"\n            {\n                uniform token info:id = \"UsdPreviewSurface\"\n                color3f inputs:diffuseColor = (%s, %s, %s)\n                float inputs:opacity = %s\n                float inputs:roughness = 0.9\n                token outputs:surface\n            }\n        }\n",
				m.name, m.path, num(m.color[0]), num(m.color[1]), num(m.color[2]), num(m.color[3]))
		}
		matsText.WriteString("    }\n")
	}
	text := "#usda 1.0\n(\n    defaultPrim = \"World\"\n    doc = \"Exported by FHR usd handler\"\n    metersPerUnit = 1\n    upAxis = \"Y\"\n)\n\ndef Xform \"World\"\n{\n" + matsText.String() + body.String() + "}\n"
	if format != ".usdz" {
		return []byte(text), nil
	}
	return usdz("scene.usda", []byte(text))
}

// usdz writes a package holding one layer: stored (not compressed) and, as the
// format requires, with the file's data starting on a 64-byte boundary.
func usdz(name string, data []byte) ([]byte, error) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	const header = 30 // a local file header, before name and extra
	pad := (64 - (header+len(name))%64) % 64
	// An extra field costs its own 4-byte header; a pad of 1..3 bytes needs 64 more.
	for pad > 0 && pad < 4 {
		pad += 64
	}
	var extra []byte
	if pad > 0 {
		extra = make([]byte, pad)
		extra[0], extra[1] = 0xff, 0xff // an unregistered tag: readers skip it
		extra[2], extra[3] = byte(pad-4), byte((pad-4)>>8)
	}
	// CreateRaw writes the header exactly as given — sizes and checksum up front,
	// no data descriptor after — which is what USD's own zip reader expects. The
	// timestamp is the DOS epoch, so the same scene always exports the same bytes.
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               name,
		Method:             zip.Store,
		Extra:              extra,
		Modified:           time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC),
		CRC32:              crc32.ChecksumIEEE(data),
		CompressedSize64:   uint64(len(data)),
		UncompressedSize64: uint64(len(data)),
	})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
