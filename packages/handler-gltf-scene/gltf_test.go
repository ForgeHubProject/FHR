package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const sceneA = `{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"name":"Cube","translation":[5,0,-2]}]}`
const sceneB = `{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"name":"Cube","translation":[9,-9,9]}]}`

// A brand-new file has no base version; it must diff as all-added (like git
// showing a new file as all additions), not produce an empty diff.
func TestDiffAddedFileFromEmptyBase(t *testing.T) {
	h := &Handler{}
	d, err := h.Diff(nil, []byte(sceneA))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Changes) == 0 {
		t.Fatal("empty base (new file) should diff as all-added, got no changes")
	}
	js, _ := json.Marshal(d)
	if !strings.Contains(string(js), `"kind":"added"`) {
		t.Fatalf("expected added entities, got %s", js)
	}
}

// A deleted file (empty head) is the symmetric case: all-removed.
func TestDiffDeletedFileToEmptyHead(t *testing.T) {
	h := &Handler{}
	d, err := h.Diff([]byte(sceneA), nil)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	if !strings.Contains(string(js), `"kind":"removed"`) {
		t.Fatalf("empty head (deleted file) should diff as all-removed, got %s", js)
	}
}

// An unchanged file must marshal changes as [] — a Go nil slice becomes JSON
// null, which crashes consumers that do diff.changes.length.
func TestDiffUnchangedMarshalsEmptyArrayNotNull(t *testing.T) {
	h := &Handler{}
	d, err := h.Diff([]byte(sceneA), []byte(sceneA))
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	if strings.Contains(string(js), `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
	if !strings.Contains(string(js), `"changes":[]`) {
		t.Fatalf("expected empty changes array, got %s", js)
	}
}

func TestDiffModifiedNode(t *testing.T) {
	h := &Handler{}
	d, err := h.Diff([]byte(sceneA), []byte(sceneB))
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	if !strings.Contains(string(js), "translation") {
		t.Fatalf("expected a translation change, got %s", js)
	}
}
