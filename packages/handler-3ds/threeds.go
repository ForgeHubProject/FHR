// Package main is the 3DS (3D Studio) handler: the chunked binary format of
// 3D Studio DOS/Max's early days, still widely exchanged. It reads mesh objects
// (vertices, triangle faces, texture coordinates), their materials' diffuse
// colours and per-face material assignment, and writes the same. 3DS is Z-up
// and unitless: a Z-up scene is rotated to glTF's Y-up on read (and back on
// write), and numbers pass through. A mesh is limited to 65535 vertices and
// 65535 faces (16-bit counts), so the writer splits larger primitives into
// several objects.
//
// Not read: the keyframer (animation and the object hierarchy it carries),
// lights, cameras, smoothing groups, textures, local-axis matrices. Objects
// are therefore flat.
//
// There is no third-party reader on hand to check this handler against (unlike
// FBX and USD, which are tested against Blender), so it is tested against the
// file layout as documented and its own round trips.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the 3DS format.
var Codec = &scene.Codec{
	ID:      "3ds",
	Formats: []string{".3ds"},
	Decode:  decode,
	Encode:  encode,
}

const (
	cMain       = 0x4D4D
	cVersion    = 0x0002
	cEditor     = 0x3D3D
	cMeshVer    = 0x3D3E
	cObject     = 0x4000
	cTriMesh    = 0x4100
	cVertices   = 0x4110
	cFaces      = 0x4120
	cFaceMat    = 0x4130
	cMapping    = 0x4140
	cMaterial   = 0xAFFF
	cMatName    = 0xA000
	cDiffuse    = 0xA020
	cColorFloat = 0x0010
	cColor24    = 0x0011
	cLinFloat   = 0x0013
	cLin24      = 0x0012

	maxChunkDepth = 16
	maxItems      = 8 << 20
)

// chunk is a view of one chunk's payload.
type chunk struct {
	id   uint16
	data []byte // the payload, without the 6-byte header
}

// chunks splits a buffer into the sibling chunks it holds.
func chunks(b []byte) ([]chunk, error) {
	var out []chunk
	for p := 0; p < len(b); {
		if p+6 > len(b) {
			return nil, fmt.Errorf("3DS chunk header runs past its parent")
		}
		id := binary.LittleEndian.Uint16(b[p:])
		n := int(binary.LittleEndian.Uint32(b[p+2:]))
		if n < 6 || p+n > len(b) || n < 0 {
			return nil, fmt.Errorf("3DS chunk %#04x has a bad length %d", id, n)
		}
		out = append(out, chunk{id, b[p+6 : p+n]})
		p += n
	}
	return out, nil
}

// cstring reads a NUL-terminated string and returns the rest.
func cstring(b []byte) (string, []byte, error) {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil, fmt.Errorf("3DS string is not terminated")
	}
	return string(b[:i]), b[i+1:], nil
}

func srgb(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func decode(blob []byte) (*scene.Model, error) {
	top, err := chunks(blob)
	if err != nil {
		return nil, err
	}
	if len(top) != 1 || top[0].id != cMain {
		return nil, fmt.Errorf("not a 3DS file: the first chunk is not the main chunk (0x4D4D)")
	}
	mainKids, err := chunks(top[0].data)
	if err != nil {
		return nil, err
	}
	var editor []byte
	for _, k := range mainKids {
		if k.id == cEditor {
			editor = k.data
		}
	}
	if editor == nil {
		return &scene.Model{}, nil
	}
	kids, err := chunks(editor)
	if err != nil {
		return nil, err
	}

	type material struct {
		name  string
		color *[4]float64
	}
	mats := map[string]*material{}
	for _, k := range kids {
		if k.id != cMaterial {
			continue
		}
		sub, err := chunks(k.data)
		if err != nil {
			return nil, err
		}
		m := &material{}
		for _, s := range sub {
			switch s.id {
			case cMatName:
				if m.name, _, err = cstring(s.data); err != nil {
					return nil, err
				}
			case cDiffuse:
				cs, err := chunks(s.data)
				if err != nil {
					return nil, err
				}
				for _, c := range cs {
					var v [3]float64
					switch {
					case (c.id == cColor24 || c.id == cLin24) && len(c.data) >= 3:
						for i := range v {
							v[i] = float64(c.data[i]) / 255
						}
						if c.id == cColor24 { // 24-bit colours are display (sRGB) values
							for i := range v {
								v[i] = srgb(v[i])
							}
						}
					case (c.id == cColorFloat || c.id == cLinFloat) && len(c.data) >= 12:
						for i := range v {
							v[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(c.data[i*4:])))
						}
					default:
						continue
					}
					m.color = &[4]float64{v[0], v[1], v[2], 1}
					break
				}
			}
		}
		if m.name != "" {
			mats[m.name] = m
		}
	}

	model := &scene.Model{}
	for _, k := range kids {
		if k.id != cObject {
			continue
		}
		name, rest, err := cstring(k.data)
		if err != nil {
			return nil, err
		}
		sub, err := chunks(rest)
		if err != nil {
			return nil, err
		}
		for _, s := range sub {
			if s.id != cTriMesh {
				continue // lights and cameras live in object chunks too
			}
			prims, err := mesh(s.data, func(n string) (string, *[4]float64) {
				if m := mats[n]; m != nil {
					return m.name, m.color
				}
				return n, nil
			})
			if err != nil {
				return nil, fmt.Errorf("object %q: %w", name, err)
			}
			if len(prims) > 0 {
				model.Roots = append(model.Roots, &scene.Object{Name: name, Prims: prims})
			}
		}
	}
	// 3DS is Z-up: (x, y, z) → (x, z, -y).
	rot := [16]float64{1, 0, 0, 0, 0, 0, -1, 0, 0, 1, 0, 0, 0, 0, 0, 1}
	for _, r := range model.Roots {
		r.Matrix = &rot
	}
	return model, nil
}

func mesh(data []byte, material func(string) (string, *[4]float64)) ([]*scene.FlatPrim, error) {
	sub, err := chunks(data)
	if err != nil {
		return nil, err
	}
	var verts [][3]float64
	var uvs [][2]float64
	var faces [][3]uint32
	type assign struct {
		name  string
		faces []int
	}
	var assigns []assign
	for _, s := range sub {
		switch s.id {
		case cVertices:
			if len(s.data) < 2 {
				return nil, fmt.Errorf("truncated vertex list")
			}
			n := int(binary.LittleEndian.Uint16(s.data))
			if len(s.data) < 2+n*12 {
				return nil, fmt.Errorf("vertex list holds %d bytes for %d vertices", len(s.data), n)
			}
			for i := 0; i < n; i++ {
				var v [3]float64
				for k := range v {
					f := float64(math.Float32frombits(binary.LittleEndian.Uint32(s.data[2+i*12+k*4:])))
					if math.IsNaN(f) || math.IsInf(f, 0) {
						return nil, fmt.Errorf("vertex %d has a non-finite coordinate", i)
					}
					v[k] = f
				}
				verts = append(verts, v)
			}
		case cMapping:
			if len(s.data) < 2 {
				return nil, fmt.Errorf("truncated mapping list")
			}
			n := int(binary.LittleEndian.Uint16(s.data))
			if len(s.data) < 2+n*8 {
				return nil, fmt.Errorf("mapping list holds %d bytes for %d coordinates", len(s.data), n)
			}
			for i := 0; i < n; i++ {
				u := float64(math.Float32frombits(binary.LittleEndian.Uint32(s.data[2+i*8:])))
				v := float64(math.Float32frombits(binary.LittleEndian.Uint32(s.data[2+i*8+4:])))
				uvs = append(uvs, [2]float64{u, 1 - v}) // 3DS v is up, glTF's down
			}
		case cFaces:
			if len(s.data) < 2 {
				return nil, fmt.Errorf("truncated face list")
			}
			n := int(binary.LittleEndian.Uint16(s.data))
			if len(s.data) < 2+n*8 {
				return nil, fmt.Errorf("face list holds %d bytes for %d faces", len(s.data), n)
			}
			for i := 0; i < n; i++ {
				o := 2 + i*8
				faces = append(faces, [3]uint32{
					uint32(binary.LittleEndian.Uint16(s.data[o:])),
					uint32(binary.LittleEndian.Uint16(s.data[o+2:])),
					uint32(binary.LittleEndian.Uint16(s.data[o+4:])),
				})
			}
			inner, err := chunks(s.data[2+n*8:])
			if err != nil {
				return nil, err
			}
			for _, c := range inner {
				if c.id != cFaceMat {
					continue
				}
				name, rest, err := cstring(c.data)
				if err != nil {
					return nil, err
				}
				if len(rest) < 2 {
					return nil, fmt.Errorf("truncated face-material list")
				}
				m := int(binary.LittleEndian.Uint16(rest))
				if len(rest) < 2+m*2 {
					return nil, fmt.Errorf("face-material list holds %d bytes for %d faces", len(rest), m)
				}
				a := assign{name: name}
				for i := 0; i < m; i++ {
					a.faces = append(a.faces, int(binary.LittleEndian.Uint16(rest[2+i*2:])))
				}
				assigns = append(assigns, a)
			}
		}
	}
	if len(faces) == 0 || len(verts) == 0 {
		return nil, nil
	}
	if len(uvs) != 0 && len(uvs) != len(verts) {
		uvs = nil // a mapping list that does not line up with the vertices is not carried
	}
	for i, f := range faces {
		for _, v := range f {
			if int(v) >= len(verts) {
				return nil, fmt.Errorf("face %d refers to vertex %d of %d", i, v, len(verts))
			}
		}
	}

	// Split faces by material (faces with none form their own group).
	faceGroup := make([]int, len(faces))
	for i := range faceGroup {
		faceGroup[i] = -1
	}
	for gi, a := range assigns {
		for _, f := range a.faces {
			if f < 0 || f >= len(faces) {
				return nil, fmt.Errorf("a material is assigned to face %d of %d", f, len(faces))
			}
			faceGroup[f] = gi
		}
	}
	var out []*scene.FlatPrim
	for gi := -1; gi < len(assigns); gi++ {
		fp := &scene.FlatPrim{Kind: scene.KindTriangles, MaterialIndex: -1}
		if gi >= 0 {
			fp.Material, fp.BaseColor = material(assigns[gi].name)
		}
		remap := map[uint32]uint32{}
		for i, f := range faces {
			if faceGroup[i] != gi {
				continue
			}
			for _, v := range f {
				id, ok := remap[v]
				if !ok {
					id = uint32(len(fp.Positions))
					remap[v] = id
					fp.Positions = append(fp.Positions, verts[v])
					if uvs != nil {
						fp.UVs = append(fp.UVs, uvs[v])
					}
				}
				fp.Indices = append(fp.Indices, id)
			}
		}
		if len(fp.Indices) > 0 {
			out = append(out, fp)
		}
	}
	return out, nil
}

// ── writer ────────────────────────────────────────────────────────────────────

type cbuf struct{ bytes.Buffer }

// chunk writes id + length + payload.
func (b *cbuf) chunk(id uint16, payload []byte) {
	var h [6]byte
	binary.LittleEndian.PutUint16(h[:], id)
	binary.LittleEndian.PutUint32(h[2:], uint32(6+len(payload)))
	b.Write(h[:])
	b.Write(payload)
}

func f32(v float64) uint32 {
	f := float32(v)
	if f == 0 {
		f = 0 // not -0
	}
	return math.Float32bits(f)
}

func linearToSRGB(c float64) float64 {
	c = math.Max(0, math.Min(1, c))
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// encode writes every triangle in the scene as 3DS objects, Z-up. Lines and
// points are dropped. Materials keep their name and diffuse colour.
func encode(roots []*scene.FlatNode, _ string) ([]byte, error) {
	type mat struct {
		name  string
		color [3]float64
	}
	var mats []*mat
	matIndex := map[string]*mat{}
	matNames := map[string]bool{}
	matOf := func(p *scene.FlatPrim) *mat {
		if p.Material == "" && p.BaseColor == nil {
			return nil
		}
		c := [3]float64{0.8, 0.8, 0.8}
		if p.BaseColor != nil {
			c = [3]float64{p.BaseColor[0], p.BaseColor[1], p.BaseColor[2]}
		}
		k := fmt.Sprintf("%s|%v", p.Material, c)
		if m, ok := matIndex[k]; ok {
			return m
		}
		base := scene.SafeName(p.Material, "material", len(mats))
		name := base
		for n := 2; matNames[name]; n++ {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		matNames[name] = true
		m := &mat{name, c}
		matIndex[k] = m
		mats = append(mats, m)
		return m
	}

	var objects cbuf
	used := map[string]bool{}
	count := 0
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
			if p.Kind != scene.KindTriangles || len(p.Indices) < 3 {
				continue
			}
			m := matOf(p)
			// Split into meshes that fit 16-bit counts.
			remap := map[uint32]uint16{}
			var verts []uint32 // original indices, in new order
			var tris [][3]uint16
			flush := func() {
				if len(tris) == 0 {
					return
				}
				name := scene.SafeName(n.Name, "node", n.Index)
				base := name
				for k := 2; used[name]; k++ {
					name = fmt.Sprintf("%s_%d", base, k)
				}
				used[name] = true
				objects.chunk(cObject, objectChunk(name, p, verts, tris, m != nil, func() string { return m.name }))
				count++
				remap = map[uint32]uint16{}
				verts, tris = nil, nil
			}
			for i := 0; i+2 < len(p.Indices); i += 3 {
				add := 0
				for k := 0; k < 3; k++ {
					if _, ok := remap[p.Indices[i+k]]; !ok {
						add++
					}
				}
				if len(verts)+add > 65535 || len(tris)+1 > 65535 {
					flush()
				}
				var t [3]uint16
				for k := 0; k < 3; k++ {
					o := p.Indices[i+k]
					id, ok := remap[o]
					if !ok {
						id = uint16(len(verts))
						remap[o] = id
						verts = append(verts, o)
					}
					t[k] = id
				}
				tris = append(tris, t)
			}
			flush()
		}
	})
	if count == 0 {
		return nil, fmt.Errorf("the scene has no triangles to write")
	}

	var editor cbuf
	var ver [4]byte
	binary.LittleEndian.PutUint32(ver[:], 3)
	editor.chunk(cMeshVer, ver[:])
	for _, m := range mats {
		var mb cbuf
		mb.chunk(cMatName, append([]byte(m.name), 0))
		var col cbuf
		col.chunk(cColor24, []byte{
			byte(math.Round(linearToSRGB(m.color[0]) * 255)),
			byte(math.Round(linearToSRGB(m.color[1]) * 255)),
			byte(math.Round(linearToSRGB(m.color[2]) * 255)),
		})
		mb.chunk(cDiffuse, col.Bytes())
		editor.chunk(cMaterial, mb.Bytes())
	}
	editor.Write(objects.Bytes())

	var main cbuf
	binary.LittleEndian.PutUint32(ver[:], 3)
	main.chunk(cVersion, ver[:])
	main.chunk(cEditor, editor.Bytes())
	var out cbuf
	out.chunk(cMain, main.Bytes())
	return out.Bytes(), nil
}

// objectChunk is one named object holding one triangle mesh. Positions are
// rotated from glTF's Y-up to 3DS's Z-up: (x, y, z) → (x, -z, y).
func objectChunk(name string, p *scene.FlatPrim, verts []uint32, tris [][3]uint16, hasMat bool, matName func() string) []byte {
	var mb cbuf
	vdata := make([]byte, 2+12*len(verts))
	binary.LittleEndian.PutUint16(vdata, uint16(len(verts)))
	for i, o := range verts {
		v := p.Positions[o]
		binary.LittleEndian.PutUint32(vdata[2+i*12:], f32(v[0]))
		binary.LittleEndian.PutUint32(vdata[2+i*12+4:], f32(-v[2]))
		binary.LittleEndian.PutUint32(vdata[2+i*12+8:], f32(v[1]))
	}
	var tri cbuf
	tri.chunk(cVertices, vdata)
	if p.UVs != nil {
		udata := make([]byte, 2+8*len(verts))
		binary.LittleEndian.PutUint16(udata, uint16(len(verts)))
		for i, o := range verts {
			binary.LittleEndian.PutUint32(udata[2+i*8:], f32(p.UVs[o][0]))
			binary.LittleEndian.PutUint32(udata[2+i*8+4:], f32(1-p.UVs[o][1])) // glTF v is down, 3DS's up
		}
		tri.chunk(cMapping, udata)
	}
	fdata := make([]byte, 2+8*len(tris))
	binary.LittleEndian.PutUint16(fdata, uint16(len(tris)))
	for i, t := range tris {
		binary.LittleEndian.PutUint16(fdata[2+i*8:], t[0])
		binary.LittleEndian.PutUint16(fdata[2+i*8+2:], t[1])
		binary.LittleEndian.PutUint16(fdata[2+i*8+4:], t[2])
		binary.LittleEndian.PutUint16(fdata[2+i*8+6:], 0x0007) // edge-visibility flags
	}
	if hasMat {
		payload := append([]byte(matName()), 0)
		ids := make([]byte, 2+2*len(tris))
		binary.LittleEndian.PutUint16(ids, uint16(len(tris)))
		for i := range tris {
			binary.LittleEndian.PutUint16(ids[2+i*2:], uint16(i))
		}
		mb.chunk(cFaceMat, append(payload, ids...))
		fdata = append(fdata, mb.Bytes()...)
	}
	tri.chunk(cFaces, fdata)
	var tm cbuf
	tm.chunk(cTriMesh, tri.Bytes())
	return append([]byte(strings.TrimSpace(name)+"\x00"), tm.Bytes()...)
}
