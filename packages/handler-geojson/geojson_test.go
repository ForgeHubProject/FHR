package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// fc builds a FeatureCollection from raw feature JSON fragments.
func fc(features ...string) string {
	return `{"type":"FeatureCollection","features":[` + strings.Join(features, ",") + `]}`
}

func point(id, name string, x, y float64) string {
	var b strings.Builder
	b.WriteString(`{"type":"Feature",`)
	if id != "" {
		b.WriteString(`"id":"` + id + `",`)
	}
	props := "{}"
	if name != "" {
		props = `{"name":"` + name + `"}`
	}
	coords, _ := json.Marshal([]float64{x, y})
	b.WriteString(`"properties":` + props + `,"geometry":{"type":"Point","coordinates":` + string(coords) + `}}`)
	return b.String()
}

func diffJSON(t *testing.T, base, head string) (StructuredDiff, string) {
	t.Helper()
	h := &Handler{}
	d, err := h.Diff([]byte(base), []byte(head))
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	return d, string(js)
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("map.geojson") || !h.Match("X.GEOJSON") {
		t.Fatal("should match .geojson case-insensitively")
	}
	if h.Match("map.json") || h.Match("geojson") {
		t.Fatal("should not match .json or extensionless paths")
	}
}

func TestIdenticalProducesEmptyList(t *testing.T) {
	doc := fc(point("42", "HQ", 1.5, 2.5), point("", "depot", 3, 4))
	d, js := diffJSON(t, doc, doc)
	if len(d.Changes) != 0 {
		t.Fatalf("identical documents must produce no changes, got %s", js)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("empty diff must marshal as [] not null: %s", js)
	}
}

func TestFeatureAddedByID(t *testing.T) {
	base := fc(point("1", "", 0, 0))
	head := fc(point("1", "", 0, 0), point("42", "", 9, 9))
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Added || d.Changes[0].Path != "features/id:42" {
		t.Fatalf("expected features/id:42 added, got %s", js)
	}
}

func TestEmptyBaseAllAdded(t *testing.T) {
	d, js := diffJSON(t, "", fc(point("1", "", 0, 0), point("2", "", 1, 1)))
	if len(d.Changes) != 2 {
		t.Fatalf("empty base should diff as all additions, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("expected only added changes, got %s", js)
		}
	}
}

func TestEmptyHeadAllRemoved(t *testing.T) {
	d, js := diffJSON(t, fc(point("1", "", 0, 0)), "")
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed || d.Changes[0].Path != "features/id:1" {
		t.Fatalf("empty head should diff as all removals, got %s", js)
	}
}

func TestGeometryTypeChange(t *testing.T) {
	base := fc(`{"type":"Feature","id":"a","properties":{},"geometry":{"type":"Point","coordinates":[0,0]}}`)
	head := fc(`{"type":"Feature","id":"a","properties":{},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}`)
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Modified || d.Changes[0].Path != "features/id:a" {
		t.Fatalf("expected features/id:a modified, got %s", js)
	}
	var typed, counted bool
	for _, k := range d.Changes[0].Children {
		if k.Path == "features/id:a/geometry" && k.Before == "Point" && k.After == "Polygon" {
			typed = true
		}
		if k.Path == "features/id:a/geometry/coordinates" && k.Label == "coordinate positions" {
			counted = true
		}
	}
	if !typed {
		t.Fatalf("expected a geometry type change Point → Polygon, got %s", js)
	}
	if !counted {
		t.Fatalf("expected a coordinate position count change (1 → 4), got %s", js)
	}
}

func TestPropertyValueChange(t *testing.T) {
	base := fc(`{"type":"Feature","id":"a","properties":{"status":"open","depth":{"m":10}},"geometry":null}`)
	head := fc(`{"type":"Feature","id":"a","properties":{"status":"closed","depth":{"m":12}},"geometry":null}`)
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Modified {
		t.Fatalf("expected one modified feature, got %s", js)
	}
	var status, nested bool
	for _, k := range d.Changes[0].Children {
		if k.Path == "features/id:a/properties/status" && k.Before == "open" && k.After == "closed" {
			status = true
		}
		if k.Path == "features/id:a/properties/depth/m" {
			nested = true
		}
	}
	if !status || !nested {
		t.Fatalf("expected status and nested depth/m property changes, got %s", js)
	}
}

func TestNameFallbackIdentity(t *testing.T) {
	// No ids: identity falls back to properties.name, so a moved feature still
	// matches and only its coordinate change is reported.
	base := fc(point("", "depot", 0, 0), point("", "hq", 5, 5))
	head := fc(point("", "hq", 5, 5), point("", "depot", 0, 1))
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "features/name:depot" {
		t.Fatalf("expected only features/name:depot modified via name identity, got %s", js)
	}
}

func TestBareGeometryInput(t *testing.T) {
	base := `{"type":"Point","coordinates":[0,0]}`
	head := `{"type":"Point","coordinates":[0,1]}`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "features/index:0" {
		t.Fatalf("bare geometry should wrap as one positional feature, got %s", js)
	}
	kids := d.Changes[0].Children
	if len(kids) != 1 || kids[0].Path != "features/index:0/geometry/coordinates" {
		t.Fatalf("expected a coordinates value change, got %s", js)
	}
}

func TestMalformedJSONErrors(t *testing.T) {
	h := &Handler{}
	if _, err := h.Diff([]byte(`{"type":"FeatureCollection",`), []byte(``)); err == nil {
		t.Fatal("malformed base JSON must error")
	} else if !strings.Contains(err.Error(), "base") {
		t.Fatalf("error should say which side failed, got %v", err)
	}
	if _, err := h.Diff([]byte(``), []byte(`not json`)); err == nil {
		t.Fatal("malformed head JSON must error")
	}
	if _, err := h.Diff([]byte(`{"type":"Sculpture"}`), []byte(``)); err == nil {
		t.Fatal("unrecognized GeoJSON type must error")
	}
}
