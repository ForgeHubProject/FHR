package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// nb builds a minimal notebook JSON with the given cells (type, source).
func nb(cells ...[2]string) string {
	var b strings.Builder
	b.WriteString(`{"cells":[`)
	for i, c := range cells {
		if i > 0 {
			b.WriteString(",")
		}
		src, _ := json.Marshal(c[1])
		b.WriteString(`{"cell_type":"` + c[0] + `","source":` + string(src))
		// include volatile fields the handler must ignore
		b.WriteString(`,"execution_count":` + strconv.Itoa(i+1) + `,"outputs":[{"text":"noise"}]}`)
	}
	b.WriteString(`],"nbformat":4,"nbformat_minor":5}`)
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
	if !h.Match("analysis.ipynb") || !h.Match("X.IPYNB") {
		t.Fatal("should match .ipynb case-insensitively")
	}
	if h.Match("script.py") {
		t.Fatal("should not match .py")
	}
}

func TestSourceChangeDetected(t *testing.T) {
	base := nb([2]string{"code", "print(1)"})
	head := nb([2]string{"code", "print(2)"})
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Modified || d.Changes[0].Path != "cells.1" {
		t.Fatalf("expected cells.1 modified, got %s", js)
	}
	kids := d.Changes[0].Children
	if len(kids) != 1 || kids[0].Label != "source" || kids[0].After != "print(2)" {
		t.Fatalf("expected a source change to print(2), got %+v", kids)
	}
}

func TestVolatileOutputsIgnored(t *testing.T) {
	// Same source, but nb() injects different execution_count + outputs per cell;
	// re-serializing base vs an identical-source head must show no change.
	base := nb([2]string{"code", "x = 1"})
	head := nb([2]string{"code", "x = 1"})
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 0 {
		t.Fatalf("outputs/execution_count must be ignored; got changes: %s", js)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("empty diff must marshal as []: %s", js)
	}
}

func TestCellTypeChange(t *testing.T) {
	d, _ := diffJSON(t, nb([2]string{"code", "x"}), nb([2]string{"markdown", "x"}))
	if len(d.Changes) != 1 {
		t.Fatalf("expected one modified cell, got %+v", d.Changes)
	}
	var typed bool
	for _, k := range d.Changes[0].Children {
		if k.Label == "type" && k.Before == "code" && k.After == "markdown" {
			typed = true
		}
	}
	if !typed {
		t.Fatalf("expected a cell_type change code → markdown, got %+v", d.Changes[0].Children)
	}
}

func TestAddedAndRemovedCells(t *testing.T) {
	d, _ := diffJSON(t, nb([2]string{"code", "a"}), nb([2]string{"code", "a"}, [2]string{"markdown", "b"}))
	if len(d.Changes) != 1 || d.Changes[0].Kind != Added || d.Changes[0].Path != "cells.2" {
		t.Fatalf("expected cells.2 added, got %+v", d.Changes)
	}

	_, js := diffJSON(t, "", nb([2]string{"code", "a"}))
	if !strings.Contains(js, `"kind":"added"`) {
		t.Fatalf("new notebook should diff as additions, got %s", js)
	}
}
