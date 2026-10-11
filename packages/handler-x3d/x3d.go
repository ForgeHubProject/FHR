// Package main is the X3D (.x3d, XML encoding) handler: the ISO web 3D format.
// It reads the scene graph — Transform/Group/Switch-like grouping, DEF/USE
// reuse — and Shapes whose geometry is IndexedFaceSet, IndexedTriangleSet,
// TriangleSet, IndexedLineSet, PointSet or Box, with Coordinate, Normal,
// TextureCoordinate and Color, and Material diffuse colours. Other geometry
// (Sphere, Cone, NURBS, …) and non-XML encodings (.x3db, .wrl) are refused
// with an error naming them rather than silently left out.
//
// X3D is Y-up and metres, like glTF, so no axis or unit change is made.
package main

import (
	"fmt"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/scene"
)

// Codec is the X3D format.
var Codec = &scene.Codec{
	ID:      "x3d",
	Formats: []string{".x3d"},
	Decode:  decode,
	Encode:  encode,
}

const (
	maxDepth = 64
	maxNodes = 2_000_000
)

func decode(blob []byte) (*scene.Model, error) {
	if !strings.Contains(string(blob[:min(len(blob), 4096)]), "<") {
		return nil, fmt.Errorf("not X3D XML (binary and classic encodings are not supported; for VRML use the vrml handler)")
	}
	root, err := scene.ParseXML(blob, maxDepth, maxNodes)
	if err != nil {
		return nil, err
	}
	if root.Name != "X3D" {
		return nil, fmt.Errorf("not an X3D document: root element is <%s>", root.Name)
	}
	sc := root.Child("Scene")
	if sc == nil {
		return &scene.Model{}, nil
	}
	return scene.DecodeX3DScene(sc)
}
