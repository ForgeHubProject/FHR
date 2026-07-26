// Package main provides a format-aware handler for .gltf and .glb files,
// exposed as a standalone binary implementing the FHR subprocess protocol.
//
// Migrated from forgehubproject/forge internal/handler/gltf/gltf.go.
// The diff and merge logic is unchanged; the forge-internal types have been
// replaced with the local wire types in types.go.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/qmuntal/gltf"
)

// Handler is the glTF/GLB format handler.
type Handler struct{}

// Match returns true for .gltf and .glb files.
func (h *Handler) Match(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".gltf" || ext == ".glb"
}

// ── semantic paths ────────────────────────────────────────────────────────────
//
// Every DiffChange.Path and SemanticConflict.Path is a "/"-separated list of
// segments, fully qualified from the document root down to the changed
// property:
//
//	nodes/Mirror_L/translation
//	materials/Paint/baseColorTexture
//	animations/Spin/channels/0/output
//
// glTF names are free-form UTF-8: "." is extremely common (Blender emits
// Cube.001) and "/" is legal too, so a raw name cannot be concatenated into a
// path unescaped. Segments are therefore percent-escaped for the two characters
// that would otherwise make a path unparseable — "%" → "%25" and "/" → "%2F" —
// which leaves the overwhelmingly common "." untouched and keeps paths readable.
// Path is the machine key; Label always carries the raw, unescaped name, so no
// UI ever displays an escaped form.
const pathSep = "/"

// escapeSegment percent-escapes the path separator (and the escape character
// itself) so a segment can be joined into a path unambiguously.
func escapeSegment(s string) string {
	if !strings.ContainsAny(s, "%/") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '%':
			b.WriteString("%25")
		case '/':
			b.WriteString("%2F")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unescapeSegment reverses escapeSegment.
func unescapeSegment(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			switch s[i+1 : i+3] {
			case "25":
				b.WriteByte('%')
				i += 2
				continue
			case "2F", "2f":
				b.WriteByte('/')
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// joinPath builds a semantic path from raw (unescaped) segments.
func joinPath(segments ...string) string {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = escapeSegment(s)
	}
	return strings.Join(escaped, pathSep)
}

// childPath qualifies a property name against its parent's already-escaped path.
func childPath(parent, segment string) string {
	if parent == "" {
		return escapeSegment(segment)
	}
	return parent + pathSep + escapeSegment(segment)
}

// splitPath splits a semantic path back into raw (unescaped) segments.
func splitPath(p string) []string {
	parts := strings.Split(p, pathSep)
	for i, s := range parts {
		parts[i] = unescapeSegment(s)
	}
	return parts
}

// ── element keys ──────────────────────────────────────────────────────────────

// uniqueKeys assigns every element of a collection its own diff key, so that
// every element participates in the diff. glTF names are not unique: a document
// with two nodes named "Wheel" used to collapse them into one map entry and the
// second was silently dropped from both the diff and the merge. Duplicates now
// get an ordinal suffix — Wheel, Wheel#1, Wheel#2 — with the first occurrence
// keeping the bare name so paths for the common (unique-name) case are
// unchanged. The suffix loop also guarantees uniqueness against a name that
// literally contains "#1".
func uniqueKeys[T any](items []T, name func(T, int) string) []string {
	keys := make([]string, len(items))
	taken := make(map[string]bool, len(items))
	for i, it := range items {
		base := name(it, i)
		k := base
		for dup := 1; taken[k]; dup++ {
			k = fmt.Sprintf("%s#%d", base, dup)
		}
		taken[k] = true
		keys[i] = k
	}
	return keys
}

// mergeKeyOrder returns the union of two key orders: everything on the base
// side in order, then keys that only exist on the head side.
func mergeKeyOrder(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Merge performs a 3-way semantic merge of glTF/GLB blobs.
//
// The algorithm mirrors what git does for text at the line level, but operates
// on named scene-graph units instead:
//
//	only ours changed a property  → take ours  (already in result)
//	only theirs changed a property → take theirs (applied to result)
//	both changed to the same value → take either (no conflict)
//	both changed to different values → keep ours, record conflict
//
// Added/removed elements follow the same logic at the element level.
func (h *Handler) Merge(base, ours, theirs Blob) (Blob, *ConflictInfo, error) {
	if len(base) == 0 {
		ci := &ConflictInfo{
			Conflicts: []SemanticConflict{{
				Path: "file", Ours: "created", Theirs: "created",
			}},
		}
		return ours, ci, nil
	}

	docBase, err := parseDoc(base)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing base: %w", err)
	}
	docOurs, err := parseDoc(ours)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing ours: %w", err)
	}
	docTheirs, err := parseDoc(theirs)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing theirs: %w", err)
	}

	var conflicts []SemanticConflict

	docOurs.Nodes = mergeNodeList(docBase.Nodes, docOurs.Nodes, docTheirs.Nodes, &conflicts)
	docOurs.Materials = mergeMaterialList(docBase.Materials, docOurs.Materials, docTheirs.Materials, &conflicts)
	docOurs.Meshes = mergeMeshList(docBase.Meshes, docOurs.Meshes, docTheirs.Meshes, &conflicts)
	docOurs.Animations = mergeAnimationList(docBase.Animations, docOurs.Animations, docTheirs.Animations, &conflicts)

	nMeshes := len(docOurs.Meshes)
	for _, n := range docOurs.Nodes {
		if n.Mesh != nil && int(*n.Mesh) >= nMeshes {
			n.Mesh = nil
		}
	}

	result, err := encodeBlob(docOurs, isGLB(ours))
	if err != nil {
		return nil, nil, fmt.Errorf("encoding merged glTF: %w", err)
	}

	var ci *ConflictInfo
	if len(conflicts) > 0 {
		ci = &ConflictInfo{Conflicts: conflicts}
	}
	return result, ci, nil
}

// ── merge: nodes ──────────────────────────────────────────────────────────────

func mergeNodeList(base, ours, theirs []*gltf.Node, conflicts *[]SemanticConflict) []*gltf.Node {
	baseMap, _ := nodeMap(base)
	oursMap, oursOrder := nodeMap(ours)
	theirsMap, theirsOrder := nodeMap(theirs)

	names := mergeKeyOrder(oursOrder, theirsOrder)

	var result []*gltf.Node
	for _, name := range names {
		bn := baseMap[name]
		on, inOurs := oursMap[name]
		tn, inTheirs := theirsMap[name]

		switch {
		case inOurs && inTheirs:
			result = append(result, merge3Node(bn, on, tn, name, conflicts))

		case inOurs && !inTheirs:
			if bn != nil {
				*conflicts = append(*conflicts, SemanticConflict{
					Path: joinPath("nodes", name), Ours: "kept", Theirs: "removed",
				})
			}
			result = append(result, on)

		case !inOurs && inTheirs:
			if bn != nil {
				*conflicts = append(*conflicts, SemanticConflict{
					Path: joinPath("nodes", name), Ours: "removed", Theirs: "kept",
				})
			} else {
				result = append(result, tn)
			}
		}
	}
	return result
}

func merge3Node(bn, on, tn *gltf.Node, name string, conflicts *[]SemanticConflict) *gltf.Node {
	out := cloneNode(on)

	var baseTr, baseSc [3]float64
	var baseRotQ [4]float64
	if bn != nil {
		baseTr = bn.TranslationOrDefault()
		baseRotQ = bn.RotationOrDefault()
		baseSc = bn.ScaleOrDefault()
	} else {
		baseRotQ = gltf.DefaultRotation
		baseSc = gltf.DefaultScale
	}

	ourTr, theirTr := on.TranslationOrDefault(), tn.TranslationOrDefault()
	if !nearEq3(ourTr, baseTr) && !nearEq3(theirTr, baseTr) {
		if nearEq3(ourTr, theirTr) {
			out.Translation = ourTr
		} else {
			*conflicts = append(*conflicts, SemanticConflict{
				Path:   joinPath("nodes", name, "translation"),
				Ours:   fmtVec3(blenderTranslation(ourTr)),
				Theirs: fmtVec3(blenderTranslation(theirTr)),
			})
		}
	} else if nearEq3(ourTr, baseTr) && !nearEq3(theirTr, baseTr) {
		out.Translation = theirTr
	}

	ourRot, theirRot := on.RotationOrDefault(), tn.RotationOrDefault()
	if !nearEq4(ourRot, baseRotQ) && !nearEq4(theirRot, baseRotQ) {
		if nearEq4(ourRot, theirRot) {
			out.Rotation = ourRot
		} else {
			*conflicts = append(*conflicts, SemanticConflict{
				Path:   joinPath("nodes", name, "rotation"),
				Ours:   fmtRot(ourRot),
				Theirs: fmtRot(theirRot),
			})
		}
	} else if nearEq4(ourRot, baseRotQ) && !nearEq4(theirRot, baseRotQ) {
		out.Rotation = theirRot
	}

	ourSc, theirSc := on.ScaleOrDefault(), tn.ScaleOrDefault()
	if !nearEq3(ourSc, baseSc) && !nearEq3(theirSc, baseSc) {
		if nearEq3(ourSc, theirSc) {
			out.Scale = ourSc
		} else {
			*conflicts = append(*conflicts, SemanticConflict{
				Path:   joinPath("nodes", name, "scale"),
				Ours:   fmtVec3(blenderScale(ourSc)),
				Theirs: fmtVec3(blenderScale(theirSc)),
			})
		}
	} else if nearEq3(ourSc, baseSc) && !nearEq3(theirSc, baseSc) {
		out.Scale = theirSc
	}

	baseMesh := ptrLabel(func() *int {
		if bn != nil {
			return bn.Mesh
		}
		return nil
	}(), "mesh")
	ourMesh, theirMesh := ptrLabel(on.Mesh, "mesh"), ptrLabel(tn.Mesh, "mesh")
	if ourMesh == baseMesh && theirMesh != baseMesh {
		out.Mesh = tn.Mesh
	} else if ourMesh != baseMesh && theirMesh != baseMesh && ourMesh != theirMesh {
		*conflicts = append(*conflicts, SemanticConflict{
			Path: joinPath("nodes", name, "mesh"),
			Ours: ourMesh, Theirs: theirMesh,
		})
	}

	return out
}

func cloneNode(n *gltf.Node) *gltf.Node {
	c := *n
	if n.Mesh != nil {
		m := *n.Mesh
		c.Mesh = &m
	}
	if n.Skin != nil {
		s := *n.Skin
		c.Skin = &s
	}
	if len(n.Children) > 0 {
		c.Children = make([]int, len(n.Children))
		copy(c.Children, n.Children)
	}
	return &c
}

// ── merge: materials ──────────────────────────────────────────────────────────

func mergeMaterialList(base, ours, theirs []*gltf.Material, conflicts *[]SemanticConflict) []*gltf.Material {
	baseMap, _ := materialMap(base)
	oursMap, oursOrder := materialMap(ours)
	theirsMap, theirsOrder := materialMap(theirs)

	names := mergeKeyOrder(oursOrder, theirsOrder)

	var result []*gltf.Material
	for _, name := range names {
		bm := baseMap[name]
		om, inOurs := oursMap[name]
		tm, inTheirs := theirsMap[name]

		switch {
		case inOurs && inTheirs:
			result = append(result, merge3Material(bm, om, tm, name, conflicts))
		case inOurs && !inTheirs:
			if bm != nil {
				*conflicts = append(*conflicts, SemanticConflict{
					Path: joinPath("materials", name), Ours: "kept", Theirs: "removed",
				})
			}
			result = append(result, om)
		case !inOurs && inTheirs:
			if bm != nil {
				*conflicts = append(*conflicts, SemanticConflict{
					Path: joinPath("materials", name), Ours: "removed", Theirs: "kept",
				})
			} else {
				result = append(result, tm)
			}
		}
	}
	return result
}

func merge3Material(bm, om, tm *gltf.Material, name string, conflicts *[]SemanticConflict) *gltf.Material {
	out := cloneMaterial(om)

	bPBR := pbrOrDefault(func() *gltf.Material {
		if bm != nil {
			return bm
		}
		return &gltf.Material{}
	}())
	oPBR := pbrOrDefault(om)
	tPBR := pbrOrDefault(tm)

	baseBC := bPBR.BaseColorFactorOrDefault()
	ourBC, theirBC := oPBR.BaseColorFactorOrDefault(), tPBR.BaseColorFactorOrDefault()
	if ourBC == baseBC && theirBC != baseBC {
		setBaseColor(out, theirBC)
	} else if ourBC != baseBC && theirBC != baseBC && ourBC != theirBC {
		*conflicts = append(*conflicts, SemanticConflict{
			Path: joinPath("materials", name, "baseColorFactor"),
			Ours: fmtVec4(ourBC), Theirs: fmtVec4(theirBC),
		})
	}

	baseMet := bPBR.MetallicFactorOrDefault()
	ourMet, theirMet := oPBR.MetallicFactorOrDefault(), tPBR.MetallicFactorOrDefault()
	if nearEq(ourMet, baseMet) && !nearEq(theirMet, baseMet) {
		setMetallic(out, theirMet)
	} else if !nearEq(ourMet, baseMet) && !nearEq(theirMet, baseMet) && !nearEq(ourMet, theirMet) {
		*conflicts = append(*conflicts, SemanticConflict{
			Path: joinPath("materials", name, "metallicFactor"),
			Ours: fmtF(ourMet), Theirs: fmtF(theirMet),
		})
	}

	baseRough := bPBR.RoughnessFactorOrDefault()
	ourRough, theirRough := oPBR.RoughnessFactorOrDefault(), tPBR.RoughnessFactorOrDefault()
	if nearEq(ourRough, baseRough) && !nearEq(theirRough, baseRough) {
		setRoughness(out, theirRough)
	} else if !nearEq(ourRough, baseRough) && !nearEq(theirRough, baseRough) && !nearEq(ourRough, theirRough) {
		*conflicts = append(*conflicts, SemanticConflict{
			Path: joinPath("materials", name, "roughnessFactor"),
			Ours: fmtF(ourRough), Theirs: fmtF(theirRough),
		})
	}

	var baseAlpha gltf.AlphaMode
	if bm != nil {
		baseAlpha = bm.AlphaMode
	}
	if om.AlphaMode == baseAlpha && tm.AlphaMode != baseAlpha {
		out.AlphaMode = tm.AlphaMode
	} else if om.AlphaMode != baseAlpha && tm.AlphaMode != baseAlpha && om.AlphaMode != tm.AlphaMode {
		*conflicts = append(*conflicts, SemanticConflict{
			Path: joinPath("materials", name, "alphaMode"),
			Ours: om.AlphaMode.String(), Theirs: tm.AlphaMode.String(),
		})
	}

	var baseDS bool
	if bm != nil {
		baseDS = bm.DoubleSided
	}
	if om.DoubleSided == baseDS && tm.DoubleSided != baseDS {
		out.DoubleSided = tm.DoubleSided
	} else if om.DoubleSided != baseDS && tm.DoubleSided != baseDS && om.DoubleSided != tm.DoubleSided {
		*conflicts = append(*conflicts, SemanticConflict{
			Path:   joinPath("materials", name, "doubleSided"),
			Ours:   fmt.Sprintf("%v", om.DoubleSided),
			Theirs: fmt.Sprintf("%v", tm.DoubleSided),
		})
	}

	return out
}

func cloneMaterial(m *gltf.Material) *gltf.Material {
	c := *m
	if m.PBRMetallicRoughness != nil {
		pbr := *m.PBRMetallicRoughness
		if pbr.BaseColorFactor != nil {
			bc := *pbr.BaseColorFactor
			pbr.BaseColorFactor = &bc
		}
		if pbr.MetallicFactor != nil {
			mf := *pbr.MetallicFactor
			pbr.MetallicFactor = &mf
		}
		if pbr.RoughnessFactor != nil {
			rf := *pbr.RoughnessFactor
			pbr.RoughnessFactor = &rf
		}
		c.PBRMetallicRoughness = &pbr
	}
	return &c
}

func setBaseColor(m *gltf.Material, v [4]float64) {
	if m.PBRMetallicRoughness == nil {
		m.PBRMetallicRoughness = &gltf.PBRMetallicRoughness{}
	}
	m.PBRMetallicRoughness.BaseColorFactor = &v
}

func setMetallic(m *gltf.Material, v float64) {
	if m.PBRMetallicRoughness == nil {
		m.PBRMetallicRoughness = &gltf.PBRMetallicRoughness{}
	}
	m.PBRMetallicRoughness.MetallicFactor = &v
}

func setRoughness(m *gltf.Material, v float64) {
	if m.PBRMetallicRoughness == nil {
		m.PBRMetallicRoughness = &gltf.PBRMetallicRoughness{}
	}
	m.PBRMetallicRoughness.RoughnessFactor = &v
}

// ── merge: meshes ─────────────────────────────────────────────────────────────

// mergeMeshList detects 3-way conflicts on mesh arrays but always returns ours
// unchanged. Meshes reference accessors/bufferViews/buffers by integer index;
// copying a mesh from theirs would produce dangling index references.
// Full index-remapping is deferred to a future release.
func mergeMeshList(base, ours, theirs []*gltf.Mesh, conflicts *[]SemanticConflict) []*gltf.Mesh {
	baseMap, _ := meshMap(base)
	oursMap, oursOrder := meshMap(ours)
	theirsMap, theirsOrder := meshMap(theirs)

	for i, om := range ours {
		name := oursOrder[i]
		bm := baseMap[name]
		tm, inTheirs := theirsMap[name]
		if !inTheirs {
			if bm != nil {
				*conflicts = append(*conflicts, SemanticConflict{
					Path: joinPath("meshes", name), Ours: "kept", Theirs: "removed",
				})
			}
			continue
		}
		ourChanged := !jsonEqual(bm, om)
		theirChanged := !jsonEqual(bm, tm)
		if ourChanged && theirChanged && !jsonEqual(om, tm) {
			*conflicts = append(*conflicts, SemanticConflict{
				Path:   joinPath("meshes", name),
				Ours:   fmt.Sprintf("%d primitives", len(om.Primitives)),
				Theirs: fmt.Sprintf("%d primitives", len(tm.Primitives)),
			})
		}
	}
	for _, name := range theirsOrder {
		if _, inOurs := oursMap[name]; inOurs {
			continue
		}
		if baseMap[name] != nil {
			*conflicts = append(*conflicts, SemanticConflict{
				Path: joinPath("meshes", name), Ours: "removed", Theirs: "kept",
			})
		}
	}
	return ours
}

// ── merge: animations ─────────────────────────────────────────────────────────

// mergeAnimationList detects 3-way conflicts on animation arrays but always
// returns ours unchanged. Same accessor-index constraint as mergeMeshList.
func mergeAnimationList(base, ours, theirs []*gltf.Animation, conflicts *[]SemanticConflict) []*gltf.Animation {
	baseMap, _ := animMap(base)
	oursMap, oursOrder := animMap(ours)
	theirsMap, theirsOrder := animMap(theirs)

	for i, oa := range ours {
		name := oursOrder[i]
		ba := baseMap[name]
		ta, inTheirs := theirsMap[name]
		if !inTheirs {
			if ba != nil {
				*conflicts = append(*conflicts, SemanticConflict{
					Path: joinPath("animations", name), Ours: "kept", Theirs: "removed",
				})
			}
			continue
		}
		ourChanged := !jsonEqual(ba, oa)
		theirChanged := !jsonEqual(ba, ta)
		if ourChanged && theirChanged && !jsonEqual(oa, ta) {
			*conflicts = append(*conflicts, SemanticConflict{
				Path:   joinPath("animations", name),
				Ours:   fmt.Sprintf("%d channels", len(oa.Channels)),
				Theirs: fmt.Sprintf("%d channels", len(ta.Channels)),
			})
		}
	}
	for _, name := range theirsOrder {
		if _, inOurs := oursMap[name]; inOurs {
			continue
		}
		if baseMap[name] != nil {
			*conflicts = append(*conflicts, SemanticConflict{
				Path: joinPath("animations", name), Ours: "removed", Theirs: "kept",
			})
		}
	}
	return ours
}

func jsonEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return bytes.Equal(aj, bj)
}

// ── conflict resolution ───────────────────────────────────────────────────────

// ApplyChoices patches merged (which holds "ours" for every conflict) by
// replacing values at takePaths with corresponding values from theirs.
func (h *Handler) ApplyChoices(merged, theirs Blob, takePaths []string) (Blob, error) {
	if len(takePaths) == 0 {
		return merged, nil
	}
	for _, p := range takePaths {
		if p == "file" {
			return theirs, nil
		}
	}
	docM, err := parseDoc(merged)
	if err != nil {
		return nil, fmt.Errorf("parsing merged: %w", err)
	}
	docT, err := parseDoc(theirs)
	if err != nil {
		return nil, fmt.Errorf("parsing theirs: %w", err)
	}
	for _, path := range takePaths {
		applyChoice(docM, docT, path)
	}
	return encodeBlob(docM, isGLB(merged))
}

func applyChoice(docM, docT *gltf.Document, path string) {
	// Conflict paths use the same escaped, "/"-separated form as diff paths, so
	// they have to be unescaped before the name is matched against the document.
	parts := splitPath(path)
	if len(parts) < 2 {
		return
	}
	name := parts[1]
	prop := ""
	if len(parts) > 2 {
		prop = parts[2]
	}

	switch parts[0] {
	case "nodes":
		tn := nodeByName(docT.Nodes, name)
		mn := nodeByName(docM.Nodes, name)
		if prop == "" {
			if tn != nil && mn == nil {
				docM.Nodes = append(docM.Nodes, tn)
			} else if tn == nil && mn != nil {
				docM.Nodes = removeNode(docM.Nodes, name)
			}
			return
		}
		if mn == nil || tn == nil {
			return
		}
		switch prop {
		case "translation":
			mn.Translation = tn.TranslationOrDefault()
		case "rotation":
			mn.Rotation = tn.RotationOrDefault()
		case "scale":
			mn.Scale = tn.ScaleOrDefault()
		case "mesh":
			mn.Mesh = tn.Mesh
		}

	case "materials":
		tm := materialByName(docT.Materials, name)
		mm := materialByName(docM.Materials, name)
		if prop == "" {
			if tm != nil && mm == nil {
				docM.Materials = append(docM.Materials, tm)
			} else if tm == nil && mm != nil {
				docM.Materials = removeMaterial(docM.Materials, name)
			}
			return
		}
		if mm == nil || tm == nil {
			return
		}
		tPBR := pbrOrDefault(tm)
		switch prop {
		case "baseColorFactor":
			setBaseColor(mm, tPBR.BaseColorFactorOrDefault())
		case "metallicFactor":
			setMetallic(mm, tPBR.MetallicFactorOrDefault())
		case "roughnessFactor":
			setRoughness(mm, tPBR.RoughnessFactorOrDefault())
		case "alphaMode":
			mm.AlphaMode = tm.AlphaMode
		case "doubleSided":
			mm.DoubleSided = tm.DoubleSided
		}
	}
}

// The *ByName/remove* helpers below resolve the same disambiguated keys the
// merge conflicts were reported with, so a choice taken on the second "Wheel"
// lands on that node and not on the first.

func nodeByName(nodes []*gltf.Node, name string) *gltf.Node {
	m, _ := nodeMap(nodes)
	return m[name]
}

func removeNode(nodes []*gltf.Node, name string) []*gltf.Node {
	_, keys := nodeMap(nodes)
	out := nodes[:0:0]
	for i, n := range nodes {
		if keys[i] != name {
			out = append(out, n)
		}
	}
	return out
}

func materialByName(mats []*gltf.Material, name string) *gltf.Material {
	m, _ := materialMap(mats)
	return m[name]
}

func removeMaterial(mats []*gltf.Material, name string) []*gltf.Material {
	_, keys := materialMap(mats)
	out := mats[:0:0]
	for i, m := range mats {
		if keys[i] != name {
			out = append(out, m)
		}
	}
	return out
}

// ── serialisation ─────────────────────────────────────────────────────────────

func isGLB(blob Blob) bool {
	return len(blob) >= 4 && string(blob[:4]) == "glTF"
}

func encodeBlob(doc *gltf.Document, binary bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := gltf.NewEncoder(&buf)
	enc.AsBinary = binary
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Diff produces a hierarchical semantic diff of two glTF/GLB blobs. An empty
// blob on either side is treated as an empty document, so an added file (no
// base) diffs as all-added and a deleted file (no head) as all-removed —
// matching git's whole-file add/delete semantics rather than showing nothing.
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	docA, err := parseSideOrEmpty(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("parsing base: %w", err)
	}
	docB, err := parseSideOrEmpty(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("parsing head: %w", err)
	}

	// Non-nil so an empty diff marshals as [] (not null) — every consumer, from
	// the renderer bundle to ForgeHub, can then trust changes is always a list.
	changes := []DiffChange{}
	if c := diffNodes(docA, docB); c != nil {
		changes = append(changes, *c)
	}
	if c := diffMaterials(docA, docB); c != nil {
		changes = append(changes, *c)
	}
	if c := diffMeshes(docA, docB); c != nil {
		changes = append(changes, *c)
	}
	if c := diffAnimations(docA, docB); c != nil {
		changes = append(changes, *c)
	}

	return StructuredDiff{Version: "1.0", Format: "gltf-scene", Changes: changes}, nil
}

// parseSideOrEmpty parses a blob, or returns an empty document for an empty
// blob (the added/deleted-file case).
func parseSideOrEmpty(blob Blob) (*gltf.Document, error) {
	if len(blob) == 0 {
		return &gltf.Document{}, nil
	}
	return parseDoc(blob)
}

func parseDoc(blob Blob) (*gltf.Document, error) {
	doc := new(gltf.Document)
	err := gltf.NewDecoder(bytes.NewReader(blob)).Decode(doc)
	if err != nil && doc.Asset.Version == "" {
		return nil, fmt.Errorf("failed to parse glTF: %w", err)
	}
	return doc, nil
}

// ── nodes ─────────────────────────────────────────────────────────────────────

func diffNodes(a, b *gltf.Document) *DiffChange {
	aIx, bIx := indexNodes(a.Nodes), indexNodes(b.Nodes)
	keys := mergeKeyOrder(aIx.keys, bIx.keys)

	var children []DiffChange
	for _, key := range keys {
		ai, inA := aIx.byKey[key]
		bi, inB := bIx.byKey[key]
		path := joinPath("nodes", key)

		switch {
		case !inA:
			c := DiffChange{
				Path: path, Label: key,
				Kind: Added, After: "node",
			}
			if props := nodePropsOneSide(bIx, bi, path, Added); len(props) > 0 {
				c.Children = props
			}
			children = append(children, c)
		case !inB:
			c := DiffChange{
				Path: path, Label: key,
				Kind: Removed, Before: "node",
			}
			if props := nodePropsOneSide(aIx, ai, path, Removed); len(props) > 0 {
				c.Children = props
			}
			children = append(children, c)
		default:
			if props := diffNodeProps(aIx, ai, bIx, bi, path); len(props) > 0 {
				children = append(children, DiffChange{
					Path:     path,
					Label:    key,
					Kind:     Modified,
					Children: props,
				})
			}
		}
	}

	if len(children) == 0 {
		return nil
	}
	return &DiffChange{
		Path: "nodes", Label: "nodes",
		Kind:     Modified,
		Children: children,
	}
}

func nodeMap(nodes []*gltf.Node) (map[string]*gltf.Node, []string) {
	order := uniqueKeys(nodes, nodeName)
	m := make(map[string]*gltf.Node, len(nodes))
	for i, n := range nodes {
		m[order[i]] = n
	}
	return m, order
}

func nodeName(n *gltf.Node, i int) string {
	if n.Name != "" {
		return n.Name
	}
	return fmt.Sprintf("node[%d]", i)
}

// nodeIndex is a document's node array plus the derived lookups the diff needs:
// a unique key per node and the parent of every node. glTF stores the hierarchy
// as child index lists, so the parent has to be inverted out of them — without
// it a re-parent (moving Mirror_L from Body to Door_L) changes nothing the flat
// node walk can see, and diffs as no change at all.
type nodeIndex struct {
	nodes  []*gltf.Node
	keys   []string       // diff key per node index
	byKey  map[string]int // diff key → node index
	parent []int          // parent node index, or rootIndex for a top-level node
}

const rootIndex = -1

// rootParentLabel is the reported parent of a node that sits at the top of the
// hierarchy (a scene root, or an orphan no node lists as a child).
const rootParentLabel = "<root>"

func indexNodes(nodes []*gltf.Node) *nodeIndex {
	ix := &nodeIndex{
		nodes:  nodes,
		keys:   uniqueKeys(nodes, nodeName),
		byKey:  make(map[string]int, len(nodes)),
		parent: make([]int, len(nodes)),
	}
	for i, k := range ix.keys {
		ix.byKey[k] = i
		ix.parent[i] = rootIndex
	}
	for i, n := range nodes {
		for _, c := range n.Children {
			// Ignore dangling indices, self-parenting and a second claim on an
			// already-parented node: malformed documents must not break the diff.
			if c >= 0 && c < len(nodes) && c != i && ix.parent[c] == rootIndex {
				ix.parent[c] = i
			}
		}
	}
	return ix
}

// parentKey names the parent of node i for display and comparison.
func (ix *nodeIndex) parentKey(i int) string {
	if i < 0 || i >= len(ix.parent) || ix.parent[i] == rootIndex {
		return rootParentLabel
	}
	return ix.keys[ix.parent[i]]
}

// diffNodeProps compares the properties of one node across both sides. path is
// the node's own fully-qualified path; every child change is qualified against
// it so consumers get a usable selection key without composing anything.
func diffNodeProps(aIx *nodeIndex, ai int, bIx *nodeIndex, bi int, path string) []DiffChange {
	a, b := aIx.nodes[ai], bIx.nodes[bi]
	var changes []DiffChange

	// Hierarchy first: a re-parent is a structural change and reads better above
	// the transform noise it usually comes with.
	if pa, pb := aIx.parentKey(ai), bIx.parentKey(bi); pa != pb {
		changes = append(changes, DiffChange{
			Path: childPath(path, "parent"), Label: "parent",
			Kind: Modified, Before: pa, After: pb,
		})
	}
	if ta, tb := a.TranslationOrDefault(), b.TranslationOrDefault(); !nearEq3(ta, tb) {
		changes = append(changes, DiffChange{
			Path: childPath(path, "translation"), Label: "translation",
			Kind: Modified, Before: fmtVec3(blenderTranslation(ta)), After: fmtVec3(blenderTranslation(tb)),
		})
	}
	if ra, rb := a.RotationOrDefault(), b.RotationOrDefault(); !nearEq4(ra, rb) {
		changes = append(changes, DiffChange{
			Path: childPath(path, "rotation"), Label: "rotation",
			Kind: Modified, Before: fmtRot(ra), After: fmtRot(rb),
		})
	}
	if sa, sb := a.ScaleOrDefault(), b.ScaleOrDefault(); !nearEq3(sa, sb) {
		changes = append(changes, DiffChange{
			Path: childPath(path, "scale"), Label: "scale",
			Kind: Modified, Before: fmtVec3(blenderScale(sa)), After: fmtVec3(blenderScale(sb)),
		})
	}
	meshA, meshB := ptrLabel(a.Mesh, "mesh"), ptrLabel(b.Mesh, "mesh")
	if meshA != meshB {
		changes = append(changes, DiffChange{
			Path: childPath(path, "mesh"), Label: "mesh",
			Kind: Modified, Before: meshA, After: meshB,
		})
	}
	return changes
}

func nodePropsOneSide(ix *nodeIndex, i int, path string, kind ChangeKind) []DiffChange {
	n := ix.nodes[i]
	var changes []DiffChange
	add := func(segment string, v string) {
		c := DiffChange{Path: childPath(path, segment), Label: segment, Kind: kind}
		if kind == Added {
			c.After = v
		} else {
			c.Before = v
		}
		changes = append(changes, c)
	}
	if p := ix.parentKey(i); p != rootParentLabel {
		add("parent", p)
	}
	if t := n.TranslationOrDefault(); !nearEq3(t, gltf.DefaultTranslation) {
		add("translation", fmtVec3(blenderTranslation(t)))
	}
	if r := n.RotationOrDefault(); !nearEq4(r, gltf.DefaultRotation) {
		add("rotation", fmtRot(r))
	}
	if s := n.ScaleOrDefault(); !nearEq3(s, gltf.DefaultScale) {
		add("scale", fmtVec3(blenderScale(s)))
	}
	if n.Mesh != nil {
		add("mesh", ptrLabel(n.Mesh, "mesh"))
	}
	return changes
}

// ── materials ─────────────────────────────────────────────────────────────────

func diffMaterials(a, b *gltf.Document) *DiffChange {
	aMap, aOrder := materialMap(a.Materials)
	bMap, bOrder := materialMap(b.Materials)
	names := mergeKeyOrder(aOrder, bOrder)

	var children []DiffChange
	for _, name := range names {
		am, inA := aMap[name]
		bm, inB := bMap[name]
		path := joinPath("materials", name)
		switch {
		case !inA:
			children = append(children, DiffChange{
				Path: path, Label: name,
				Kind: Added, After: "material",
			})
		case !inB:
			children = append(children, DiffChange{
				Path: path, Label: name,
				Kind: Removed, Before: "material",
			})
		default:
			if props := diffMaterialProps(am, bm, a, b, path); len(props) > 0 {
				children = append(children, DiffChange{
					Path: path, Label: name,
					Kind: Modified, Children: props,
				})
			}
		}
	}
	if len(children) == 0 {
		return nil
	}
	return &DiffChange{
		Path: "materials", Label: "materials",
		Kind: Modified, Children: children,
	}
}

func materialMap(mats []*gltf.Material) (map[string]*gltf.Material, []string) {
	order := uniqueKeys(mats, materialName)
	m := make(map[string]*gltf.Material, len(mats))
	for i, mat := range mats {
		m[order[i]] = mat
	}
	return m, order
}

func materialName(m *gltf.Material, i int) string {
	if m.Name != "" {
		return m.Name
	}
	return fmt.Sprintf("material[%d]", i)
}

// diffMaterialProps compares one material across both sides. docA/docB are the
// owning documents, needed to resolve texture → image/sampler references.
func diffMaterialProps(a, b *gltf.Material, docA, docB *gltf.Document, path string) []DiffChange {
	var changes []DiffChange
	emit := func(segment, before, after string) {
		changes = append(changes, DiffChange{
			Path: childPath(path, segment), Label: segment,
			Kind: Modified, Before: before, After: after,
		})
	}
	// emitIfDiff is for properties whose descriptor string *is* the comparison.
	emitIfDiff := func(segment, before, after string) {
		if before != after {
			emit(segment, before, after)
		}
	}
	aPBR := pbrOrDefault(a)
	bPBR := pbrOrDefault(b)
	if ca, cb := aPBR.BaseColorFactorOrDefault(), bPBR.BaseColorFactorOrDefault(); ca != cb {
		emit("baseColorFactor", fmtVec4(ca), fmtVec4(cb))
	}
	if ma, mb := aPBR.MetallicFactorOrDefault(), bPBR.MetallicFactorOrDefault(); !nearEq(ma, mb) {
		emit("metallicFactor", fmtF(ma), fmtF(mb))
	}
	if ra, rb := aPBR.RoughnessFactorOrDefault(), bPBR.RoughnessFactorOrDefault(); !nearEq(ra, rb) {
		emit("roughnessFactor", fmtF(ra), fmtF(rb))
	}
	if a.EmissiveFactor != b.EmissiveFactor {
		emit("emissiveFactor", fmtVec3(a.EmissiveFactor), fmtVec3(b.EmissiveFactor))
	}
	emitIfDiff("alphaMode", a.AlphaMode.String(), b.AlphaMode.String())
	emitIfDiff("doubleSided", fmt.Sprintf("%v", a.DoubleSided), fmt.Sprintf("%v", b.DoubleSided))

	// Texture slots. Retexturing a model changes nothing else in the material,
	// so without these a retexture-only commit diffed as no change at all.
	for _, slot := range textureSlots {
		emitIfDiff(slot.label, slot.describe(docA, a), slot.describe(docB, b))
	}
	return changes
}

func pbrOrDefault(m *gltf.Material) *gltf.PBRMetallicRoughness {
	if m.PBRMetallicRoughness != nil {
		return m.PBRMetallicRoughness
	}
	return &gltf.PBRMetallicRoughness{}
}

// ── material texture slots ────────────────────────────────────────────────────

// textureSlot is one texture-bearing property of a material, rendered as a
// self-contained descriptor string for diffing.
//
// The descriptor deliberately resolves the reference to *content* — image URI,
// or mime + content hash for embedded images, plus the sampler's own filter and
// wrap modes — and never mentions the texture/image/sampler array indices. Those
// indices shift whenever an unrelated texture is inserted upstream, which would
// report every material in the document as modified; two slots that describe
// identically do reference identical image data.
type textureSlot struct {
	label    string
	describe func(doc *gltf.Document, m *gltf.Material) string
}

var textureSlots = []textureSlot{
	{"baseColorTexture", func(doc *gltf.Document, m *gltf.Material) string {
		if p := m.PBRMetallicRoughness; p != nil && p.BaseColorTexture != nil {
			return textureInfoLabel(doc, &p.BaseColorTexture.Index, p.BaseColorTexture.TexCoord, "")
		}
		return noTexture
	}},
	{"metallicRoughnessTexture", func(doc *gltf.Document, m *gltf.Material) string {
		if p := m.PBRMetallicRoughness; p != nil && p.MetallicRoughnessTexture != nil {
			return textureInfoLabel(doc, &p.MetallicRoughnessTexture.Index, p.MetallicRoughnessTexture.TexCoord, "")
		}
		return noTexture
	}},
	{"normalTexture", func(doc *gltf.Document, m *gltf.Material) string {
		if t := m.NormalTexture; t != nil && t.Index != nil {
			return textureInfoLabel(doc, t.Index, t.TexCoord, "scale="+fmtF(t.ScaleOrDefault()))
		}
		return noTexture
	}},
	{"occlusionTexture", func(doc *gltf.Document, m *gltf.Material) string {
		if t := m.OcclusionTexture; t != nil && t.Index != nil {
			return textureInfoLabel(doc, t.Index, t.TexCoord, "strength="+fmtF(t.StrengthOrDefault()))
		}
		return noTexture
	}},
	{"emissiveTexture", func(doc *gltf.Document, m *gltf.Material) string {
		if t := m.EmissiveTexture; t != nil {
			return textureInfoLabel(doc, &t.Index, t.TexCoord, "")
		}
		return noTexture
	}},
}

const noTexture = "<none>"

// textureInfoLabel renders a resolved texture reference: what image it points
// at, how it is sampled, and which UV set it uses.
func textureInfoLabel(doc *gltf.Document, idx *int, texCoord int, extra string) string {
	if idx == nil {
		return noTexture
	}
	if *idx < 0 || *idx >= len(doc.Textures) {
		return fmt.Sprintf("<dangling texture %d>", *idx)
	}
	t := doc.Textures[*idx]
	parts := []string{imageRefLabel(doc, t.Source), samplerRefLabel(doc, t.Sampler), fmt.Sprintf("uv=%d", texCoord)}
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, " ")
}

// imageRefLabel describes the image a texture samples. External images are
// identified by URI; embedded ones (data URI or bufferView) by mime type plus a
// hash of their bytes, so swapping the embedded pixels shows up as a change.
func imageRefLabel(doc *gltf.Document, src *int) string {
	if src == nil {
		return "image=<none>"
	}
	if *src < 0 || *src >= len(doc.Images) {
		return fmt.Sprintf("image=<dangling %d>", *src)
	}
	im := doc.Images[*src]
	switch {
	case im.IsEmbeddedResource():
		data, err := im.MarshalData()
		if err != nil {
			data = []byte(im.URI)
		}
		return fmt.Sprintf("image=embedded mime=%s hash=%s", imageMime(im), contentHash(data))
	case im.URI != "":
		return "image=" + im.URI
	case im.BufferView != nil:
		data, ok := bufferViewBytes(doc, *im.BufferView)
		if !ok {
			return fmt.Sprintf("image=bufferView[%d] mime=%s hash=<unreadable>", *im.BufferView, imageMime(im))
		}
		return fmt.Sprintf("image=bufferView mime=%s hash=%s", imageMime(im), contentHash(data))
	default:
		return "image=<empty>"
	}
}

func imageMime(im *gltf.Image) string {
	if im.MimeType != "" {
		return im.MimeType
	}
	// Recover the mime type from a data URI: "data:image/png;base64,…".
	if rest, ok := strings.CutPrefix(im.URI, "data:"); ok {
		if mime, _, found := strings.Cut(rest, ";"); found {
			return mime
		}
	}
	return "<unknown>"
}

func samplerRefLabel(doc *gltf.Document, s *int) string {
	if s == nil {
		return "sampler=default"
	}
	if *s < 0 || *s >= len(doc.Samplers) {
		return fmt.Sprintf("sampler=<dangling %d>", *s)
	}
	sm := doc.Samplers[*s]
	return fmt.Sprintf("sampler=(mag=%d min=%d wrapS=%d wrapT=%d)", sm.MagFilter, sm.MinFilter, sm.WrapS, sm.WrapT)
}

// ── meshes ────────────────────────────────────────────────────────────────────

func diffMeshes(a, b *gltf.Document) *DiffChange {
	aMap, aOrder := meshMap(a.Meshes)
	bMap, bOrder := meshMap(b.Meshes)
	names := mergeKeyOrder(aOrder, bOrder)

	aSide, bSide := newMeshSide(a), newMeshSide(b)

	var children []DiffChange
	for _, name := range names {
		am, inA := aMap[name]
		bm, inB := bMap[name]
		path := joinPath("meshes", name)
		switch {
		case !inA:
			children = append(children, DiffChange{
				Path: path, Label: name,
				Kind: Added, After: fmt.Sprintf("%d primitives", len(bm.Primitives)),
			})
		case !inB:
			children = append(children, DiffChange{
				Path: path, Label: name,
				Kind: Removed, Before: fmt.Sprintf("%d primitives", len(am.Primitives)),
			})
		default:
			if props := diffMeshPrimitives(am, bm, aSide, bSide, path); len(props) > 0 {
				children = append(children, DiffChange{
					Path: path, Label: name, Kind: Modified, Children: props,
				})
			}
		}
	}
	if len(children) == 0 {
		return nil
	}
	return &DiffChange{
		Path: "meshes", Label: "meshes",
		Kind: Modified, Children: children,
	}
}

func meshMap(meshes []*gltf.Mesh) (map[string]*gltf.Mesh, []string) {
	order := uniqueKeys(meshes, meshName)
	m := make(map[string]*gltf.Mesh, len(meshes))
	for i, mesh := range meshes {
		m[order[i]] = mesh
	}
	return m, order
}

func meshName(m *gltf.Mesh, i int) string {
	if m.Name != "" {
		return m.Name
	}
	return fmt.Sprintf("mesh[%d]", i)
}

// ── mesh primitives: geometry, metrics, material ──────────────────────────────
//
// A mesh used to be compared by name and primitive *count* only, so the single
// most common edit in a 3D review — sculpting vertices without touching
// topology — diffed as no change at all, and so did reassigning a primitive to
// another existing material. Both are compared here.
//
// The cost discipline, in the order the checks run:
//
//	geometry   a memory compare of the accessor byte spans. No hashing on the
//	           unchanged path, which is the overwhelmingly common one, and a
//	           digest computed only for the streams actually displayed.
//	material   a name lookup; zero byte reads.
//	vertices   Accessor.Count; zero byte reads.
//	bounds     Accessor.Min/Max, which glTF *requires* on POSITION; zero
//	           byte reads.
//	centroid   the only check that decodes typed vertex data, so it runs
//	           exclusively for primitives whose POSITION bytes already differ.
//
// What is deliberately never emitted is a per-vertex payload: a StructuredDiff
// carrying 400k displacements is multiple megabytes of JSON, and the change tree
// is not where that belongs. Booleans and metrics only; the per-vertex deviation
// heatmap is issue #46, computed renderer-side from the two models it already has.

// meshSide bundles one side of the comparison with the document-wide lookups the
// primitive compare needs, so the helpers below take two symmetric arguments
// instead of a widening list of positional ones.
type meshSide struct {
	doc *gltf.Document
	// materialKeys maps a material index to the disambiguated key the materials
	// collection is diffed under, resolved once per document rather than once per
	// primitive.
	materialKeys []string
}

func newMeshSide(doc *gltf.Document) meshSide {
	return meshSide{doc: doc, materialKeys: uniqueKeys(doc.Materials, materialName)}
}

// diffMeshPrimitives compares the primitive lists of one mesh that exists on
// both sides.
//
// glTF primitives carry no identity of their own — they are an ordered array —
// so they are compared pairwise by index, and a count change is reported as its
// own row while the overlapping prefix is still compared. Content-based
// primitive matching, like content-based node matching, belongs with the
// identity cascade of issue #42.
func diffMeshPrimitives(am, bm *gltf.Mesh, a, b meshSide, path string) []DiffChange {
	var changes []DiffChange
	primitivesPath := childPath(path, "primitives")
	if len(am.Primitives) != len(bm.Primitives) {
		changes = append(changes, DiffChange{
			Path: primitivesPath, Label: "primitives", Kind: Modified,
			Before: fmt.Sprintf("%d", len(am.Primitives)),
			After:  fmt.Sprintf("%d", len(bm.Primitives)),
		})
	}
	for i := range min(len(am.Primitives), len(bm.Primitives)) {
		primPath := childPath(primitivesPath, strconv.Itoa(i))
		props := diffPrimitive(am.Primitives[i], bm.Primitives[i], a, b, primPath)
		if len(props) > 0 {
			changes = append(changes, DiffChange{
				Path: primPath, Label: fmt.Sprintf("primitive[%d]", i),
				Kind: Modified, Children: props,
			})
		}
	}
	return changes
}

// diffPrimitive compares one primitive across both sides.
func diffPrimitive(ap, bp *gltf.Primitive, a, b meshSide, path string) []DiffChange {
	var changes []DiffChange
	emit := func(segment, before, after string) {
		changes = append(changes, DiffChange{
			Path: childPath(path, segment), Label: segment,
			Kind: Modified, Before: before, After: after,
		})
	}

	// Which material a primitive points at. Assigning an object a different
	// material that already exists in the file changes nothing but this index,
	// which used to diff as no change whatsoever.
	if am, bm := a.materialKey(ap.Material), b.materialKey(bp.Material); am != bm {
		emit("material", am, bm)
	}

	geometry, positionChanged := diffPrimitiveGeometry(ap, bp, a, b, path)
	if geometry != nil {
		changes = append(changes, *geometry)
	}

	aPos, bPos := positionAccessor(a.doc, ap), positionAccessor(b.doc, bp)
	if aPos == nil || bPos == nil {
		return changes
	}
	if aPos.Count != bPos.Count {
		emit("vertices", fmtCount(aPos.Count), fmtCount(bPos.Count)+" ("+fmtSigned(bPos.Count-aPos.Count)+")")
	}
	aSize, aOK := primitiveExtent(aPos)
	bSize, bOK := primitiveExtent(bPos)
	if aOK && bOK && !nearEq3(aSize, bSize) {
		emit("bounds", fmtVec3(aSize), fmtVec3(bSize)+" ("+dominantAxisDelta(aSize, bSize)+")")
	}
	if positionChanged {
		if aC, ok := primitiveCentroid(a.doc, aPos); ok {
			if bC, ok := primitiveCentroid(b.doc, bPos); ok && !nearEq3(aC, bC) {
				emit("centroid", fmtVec3(aC), fmtVec3(bC)+" (moved "+fmtDistance(aC, bC)+")")
			}
		}
	}
	return changes
}

// materialKey names the material an index refers to, using the same
// disambiguated key the materials collection is diffed under. The *name* and not
// the array index is deliberate: inserting an unrelated material upstream shifts
// every index after it, which would report every primitive in the document as
// reassigned.
func (s meshSide) materialKey(idx *int) string {
	if idx == nil {
		return noMaterial
	}
	if *idx < 0 || *idx >= len(s.materialKeys) {
		return fmt.Sprintf("<dangling material %d>", *idx)
	}
	return s.materialKeys[*idx]
}

const noMaterial = "<none>"

// ── primitive geometry ────────────────────────────────────────────────────────

// leadingAttributes are the vertex semantics reported first, being the ones a
// reviewer looks for. Every other semantic the primitive carries is compared
// too, just after these; a fixed list of three would silently ignore an edit to
// TANGENT or COLOR_0, which is the same class of blind spot this compare exists
// to remove.
var leadingAttributes = []string{gltf.POSITION, gltf.NORMAL, gltf.TEXCOORD_0}

// indicesStream names the index buffer in the stream list. It is not an
// attribute semantic, so it cannot collide with one.
const indicesStream = "indices"

const (
	noStream      = "<none>"
	readable      = "readable"
	notComparable = "not comparable (external buffer or sparse accessor)"
)

// diffPrimitiveGeometry compares every vertex stream of a primitive pair. It
// reports whether POSITION specifically changed, which is what gates the one
// expensive metric (the centroid).
func diffPrimitiveGeometry(ap, bp *gltf.Primitive, a, b meshSide, path string) (*DiffChange, bool) {
	geometryPath := childPath(path, "geometry")
	var streams []DiffChange
	var aBlocked, bBlocked, positionChanged bool

	for _, stream := range primitiveStreams(ap, bp) {
		as := readStream(a.doc, ap, stream)
		bs := readStream(b.doc, bp, stream)
		changed := false
		switch {
		case !as.present && !bs.present:
			// Neither side carries this semantic; nothing to compare.
		case as.present != bs.present:
			// A stream gained or lost outright — added UVs, stripped tangents.
			changed = true
		case !as.readable || !bs.readable:
			// Bytes we cannot read must never be reported as "unchanged". The row
			// is emitted whether or not the two descriptors happen to match, so a
			// document whose geometry lives in an external .bin says so instead of
			// silently claiming the mesh is untouched.
			aBlocked = aBlocked || !as.readable
			bBlocked = bBlocked || !bs.readable
			changed = true
		default:
			changed = !equalStreams(as, bs)
		}
		if !changed {
			continue
		}
		if stream == gltf.POSITION {
			positionChanged = true
		}
		streams = append(streams, DiffChange{
			Path: childPath(geometryPath, stream), Label: stream, Kind: Modified,
			Before: as.describe(a.doc), After: bs.describe(b.doc),
		})
	}

	if len(streams) == 0 {
		return nil, false
	}
	change := &DiffChange{
		Path: geometryPath, Label: "geometry", Kind: Modified, Children: streams,
	}
	// The geometry row's own value reports comparability; its children report
	// content. It carries no value at all in the ordinary case, where the changed
	// streams below it are the whole story.
	if aBlocked || bBlocked {
		change.Before = comparability(aBlocked)
		change.After = comparability(bBlocked)
	}
	return change, positionChanged
}

func comparability(blocked bool) string {
	if blocked {
		return notComparable
	}
	return readable
}

// primitiveStreams lists the vertex streams to compare for a primitive pair:
// the union of both sides' attribute semantics — so a semantic present on only
// one side still participates — with leadingAttributes first, the remainder in
// name order for a stable report, and the index buffer last.
func primitiveStreams(ap, bp *gltf.Primitive) []string {
	present := make(map[string]bool, len(ap.Attributes)+len(bp.Attributes))
	for _, attrs := range []gltf.PrimitiveAttributes{ap.Attributes, bp.Attributes} {
		for name := range attrs {
			present[name] = true
		}
	}
	out := make([]string, 0, len(present)+1)
	for _, name := range leadingAttributes {
		if present[name] {
			out = append(out, name)
		}
	}
	rest := make([]string, 0, len(present))
	for name := range present {
		if !slices.Contains(leadingAttributes, name) {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	out = append(out, rest...)
	if ap.Indices != nil || bp.Indices != nil {
		out = append(out, indicesStream)
	}
	return out
}

// streamState is one side's view of one vertex stream. The descriptor is
// deliberately *not* built here: it hashes the bytes, and the unchanged path
// must not pay for a value nobody displays.
type streamState struct {
	present  bool
	index    int
	data     []byte
	stride   int // bytes between consecutive elements; == elem when tightly packed
	elem     int // bytes per element
	count    int
	readable bool
}

func readStream(doc *gltf.Document, p *gltf.Primitive, stream string) streamState {
	idx, ok := primitiveStreamIndex(p, stream)
	if !ok {
		return streamState{index: -1}
	}
	s := streamState{present: true, index: idx}
	if idx >= 0 && idx < len(doc.Accessors) {
		acc := doc.Accessors[idx]
		s.elem = acc.ComponentType.ByteSize() * acc.Type.Components()
		s.count = acc.Count
		s.data, s.stride, s.readable = accessorSpan(doc, acc)
	}
	return s
}

// equalStreams reports whether two vertex streams hold the same values.
//
// Tightly packed data — the overwhelmingly common case — is a single memory
// compare, which is the reason this whole compare is affordable: no hashing, no
// decoding, no allocation on the unchanged path.
//
// Interleaved data has to be compared element by element instead, because an
// interleaved accessor's byte span *contains its neighbours' bytes*: POSITION at
// stride 24 and NORMAL at the same view offset 12 overlap everywhere except the
// first and last element. Comparing those spans whole would report NORMAL as
// changed every time a position moved, which on an optimiser's output means
// every stream of every edited primitive. Comparing per element also makes the
// layout itself irrelevant, so the same vertices packed tightly in one file and
// interleaved in another compare equal, as they should.
func equalStreams(a, b streamState) bool {
	if a.elem != b.elem || a.elem <= 0 {
		return false
	}
	if a.stride == a.elem && b.stride == b.elem {
		return bytes.Equal(a.data, b.data)
	}
	if a.count != b.count {
		return false
	}
	for i := range a.count {
		av, bv := a.data[i*a.stride:], b.data[i*b.stride:]
		if !bytes.Equal(av[:a.elem], bv[:b.elem]) {
			return false
		}
	}
	return true
}

// describe renders the stream the way animation keyframe streams are rendered —
// shape plus a content digest, or an explicit <unreadable> marker — so the two
// halves of the handler report accessor data in one voice.
func (s streamState) describe(doc *gltf.Document) string {
	if !s.present {
		return noStream
	}
	return accessorLabel(doc, s.index)
}

// primitiveStreamIndex resolves a stream name to the accessor index it uses. ok
// is false when the primitive does not carry that stream; a dangling index is
// reported as present, and rendered as dangling, rather than as absent.
func primitiveStreamIndex(p *gltf.Primitive, stream string) (int, bool) {
	if stream == indicesStream {
		if p.Indices == nil {
			return 0, false
		}
		return *p.Indices, true
	}
	idx, ok := p.Attributes[stream]
	return idx, ok
}

func positionAccessor(doc *gltf.Document, p *gltf.Primitive) *gltf.Accessor {
	idx, ok := p.Attributes[gltf.POSITION]
	if !ok || idx < 0 || idx >= len(doc.Accessors) {
		return nil
	}
	return doc.Accessors[idx]
}

// primitiveExtent returns the size of a primitive's axis-aligned bounding box,
// in Blender space, for free: glTF *requires* min/max on a POSITION accessor, so
// this reads no vertex bytes at all. Files that omit them anyway report !ok and
// the bounds row is simply not emitted — the geometry row above it already says
// whether the vertices changed.
//
// Extents are unsigned, so converting them to Blender space only reorders the
// axes; the sign flip that blenderTranslation applies to a position would be
// wrong here.
func primitiveExtent(acc *gltf.Accessor) ([3]float64, bool) {
	if len(acc.Min) < 3 || len(acc.Max) < 3 {
		return [3]float64{}, false
	}
	var size [3]float64
	for i := range 3 {
		size[i] = acc.Max[i] - acc.Min[i]
	}
	return blenderScale(size), true
}

// primitiveCentroid averages a primitive's vertex positions, in Blender space —
// the cheapest single number that answers "did the shape move, or only change
// shape". It is the one metric that decodes typed vertex data, so callers run it
// only for primitives already known to have changed POSITION bytes.
//
// It decodes in place from the accessor's own span rather than materialising a
// slice of vectors, and reports !ok rather than guessing whenever the data is
// not plain little-endian float32 VEC3: an unreadable buffer, or positions
// quantized by KHR_mesh_quantization.
func primitiveCentroid(doc *gltf.Document, acc *gltf.Accessor) ([3]float64, bool) {
	if acc.Count == 0 || acc.ComponentType != gltf.ComponentFloat || acc.Type != gltf.AccessorVec3 {
		return [3]float64{}, false
	}
	data, stride, ok := accessorSpan(doc, acc)
	if !ok {
		return [3]float64{}, false
	}
	const elem = 3 * 4 // VEC3 of float32; glTF buffers are little-endian by spec.
	var sum [3]float64
	for i := range acc.Count {
		v := data[i*stride:]
		if len(v) < elem {
			return [3]float64{}, false
		}
		sum[0] += float64(math.Float32frombits(binary.LittleEndian.Uint32(v[0:4])))
		sum[1] += float64(math.Float32frombits(binary.LittleEndian.Uint32(v[4:8])))
		sum[2] += float64(math.Float32frombits(binary.LittleEndian.Uint32(v[8:12])))
	}
	n := float64(acc.Count)
	return blenderTranslation([3]float64{sum[0] / n, sum[1] / n, sum[2] / n}), true
}

// ── animations ────────────────────────────────────────────────────────────────

func diffAnimations(a, b *gltf.Document) *DiffChange {
	aMap, aOrder := animMap(a.Animations)
	bMap, bOrder := animMap(b.Animations)
	names := mergeKeyOrder(aOrder, bOrder)

	aIx, bIx := indexNodes(a.Nodes), indexNodes(b.Nodes)

	var children []DiffChange
	for _, name := range names {
		aa, inA := aMap[name]
		ba, inB := bMap[name]
		path := joinPath("animations", name)
		switch {
		case !inA:
			children = append(children, DiffChange{
				Path: path, Label: name,
				Kind: Added, After: fmt.Sprintf("%d channels", len(ba.Channels)),
			})
		case !inB:
			children = append(children, DiffChange{
				Path: path, Label: name,
				Kind: Removed, Before: fmt.Sprintf("%d channels", len(aa.Channels)),
			})
		default:
			if props := diffAnimationProps(aa, ba, a, b, aIx, bIx, path); len(props) > 0 {
				children = append(children, DiffChange{
					Path: path, Label: name, Kind: Modified, Children: props,
				})
			}
		}
	}
	if len(children) == 0 {
		return nil
	}
	return &DiffChange{
		Path: "animations", Label: "animations",
		Kind: Modified, Children: children,
	}
}

func animMap(anims []*gltf.Animation) (map[string]*gltf.Animation, []string) {
	order := uniqueKeys(anims, animName)
	m := make(map[string]*gltf.Animation, len(anims))
	for i, a := range anims {
		m[order[i]] = a
	}
	return m, order
}

func animName(a *gltf.Animation, i int) string {
	if a.Name != "" {
		return a.Name
	}
	return fmt.Sprintf("anim[%d]", i)
}

// diffAnimationProps compares two animations that exist on both sides. Channel
// counts alone miss the common case — an artist retimes or rescales the
// keyframes of an existing channel — which used to diff as no change at all, so
// each channel is also compared by target, interpolation, and the bytes of its
// sampler's input (times) and output (values) accessors.
func diffAnimationProps(aa, ba *gltf.Animation, docA, docB *gltf.Document, aIx, bIx *nodeIndex, path string) []DiffChange {
	var changes []DiffChange
	channelsPath := childPath(path, "channels")
	if len(aa.Channels) != len(ba.Channels) {
		changes = append(changes, DiffChange{
			Path: channelsPath, Label: "channels", Kind: Modified,
			Before: fmt.Sprintf("%d", len(aa.Channels)),
			After:  fmt.Sprintf("%d", len(ba.Channels)),
		})
	}

	// glTF channels are positional and carry no identity of their own, so they
	// are compared pairwise by index. Content-based channel matching belongs with
	// the identity cascade (issue #42).
	for i := range min(len(aa.Channels), len(ba.Channels)) {
		ac, bc := aa.Channels[i], ba.Channels[i]
		chPath := childPath(channelsPath, strconv.Itoa(i))
		var props []DiffChange
		emit := func(segment, before, after string) {
			if before == after {
				return
			}
			props = append(props, DiffChange{
				Path: childPath(chPath, segment), Label: segment,
				Kind: Modified, Before: before, After: after,
			})
		}
		emit("target", channelTargetLabel(aIx, ac), channelTargetLabel(bIx, bc))
		as, bs := animSampler(aa, ac.Sampler), animSampler(ba, bc.Sampler)
		emit("interpolation", samplerInterpolationLabel(as), samplerInterpolationLabel(bs))
		emit("input", samplerStreamLabel(docA, as, true), samplerStreamLabel(docB, bs, true))
		emit("output", samplerStreamLabel(docA, as, false), samplerStreamLabel(docB, bs, false))
		if len(props) > 0 {
			changes = append(changes, DiffChange{
				Path: chPath, Label: fmt.Sprintf("channel[%d]", i),
				Kind: Modified, Children: props,
			})
		}
	}
	return changes
}

func animSampler(a *gltf.Animation, i int) *gltf.AnimationSampler {
	if i < 0 || i >= len(a.Samplers) {
		return nil
	}
	return a.Samplers[i]
}

// channelTargetLabel names what a channel drives: the target node (by diff key,
// so a channel repointed at another node surfaces) and the animated property.
func channelTargetLabel(ix *nodeIndex, c *gltf.AnimationChannel) string {
	node := "<none>"
	if c.Target.Node != nil {
		if n := *c.Target.Node; n >= 0 && n < len(ix.keys) {
			node = ix.keys[n]
		} else {
			node = fmt.Sprintf("<dangling node %d>", n)
		}
	}
	return node + "." + c.Target.Path.String()
}

func samplerInterpolationLabel(s *gltf.AnimationSampler) string {
	if s == nil {
		return "<missing sampler>"
	}
	// The zero value is the glTF default, LINEAR.
	return s.Interpolation.String()
}

// samplerStreamLabel describes one keyframe stream by its shape plus a hash of
// the bytes it addresses, so editing the values shows up even though the
// keyframe count and the accessor index stay the same. A byte compare is all
// this needs; the shared geometry-compare helper is issue #43.
func samplerStreamLabel(doc *gltf.Document, s *gltf.AnimationSampler, input bool) string {
	if s == nil {
		return "<missing sampler>"
	}
	if input {
		return accessorLabel(doc, s.Input)
	}
	return accessorLabel(doc, s.Output)
}

func accessorLabel(doc *gltf.Document, idx int) string {
	if idx < 0 || idx >= len(doc.Accessors) {
		return fmt.Sprintf("<dangling accessor %d>", idx)
	}
	acc := doc.Accessors[idx]
	shape := fmt.Sprintf("count=%d type=%v component=%v", acc.Count, acc.Type, acc.ComponentType)
	data, ok := accessorBytes(doc, acc)
	if !ok {
		return shape + " hash=<unreadable>"
	}
	return shape + " hash=" + contentHash(data)
}

// accessorBytes returns the buffer bytes an accessor addresses. Sparse
// accessors, dangling indices and buffers whose data was never loaded (an
// external URI with no filesystem to read it from) report !ok; the caller then
// reports the accessor's shape without a hash rather than guessing at equality.
func accessorBytes(doc *gltf.Document, acc *gltf.Accessor) ([]byte, bool) {
	data, _, ok := accessorSpan(doc, acc)
	return data, ok
}

// accessorSpan is accessorBytes plus the stride between consecutive elements:
// the element size for tightly packed data, and BufferView.ByteStride when the
// attribute is interleaved with others in one buffer view. Callers that only
// compare bytes ignore the stride; callers that decode typed values need it to
// step over the neighbouring attributes.
//
// The span length is stride×(count−1) + element size, not count×element size:
// the naive product under-reads interleaved data by the trailing padding of the
// last element and over-reads nothing, so it can miss an edit to the final
// vertex entirely.
func accessorSpan(doc *gltf.Document, acc *gltf.Accessor) ([]byte, int, bool) {
	if acc.BufferView == nil || acc.Sparse != nil {
		return nil, 0, false
	}
	if *acc.BufferView < 0 || *acc.BufferView >= len(doc.BufferViews) {
		return nil, 0, false
	}
	bv := doc.BufferViews[*acc.BufferView]
	if bv.Buffer < 0 || bv.Buffer >= len(doc.Buffers) {
		return nil, 0, false
	}
	data := doc.Buffers[bv.Buffer].Data
	elem := acc.ComponentType.ByteSize() * acc.Type.Components()
	stride := elem
	if bv.ByteStride > 0 {
		stride = bv.ByteStride
	}
	length := 0
	if acc.Count > 0 {
		length = (acc.Count-1)*stride + elem
	}
	start := bv.ByteOffset + acc.ByteOffset
	if elem <= 0 || length < 0 || start < 0 || start+length > len(data) {
		return nil, 0, false
	}
	if bv.ByteLength > 0 && acc.ByteOffset+length > bv.ByteLength {
		return nil, 0, false
	}
	return data[start : start+length], stride, true
}

// bufferViewBytes returns a buffer view's bytes, used to hash images stored in
// the binary chunk rather than referenced by URI.
func bufferViewBytes(doc *gltf.Document, idx int) ([]byte, bool) {
	if idx < 0 || idx >= len(doc.BufferViews) {
		return nil, false
	}
	bv := doc.BufferViews[idx]
	if bv.Buffer < 0 || bv.Buffer >= len(doc.Buffers) {
		return nil, false
	}
	data := doc.Buffers[bv.Buffer].Data
	if bv.ByteOffset < 0 || bv.ByteLength < 0 || bv.ByteOffset+bv.ByteLength > len(data) {
		return nil, false
	}
	return data[bv.ByteOffset : bv.ByteOffset+bv.ByteLength], true
}

// contentHash is a short, stable digest for diff labels. FNV-1a is not a
// cryptographic hash and does not need to be: its only job is to make "these
// bytes differ" visible in a human-readable value.
func contentHash(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

// ── math / formatting helpers ─────────────────────────────────────────────────

const eps = 1e-5

func nearEq(a, b float64) bool { return math.Abs(a-b) < eps }
func nearEq3(a, b [3]float64) bool {
	return nearEq(a[0], b[0]) && nearEq(a[1], b[1]) && nearEq(a[2], b[2])
}
func nearEq4(a, b [4]float64) bool {
	return nearEq(a[0], b[0]) && nearEq(a[1], b[1]) && nearEq(a[2], b[2]) && nearEq(a[3], b[3])
}

// blenderTranslation converts glTF XYZ to Blender coordinate space.
// Blender X = glTF X,  Blender Y = −glTF Z,  Blender Z = glTF Y
func blenderTranslation(v [3]float64) [3]float64 {
	y := -v[2]
	if y == 0 {
		y = 0
	}
	return [3]float64{v[0], y, v[1]}
}

// blenderScale converts glTF XYZ scale to Blender coordinate space.
// Blender X = glTF X,  Blender Y = glTF Z,  Blender Z = glTF Y
func blenderScale(v [3]float64) [3]float64 { return [3]float64{v[0], v[2], v[1]} }

// fmtRot formats a quaternion as Euler degrees in Blender space.
func fmtRot(q [4]float64) string {
	e := quatToBlenderEulerDeg(q)
	return fmt.Sprintf("(%.2f° %.2f° %.2f°)", e[0], e[1], e[2])
}

func quatToBlenderEulerDeg(q [4]float64) [3]float64 {
	m := quatToMatrix(q)
	rb2g := [3][3]float64{{1, 0, 0}, {0, 0, 1}, {0, -1, 0}}
	rg2b := [3][3]float64{{1, 0, 0}, {0, 0, -1}, {0, 1, 0}}
	mb := mat3Mul(mat3Mul(rg2b, m), rb2g)
	e := mat3ToEulerXYZ(mb)
	const toDeg = 180.0 / math.Pi
	return [3]float64{e[0] * toDeg, e[1] * toDeg, e[2] * toDeg}
}

func quatToMatrix(q [4]float64) [3][3]float64 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	return [3][3]float64{
		{1 - 2*(y*y+z*z), 2 * (x*y - w*z), 2 * (x*z + w*y)},
		{2 * (x*y + w*z), 1 - 2*(x*x+z*z), 2 * (y*z - w*x)},
		{2 * (x*z - w*y), 2 * (y*z + w*x), 1 - 2*(x*x+y*y)},
	}
}

func mat3Mul(a, b [3][3]float64) [3][3]float64 {
	var c [3][3]float64
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				c[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return c
}

func mat3ToEulerXYZ(m [3][3]float64) [3]float64 {
	beta := math.Asin(-clampF(m[2][0], -1, 1))
	cosBeta := math.Cos(beta)
	var alpha, gamma float64
	if math.Abs(cosBeta) > 1e-6 {
		alpha = math.Atan2(m[2][1], m[2][2])
		gamma = math.Atan2(m[1][0], m[0][0])
	} else {
		alpha = math.Atan2(-m[1][2], m[1][1])
		gamma = 0
	}
	return [3]float64{alpha, beta, gamma}
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func fmtF(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 32)
}

func fmtVec3(v [3]float64) string {
	return fmt.Sprintf("[%s %s %s]", fmtF(v[0]), fmtF(v[1]), fmtF(v[2]))
}

func fmtVec4(v [4]float64) string {
	return fmt.Sprintf("[%s %s %s %s]", fmtF(v[0]), fmtF(v[1]), fmtF(v[2]), fmtF(v[3]))
}

// fmtCount groups a count in thousands. Vertex counts run to six and seven
// digits, where an ungrouped run of digits is genuinely hard to compare against
// the one next to it.
func fmtCount(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	b.Grow(len(digits) + len(digits)/3 + 1)
	for i := range len(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return sign + b.String()
}

// fmtSigned renders a delta with an explicit sign, so "+624" reads as a gain
// without the reader having to compare the two numbers themselves.
func fmtSigned(n int) string {
	if n >= 0 {
		return "+" + fmtCount(n)
	}
	return fmtCount(n)
}

// dominantAxisDelta names the axis that moved most between two vectors, e.g.
// "+0.15 Y" — the one fact a reviewer wants from a bounding-box change.
func dominantAxisDelta(before, after [3]float64) string {
	axes := [3]string{"X", "Y", "Z"}
	best := 0
	for i := 1; i < 3; i++ {
		if math.Abs(after[i]-before[i]) > math.Abs(after[best]-before[best]) {
			best = i
		}
	}
	d := after[best] - before[best]
	sign := ""
	if d >= 0 {
		sign = "+"
	}
	return sign + fmtF(d) + " " + axes[best]
}

// fmtDistance formats a distance between two points. It carries one more decimal
// than fmtF because it is the only value here that is meaningful at a scale
// finer than the dimensions around it: a centroid shift of a few millimetres on
// a metre-scale model is a real edit, and fmtF's two decimals would print it as
// "0.00".
func fmtDistance(a, b [3]float64) string {
	d := math.Sqrt((b[0]-a[0])*(b[0]-a[0]) + (b[1]-a[1])*(b[1]-a[1]) + (b[2]-a[2])*(b[2]-a[2]))
	return strconv.FormatFloat(d, 'f', 3, 64)
}

func ptrLabel(p *int, prefix string) string {
	if p == nil {
		return "<none>"
	}
	return fmt.Sprintf("%s[%d]", prefix, *p)
}
