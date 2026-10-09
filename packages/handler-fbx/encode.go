package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

const (
	writeVersion = 7400
	// The fixed trailer of every FBX binary file (as Blender's exporter writes it).
	footerID    = "\xfa\xbc\xab\x09\xd0\xc8\xd4\x66\xb1\x76\xfb\x83\x1c\xf7\x26\x7e"
	footerMagic = "\xf8\x5a\x8c\x6a\xde\xf5\xd9\x7e\xec\xe9\x0c\xe3\x75\x8f\x29\x0b"
)

// wnode is a node being written. Properties are pre-encoded (type byte + data).
type wnode struct {
	name  string
	props [][]byte
	kids  []*wnode
}

func newNode(name string, props ...[]byte) *wnode { return &wnode{name: name, props: props} }

func (n *wnode) add(k ...*wnode) *wnode { n.kids = append(n.kids, k...); return n }

func pInt32(v int32) []byte {
	b := []byte{'I', 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[1:], uint32(v))
	return b
}

func pInt64(v int64) []byte {
	b := make([]byte, 9)
	b[0] = 'L'
	binary.LittleEndian.PutUint64(b[1:], uint64(v))
	return b
}

func pFloat64(v float64) []byte {
	b := make([]byte, 9)
	b[0] = 'D'
	binary.LittleEndian.PutUint64(b[1:], math.Float64bits(v))
	return b
}

func pBool(v bool) []byte {
	if v {
		return []byte{'C', 1}
	}
	return []byte{'C', 0}
}

func pString(s string) []byte {
	b := make([]byte, 5+len(s))
	b[0] = 'S'
	binary.LittleEndian.PutUint32(b[1:], uint32(len(s)))
	copy(b[5:], s)
	return b
}

// pRaw is the file-id style raw-bytes property.
func pRaw(r []byte) []byte {
	b := make([]byte, 5+len(r))
	b[0] = 'R'
	binary.LittleEndian.PutUint32(b[1:], uint32(len(r)))
	copy(b[5:], r)
	return b
}

// Arrays are written uncompressed (encoding 0): bigger than zlib, but the same
// scene always exports to the same bytes whatever the Go version.
func pFloat64Array(v []float64) []byte {
	b := make([]byte, 13+8*len(v))
	b[0] = 'd'
	binary.LittleEndian.PutUint32(b[1:], uint32(len(v)))
	binary.LittleEndian.PutUint32(b[5:], 0)
	binary.LittleEndian.PutUint32(b[9:], uint32(8*len(v)))
	for i, f := range v {
		// glTF stores float32: writing the float64 a transform produced would
		// make every import → export differ in the low bits.
		if f = float64(float32(f)); f == 0 {
			f = 0 // not -0
		}
		binary.LittleEndian.PutUint64(b[13+8*i:], math.Float64bits(f))
	}
	return b
}

func pInt32Array(v []int32) []byte {
	b := make([]byte, 13+4*len(v))
	b[0] = 'i'
	binary.LittleEndian.PutUint32(b[1:], uint32(len(v)))
	binary.LittleEndian.PutUint32(b[5:], 0)
	binary.LittleEndian.PutUint32(b[9:], uint32(4*len(v)))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[13+4*i:], uint32(x))
	}
	return b
}

// write serializes the node at the buffer's current offset. A node with
// children is closed by a null record; a node with neither properties nor
// children is followed by one unless it is the last of its siblings (the
// conventions Blender's exporter and every reader agree on).
func (n *wnode) write(b *bytes.Buffer, last bool) {
	headerAt := b.Len()
	b.Write(make([]byte, 12)) // end offset, property count, property length
	b.WriteByte(byte(len(n.name)))
	b.WriteString(n.name)
	propsAt := b.Len()
	for _, p := range n.props {
		b.Write(p)
	}
	propsLen := b.Len() - propsAt
	if len(n.kids) > 0 {
		for i, k := range n.kids {
			k.write(b, i == len(n.kids)-1)
		}
		b.Write(make([]byte, 13))
	} else if len(n.props) == 0 && !last {
		b.Write(make([]byte, 13))
	}
	h := b.Bytes()[headerAt:]
	binary.LittleEndian.PutUint32(h[0:], uint32(b.Len()))
	binary.LittleEndian.PutUint32(h[4:], uint32(len(n.props)))
	binary.LittleEndian.PutUint32(h[8:], uint32(propsLen))
}

func p70(name, typ, label, flags string, vals ...any) *wnode {
	props := [][]byte{pString(name), pString(typ), pString(label), pString(flags)}
	for _, v := range vals {
		switch x := v.(type) {
		case float64:
			props = append(props, pFloat64(x))
		case int:
			props = append(props, pInt32(int32(x)))
		case string:
			props = append(props, pString(x))
		}
	}
	return &wnode{name: "P", props: props}
}

// encode writes the scene as a binary FBX 7.4 file: Y-up, one Model + Geometry
// per node that draws triangles, in world space (so no transforms to carry),
// one Material per distinct (name, colour) connected to the models that use
// it, and per-polygon material slots. Normals and texture coordinates are
// written when every primitive of the node has them. Lines and points are
// dropped. Coordinates are glTF's metres, declared as such (UnitScaleFactor 100).
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	type mat struct {
		name  string
		color [4]float64
		id    int64
	}
	var mats []*mat
	matIndex := map[string]*mat{}
	next := int64(1_000_000)
	newID := func() int64 { next++; return next }
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
		m := &mat{name: name, color: c, id: newID()}
		matIndex[k] = m
		mats = append(mats, m)
		return m
	}

	objects := newNode("Objects")
	connections := newNode("Connections")
	conn := func(child, parent int64) {
		connections.add(newNode("C", pString("OO"), pInt64(child), pInt64(parent)))
	}
	nModels := 0
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
		wantN, wantUV := true, true
		for _, p := range tris {
			wantN = wantN && p.Normals != nil
			wantUV = wantUV && p.UVs != nil
		}
		var pos, nrm, uv []float64
		var polys, nIdx, uvIdx, slots []int32
		// Material slots, in the order a reader will see the connections. A
		// primitive that names no material takes slot 0: FBX has no "no material".
		var slotMats []*mat
		slotOf := map[*mat]int32{}
		for _, p := range tris {
			if m := matOf(p); m != nil {
				if _, ok := slotOf[m]; !ok {
					slotOf[m] = int32(len(slotMats))
					slotMats = append(slotMats, m)
				}
			}
		}
		nv := 0
		for _, p := range tris {
			base := int32(nv)
			for i, v := range p.Positions {
				pos = append(pos, v[0], v[1], v[2])
				if wantN {
					nrm = append(nrm, p.Normals[i][0], p.Normals[i][1], p.Normals[i][2])
				}
				if wantUV {
					uv = append(uv, p.UVs[i][0], 1-p.UVs[i][1]) // glTF v is down, FBX's up
				}
				nv++
			}
			s := int32(0)
			if m := matOf(p); m != nil {
				s = slotOf[m]
			}
			for i := 0; i+2 < len(p.Indices); i += 3 {
				a, b, c := base+int32(p.Indices[i]), base+int32(p.Indices[i+1]), base+int32(p.Indices[i+2])
				polys = append(polys, a, b, ^c) // the last index of a polygon is stored as ~i
				nIdx = append(nIdx, a, b, c)
				uvIdx = append(uvIdx, a, b, c)
				slots = append(slots, s)
			}
		}

		geoID, modelID := newID(), newID()
		name := scene.SafeName(n.Name, "node", n.Index)
		geo := newNode("Geometry", pInt64(geoID), pString(name+"\x00\x01Geometry"), pString("Mesh"))
		geo.add(newNode("Properties70"),
			newNode("GeometryVersion", pInt32(124)),
			newNode("Vertices", pFloat64Array(pos)),
			newNode("PolygonVertexIndex", pInt32Array(polys)))
		var layerEls []*wnode
		if wantN {
			geo.add(newNode("LayerElementNormal", pInt32(0)).add(
				newNode("Version", pInt32(101)), newNode("Name", pString("")),
				newNode("MappingInformationType", pString("ByPolygonVertex")),
				newNode("ReferenceInformationType", pString("IndexToDirect")),
				newNode("Normals", pFloat64Array(nrm)), newNode("NormalsIndex", pInt32Array(nIdx))))
			layerEls = append(layerEls, newNode("LayerElement").add(newNode("Type", pString("LayerElementNormal")), newNode("TypedIndex", pInt32(0))))
		}
		if wantUV {
			geo.add(newNode("LayerElementUV", pInt32(0)).add(
				newNode("Version", pInt32(101)), newNode("Name", pString("UVMap")),
				newNode("MappingInformationType", pString("ByPolygonVertex")),
				newNode("ReferenceInformationType", pString("IndexToDirect")),
				newNode("UV", pFloat64Array(uv)), newNode("UVIndex", pInt32Array(uvIdx))))
			layerEls = append(layerEls, newNode("LayerElement").add(newNode("Type", pString("LayerElementUV")), newNode("TypedIndex", pInt32(0))))
		}
		if len(slotMats) > 0 {
			geo.add(newNode("LayerElementMaterial", pInt32(0)).add(
				newNode("Version", pInt32(101)), newNode("Name", pString("")),
				newNode("MappingInformationType", pString("ByPolygon")),
				newNode("ReferenceInformationType", pString("IndexToDirect")),
				newNode("Materials", pInt32Array(slots))))
			layerEls = append(layerEls, newNode("LayerElement").add(newNode("Type", pString("LayerElementMaterial")), newNode("TypedIndex", pInt32(0))))
		}
		geo.add(newNode("Layer", pInt32(0)).add(append([]*wnode{newNode("Version", pInt32(100))}, layerEls...)...))
		objects.add(geo)

		model := newNode("Model", pInt64(modelID), pString(name+"\x00\x01Model"), pString("Mesh"))
		model.add(newNode("Version", pInt32(232)),
			newNode("Properties70").add(p70("InheritType", "enum", "", "", 1)),
			newNode("MultiLayer", pInt32(0)), newNode("MultiTake", pInt32(0)),
			newNode("Shading", pBool(true)), newNode("Culling", pString("CullingOff")))
		objects.add(model)
		conn(modelID, 0)
		conn(geoID, modelID)
		for _, m := range slotMats {
			conn(m.id, modelID)
		}
		nModels++
	})
	if nModels == 0 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}
	// A material is connected once per model that uses it, in slot order, so the
	// connection order a reader sees is the slot order the indices refer to.
	for _, m := range mats {
		objects.add(newNode("Material", pInt64(m.id), pString(m.name+"\x00\x01Material"), pString("")).add(
			newNode("Version", pInt32(102)), newNode("ShadingModel", pString("phong")), newNode("MultiLayer", pInt32(0)),
			newNode("Properties70").add(
				p70("DiffuseColor", "Color", "", "A", m.color[0], m.color[1], m.color[2]),
				p70("Opacity", "double", "Number", "", m.color[3]))))
	}

	definitions := newNode("Definitions").add(newNode("Version", pInt32(100)), newNode("Count", pInt32(int32(1+nModels*2+len(mats)))),
		newNode("ObjectType", pString("Model")).add(newNode("Count", pInt32(int32(nModels)))),
		newNode("ObjectType", pString("Geometry")).add(newNode("Count", pInt32(int32(nModels)))))
	if len(mats) > 0 {
		definitions.add(newNode("ObjectType", pString("Material")).add(newNode("Count", pInt32(int32(len(mats))))))
	}
	top := []*wnode{
		newNode("FBXHeaderExtension").add(
			newNode("FBXHeaderVersion", pInt32(1003)), newNode("FBXVersion", pInt32(writeVersion)),
			newNode("Creator", pString("FHR fbx handler"))),
		newNode("GlobalSettings").add(newNode("Version", pInt32(1000)), newNode("Properties70").add(
			p70("UpAxis", "int", "Integer", "", 1), p70("UpAxisSign", "int", "Integer", "", 1),
			p70("FrontAxis", "int", "Integer", "", 2), p70("FrontAxisSign", "int", "Integer", "", 1),
			p70("CoordAxis", "int", "Integer", "", 0), p70("CoordAxisSign", "int", "Integer", "", 1),
			p70("UnitScaleFactor", "double", "Number", "", 100.0))),
		newNode("Documents").add(newNode("Count", pInt32(1)), newNode("Document", pInt64(1), pString("Scene"), pString("Scene")).add(
			newNode("RootNode", pInt64(0)))),
		newNode("References"),
		definitions,
		objects,
		connections,
		newNode("Takes").add(newNode("Current", pString(""))),
	}

	var b bytes.Buffer
	b.WriteString(binaryMagic)
	_ = binary.Write(&b, binary.LittleEndian, uint32(writeVersion))
	for i, n := range top {
		n.write(&b, i == len(top)-1)
	}
	b.Write(make([]byte, 13)) // the null record that closes the top level
	b.WriteString(footerID)
	b.Write(make([]byte, 4))
	pad := ((b.Len() + 15) &^ 15) - b.Len()
	if pad == 0 {
		pad = 16
	}
	b.Write(make([]byte, pad))
	_ = binary.Write(&b, binary.LittleEndian, uint32(writeVersion))
	b.Write(make([]byte, 120))
	b.WriteString(footerMagic)
	return b.Bytes(), nil
}
