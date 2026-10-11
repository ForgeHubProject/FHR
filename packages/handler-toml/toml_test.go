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

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("config/settings.toml") || !h.Match("X.TOML") {
		t.Fatal("should match .toml case-insensitively")
	}
	if h.Match("notes.txt") || h.Match("Cargo.lock") {
		t.Fatal("should not match non-toml files")
	}
}

func TestUnchangedIsEmptyArray(t *testing.T) {
	same := "title = \"demo\"\n\n[servers.alpha]\nport = 8080\n"
	_, js := diffJSON(t, same, same)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("identical files should yield an empty changes array, got %s", js)
	}
}

func TestValueChange(t *testing.T) {
	base := "[servers.alpha]\nport = 8080\nhost = \"a.example\"\n"
	head := "[servers.alpha]\nport = 9090\nhost = \"a.example\"\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %d: %s", len(d.Changes), js)
	}
	c := d.Changes[0]
	if c.Path != "servers.alpha.port" || c.Kind != Modified {
		t.Fatalf("expected servers.alpha.port modified, got %+v", c)
	}
	if c.Before != "8080" || c.After != "9090" {
		t.Fatalf("expected 8080 → 9090, got %v → %v", c.Before, c.After)
	}
}

func TestTableAdded(t *testing.T) {
	base := "[servers.alpha]\nport = 8080\n"
	head := "[servers.alpha]\nport = 8080\n\n[servers.beta]\nport = 9090\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %d: %s", len(d.Changes), js)
	}
	c := d.Changes[0]
	if c.Path != "servers.beta" || c.Kind != Added || c.Label != "beta" {
		t.Fatalf("expected servers.beta added, got %+v", c)
	}
}

func TestArrayOfTablesEntryAdded(t *testing.T) {
	base := "[[fruit]]\nname = \"apple\"\n"
	head := "[[fruit]]\nname = \"apple\"\n\n[[fruit]]\nname = \"pear\"\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %d: %s", len(d.Changes), js)
	}
	c := d.Changes[0]
	if c.Path != "fruit.1" || c.Kind != Added {
		t.Fatalf("expected fruit.1 added, got %+v", c)
	}
	if after, _ := c.After.(string); !strings.Contains(after, "pear") {
		t.Fatalf("added entry should carry its value, got %v", c.After)
	}
}

func TestInlineTableChange(t *testing.T) {
	base := "point = { x = 1, y = 2 }\n"
	head := "point = { x = 1, y = 3 }\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %d: %s", len(d.Changes), js)
	}
	c := d.Changes[0]
	if c.Path != "point.y" || c.Kind != Modified || c.Before != "2" || c.After != "3" {
		t.Fatalf("expected point.y 2 → 3, got %+v", c)
	}
}

func TestEmptyBaseAllAdded(t *testing.T) {
	d, js := diffJSON(t, "", "title = \"demo\"\n\n[owner]\nname = \"amy\"\n")
	if len(d.Changes) == 0 {
		t.Fatalf("new file should diff as additions, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("expected only additions, got %+v", c)
		}
	}
	if strings.Contains(js, `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestEmptyHeadAllRemoved(t *testing.T) {
	d, _ := diffJSON(t, "title = \"demo\"\n\n[owner]\nname = \"amy\"\n", "")
	if len(d.Changes) == 0 {
		t.Fatal("deleted file should diff as removals")
	}
	for _, c := range d.Changes {
		if c.Kind != Removed {
			t.Fatalf("expected only removals, got %+v", c)
		}
	}
}

func TestMalformedTOMLErrors(t *testing.T) {
	h := &Handler{}
	if _, err := h.Diff([]byte("this is = not [ valid toml"), []byte("a = 1\n")); err == nil {
		t.Fatal("malformed base should error")
	} else if !strings.Contains(err.Error(), "base") {
		t.Fatalf("error should name the failing side, got %v", err)
	}
	if _, err := h.Diff([]byte("a = 1\n"), []byte("= nope")); err == nil {
		t.Fatal("malformed head should error")
	} else if !strings.Contains(err.Error(), "head") {
		t.Fatalf("error should name the failing side, got %v", err)
	}
}

func TestLongValueTruncated(t *testing.T) {
	long := strings.Repeat("x", 400)
	d, _ := diffJSON(t, "s = \"a\"\n", "s = \""+long+"\"\n")
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %+v", d.Changes)
	}
	after, _ := d.Changes[0].After.(string)
	if len([]rune(after)) > maxValueLen+1 {
		t.Fatalf("after value should be truncated to ~%d runes, got %d", maxValueLen, len([]rune(after)))
	}
	if !strings.HasSuffix(after, "…") {
		t.Fatalf("truncated value should end with an ellipsis, got %q", after)
	}
}

func TestMergeNotSupported(t *testing.T) {
	h := &Handler{}
	if _, _, err := h.Merge(nil, nil, nil); err == nil {
		t.Fatal("merge should report not supported")
	}
}
