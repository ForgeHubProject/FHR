package scene

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// A Codec is a 3D format reduced to its two real jobs: reading its bytes into
// a Model, and writing a flattened scene back out. Handler turns that pair into
// a complete FHR handler — semantic diff, import, export and preview all
// derive from the same glTF the model builds — so adding a format to the 3D
// family costs a parser and a writer, never a diff or a converter.
type Codec struct {
	ID      string
	Formats []string // extensions, lowercase with the dot
	// Decode reads one file. It is never called with an empty blob.
	Decode func(Blob) (*Model, error)
	// Encode writes a flattened scene (Flatten) as one of Formats.
	Encode func(roots []*FlatNode, format string) (Blob, error)
	// Merge is not offered: formats without stable element identity cannot be
	// merged semantically, and forge falls back to blob-pick for them.
}

// Model is a decoded file: a forest of objects.
type Model struct {
	Roots []*Object
}

// Object is one named thing in a model. Prims are in the object's own
// space; Matrix (column-major, glTF's layout), when set, places it in its
// parent's.
type Object struct {
	Name     string
	Matrix   *[16]float64
	Prims    []*FlatPrim
	Children []*Object
}

// Info is the handler's declaration for fhr.Run.
func (c *Codec) Info() fhr.Info {
	return fhr.Info{ID: c.ID, Formats: c.Formats, Capabilities: &fhr.Capabilities{SemanticCompare: true}}
}

// Handler returns the FHR handler for the codec.
func (c *Codec) Handler() *CodecHandler { return &CodecHandler{c: c} }

// CodecHandler is the handler a Codec derives. It implements fhr.Handler,
// Previewer, Importer and Exporter.
type CodecHandler struct{ c *Codec }

func (h *CodecHandler) Match(path string) bool {
	return slices.Contains(h.c.Formats, strings.ToLower(filepath.Ext(path)))
}

func (h *CodecHandler) document(blob Blob) (*gltf.Document, error) {
	if len(blob) == 0 {
		return (&Model{}).Document()
	}
	m, err := h.c.Decode(blob)
	if err != nil {
		return nil, err
	}
	return m.Document()
}

// Diff compares the two files as the glTF scenes they describe. An empty blob
// is the added/deleted-file case.
func (h *CodecHandler) Diff(base, head Blob) (StructuredDiff, error) {
	a, err := h.document(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := h.document(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}
	return StructuredDiff{Version: "1.0", Format: h.c.ID, Changes: DiffDocuments(a, b)}, nil
}

// Merge is unsupported; see Codec.
func (h *CodecHandler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not supported for %s", h.c.ID)
}

// Import converts the file to a GLB, faithfully.
func (h *CodecHandler) Import(blob Blob) (Blob, error) {
	doc, err := h.document(blob)
	if err != nil {
		return nil, err
	}
	return encodeBlob(doc, true)
}

// PreviewMediaType is the GLB the preview produces.
func (h *CodecHandler) PreviewMediaType() string { return fhr.MediaTypeGLB }

// Preview is Import dressed with a viewable surface (DressForPreview).
func (h *CodecHandler) Preview(blob Blob) (Blob, error) {
	doc, err := h.document(blob)
	if err != nil {
		return nil, err
	}
	DressForPreview(doc)
	return encodeBlob(doc, true)
}

// Export writes a GLB as one of the codec's formats.
func (h *CodecHandler) Export(glb Blob, format string) (Blob, error) {
	format = strings.ToLower(format)
	if !slices.Contains(h.c.Formats, format) {
		return nil, fmt.Errorf("%s exports %v, not %q", h.c.ID, h.c.Formats, format)
	}
	roots, err := Flatten(glb)
	if err != nil {
		return nil, fmt.Errorf("reading glTF: %w", err)
	}
	return h.c.Encode(roots, format)
}

// Document builds the glTF the model describes: one node per object (named as
// the object is, in the object's order), a mesh per object with primitives,
// and a material per distinct (name, colour). Deterministic — the same model
// always yields the same document — which the diff's content signatures rely on.
func (m *Model) Document() (*gltf.Document, error) {
	doc := &gltf.Document{
		Asset:  gltf.Asset{Version: "2.0", Generator: "FHR"},
		Scene:  gltf.Index(0),
		Scenes: []*gltf.Scene{{}},
	}
	b := docBuilder{doc: doc, materials: map[string]int{}}
	for _, r := range m.Roots {
		i, err := b.node(r)
		if err != nil {
			return nil, err
		}
		doc.Scenes[0].Nodes = append(doc.Scenes[0].Nodes, i)
	}
	return doc, nil
}

type docBuilder struct {
	doc       *gltf.Document
	materials map[string]int
}

func (b *docBuilder) node(o *Object) (int, error) {
	idx := len(b.doc.Nodes)
	name := o.Name
	if name == "" {
		name = fmt.Sprintf("object%d", idx)
	}
	n := &gltf.Node{Name: name}
	if o.Matrix != nil && *o.Matrix != [16]float64(identity4) {
		n.Matrix = *o.Matrix
	}
	b.doc.Nodes = append(b.doc.Nodes, n)
	if len(o.Prims) > 0 {
		mesh := &gltf.Mesh{Name: name}
		for _, p := range o.Prims {
			gp, err := b.primitive(p)
			if err != nil {
				return 0, fmt.Errorf("object %q: %w", name, err)
			}
			if gp != nil {
				mesh.Primitives = append(mesh.Primitives, gp)
			}
		}
		if len(mesh.Primitives) > 0 {
			b.doc.Meshes = append(b.doc.Meshes, mesh)
			n.Mesh = gltf.Index(len(b.doc.Meshes) - 1)
		}
	}
	for _, c := range o.Children {
		ci, err := b.node(c)
		if err != nil {
			return 0, err
		}
		n.Children = append(n.Children, ci)
	}
	return idx, nil
}

func (b *docBuilder) primitive(p *FlatPrim) (*gltf.Primitive, error) {
	n := len(p.Positions)
	if n == 0 || len(p.Indices) == 0 {
		return nil, nil
	}
	if (p.Normals != nil && len(p.Normals) != n) || (p.UVs != nil && len(p.UVs) != n) || (p.Colors != nil && len(p.Colors) != n) {
		return nil, fmt.Errorf("attribute counts disagree with positions")
	}
	for _, i := range p.Indices {
		if int(i) >= n {
			return nil, fmt.Errorf("index %d out of range (%d vertices)", i, n)
		}
	}
	mode := gltf.PrimitiveTriangles
	switch p.Kind {
	case KindLines:
		mode = gltf.PrimitiveLines
	case KindPoints:
		mode = gltf.PrimitivePoints
	}
	pos := make([][3]float32, n)
	for i, v := range p.Positions {
		pos[i] = [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}
	}
	gp := &gltf.Primitive{
		Mode:       mode,
		Attributes: gltf.PrimitiveAttributes{gltf.POSITION: modeler.WritePosition(b.doc, pos)},
		Indices:    gltf.Index(modeler.WriteIndices(b.doc, p.Indices)),
	}
	if p.Normals != nil {
		ns := make([][3]float32, n)
		for i, v := range p.Normals {
			u := unit3(v)
			ns[i] = [3]float32{float32(u[0]), float32(u[1]), float32(u[2])}
		}
		gp.Attributes[gltf.NORMAL] = modeler.WriteNormal(b.doc, ns)
	}
	if p.UVs != nil {
		uv := make([][2]float32, n)
		for i, v := range p.UVs {
			uv[i] = [2]float32{float32(v[0]), float32(v[1])}
		}
		gp.Attributes[gltf.TEXCOORD_0] = modeler.WriteTextureCoord(b.doc, uv)
	}
	if p.Colors != nil {
		cs := make([][4]float32, n)
		for i, v := range p.Colors {
			cs[i] = [4]float32{float32(v[0]), float32(v[1]), float32(v[2]), float32(v[3])}
		}
		gp.Attributes[gltf.COLOR_0] = modeler.WriteColor(b.doc, cs)
	}
	if p.Material != "" || p.BaseColor != nil {
		gp.Material = gltf.Index(b.material(p))
	}
	return gp, nil
}

// material returns the index of the material for p, adding it on first use.
// Materials are keyed by name and colour, so two surfaces that differ only in
// colour are two materials.
func (b *docBuilder) material(p *FlatPrim) int {
	key := fmt.Sprintf("%s|%v", p.Material, p.BaseColor)
	if i, ok := b.materials[key]; ok {
		return i
	}
	m := &gltf.Material{Name: p.Material}
	if p.BaseColor != nil {
		m.PBRMetallicRoughness = &gltf.PBRMetallicRoughness{BaseColorFactor: p.BaseColor}
		if p.BaseColor[3] < 1 {
			m.AlphaMode = gltf.AlphaBlend
		}
	}
	b.doc.Materials = append(b.doc.Materials, m)
	b.materials[key] = len(b.doc.Materials) - 1
	return b.materials[key]
}

// DressForPreview gives a document the surface a viewer should draw: a
// neutral, non-metallic, double-sided material everywhere (glTF's defaults are
// fully metallic, which renders near-black without an environment map, and mesh
// winding is too often inconsistent to cull back faces). Materials that state
// a base colour keep it; every other is restyled, and material-less primitives
// share one unnamed material, so every node and mesh name — what the diff's
// paths address — is unchanged.
func DressForPreview(doc *gltf.Document) {
	neutral := func(m *gltf.Material) {
		m.DoubleSided = true
		color := &[4]float64{0.8, 0.8, 0.8, 1}
		if m.PBRMetallicRoughness != nil && m.PBRMetallicRoughness.BaseColorFactor != nil {
			color = m.PBRMetallicRoughness.BaseColorFactor
		}
		m.PBRMetallicRoughness = &gltf.PBRMetallicRoughness{
			BaseColorFactor: color,
			MetallicFactor:  gltf.Float(0),
			RoughnessFactor: gltf.Float(0.9),
		}
	}
	for _, m := range doc.Materials {
		neutral(m)
	}
	fallback := -1
	for _, mesh := range doc.Meshes {
		for _, p := range mesh.Primitives {
			if p.Material != nil {
				continue
			}
			if fallback < 0 {
				m := &gltf.Material{}
				neutral(m)
				doc.Materials = append(doc.Materials, m)
				fallback = len(doc.Materials) - 1
			}
			p.Material = gltf.Index(fallback)
		}
	}
}
