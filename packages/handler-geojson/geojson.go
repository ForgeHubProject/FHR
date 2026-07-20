// Package main is a format-aware handler for GeoJSON (.geojson) files
// (RFC 7946). GeoJSON is JSON, so a text diff drowns the real change in
// coordinate churn and re-serialization noise. This handler diffs at the
// feature level: features added/removed/modified, geometry type changes,
// coordinate position count changes, and a structural properties diff.
//
// Feature identity, in fallback order:
//  1. the feature's top-level "id" (RFC 7946 §3.2) — key "id:<value>"
//  2. properties.name — key "name:<value>"
//  3. position in the collection — key "index:<i>"
//
// Inputs may be a FeatureCollection, a single Feature, or a bare geometry
// (wrapped as one feature). Geometry-aware distance metrics and a map-preview
// renderer are planned follow-ups — issue #29.
package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Handler is the GeoJSON format handler.
type Handler struct{}

// Match returns true for .geojson files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".geojson")
}

// geometry captures a GeoJSON geometry object. Coordinates stay raw — their
// nesting depth varies by type, so they're decoded generically on demand.
type geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates,omitempty"`
	Geometries  []geometry      `json:"geometries,omitempty"` // GeometryCollection
}

// feature captures the fields we diff. ID stays raw because RFC 7946 allows
// it to be a string or a number.
type feature struct {
	ID         json.RawMessage `json:"id,omitempty"`
	Geometry   *geometry       `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

// parseGeoJSON accepts a FeatureCollection, a single Feature, or a bare
// geometry (wrapped as one feature). An empty blob yields no features (not an
// error) — the added/deleted-file case.
func parseGeoJSON(blob Blob) ([]feature, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(blob, &probe); err != nil {
		return nil, fmt.Errorf("parsing GeoJSON: %w", err)
	}
	switch probe.Type {
	case "FeatureCollection":
		var fc struct {
			Features []feature `json:"features"`
		}
		if err := json.Unmarshal(blob, &fc); err != nil {
			return nil, fmt.Errorf("parsing FeatureCollection: %w", err)
		}
		return fc.Features, nil
	case "Feature":
		var f feature
		if err := json.Unmarshal(blob, &f); err != nil {
			return nil, fmt.Errorf("parsing Feature: %w", err)
		}
		return []feature{f}, nil
	case "Point", "MultiPoint", "LineString", "MultiLineString",
		"Polygon", "MultiPolygon", "GeometryCollection":
		var g geometry
		if err := json.Unmarshal(blob, &g); err != nil {
			return nil, fmt.Errorf("parsing geometry: %w", err)
		}
		return []feature{{Geometry: &g}}, nil
	default:
		return nil, fmt.Errorf("unrecognized GeoJSON type %q", probe.Type)
	}
}

// featureKey returns the identity key for feature i, in fallback order:
// top-level "id" ("id:<value>"), then properties.name ("name:<value>"), then
// position ("index:<i>").
func featureKey(f feature, i int) string {
	if len(f.ID) > 0 && string(f.ID) != "null" {
		var s string
		if err := json.Unmarshal(f.ID, &s); err == nil {
			return "id:" + s
		}
		return "id:" + string(f.ID) // numeric ids keep their JSON text
	}
	if name, ok := f.Properties["name"].(string); ok && name != "" {
		return "name:" + name
	}
	return fmt.Sprintf("index:%d", i)
}

// featureKeys assigns each feature its identity key. A duplicated key falls
// back to the positional key so alignment stays one-to-one.
func featureKeys(fs []feature) []string {
	keys := make([]string, len(fs))
	seen := make(map[string]bool, len(fs))
	for i, f := range fs {
		k := featureKey(f, i)
		if seen[k] {
			k = fmt.Sprintf("index:%d", i)
		}
		keys[i] = k
		seen[k] = true
	}
	return keys
}

func geomType(g *geometry) string {
	if g == nil {
		return "no geometry"
	}
	return g.Type
}

// decodeCoords generically decodes a raw coordinates value. The document
// already parsed, so failures only occur for absent coordinates.
func decodeCoords(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// countPositions counts coordinate positions in a decoded coordinates value.
// A position is a leaf array whose first element is a number ([lon, lat, ...]),
// so a Point counts 1, a LineString its vertices, a Polygon its ring points.
func countPositions(v any) int {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return 0
	}
	if _, isNum := arr[0].(float64); isNum {
		return 1
	}
	n := 0
	for _, e := range arr {
		n += countPositions(e)
	}
	return n
}

// positionCount recursively counts a geometry's coordinate positions,
// descending into GeometryCollection members.
func positionCount(g *geometry) int {
	if g == nil {
		return 0
	}
	n := countPositions(decodeCoords(g.Coordinates))
	for i := range g.Geometries {
		n += positionCount(&g.Geometries[i])
	}
	return n
}

// coordsValue normalizes a geometry's coordinate data (including nested
// GeometryCollection members) into decoded form for equality comparison.
func coordsValue(g *geometry) any {
	if g == nil {
		return nil
	}
	v := []any{decodeCoords(g.Coordinates)}
	for i := range g.Geometries {
		m := &g.Geometries[i]
		v = append(v, m.Type, coordsValue(m))
	}
	return v
}

// Diff produces a feature-level semantic diff of two GeoJSON blobs. An empty
// blob on either side is the added/deleted-file case (all features added /
// removed). Paths look like features/id:42/geometry.
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	a, err := parseGeoJSON(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := parseGeoJSON(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	changes := []DiffChange{} // non-nil so an empty diff marshals as [] not null
	aKeys, bKeys := featureKeys(a), featureKeys(b)
	aIndex := make(map[string]int, len(aKeys))
	for i, k := range aKeys {
		aIndex[k] = i
	}
	bIndex := make(map[string]int, len(bKeys))
	for i, k := range bKeys {
		bIndex[k] = i
	}

	// Base order: removed and modified features.
	for i, k := range aKeys {
		path := "features/" + k
		j, ok := bIndex[k]
		if !ok {
			changes = append(changes, DiffChange{Path: path, Kind: Removed, Label: k + " (" + geomType(a[i].Geometry) + ")", Before: geomType(a[i].Geometry)})
			continue
		}
		if kids := diffFeature(a[i], b[j], path); len(kids) > 0 {
			changes = append(changes, DiffChange{Path: path, Kind: Modified, Label: k, Children: kids})
		}
	}
	// Head order: added features.
	for j, k := range bKeys {
		if _, ok := aIndex[k]; !ok {
			changes = append(changes, DiffChange{Path: "features/" + k, Kind: Added, Label: k + " (" + geomType(b[j].Geometry) + ")", After: geomType(b[j].Geometry)})
		}
	}

	return StructuredDiff{Version: "1.0", Format: "geojson", Changes: changes}, nil
}

// diffFeature compares one matched feature pair: geometry, then properties.
func diffFeature(a, b feature, path string) []DiffChange {
	kids := diffGeometry(a.Geometry, b.Geometry, path+"/geometry")
	kids = append(kids, diffProperties(a.Properties, b.Properties, path+"/properties")...)
	return kids
}

// diffGeometry reports geometry presence, type, and coordinate changes.
// Coordinate changes surface as a position-count change when counts differ,
// or a value-only change (same count, moved positions) otherwise.
func diffGeometry(a, b *geometry, path string) []DiffChange {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		return []DiffChange{{Path: path, Kind: Added, Label: "geometry", After: b.Type}}
	case b == nil:
		return []DiffChange{{Path: path, Kind: Removed, Label: "geometry", Before: a.Type}}
	}
	var ch []DiffChange
	if a.Type != b.Type {
		ch = append(ch, DiffChange{Path: path, Kind: Modified, Label: "geometry type", Before: a.Type, After: b.Type})
	}
	an, bn := positionCount(a), positionCount(b)
	switch {
	case an != bn:
		ch = append(ch, DiffChange{Path: path + "/coordinates", Kind: Modified, Label: "coordinate positions", Before: an, After: bn})
	case !reflect.DeepEqual(coordsValue(a), coordsValue(b)):
		ch = append(ch, DiffChange{Path: path + "/coordinates", Kind: Modified, Label: fmt.Sprintf("coordinates (%d positions)", an)})
	}
	return ch
}

// diffProperties structurally diffs two properties objects with a
// self-contained walk: nested objects recurse, any other changed value is
// reported with its before/after. Keys are visited in sorted order so the
// output is deterministic.
func diffProperties(a, b map[string]any, path string) []DiffChange {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	var ch []DiffChange
	for _, k := range unionKeys(a, b) {
		av, aok := a[k]
		bv, bok := b[k]
		p := path + "/" + k
		switch {
		case !aok:
			ch = append(ch, DiffChange{Path: p, Kind: Added, Label: k, After: bv})
		case !bok:
			ch = append(ch, DiffChange{Path: p, Kind: Removed, Label: k, Before: av})
		default:
			am, aIsMap := av.(map[string]any)
			bm, bIsMap := bv.(map[string]any)
			if aIsMap && bIsMap {
				ch = append(ch, diffProperties(am, bm, p)...)
			} else if !reflect.DeepEqual(av, bv) {
				ch = append(ch, DiffChange{Path: p, Kind: Modified, Label: k, Before: av, After: bv})
			}
		}
	}
	return ch
}

// unionKeys returns the sorted union of both maps' keys.
func unionKeys(a, b map[string]any) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Merge is not yet supported for GeoJSON (v0 is diff-only). Feature-level
// 3-way merge is a planned follow-up (issue #29).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for geojson")
}
