// Package main is the AMF (Additive Manufacturing File Format, ISO/ASTM 52915)
// handler: the XML 3D-printing format 3MF superseded. It reads objects (meshes of
// vertices and triangle volumes, with per-vertex normals), materials' colours and
// names, and constellations (placed, rotated instances); plain and zip-compressed
// files. A file's <unit> is applied — its numbers become glTF's metres — and the
// writer declares metres, so a transcode keeps physical size.
//
// Not read: textures, per-vertex/per-triangle colours, curved triangle patches
// (<edge>), lattices, metadata beyond names, and function-defined colours.
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the AMF format.
var Codec = &scene.Codec{
	ID:      "amf",
	Formats: []string{".amf"},
	Decode:  decode,
	Encode:  encode,
}

const (
	maxDepth   = 32
	maxNodes   = 4_000_000
	maxPackage = 256 << 20
)

// unitMetres is metres per AMF unit; the default, with no unit attribute, is millimetres.
var unitMetres = map[string]float64{
	"millimeter": 0.001, "inch": 0.0254, "feet": 0.3048, "meter": 1, "micrometer": 1e-6,
}

func decode(blob []byte) (*scene.Model, error) {
	if bytes.HasPrefix(blob, []byte("PK")) { // a zip holding one .amf
		zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
		if err != nil {
			return nil, fmt.Errorf("not an AMF file: %w", err)
		}
		for _, f := range zr.File {
			if !strings.HasSuffix(strings.ToLower(f.Name), ".amf") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			data, err := io.ReadAll(io.LimitReader(rc, maxPackage+1))
			if err != nil {
				return nil, err
			}
			if len(data) > maxPackage {
				return nil, fmt.Errorf("%s is larger than %d MiB once unpacked", f.Name, maxPackage>>20)
			}
			return decode(data)
		}
		return nil, fmt.Errorf("the zip holds no .amf file")
	}
	root, err := scene.ParseXML(blob, maxDepth, maxNodes)
	if err != nil {
		return nil, err
	}
	if root.Name != "amf" {
		return nil, fmt.Errorf("not an AMF document: root element is <%s>", root.Name)
	}
	unit := 0.001
	if u, ok := root.Attr["unit"]; ok {
		k, known := unitMetres[strings.ToLower(u)]
		if !known {
			return nil, fmt.Errorf("unknown unit %q", u)
		}
		unit = k
	}

	type material struct {
		name  string
		color *[4]float64
	}
	mats := map[string]*material{}
	for _, m := range root.Children("material") {
		mat := &material{}
		for _, md := range m.Children("metadata") {
			if md.Attr["type"] == "name" {
				mat.name = md.Text
			}
		}
		if c := m.Child("color"); c != nil {
			col := [4]float64{1, 1, 1, 1}
			for i, ch := range []string{"r", "g", "b", "a"} {
				if n := c.Child(ch); n != nil {
					v, err := strconv.ParseFloat(strings.TrimSpace(n.Text), 64)
					if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
						return nil, fmt.Errorf("material %q has a bad colour component", m.Attr["id"])
					}
					col[i] = v
				}
			}
			mat.color = &col
		}
		if mat.name == "" {
			mat.name = "material" + m.Attr["id"]
		}
		mats[m.Attr["id"]] = mat
	}

	objects := map[string]*scene.Object{}
	var order []string
	for _, o := range root.Children("object") {
		id := o.Attr["id"]
		obj := &scene.Object{Name: "object" + id}
		for _, md := range o.Children("metadata") {
			if md.Attr["type"] == "name" && md.Text != "" {
				obj.Name = md.Text
			}
		}
		mesh := o.Child("mesh")
		if mesh == nil {
			continue
		}
		var pos [][3]float64
		if vs := mesh.Child("vertices"); vs != nil {
			for i, v := range vs.Children("vertex") {
				c := v.Child("coordinates")
				if c == nil {
					return nil, fmt.Errorf("object %s: vertex %d has no coordinates", id, i)
				}
				var p [3]float64
				for k, ax := range []string{"x", "y", "z"} {
					n := c.Child(ax)
					if n == nil {
						return nil, fmt.Errorf("object %s: vertex %d has no %s", id, i, ax)
					}
					f, err := strconv.ParseFloat(strings.TrimSpace(n.Text), 64)
					if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
						return nil, fmt.Errorf("object %s: vertex %d has a bad %s", id, i, ax)
					}
					p[k] = f
				}
				pos = append(pos, p)
			}
		}
		for vi, vol := range mesh.Children("volume") {
			fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
			if m := mats[vol.Attr["materialid"]]; m != nil {
				fp.Material, fp.BaseColor = m.name, m.color
			}
			remap := map[int]uint32{}
			for ti, t := range vol.Children("triangle") {
				for _, vn := range []string{"v1", "v2", "v3"} {
					n := t.Child(vn)
					if n == nil {
						return nil, fmt.Errorf("object %s: triangle %d of volume %d has no %s", id, ti, vi, vn)
					}
					idx, err := strconv.Atoi(strings.TrimSpace(n.Text))
					if err != nil || idx < 0 || idx >= len(pos) {
						return nil, fmt.Errorf("object %s: triangle %d refers to vertex %q of %d", id, ti, n.Text, len(pos))
					}
					g, ok := remap[idx]
					if !ok {
						g = uint32(len(fp.Positions))
						remap[idx] = g
						fp.Positions = append(fp.Positions, pos[idx])
					}
					fp.Indices = append(fp.Indices, g)
				}
			}
			if len(fp.Indices) > 0 {
				obj.Prims = append(obj.Prims, fp)
			}
		}
		objects[id] = obj
		order = append(order, id)
	}

	m := &scene.Model{}
	if cs := root.Children("constellation"); len(cs) > 0 { // placed instances of the objects
		for _, in := range cs[0].Children("instance") {
			src := objects[in.Attr["objectid"]]
			if src == nil {
				return nil, fmt.Errorf("an instance refers to the missing object %q", in.Attr["objectid"])
			}
			var d, r [3]float64
			for k, ax := range []string{"x", "y", "z"} {
				d[k] = childFloat(in, "delta"+ax)
				r[k] = childFloat(in, "r"+ax)
			}
			cp := *src
			cp.Matrix = instanceMatrix(d, r)
			m.Roots = append(m.Roots, &cp)
		}
	} else {
		for _, id := range order {
			m.Roots = append(m.Roots, objects[id])
		}
	}
	if unit != 1 {
		sm := [16]float64{unit, 0, 0, 0, 0, unit, 0, 0, 0, 0, unit, 0, 0, 0, 0, 1}
		for _, r := range m.Roots {
			r.Matrix = mulMat(&sm, r.Matrix)
		}
	}
	return m, nil
}

func childFloat(n *scene.XMLNode, name string) float64 {
	c := n.Child(name)
	if c == nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(c.Text), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// instanceMatrix is AMF's placement: rotate about x, then y, then z (degrees),
// then translate by the deltas.
func instanceMatrix(d, r [3]float64) *[16]float64 {
	rot := func(axis int, deg float64) [16]float64 {
		a := deg * math.Pi / 180
		c, s := math.Cos(a), math.Sin(a)
		switch axis {
		case 0:
			return [16]float64{1, 0, 0, 0, 0, c, s, 0, 0, -s, c, 0, 0, 0, 0, 1}
		case 1:
			return [16]float64{c, 0, -s, 0, 0, 1, 0, 0, s, 0, c, 0, 0, 0, 0, 1}
		}
		return [16]float64{c, s, 0, 0, -s, c, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	}
	m := [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	for ax := 0; ax < 3; ax++ {
		a := rot(ax, r[ax])
		m = *mulMat(&a, &m)
	}
	m[12], m[13], m[14] = d[0], d[1], d[2]
	return &m
}

func mulMat(a, b *[16]float64) *[16]float64 {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	var o [16]float64
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			var s float64
			for k := 0; k < 4; k++ {
				s += a[k*4+r] * b[c*4+k]
			}
			o[c*4+r] = s
		}
	}
	return &o
}
