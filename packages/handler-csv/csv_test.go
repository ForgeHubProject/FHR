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
	if !h.Match("data/report.csv") || !h.Match("X.CSV") {
		t.Fatal("should match .csv case-insensitively")
	}
	if h.Match("notes.txt") {
		t.Fatal("should not match .txt")
	}
}

func TestCellChange(t *testing.T) {
	base := "id,name,qty\n1,Bolt,10\n2,Nut,20\n"
	head := "id,name,qty\n1,Bolt,12\n2,Nut,20\n"
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one changed row, got %d: %s", len(d.Changes), js)
	}
	rowc := d.Changes[0]
	if rowc.Kind != Modified || rowc.Path != "rows.2" {
		t.Fatalf("expected rows.2 modified, got %+v", rowc)
	}
	if len(rowc.Children) != 1 || rowc.Children[0].Label != "qty" {
		t.Fatalf("expected a 'qty' cell change, got %+v", rowc.Children)
	}
	if rowc.Children[0].Before != "10" || rowc.Children[0].After != "12" {
		t.Fatalf("expected 10 → 12, got %v → %v", rowc.Children[0].Before, rowc.Children[0].After)
	}
}

func TestRowAddedRemoved(t *testing.T) {
	base := "id,name\n1,A\n2,B\n"
	head := "id,name\n1,A\n" // row 2 removed
	d, _ := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed || d.Changes[0].Path != "rows.3" {
		t.Fatalf("expected rows.3 removed, got %+v", d.Changes)
	}

	d2, _ := diffJSON(t, "id,name\n1,A\n", "id,name\n1,A\n2,B\n")
	if len(d2.Changes) != 1 || d2.Changes[0].Kind != Added {
		t.Fatalf("expected an added row, got %+v", d2.Changes)
	}
}

func TestColumnChange(t *testing.T) {
	base := "id,name\n1,A\n"
	head := "id,label\n1,A\n" // renamed column 2
	d, _ := diffJSON(t, base, head)
	var found bool
	for _, c := range d.Changes {
		if c.Path == "columns.name" && c.Kind == Modified && c.After == "label" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected column rename name → label, got %+v", d.Changes)
	}
}

func TestAddedFileAllRowsAdded(t *testing.T) {
	_, js := diffJSON(t, "", "id,name\n1,A\n2,B\n")
	if !strings.Contains(js, `"kind":"added"`) {
		t.Fatalf("new file should diff as additions, got %s", js)
	}
	if strings.Contains(js, `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestUnchangedIsEmptyArray(t *testing.T) {
	same := "id,name\n1,A\n"
	_, js := diffJSON(t, same, same)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("identical files should yield an empty changes array, got %s", js)
	}
}
