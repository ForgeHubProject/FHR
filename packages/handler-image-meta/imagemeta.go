// Package main is a format-aware handler for raster images (.png, .jpg,
// .jpeg, .gif). It produces a metadata-level semantic diff — dimensions,
// decoded format, color model, byte size — which reads far better than
// "binary files differ".
//
// Only image.DecodeConfig is used (header-only decode, stdlib codecs
// registered via blank imports): no pixel data is ever decoded, so the
// handler is fast, allocation-light, and wasm-safe. Perceptual / pixel-region
// diffing and EXIF fields are planned follow-ups — see issue #28.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"strings"

	// Register the stdlib codecs with image.DecodeConfig. Decoding stays
	// header-only; these imports never pull pixel data.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Handler is the raster image metadata handler.
type Handler struct{}

// imageExts are the extensions this handler claims, matched case-insensitively.
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif"}

// Match returns true for .png / .jpg / .jpeg / .gif files.
func (h *Handler) Match(path string) bool {
	ext := filepath.Ext(path)
	for _, e := range imageExts {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

// imageMeta is the decoded metadata for one side of a diff.
type imageMeta struct {
	format     string // decoded format ("png", "jpeg", "gif") — not the extension
	width      int
	height     int
	colorModel string
	size       int // blob length in bytes
}

func (m imageMeta) dimensions() string {
	return fmt.Sprintf("%dx%d", m.width, m.height)
}

// decodeMeta reads only the image header (image.DecodeConfig) — never pixels.
func decodeMeta(blob Blob) (imageMeta, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(blob))
	if err != nil {
		return imageMeta{}, fmt.Errorf("decoding image header: %w", err)
	}
	return imageMeta{
		format:     format,
		width:      cfg.Width,
		height:     cfg.Height,
		colorModel: colorModelName(cfg.ColorModel),
		size:       len(blob),
	}, nil
}

// colorModelName maps well-known stdlib color models to readable names.
// Anything unrecognized (custom models from third-party codecs) is "other".
func colorModelName(m color.Model) string {
	switch m {
	case color.RGBAModel:
		return "RGBA"
	case color.RGBA64Model:
		return "RGBA64"
	case color.NRGBAModel:
		return "NRGBA"
	case color.NRGBA64Model:
		return "NRGBA64"
	case color.AlphaModel:
		return "Alpha"
	case color.Alpha16Model:
		return "Alpha16"
	case color.GrayModel:
		return "Gray"
	case color.Gray16Model:
		return "Gray16"
	case color.YCbCrModel:
		return "YCbCr"
	case color.NYCbCrAModel:
		return "NYCbCrA"
	case color.CMYKModel:
		return "CMYK"
	}
	if _, ok := m.(color.Palette); ok {
		return "Paletted"
	}
	return "other"
}

// humanSize renders a byte count as e.g. "12.3 KB".
func humanSize(n int) string {
	const unit = 1024.0
	f := float64(n)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		f /= unit
		if f < unit {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%.1f PB", f/unit)
}

// sizeString renders raw bytes plus a human reading, e.g. "12628 bytes (12.3 KB)".
func sizeString(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d bytes (%s)", n, humanSize(n))
}

// Diff produces a metadata-level semantic diff of two image blobs. An empty
// blob on either side is the added/deleted-file case (all metadata added /
// removed). Metadata equal but bytes different yields exactly one "content"
// change, so a pixel-only edit never diffs as empty.
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	// Non-nil so an empty diff marshals as [] (never null).
	changes := []DiffChange{}

	switch {
	case len(base) == 0 && len(head) == 0:
		// Nothing on either side.
	case len(base) == 0:
		meta, err := decodeMeta(head)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("head: %w", err)
		}
		changes = append(changes, sideChanges(meta, Added)...)
	case len(head) == 0:
		meta, err := decodeMeta(base)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("base: %w", err)
		}
		changes = append(changes, sideChanges(meta, Removed)...)
	default:
		baseMeta, err := decodeMeta(base)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("base: %w", err)
		}
		headMeta, err := decodeMeta(head)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("head: %w", err)
		}
		changes = append(changes, diffMeta(baseMeta, headMeta, bytes.Equal(base, head))...)
	}

	return StructuredDiff{Version: "1.0", Format: handlerID, Changes: changes}, nil
}

// metaFields lists one side's metadata as (path, label, value) rows, in the
// order they render.
func metaFields(m imageMeta) []struct{ path, label, value string } {
	return []struct{ path, label, value string }{
		{"dimensions", "dimensions", m.dimensions()},
		{"format", "format", m.format},
		{"colorModel", "color model", m.colorModel},
		{"byteSize", "byte size", sizeString(m.size)},
	}
}

// sideChanges reports every metadata field of one side as added or removed —
// the new-file / deleted-file case, with the side's stats as values.
func sideChanges(m imageMeta, kind ChangeKind) []DiffChange {
	fields := metaFields(m)
	ch := make([]DiffChange, 0, len(fields))
	for _, f := range fields {
		c := DiffChange{Path: f.path, Kind: kind, Label: f.label}
		if kind == Added {
			c.After = f.value
		} else {
			c.Before = f.value
		}
		ch = append(ch, c)
	}
	return ch
}

// diffMeta compares two sides' metadata field by field. The decoded format is
// diffed (not the extension) — an extension can lie, and a PNG renamed to
// .jpg is exactly the kind of change worth surfacing.
func diffMeta(base, head imageMeta, sameBytes bool) []DiffChange {
	ch := []DiffChange{}
	bf, hf := metaFields(base), metaFields(head)
	for i := range bf {
		if bf[i].value == hf[i].value {
			continue
		}
		ch = append(ch, DiffChange{
			Path:   bf[i].path,
			Kind:   Modified,
			Label:  bf[i].label,
			Before: bf[i].value,
			After:  hf[i].value,
		})
	}
	if len(ch) == 0 && !sameBytes {
		// All metadata equal, but the bytes changed — a pixel-level edit that
		// header inspection can't describe. Say so rather than diffing empty.
		ch = append(ch, DiffChange{
			Path:   "content",
			Kind:   Modified,
			Label:  "content",
			Before: "bytes differ: " + sizeString(base.size),
			After:  "bytes differ: " + sizeString(head.size),
		})
	}
	return ch
}

// Merge is not yet supported for image-meta (v0 is diff-only) — Forge falls
// back to blob-pick, same as plain git. See issue #28 for follow-ups.
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for image-meta")
}
