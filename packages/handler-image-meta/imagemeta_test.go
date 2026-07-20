package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// Test images are encoded in-process — no binary fixtures.

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	img.SetColorIndex(0, 0, 1)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 11)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func diffJSON(t *testing.T, base, head []byte) (StructuredDiff, string) {
	t.Helper()
	h := &Handler{}
	d, err := h.Diff(base, head)
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
	for _, p := range []string{"logo.png", "photo.JPG", "art/pic.jpeg", "anim.GIF", "X.Png"} {
		if !h.Match(p) {
			t.Fatalf("should match %q case-insensitively", p)
		}
	}
	for _, p := range []string{"notes.txt", "model.gltf", "picture.png.bak", "gif"} {
		if h.Match(p) {
			t.Fatalf("should not match %q", p)
		}
	}
}

func TestIdenticalIsEmptyArray(t *testing.T) {
	a := encodePNG(t, 2, 2)
	_, js := diffJSON(t, a, a)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("identical files should yield an empty changes array, got %s", js)
	}
}

func TestDimensionChange(t *testing.T) {
	base := encodePNG(t, 2, 2)
	head := encodePNG(t, 4, 2)
	d, js := diffJSON(t, base, head)
	dim := findChange(d, "dimensions")
	if dim == nil || dim.Kind != Modified {
		t.Fatalf("expected a modified dimensions change, got %s", js)
	}
	if dim.Before != "2x2" || dim.After != "4x2" {
		t.Fatalf("expected 2x2 → 4x2, got %v → %v", dim.Before, dim.After)
	}
	if c := findChange(d, "format"); c != nil {
		t.Fatalf("format did not change, got %+v", c)
	}
}

func TestDecodedFormatChange(t *testing.T) {
	// The decoded format is diffed, not the extension — both sides here could
	// be named .gif while the head is really a JPEG.
	base := encodeGIF(t)
	head := encodeJPEG(t)
	d, js := diffJSON(t, base, head)
	f := findChange(d, "format")
	if f == nil || f.Kind != Modified || f.Before != "gif" || f.After != "jpeg" {
		t.Fatalf("expected format gif → jpeg, got %s", js)
	}
	cm := findChange(d, "colorModel")
	if cm == nil || cm.Before != "Paletted" || cm.After != "YCbCr" {
		t.Fatalf("expected color model Paletted → YCbCr, got %s", js)
	}
}

func TestSameMetaDifferentBytesIsSingleContentChange(t *testing.T) {
	base := encodePNG(t, 2, 2)
	// Same header, same length, different trailing bytes — a pixel-level edit
	// as far as header-only decoding can tell.
	head := append([]byte(nil), base...)
	head[len(head)-1] ^= 0xFF
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected exactly one change, got %s", js)
	}
	c := d.Changes[0]
	if c.Path != "content" || c.Kind != Modified {
		t.Fatalf("expected a modified content change, got %+v", c)
	}
	if !strings.Contains(js, "bytes differ") {
		t.Fatalf("content change should say the bytes differ, got %s", js)
	}
}

func TestAddedFileAllStatsAdded(t *testing.T) {
	d, js := diffJSON(t, nil, encodePNG(t, 2, 2))
	if len(d.Changes) != 4 {
		t.Fatalf("expected 4 added metadata fields, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("new file should diff as additions, got %+v", c)
		}
	}
	dim := findChange(d, "dimensions")
	if dim == nil || dim.After != "2x2" {
		t.Fatalf("expected added dimensions 2x2, got %s", js)
	}
	f := findChange(d, "format")
	if f == nil || f.After != "png" {
		t.Fatalf("expected added format png, got %s", js)
	}
	if strings.Contains(js, `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestRemovedFileAllStatsRemoved(t *testing.T) {
	d, js := diffJSON(t, encodeGIF(t), nil)
	if len(d.Changes) != 4 {
		t.Fatalf("expected 4 removed metadata fields, got %s", js)
	}
	for _, c := range d.Changes {
		if c.Kind != Removed {
			t.Fatalf("deleted file should diff as removals, got %+v", c)
		}
	}
	f := findChange(d, "format")
	if f == nil || f.Before != "gif" {
		t.Fatalf("expected removed format gif, got %s", js)
	}
}

func TestBothEmptyIsEmptyArray(t *testing.T) {
	_, js := diffJSON(t, nil, nil)
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("two empty blobs should yield an empty changes array, got %s", js)
	}
}

func TestMalformedErrorsCleanly(t *testing.T) {
	h := &Handler{}
	if _, err := h.Diff([]byte("not an image"), encodePNG(t, 2, 2)); err == nil {
		t.Fatal("malformed base should error")
	} else if !strings.Contains(err.Error(), "base") {
		t.Fatalf("error should say which side failed, got %v", err)
	}
	if _, err := h.Diff(encodePNG(t, 2, 2), []byte{0x89, 0x50}); err == nil {
		t.Fatal("malformed head should error")
	} else if !strings.Contains(err.Error(), "head") {
		t.Fatalf("error should say which side failed, got %v", err)
	}
}

func TestByteSizeChangeUsesHumanRendering(t *testing.T) {
	// Two valid PNGs of the same dimensions but different byte lengths: pad
	// one with a trailing chunk-free tail is invalid, so instead compare a
	// compressible image with a noisy one at 64x64 — sizes always differ.
	flat := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	noisy := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	seed := uint32(42)
	for i := range noisy.Pix {
		seed = seed*1664525 + 1013904223 // LCG; high bits are noisy
		noisy.Pix[i] = byte(seed >> 24)
	}
	var b1, b2 bytes.Buffer
	if err := png.Encode(&b1, flat); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&b2, noisy); err != nil {
		t.Fatal(err)
	}
	if b1.Len() == b2.Len() {
		t.Fatal("test premise broken: encoded sizes should differ")
	}
	d, js := diffJSON(t, b1.Bytes(), b2.Bytes())
	sz := findChange(d, "byteSize")
	if sz == nil || sz.Kind != Modified {
		t.Fatalf("expected a byte size change, got %s", js)
	}
	if !strings.Contains(sz.Before.(string), "bytes") || !strings.Contains(sz.After.(string), "bytes") {
		t.Fatalf("byte size values should include raw bytes, got %v → %v", sz.Before, sz.After)
	}
	if !strings.Contains(sz.After.(string), "KB") && !strings.Contains(sz.Before.(string), "KB") {
		t.Fatalf("larger side should carry a human KB rendering, got %v → %v", sz.Before, sz.After)
	}
}
