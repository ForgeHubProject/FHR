package main

import (
	"encoding/json"
	"strconv"
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
	if !h.Match("config/settings.json") || !h.Match("X.JSON") {
		t.Fatal("should match .json case-insensitively")
	}
	if h.Match("notes.txt") || h.Match("data.jsonl") {
		t.Fatal("should not match non-.json files")
	}
}

func TestIdenticalIsEmptyArray(t *testing.T) {
	same := `{"name":"forge","port":8080}`
	d, js := diffJSON(t, same, same)
	if len(d.Changes) != 0 {
		t.Fatalf("identical documents must produce no changes, got %s", js)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("empty diff must marshal as [] not null: %s", js)
	}
}

func TestKeyOrderIsNotAChange(t *testing.T) {
	_, js := diffJSON(t, `{"a":1,"b":2}`, `{"b":2,"a":1}`)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("key order must not register as a change: %s", js)
	}
}

func TestKeyAddRemoveChange(t *testing.T) {
	base := `{"name":"forge","port":80,"old":true}`
	head := `{"name":"forge","port":8080,"debug":false}`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 3 {
		t.Fatalf("expected 3 changes, got %s", js)
	}
	if c, ok := findPath(d, "debug"); !ok || c.Kind != Added || c.After != "false" {
		t.Fatalf("expected 'debug' added, got %s", js)
	}
	if c, ok := findPath(d, "old"); !ok || c.Kind != Removed || c.Before != "true" {
		t.Fatalf("expected 'old' removed, got %s", js)
	}
	if c, ok := findPath(d, "port"); !ok || c.Kind != Modified || c.Before != "80" || c.After != "8080" {
		t.Fatalf("expected 'port' 80 → 8080, got %s", js)
	}
}

func TestNestedObjectChange(t *testing.T) {
	base := `{"server":{"host":"a","tls":{"min":"1.2"}}}`
	head := `{"server":{"host":"a","tls":{"min":"1.3"}}}`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected exactly one change, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "server.tls.min" || c.Kind != Modified || c.Before != `"1.2"` || c.After != `"1.3"` {
		t.Fatalf("expected server.tls.min \"1.2\" → \"1.3\", got %+v", c)
	}
}

func TestArrayInsertPositionalCascade(t *testing.T) {
	// Positional v0: inserting at the front reports a length change and makes
	// every following index read as modified. That cascade is the documented
	// tradeoff — this test asserts it deliberately.
	base := `{"servers":["alpha","beta"]}`
	head := `{"servers":["new","alpha","beta"]}`
	d, js := diffJSON(t, base, head)
	lc, ok := findPath(d, "servers")
	if !ok || lc.Kind != Modified || !strings.Contains(lc.Label, "length 2 → 3") {
		t.Fatalf("expected a length change on 'servers', got %s", js)
	}
	if c, ok := findPath(d, "servers[0]"); !ok || c.Kind != Modified || c.Before != `"alpha"` || c.After != `"new"` {
		t.Fatalf("expected servers[0] alpha → new (cascade), got %s", js)
	}
	if c, ok := findPath(d, "servers[1]"); !ok || c.Kind != Modified || c.Before != `"beta"` || c.After != `"alpha"` {
		t.Fatalf("expected servers[1] beta → alpha (cascade), got %s", js)
	}
	if len(d.Changes) != 3 {
		t.Fatalf("expected length change + 2 cascaded index changes, got %s", js)
	}
}

func TestArrayElementChangePath(t *testing.T) {
	base := `{"servers":[{"port":80},{"port":81}]}`
	head := `{"servers":[{"port":80},{"port":9091}]}`
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %s", js)
	}
	if c := d.Changes[0]; c.Path != "servers[1].port" || c.Before != "81" || c.After != "9091" {
		t.Fatalf("expected servers[1].port 81 → 9091, got %+v", c)
	}
}

func TestTypeChangeStringToNumber(t *testing.T) {
	d, js := diffJSON(t, `{"port":"8080"}`, `{"port":8080}`)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "port" || c.Kind != Modified || c.Label != "type: string → number" {
		t.Fatalf("expected a string → number type change on 'port', got %+v", c)
	}
	if c.Before != `"8080"` || c.After != "8080" {
		t.Fatalf("expected before \"8080\" after 8080, got %+v", c)
	}
}

func TestEmptyBaseAllAdded(t *testing.T) {
	d, js := diffJSON(t, "", `{"name":"forge","port":8080}`)
	if len(d.Changes) != 2 {
		t.Fatalf("expected every top-level key added, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("expected only additions for a new file, got %s", js)
		}
	}
	if _, ok := findPath(d, "port"); !ok {
		t.Fatalf("expected 'port' among added keys, got %s", js)
	}
}

func TestEmptyHeadAllRemoved(t *testing.T) {
	d, js := diffJSON(t, `{"name":"forge"}`, "")
	if len(d.Changes) != 1 || d.Changes[0].Kind != Removed || d.Changes[0].Path != "name" {
		t.Fatalf("expected 'name' removed for a deleted file, got %s", js)
	}
}

func TestEmptyBaseScalarDocument(t *testing.T) {
	d, _ := diffJSON(t, "", `[1,2,3]`)
	if len(d.Changes) != 1 || d.Changes[0].Kind != Added || d.Changes[0].Path != "$" {
		t.Fatalf("expected a single document-level addition, got %+v", d.Changes)
	}
}

func TestMalformedJSONErrors(t *testing.T) {
	h := &Handler{}
	if _, err := h.Diff([]byte(`{"broken":`), []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("malformed base must error cleanly naming the side, got %v", err)
	}
	if _, err := h.Diff([]byte(`{}`), []byte(`{"broken":`)); err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("malformed head must error cleanly naming the side, got %v", err)
	}
	if _, err := h.Diff([]byte(`{} trailing`), []byte(`{}`)); err == nil {
		t.Fatal("trailing garbage must error")
	}
}

func TestLongValueTruncated(t *testing.T) {
	long := strings.Repeat("x", 500)
	d, _ := diffJSON(t, `{"blob":"short"}`, `{"blob":"`+long+`"}`)
	if len(d.Changes) != 1 {
		t.Fatalf("expected one change, got %+v", d.Changes)
	}
	after, _ := d.Changes[0].After.(string)
	if len([]rune(after)) > maxValueChars+1 || !strings.HasSuffix(after, "…") {
		t.Fatalf("expected truncated value ending in ellipsis, got %d chars", len([]rune(after)))
	}
}

func TestChangeCapTruncatesCleanly(t *testing.T) {
	// Build a document with far more changed keys than the cap.
	var a, b strings.Builder
	a.WriteString("{")
	b.WriteString("{")
	for i := 0; i < maxCollected+100; i++ {
		if i > 0 {
			a.WriteString(",")
			b.WriteString(",")
		}
		key := `"k` + strconv.Itoa(i) + `":`
		a.WriteString(key + "1")
		b.WriteString(key + "2")
	}
	a.WriteString("}")
	b.WriteString("}")

	d, _ := diffJSON(t, a.String(), b.String())
	if len(d.Changes) != maxCollected+1 {
		t.Fatalf("expected %d changes + 1 truncation marker, got %d", maxCollected, len(d.Changes))
	}
	last := d.Changes[len(d.Changes)-1]
	if !strings.Contains(last.Label, "truncated") {
		t.Fatalf("final change must note the truncation, got %+v", last)
	}
}

func TestMergeNotSupported(t *testing.T) {
	h := &Handler{}
	if _, _, err := h.Merge(nil, nil, nil); err == nil {
		t.Fatal("merge must report not supported")
	}
}
