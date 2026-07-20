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
	if !h.Match("logo.svg") || !h.Match("ICON.SVG") {
		t.Fatal("should match .svg case-insensitively")
	}
	if h.Match("logo.png") || h.Match("svg") {
		t.Fatal("should not match non-.svg paths")
	}
}

func TestIdenticalFilesEmptyDiff(t *testing.T) {
	doc := `<svg viewBox="0 0 10 10"><g id="grp"><rect width="4" height="4"/></g></svg>`
	d, js := diffJSON(t, doc, doc)
	if len(d.Changes) != 0 {
		t.Fatalf("identical files must produce no changes, got %s", js)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("empty diff must marshal as [] never null: %s", js)
	}
}

func TestBothEmptyIsEmptyDiff(t *testing.T) {
	_, js := diffJSON(t, "", "")
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("empty vs empty must marshal changes as []: %s", js)
	}
}

func TestEmptyBaseAllAdded(t *testing.T) {
	d, js := diffJSON(t, "", `<svg><rect width="4"/><circle r="2"/></svg>`)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Added || d.Changes[0].Path != "svg" {
		t.Fatalf("new file should diff as an added svg root, got %s", js)
	}
	if len(d.Changes[0].Children) != 2 {
		t.Fatalf("added subtree should list its elements, got %s", js)
	}
	if d.Changes[0].Children[0].Path != "svg/rect[0]" || d.Changes[0].Children[1].Path != "svg/circle[0]" {
		t.Fatalf("expected svg/rect[0] and svg/circle[0] added, got %s", js)
	}
}

func TestEmptyHeadAllRemoved(t *testing.T) {
	d, js := diffJSON(t, `<svg><rect width="4"/></svg>`, "")
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed || d.Changes[0].Path != "svg" {
		t.Fatalf("deleted file should diff as a removed svg root, got %s", js)
	}
	if len(d.Changes[0].Children) != 1 || d.Changes[0].Children[0].Kind != Removed {
		t.Fatalf("removed subtree should list its elements, got %s", js)
	}
}

func TestMalformedXMLErrorsCleanly(t *testing.T) {
	h := &Handler{}
	for _, bad := range []string{
		`<svg><rect></svg>`,      // mismatched close tag
		`<svg><rect width="4"/>`, // unclosed root
		`not xml at all`,         // no element
		`<svg/><svg/>`,           // multiple roots
	} {
		if _, err := h.Diff([]byte(`<svg/>`), []byte(bad)); err == nil {
			t.Fatalf("malformed head %q must error", bad)
		} else if !strings.Contains(err.Error(), "head") {
			t.Fatalf("error should say which side failed, got %v", err)
		}
		if _, err := h.Diff([]byte(bad), []byte(`<svg/>`)); err == nil {
			t.Fatalf("malformed base %q must error", bad)
		}
	}
}

func TestIDKeyedAttributeChange(t *testing.T) {
	// The id-keyed element pairs up despite being reordered; only the fill
	// change is reported.
	base := `<svg><rect id="box" fill="red" width="4"/><circle id="dot" r="2"/></svg>`
	head := `<svg><circle id="dot" r="2"/><rect id="box" fill="blue" width="4"/></svg>`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected exactly one changed element, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "svg/#box" || c.Kind != Modified {
		t.Fatalf("expected svg/#box modified, got %s", js)
	}
	if len(c.Children) != 1 || c.Children[0].Path != "@fill" || c.Children[0].Before != "red" || c.Children[0].After != "blue" {
		t.Fatalf("expected @fill red → blue, got %s", js)
	}
}

func TestAttributeAddedAndRemoved(t *testing.T) {
	base := `<svg><path id="p" d="M0 0" stroke="black"/></svg>`
	head := `<svg><path id="p" d="M0 0" opacity="0.5"/></svg>`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "svg/#p" {
		t.Fatalf("expected one change at svg/#p, got %s", js)
	}
	var added, removed bool
	for _, k := range d.Changes[0].Children {
		if k.Path == "@opacity" && k.Kind == Added && k.After == "0.5" {
			added = true
		}
		if k.Path == "@stroke" && k.Kind == Removed && k.Before == "black" {
			removed = true
		}
	}
	if !added || !removed {
		t.Fatalf("expected @opacity added and @stroke removed, got %s", js)
	}
}

func TestPositionalAddRemoveWithoutIDs(t *testing.T) {
	base := `<svg><rect width="1"/><rect width="2"/></svg>`
	head := `<svg><rect width="1"/><rect width="2"/><rect width="3"/></svg>`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Added || d.Changes[0].Path != "svg/rect[2]" {
		t.Fatalf("expected svg/rect[2] added, got %s", js)
	}

	d, js = diffJSON(t, head, base)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed || d.Changes[0].Path != "svg/rect[2]" {
		t.Fatalf("expected svg/rect[2] removed, got %s", js)
	}
}

func TestViewBoxChange(t *testing.T) {
	base := `<svg width="100" viewBox="0 0 100 100"><rect width="4"/></svg>`
	head := `<svg width="100" viewBox="0 0 200 200"><rect width="4"/></svg>`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "svg" || d.Changes[0].Kind != Modified {
		t.Fatalf("expected the svg root modified, got %s", js)
	}
	kids := d.Changes[0].Children
	if len(kids) != 1 || kids[0].Path != "@viewBox" || kids[0].Before != "0 0 100 100" || kids[0].After != "0 0 200 200" {
		t.Fatalf("expected @viewBox 0 0 100 100 → 0 0 200 200, got %s", js)
	}
}

func TestTextContentChange(t *testing.T) {
	base := `<svg><text x="1"> Hello </text></svg>`
	head := `<svg><text x="1"> Goodbye </text></svg>`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "svg/text[0]" {
		t.Fatalf("expected svg/text[0] modified, got %s", js)
	}
	kids := d.Changes[0].Children
	if len(kids) != 1 || kids[0].Path != "text" || kids[0].Before != "Hello" || kids[0].After != "Goodbye" {
		t.Fatalf("expected trimmed text Hello → Goodbye, got %s", js)
	}
}

func TestNestedStructuralPaths(t *testing.T) {
	base := `<svg><g><path d="M0 0"/></g><g><path d="M1 1"/></g></svg>`
	head := `<svg><g><path d="M0 0"/></g><g><path d="M9 9"/></g></svg>`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "svg/g[1]/path[0]" {
		t.Fatalf("expected a change at svg/g[1]/path[0], got %s", js)
	}
}

func TestChangeCap(t *testing.T) {
	// 1500 added rects must be capped at maxChanges total nodes.
	var b strings.Builder
	b.WriteString("<svg>")
	for i := 0; i < 1500; i++ {
		b.WriteString(`<rect width="1"/>`)
	}
	b.WriteString("</svg>")
	d, _ := diffJSON(t, "<svg/>", b.String())
	total := 0
	var count func(cs []DiffChange)
	count = func(cs []DiffChange) {
		total += len(cs)
		for _, c := range cs {
			count(c.Children)
		}
	}
	count(d.Changes)
	if total > maxChanges {
		t.Fatalf("diff must be capped at %d changes, got %d", maxChanges, total)
	}
	if total < maxChanges {
		t.Fatalf("cap should be reached for an oversized diff, got %d", total)
	}
}
