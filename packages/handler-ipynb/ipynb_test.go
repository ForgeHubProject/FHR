package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// c is one test cell: type, id (may be empty) and source; outputs optional.
type c struct {
	kind, id, src string
	outputs       []any
}

// nb builds a notebook JSON from cells. Every code cell gets an execution
// count that differs per call site, which the handler must ignore.
func nb(execBase int, cells ...c) string {
	var list []map[string]any
	for i, x := range cells {
		m := map[string]any{"cell_type": x.kind, "source": x.src, "metadata": map[string]any{"collapsed": i%2 == 0}}
		if x.id != "" {
			m["id"] = x.id
		}
		if x.kind == "code" {
			m["execution_count"] = execBase + i
			out := x.outputs
			if out == nil {
				out = []any{}
			}
			m["outputs"] = out
		}
		list = append(list, m)
	}
	js, _ := json.Marshal(map[string]any{"cells": list, "nbformat": 4, "nbformat_minor": 5,
		"metadata": map[string]any{"kernelspec": map[string]any{"name": "python3", "display_name": "Python 3"}}})
	return string(js)
}

func stream(s string) map[string]any {
	return map[string]any{"output_type": "stream", "name": "stdout", "text": s}
}

func diffOf(t *testing.T, base, head string) (StructuredDiff, string) {
	t.Helper()
	d, err := (&Handler{}).Diff([]byte(base), []byte(head))
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	return d, string(js)
}

func child(ch DiffChange, path string) *DiffChange {
	for i := range ch.Children {
		if ch.Children[i].Path == path {
			return &ch.Children[i]
		}
	}
	return nil
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("analysis.ipynb") || !h.Match("X.IPYNB") || h.Match("script.py") {
		t.Fatal("should match .ipynb case-insensitively and nothing else")
	}
}

func TestSourceEditIsOneModifiedCellWithBothTexts(t *testing.T) {
	d, js := diffOf(t, nb(1, c{"code", "a", "x = 1\nprint(x)", nil}), nb(1, c{"code", "a", "x = 2\nprint(x)", nil}))
	if len(d.Changes) != 1 || d.Changes[0].Kind != Modified || d.Changes[0].Path != "cells/a" || d.Changes[0].Label != "code cell 1" {
		t.Fatalf("expected cells/a modified, got %s", js)
	}
	src := child(d.Changes[0], "source")
	if src == nil || src.Before != "x = 1\nprint(x)" || src.After != "x = 2\nprint(x)" {
		t.Fatalf("source row should carry both whole texts, got %s", js)
	}
}

// The point of pairing: inserting a cell must not turn every later cell into
// an edit, with ids and without.
func TestInsertedCellLeavesTheOthersAlone(t *testing.T) {
	cells := []c{{"markdown", "intro", "# Title", nil}, {"code", "load", "df = load()", nil}, {"code", "plot", "df.plot()", nil}}
	for _, withIDs := range []bool{true, false} {
		strip := func(cs []c) []c {
			out := append([]c(nil), cs...)
			if !withIDs {
				for i := range out {
					out[i].id = ""
				}
			}
			return out
		}
		head := strip(append([]c{{"code", "setup", "import pandas as pd", nil}}, cells...))
		d, js := diffOf(t, nb(1, strip(cells)...), nb(7, head...))
		if len(d.Changes) != 1 || d.Changes[0].Kind != Added || d.Changes[0].Label != "code cell 1" {
			t.Fatalf("ids=%v: expected only the inserted cell, got %s", withIDs, js)
		}
		if src := child(d.Changes[0], "source"); src == nil || src.After != "import pandas as pd" {
			t.Fatalf("ids=%v: added cell should carry its source, got %s", withIDs, js)
		}
	}
}

func TestRemovedCellKeepsItsPlace(t *testing.T) {
	base := nb(1, c{"code", "", "a = 1", nil}, c{"code", "", "b = 2", nil}, c{"code", "", "c = 3", nil})
	head := nb(1, c{"code", "", "a = 1", nil}, c{"code", "", "c = 3", nil})
	d, js := diffOf(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed || d.Changes[0].Path != "cells/base-2" || d.Changes[0].Label != "code cell (was 2)" {
		t.Fatalf("expected base cell 2 removed, got %s", js)
	}
	// A removal from the very start sorts ahead of everything else.
	d, js = diffOf(t, nb(1, c{"markdown", "", "gone", nil}, c{"code", "", "a = 1", nil}),
		nb(1, c{"code", "", "a = 1", nil}, c{"code", "", "new()", nil}))
	if len(d.Changes) != 2 || d.Changes[0].Kind != Removed || d.Changes[1].Kind != Added {
		t.Fatalf("expected the removed first cell before the added last one, got %s", js)
	}
}

func TestEditedCellWithoutIDPairsByContent(t *testing.T) {
	base := nb(1, c{"code", "", "import numpy as np\nx = np.arange(10)\nprint(x.sum())", nil})
	head := nb(1, c{"markdown", "", "Notes", nil}, c{"code", "", "import numpy as np\nx = np.arange(20)\nprint(x.sum())", nil})
	d, js := diffOf(t, base, head)
	if len(d.Changes) != 2 || d.Changes[0].Kind != Added || d.Changes[1].Kind != Modified || d.Changes[1].Label != "code cell 2" {
		t.Fatalf("expected one added markdown cell and the code cell edited, got %s", js)
	}
}

func TestUnrelatedCellsAreRemovedAndAddedNotEdited(t *testing.T) {
	d, js := diffOf(t, nb(1, c{"code", "", "alpha()\nbeta()", nil}), nb(1, c{"code", "", "gamma()\ndelta()", nil}))
	if len(d.Changes) != 2 || d.Changes[0].Kind != Removed || d.Changes[1].Kind != Added {
		t.Fatalf("cells with nothing in common should not pair, got %s", js)
	}
}

func TestMovedCellIsReportedAsMoved(t *testing.T) {
	base := nb(1, c{"code", "a", "a()", nil}, c{"code", "b", "b()", nil}, c{"code", "c", "c()", nil})
	head := nb(1, c{"code", "c", "c()", nil}, c{"code", "a", "a()", nil}, c{"code", "b", "b()", nil})
	d, js := diffOf(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "cells/c" {
		t.Fatalf("expected only cell c to move, got %s", js)
	}
	pos := child(d.Changes[0], "position")
	if pos == nil || pos.Before != 3 || pos.After != 1 {
		t.Fatalf("expected position 3 → 1, got %s", js)
	}
}

func TestOutputsAreDiffedButExecutionCountsAndMetadataAreNot(t *testing.T) {
	base := nb(1, c{"code", "a", "print(sum(xs))", []any{stream("140.0\n")}})
	same := nb(42, c{"code", "a", "print(sum(xs))", []any{stream("140.0\n")}})
	d, js := diffOf(t, base, same)
	if len(d.Changes) != 0 || !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("a re-run with the same output is no change, got %s", js)
	}
	changed := nb(42, c{"code", "a", "print(sum(xs))", []any{stream("150.0\n")}})
	d, js = diffOf(t, base, changed)
	out := child(d.Changes[0], "outputs")
	if len(d.Changes) != 1 || out == nil || out.Before != "140.0\n" || out.After != "150.0\n" || child(d.Changes[0], "source") != nil {
		t.Fatalf("expected only an outputs change 140.0 → 150.0, got %s", js)
	}
}

func TestOutputKindsReadAsText(t *testing.T) {
	outs := []any{
		map[string]any{"output_type": "execute_result", "execution_count": 3, "data": map[string]any{"text/plain": []string{"<Axes>"}, "image/png": "iVBORw0KGgo="}},
		map[string]any{"output_type": "error", "ename": "ValueError", "evalue": "bad", "traceback": []string{"\x1b[0;31mValueError\x1b[0m: bad"}},
	}
	d, js := diffOf(t, "", nb(1, c{"code", "a", "plot()", outs}))
	got := child(d.Changes[0], "outputs").After
	// Without a traceback, an error reads as "name: value".
	d2, _ := diffOf(t, "", nb(1, c{"code", "b", "x", []any{map[string]any{"output_type": "error", "ename": "KeyError", "evalue": "'k'", "traceback": []string{}}}}))
	if bare := child(d2.Changes[0], "outputs").After; bare != "KeyError: 'k'\n" {
		t.Fatalf("bare error output: got %q", bare)
	}
	want := "<Axes>\n[image/png, 8 B]\nValueError: bad\n"
	if got != want {
		t.Fatalf("outputs text\n got %q\nwant %q\n(%s)", got, want, js)
	}
}

func TestCellTypeAndKernelChanges(t *testing.T) {
	base := nb(1, c{"code", "a", "x", nil})
	head := strings.Replace(nb(1, c{"markdown", "a", "x", nil}), `"display_name":"Python 3"`, `"display_name":"Julia 1.10"`, 1)
	d, js := diffOf(t, base, head)
	if len(d.Changes) != 2 || d.Changes[0].Path != "metadata/kernel" || d.Changes[0].After != "Julia 1.10" {
		t.Fatalf("expected the kernel change first, got %s", js)
	}
	if ty := child(d.Changes[1], "cell_type"); ty == nil || ty.Before != "code" || ty.After != "markdown" {
		t.Fatalf("expected the type change on cells/a, got %s", js)
	}
}

func TestSourceAsListOfLines(t *testing.T) {
	base := `{"cells":[{"cell_type":"code","id":"a","source":["x = 1\n","y = 2"],"outputs":[]}]}`
	head := `{"cells":[{"cell_type":"code","id":"a","source":"x = 1\ny = 3","outputs":[]}]}`
	d, js := diffOf(t, base, head)
	if src := child(d.Changes[0], "source"); src == nil || src.Before != "x = 1\ny = 2" || src.After != "x = 1\ny = 3" {
		t.Fatalf("list and string sources should join the same way, got %s", js)
	}
}

func TestIDsAndPathsAreEscaped(t *testing.T) {
	d, js := diffOf(t, "", nb(1, c{"code", "a/b%c", "x", nil}))
	if d.Changes[0].Path != "cells/a%2Fb%25c" {
		t.Fatalf("expected an escaped path segment, got %s", js)
	}
}

func TestAddedAndRemovedNotebooks(t *testing.T) {
	d, js := diffOf(t, "", nb(1, c{"markdown", "", "# T", nil}, c{"code", "", "x", nil}))
	if len(d.Changes) != 2 || d.Changes[0].Kind != Added || d.Changes[1].Kind != Added {
		t.Fatalf("a new notebook is every cell added, got %s", js)
	}
	d, js = diffOf(t, nb(1, c{"code", "", "x", nil}), "")
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed {
		t.Fatalf("a deleted notebook is every cell removed, got %s", js)
	}
}

func TestMalformedErrorsCleanly(t *testing.T) {
	if _, err := (&Handler{}).Diff([]byte("{not json"), []byte(nb(1))); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("malformed base should error naming the side, got %v", err)
	}
}

// Handlers run in a server-side wasm worker on blobs anyone can push: a
// truncated notebook must be an error, never a panic.
func TestTruncatedNeverPanics(t *testing.T) {
	good := []byte(nb(1, c{"markdown", "i", "# T", nil}, c{"code", "a", "x = 1", []any{stream("1\n")}}))
	for n := 0; n < len(good); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on a %d-byte prefix: %v", n, r)
				}
			}()
			_, _ = (&Handler{}).Diff(good[:n], good)
		}()
	}
}

// Without ids, a removed cell's position is in the base and an edited cell's
// in the head, so the same number names two different cells: paths (selection
// keys) must still be unique.
func TestPathsStayUniqueWithoutIDs(t *testing.T) {
	base := nb(1, c{"code", "", "a()", nil}, c{"code", "", "scratch()", nil})
	head := nb(1, c{"markdown", "", "# Title", nil}, c{"code", "", "a()\nb()", nil})
	d, js := diffOf(t, base, head)
	seen := map[string]bool{}
	for _, ch := range d.Changes {
		if seen[ch.Path] {
			t.Fatalf("duplicate path %q in %s", ch.Path, js)
		}
		seen[ch.Path] = true
	}
	// Duplicate ids pairing cannot use still yield distinct paths.
	d, js = diffOf(t, "", nb(1, c{"code", "x", "a()", nil}, c{"code", "x", "b()", nil}))
	if len(d.Changes) != 2 || d.Changes[0].Path == d.Changes[1].Path {
		t.Fatalf("duplicate ids should still give distinct paths, got %s", js)
	}
}

// A cell that moved past others pairs with itself rather than reading as one
// removed and one added, even without ids.
func TestMovedWithoutIDs(t *testing.T) {
	base := nb(1, c{"code", "", "load()", nil}, c{"code", "", "clean()", nil}, c{"markdown", "", "## Plot", nil}, c{"code", "", "plot()", nil})
	head := nb(1, c{"markdown", "", "## Plot", nil}, c{"code", "", "plot()", nil}, c{"code", "", "load()", nil}, c{"code", "", "clean()", nil})
	d, js := diffOf(t, base, head)
	for _, ch := range d.Changes {
		if ch.Kind != Modified || child(ch, "position") == nil || len(ch.Children) != 1 {
			t.Fatalf("expected only moves, got %s", js)
		}
	}
	if len(d.Changes) != 2 {
		t.Fatalf("expected two cells moved, got %s", js)
	}
}
