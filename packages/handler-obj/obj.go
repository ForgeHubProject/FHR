// Command forge-handler-obj is the Wavefront OBJ handler.
//
// It does not diff OBJ text. It converts each side to a glTF document
// (convert.go) and diffs those with the 3D family's scene engine
// (packages/go/scene) — the same identity matching, geometry signatures and
// change paths as gltf-scene — then adds what glTF has no place for: the
// mtllib references and any statements it does not interpret. The same
// document, given a viewable surface and encoded as GLB, is the preview a
// viewer draws, so a change path always names a node that exists in it.
package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/forgehubproject/fhr/packages/go/scene"
	"github.com/qmuntal/gltf"
)

// Handler is the Wavefront OBJ format handler.
type Handler struct{}

// Match returns true for .obj files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".obj")
}

// Diff produces the semantic diff of two OBJ blobs. An empty blob on either
// side is the added/deleted-file case and diffs as all-added/all-removed.
func (h *Handler) Diff(base, head fhr.Blob) (fhr.StructuredDiff, error) {
	a, err := parseOBJ(base)
	if err != nil {
		return fhr.StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := parseOBJ(head)
	if err != nil {
		return fhr.StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	changes := scene.DiffDocuments(toGLTF(a), toGLTF(b))
	relabel(changes)
	changes = append(changes, diffMtlLibs(a.mtlLibs, b.mtlLibs)...)
	changes = append(changes, diffUninterpreted(a, b)...)
	return fhr.StructuredDiff{Version: "1.0", Format: "obj", Changes: changes}, nil
}

// Merge is not supported: a merged glTF cannot be written back as the OBJ it
// came from, and a text merge of OBJ is not semantic.
func (h *Handler) Merge(_, _, _ fhr.Blob) (fhr.Blob, *fhr.ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for obj")
}

// PreviewMediaType is the GLB the preview produces.
func (h *Handler) PreviewMediaType() string { return fhr.MediaTypeGLB }

// Preview converts an OBJ blob to the GLB the diff was computed over, dressed
// with a viewable surface (dressForPreview).
func (h *Handler) Preview(blob fhr.Blob) (fhr.Blob, error) {
	f, err := parseOBJ(blob)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := gltf.NewEncoder(&buf)
	enc.AsBinary = true
	doc := toGLTF(f)
	dressForPreview(doc)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encoding preview: %w", err)
	}
	return buf.Bytes(), nil
}

// topLabels renames the engine's top-level groups into OBJ vocabulary. Only
// labels change; paths stay the engine's, because they are what the preview's
// node names and the gltf viewer are keyed on.
var topLabels = map[string]string{
	"nodes":  "objects & groups",
	"meshes": "geometry",
}

func relabel(changes []fhr.DiffChange) {
	for i := range changes {
		if l, ok := topLabels[changes[i].Path]; ok {
			changes[i].Label = l
		}
	}
}

// diffMtlLibs reports a change to the mtllib references. glTF has no place for
// them, so they are diffed here, beside the engine's output.
func diffMtlLibs(a, b []string) []fhr.DiffChange {
	as, bs := strings.Join(a, ", "), strings.Join(b, ", ")
	if as == bs {
		return nil
	}
	return []fhr.DiffChange{sideChange("mtllib", "material libraries", as, bs)}
}

// diffUninterpreted is the honesty row. Statements outside the interpreted set
// (smoothing groups, free-form curves and surfaces, …) are not diffed
// semantically, but if they changed the diff says so instead of reporting a
// changed file as unchanged.
func diffUninterpreted(a, b *objFile) []fhr.DiffChange {
	if a.otherHash == b.otherHash && a.otherSummary() == b.otherSummary() {
		return nil
	}
	c := sideChange("other", "statements not diffed semantically", a.otherSummary(), b.otherSummary())
	if c.Kind == fhr.Modified && a.otherSummary() == b.otherSummary() {
		// Same keywords and counts, different content.
		c.Before, c.After = a.otherSummary(), b.otherSummary()+" (content changed)"
	}
	return []fhr.DiffChange{c}
}

// sideChange builds a before/after row whose kind follows which sides exist.
func sideChange(path, label, before, after string) fhr.DiffChange {
	c := fhr.DiffChange{Path: path, Label: label}
	switch {
	case before == "":
		c.Kind, c.After = fhr.Added, after
	case after == "":
		c.Kind, c.Before = fhr.Removed, before
	default:
		c.Kind, c.Before, c.After = fhr.Modified, before, after
	}
	return c
}
