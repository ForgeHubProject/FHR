package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// Test images are encoded in-process: no binary fixtures.

func encode(t *testing.T, w, h int, gray bool) []byte {
	t.Helper()
	var img image.Image
	if gray {
		g := image.NewGray(image.Rect(0, 0, w, h))
		for i := range g.Pix {
			g.Pix[i] = byte(i * 11)
		}
		img = g
	} else {
		n := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := range n.Pix {
			n.Pix[i] = byte(i * 11)
		}
		img = n
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// other is a valid image of a different format, which this handler must refuse.
func other(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func diffJSON(t *testing.T, base, head []byte) (StructuredDiff, string) {
	t.Helper()
	d, err := (&Handler{}).Diff(base, head)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(d)
	return d, string(js)
}

func findChange(d StructuredDiff, path string) *DiffChange {
	for i := range d.Changes {
		if d.Changes[i].Path == path {
			return &d.Changes[i]
		}
	}
	return nil
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	for _, p := range []string{"pic.jpg", "pic.jpeg", "art/X.JPG"} {
		if !h.Match(p) {
			t.Fatalf("should match %q case-insensitively", p)
		}
	}
	for _, p := range []string{"notes.txt", "model.gltf", "pic.jpg.bak", "jpeg", "pic.png", "pic.gif"} {
		if h.Match(p) {
			t.Fatalf("should not match %q", p)
		}
	}
}

func TestIdenticalIsEmptyArray(t *testing.T) {
	a := encode(t, 2, 2, false)
	_, js := diffJSON(t, a, a)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("identical files should yield an empty changes array, got %s", js)
	}
}

func TestDimensionChange(t *testing.T) {
	d, js := diffJSON(t, encode(t, 2, 2, false), encode(t, 4, 2, false))
	dim := findChange(d, "dimensions")
	if dim == nil || dim.Kind != Modified || dim.Before != "2x2" || dim.After != "4x2" {
		t.Fatalf("expected dimensions 2x2 → 4x2, got %s", js)
	}
}

func TestColorModelChange(t *testing.T) {
	d, js := diffJSON(t, encode(t, 2, 2, false), encode(t, 2, 2, true))
	cm := findChange(d, "colorModel")
	if cm == nil || cm.Before != "YCbCr" || cm.After != "Gray" {
		t.Fatalf("expected color model YCbCr → Gray, got %s", js)
	}
}

func TestSameMetaDifferentBytesIsSingleContentChange(t *testing.T) {
	base := encode(t, 2, 2, false)
	// Same header, same length, different trailing bytes: a pixel-level edit
	// as far as header-only decoding can tell.
	head := append([]byte(nil), base...)
	head[len(head)-2] ^= 0xFF
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 || d.Changes[0].Path != "content" || d.Changes[0].Kind != Modified {
		t.Fatalf("expected exactly one modified content change, got %s", js)
	}
	if !strings.Contains(js, "bytes differ") {
		t.Fatalf("content change should say the bytes differ, got %s", js)
	}
}

func TestAddedAndRemovedFiles(t *testing.T) {
	d, js := diffJSON(t, nil, encode(t, 2, 2, false))
	if len(d.Changes) != 3 {
		t.Fatalf("expected 3 added metadata fields, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("new file should diff as additions, got %+v", c)
		}
	}
	if dim := findChange(d, "dimensions"); dim == nil || dim.After != "2x2" {
		t.Fatalf("expected added dimensions 2x2, got %s", js)
	}
	d, js = diffJSON(t, encode(t, 2, 2, false), nil)
	if len(d.Changes) != 3 || d.Changes[0].Kind != Removed {
		t.Fatalf("deleted file should diff as 3 removals, got %s", js)
	}
}

func TestBothEmptyIsEmptyArray(t *testing.T) {
	_, js := diffJSON(t, nil, nil)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("two empty blobs should yield an empty changes array, got %s", js)
	}
}

func TestMalformedAndOtherFormatsErrorCleanly(t *testing.T) {
	h := &Handler{}
	good := encode(t, 2, 2, false)
	if _, err := h.Diff([]byte("not an image"), good); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("malformed base should error naming the side, got %v", err)
	}
	// A valid image of another format, e.g. a PNG renamed to .jpg, is refused
	// rather than diffed as this format.
	if _, err := h.Diff(good, other(t)); err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("another format on the head should error naming the side, got %v", err)
	}
}

// Handlers run in a server-side wasm worker on blobs anyone can push: a
// truncated file must be an error, never a panic.
func TestTruncatedNeverPanics(t *testing.T) {
	good := encode(t, 8, 8, false)
	h := &Handler{}
	for n := 1; n < len(good); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on a %d-byte prefix: %v", n, r)
				}
			}()
			_, _ = h.Diff(good[:n], good)
		}()
	}
}
