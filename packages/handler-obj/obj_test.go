package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A small two-group model: a cube-ish "body" and a "wheel", sharing a header.
const baseOBJ = `# sample
mtllib car.mtl
v 0 0 0
v 1 0 0
v 1 1 0
v 0 1 0
vn 0 0 1
vt 0 0
o body
usemtl red
f 1/1/1 2/1/1 3/1/1
f 1 3 4
o wheel
usemtl rubber
f 1 2 4
`

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

func findChange(d StructuredDiff, path string) (DiffChange, bool) {
	for _, c := range d.Changes {
		if c.Path == path {
			return c, true
		}
	}
	return DiffChange{}, false
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("model.obj") || !h.Match("SCENE.OBJ") {
		t.Fatal("should match .obj case-insensitively")
	}
	if h.Match("model.mtl") || h.Match("model.stl") {
		t.Fatal("should match only .obj (mtl pairing is deferred — issue #15)")
	}
}

func TestIdenticalFilesEmptyDiff(t *testing.T) {
	d, js := diffJSON(t, baseOBJ, baseOBJ)
	if len(d.Changes) != 0 {
		t.Fatalf("identical files must produce no changes, got %s", js)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("empty diff must marshal as [] not null: %s", js)
	}
}

func TestEmptyBaseAllAdded(t *testing.T) {
	d, js := diffJSON(t, "", baseOBJ)
	if len(d.Changes) == 0 {
		t.Fatalf("new file should diff as additions, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("expected only added changes for an empty base, got %s", js)
		}
	}
	if _, ok := findChange(d, "objects/body"); !ok {
		t.Fatalf("expected objects/body added, got %s", js)
	}
}

func TestEmptyHeadAllRemoved(t *testing.T) {
	d, js := diffJSON(t, baseOBJ, "")
	if len(d.Changes) == 0 {
		t.Fatalf("deleted file should diff as removals, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Removed {
			t.Fatalf("expected only removed changes for an empty head, got %s", js)
		}
	}
}

func TestMalformedFaceError(t *testing.T) {
	h := &Handler{}
	bad := "v 0 0 0\nv 1 0 0\nv 1 1 0\nf 1 nope 3\n"
	if _, err := h.Diff([]byte(bad), []byte(baseOBJ)); err == nil || !strings.Contains(err.Error(), "base:") {
		t.Fatalf("malformed base must produce a clean error, got %v", err)
	}
	if _, err := h.Diff([]byte(baseOBJ), []byte("f 1 2\n")); err == nil || !strings.Contains(err.Error(), "head:") {
		t.Fatalf("a face with fewer than 3 vertices must error, got %v", err)
	}
	if _, err := h.Diff([]byte("\x00\x01binary"), []byte(baseOBJ)); err == nil {
		t.Fatal("binary garbage must produce a clean error")
	}
}

func TestGroupAdded(t *testing.T) {
	head := baseOBJ + "o spoiler\nf 2 3 4\n"
	d, js := diffJSON(t, baseOBJ, head)
	c, ok := findChange(d, "objects/spoiler")
	if !ok || c.Kind != Added {
		t.Fatalf("expected objects/spoiler added, got %s", js)
	}
	if len(d.Changes) != 1 {
		t.Fatalf("only the new group should change, got %s", js)
	}
}

func TestFaceCountChangeWithinGroup(t *testing.T) {
	head := strings.Replace(baseOBJ, "f 1 3 4\n", "f 1 3 4\nf 2 3 4\n", 1)
	d, js := diffJSON(t, baseOBJ, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected exactly one change, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "objects/body" || c.Kind != Modified {
		t.Fatalf("expected objects/body modified, got %s", js)
	}
	var counted bool
	for _, k := range c.Children {
		if k.Path == "faces" && k.Before == 2 && k.After == 3 {
			counted = true
		}
	}
	if !counted {
		t.Fatalf("expected a face count change 2 → 3, got %s", js)
	}
}

func TestUsemtlChange(t *testing.T) {
	head := strings.Replace(baseOBJ, "usemtl red\n", "usemtl blue\n", 1)
	d, js := diffJSON(t, baseOBJ, head)
	c, ok := findChange(d, "objects/body")
	if !ok || c.Kind != Modified {
		t.Fatalf("expected objects/body modified, got %s", js)
	}
	var mat bool
	for _, k := range c.Children {
		if k.Path == "materials" && k.Before == "red" && k.After == "blue" {
			mat = true
		}
	}
	if !mat {
		t.Fatalf("expected a materials change red → blue, got %s", js)
	}
}

func TestMtllibChange(t *testing.T) {
	head := strings.Replace(baseOBJ, "mtllib car.mtl\n", "mtllib van.mtl\n", 1)
	d, js := diffJSON(t, baseOBJ, head)
	c, ok := findChange(d, "materials/libraries")
	if !ok || c.Kind != Modified || c.Before != "car.mtl" || c.After != "van.mtl" {
		t.Fatalf("expected materials/libraries car.mtl → van.mtl, got %s", js)
	}
}

func TestNoGroupLinesLandInDefault(t *testing.T) {
	flat := "v 0 0 0\nv 1 0 0\nv 1 1 0\nf 1 2 3\n"
	d, js := diffJSON(t, "", flat)
	c, ok := findChange(d, "objects/(default)")
	if !ok || c.Kind != Added {
		t.Fatalf("a file with no o/g lines must land in %q, got %s", defaultGroup, js)
	}

	// A face-count change inside the implicit group is reported on it too.
	d, js = diffJSON(t, flat, flat+"f 3 2 1\n")
	if c, ok = findChange(d, "objects/(default)"); !ok || c.Kind != Modified {
		t.Fatalf("expected objects/(default) modified, got %s", js)
	}
}

func TestMergeNotSupported(t *testing.T) {
	h := &Handler{}
	if _, _, err := h.Merge(nil, nil, nil); err == nil {
		t.Fatal("merge should report not supported")
	}
}
