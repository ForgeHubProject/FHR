package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func findPath(d StructuredDiff, path string) (DiffChange, bool) {
	for _, c := range d.Changes {
		if c.Path == path {
			return c, true
		}
	}
	return DiffChange{}, false
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("config/app.yaml") || !h.Match("X.YML") || !h.Match("deploy.Yaml") {
		t.Fatal("should match .yaml and .yml case-insensitively")
	}
	if h.Match("notes.txt") || h.Match("yaml") {
		t.Fatal("should not match non-yaml paths")
	}
}

func TestKeyChange(t *testing.T) {
	base := "name: app\nreplicas: 2\n"
	head := "name: app\nreplicas: 3\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "replicas" || c.Kind != Modified || c.Before != "2" || c.After != "3" {
		t.Fatalf("expected replicas modified 2 → 3, got %+v", c)
	}
}

func TestNestedListChange(t *testing.T) {
	base := "spec:\n  ports:\n    - 80\n    - 443\n"
	head := "spec:\n  ports:\n    - 80\n    - 8443\n"
	d, js := diffJSON(t, base, head)
	c, ok := findPath(d, "spec.ports[1]")
	if !ok || c.Kind != Modified || c.Before != "443" || c.After != "8443" {
		t.Fatalf("expected spec.ports[1] modified 443 → 8443, got %s", js)
	}
}

func TestSequenceLengthChange(t *testing.T) {
	base := "items:\n  - a\n  - b\n  - c\n"
	head := "items:\n  - a\n  - b\n"
	d, js := diffJSON(t, base, head)
	c, ok := findPath(d, "items")
	if !ok || c.Kind != Modified || c.Before != 3 || c.After != 2 {
		t.Fatalf("expected items length change 3 → 2, got %s", js)
	}
	if len(d.Changes) != 1 {
		t.Fatalf("shared prefix is identical, expected only the length change, got %s", js)
	}
}

func TestKeyAddedRemoved(t *testing.T) {
	base := "a: 1\nb: 2\n"
	head := "a: 1\nc: 3\n"
	d, js := diffJSON(t, base, head)
	if c, ok := findPath(d, "b"); !ok || c.Kind != Removed || c.Before != "2" {
		t.Fatalf("expected b removed, got %s", js)
	}
	if c, ok := findPath(d, "c"); !ok || c.Kind != Added || c.After != "3" {
		t.Fatalf("expected c added, got %s", js)
	}
}

func TestAnchorAliasResolved(t *testing.T) {
	base := "defaults: &d\n  timeout: 30\nservice: *d\n"
	head := "defaults: &d\n  timeout: 60\nservice: *d\n"
	d, js := diffJSON(t, base, head)
	// The change was made once, through the anchor — it must surface resolved
	// at every path that references it.
	for _, p := range []string{"defaults.timeout", "service.timeout"} {
		c, ok := findPath(d, p)
		if !ok || c.Kind != Modified || c.Before != "30" || c.After != "60" {
			t.Fatalf("expected %s modified 30 → 60 (alias resolved), got %s", p, js)
		}
	}
}

func TestMultiDocumentStream(t *testing.T) {
	base := "name: a\n---\nname: b\n"
	head := "name: a\n---\nname: c\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "doc[2].name" || c.Kind != Modified || c.Before != "b" || c.After != "c" {
		t.Fatalf("expected doc[2].name modified b → c, got %+v", c)
	}
}

func TestNonStringKeysStringified(t *testing.T) {
	base := "1: a\ntrue: b\n"
	head := "1: c\ntrue: b\n"
	d, js := diffJSON(t, base, head)
	c, ok := findPath(d, "1")
	if !ok || c.Kind != Modified || c.Before != "a" || c.After != "c" {
		t.Fatalf("expected key 1 modified a → c, got %s", js)
	}
}

func TestMalformedYAMLErrors(t *testing.T) {
	h := &Handler{}
	_, err := h.Diff([]byte("a: [1, 2\n"), []byte("a: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("expected a clean base parse error, got %v", err)
	}
	_, err = h.Diff([]byte("a: 1\n"), []byte("b: {x: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("expected a clean head parse error, got %v", err)
	}
}

func TestIdenticalIsEmptyArray(t *testing.T) {
	same := "a: 1\nnested:\n  b: [1, 2]\n"
	d, js := diffJSON(t, same, same)
	if len(d.Changes) != 0 {
		t.Fatalf("identical files should yield no changes, got %s", js)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestEmptyBaseAllAdded(t *testing.T) {
	d, js := diffJSON(t, "", "a: 1\nb: 2\n")
	if len(d.Changes) != 2 {
		t.Fatalf("expected both top-level keys added, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("new file should diff as additions, got %+v", c)
		}
	}
	if strings.Contains(js, `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestEmptyHeadAllRemoved(t *testing.T) {
	d, js := diffJSON(t, "a: 1\nb: 2\n", "")
	if len(d.Changes) != 2 {
		t.Fatalf("expected both top-level keys removed, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Removed {
			t.Fatalf("deleted file should diff as removals, got %+v", c)
		}
	}
}

func TestTypeMismatchIsLeafModified(t *testing.T) {
	base := "a:\n  b: 1\n"
	head := "a: scalar\n"
	d, js := diffJSON(t, base, head)
	c, ok := findPath(d, "a")
	if !ok || c.Kind != Modified {
		t.Fatalf("expected a modified (map → scalar), got %s", js)
	}
}

func TestLongValuesTruncated(t *testing.T) {
	long := strings.Repeat("x", 500)
	d, _ := diffJSON(t, "a: short\n", "a: "+long+"\n")
	c, ok := findPath(d, "a")
	if !ok {
		t.Fatal("expected a change at a")
	}
	after, _ := c.After.(string)
	if len([]rune(after)) > maxValueLen+1 {
		t.Fatalf("after value should be truncated to ~%d chars, got %d", maxValueLen, len(after))
	}
	if !strings.HasSuffix(after, "…") {
		t.Fatalf("truncated value should end with ellipsis, got %q", after)
	}
}
