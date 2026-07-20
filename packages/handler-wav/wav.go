// Package main is a format-aware handler for .wav audio files. A WAV is a
// RIFF container, so a text diff of one is binary noise. This handler parses
// the RIFF/WAVE header and the "fmt " and "data" chunks by hand (stdlib byte
// reads only, so the same code runs native and in wasm) and diffs the
// properties people actually care about — sample rate, channels, bit depth,
// audio format, duration, data size — a semantic answer to "what changed in
// this recording container".
//
// v0 diffs container metadata only. A waveform-region diff (which part of the
// audio changed) is the planned follow-up — see issue #30.
package main

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
)

// Handler is the WAV format handler.
type Handler struct{}

// Match returns true for .wav files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".wav")
}

// wavInfo holds the container properties extracted from the "fmt " and "data"
// chunks — everything the diff reports.
type wavInfo struct {
	audioFormat   uint16
	channels      uint16
	sampleRate    uint32
	byteRate      uint32
	bitsPerSample uint16
	dataSize      uint32
	hasFmt        bool
	hasData       bool
}

// parseWAV walks the RIFF chunk list by hand: 4-byte chunk id, uint32 LE size,
// then the chunk body (odd sizes carry one pad byte — RIFF chunks are
// word-aligned). Unknown chunks ("LIST", "fact", …) are skipped, not choked
// on. Truncated or non-WAVE input errors cleanly.
func parseWAV(blob Blob) (*wavInfo, error) {
	if len(blob) < 12 {
		return nil, fmt.Errorf("not a RIFF/WAVE file: %d bytes is too short for the 12-byte header", len(blob))
	}
	if string(blob[0:4]) != "RIFF" {
		return nil, fmt.Errorf("not a RIFF container: missing RIFF magic")
	}
	if string(blob[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a WAVE file: RIFF form type is %q", string(blob[8:12]))
	}

	w := &wavInfo{}
	off := 12
	for off+8 <= len(blob) {
		id := string(blob[off : off+4])
		size := int(binary.LittleEndian.Uint32(blob[off+4 : off+8]))
		body := off + 8
		if size < 0 || body+size > len(blob) {
			return nil, fmt.Errorf("truncated %q chunk: declares %d bytes but only %d remain", id, size, len(blob)-body)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf(`"fmt " chunk too small: %d bytes (need 16)`, size)
			}
			w.audioFormat = binary.LittleEndian.Uint16(blob[body : body+2])
			w.channels = binary.LittleEndian.Uint16(blob[body+2 : body+4])
			w.sampleRate = binary.LittleEndian.Uint32(blob[body+4 : body+8])
			w.byteRate = binary.LittleEndian.Uint32(blob[body+8 : body+12])
			w.bitsPerSample = binary.LittleEndian.Uint16(blob[body+14 : body+16])
			w.hasFmt = true
		case "data":
			w.dataSize = uint32(size)
			w.hasData = true
		}
		off = body + size
		if size%2 == 1 {
			off++ // pad byte after odd-sized chunks
		}
	}
	if !w.hasFmt {
		return nil, fmt.Errorf(`malformed WAVE file: no "fmt " chunk`)
	}
	if !w.hasData {
		return nil, fmt.Errorf(`malformed WAVE file: no "data" chunk`)
	}
	return w, nil
}

// formatName renders the fmt chunk's audio-format code. The two ubiquitous
// codes get names; anything else keeps its numeric code.
func formatName(code uint16) string {
	switch code {
	case 1:
		return "PCM"
	case 3:
		return "IEEE float"
	default:
		return fmt.Sprintf("format %d", code)
	}
}

// durationText derives playback length from data size / byte rate, guarded
// against a zero byte rate (malformed but parseable headers).
func durationText(w *wavInfo) string {
	if w.byteRate == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.2fs", float64(w.dataSize)/float64(w.byteRate))
}

// prop is one diffable container property.
type prop struct {
	path  string
	label string
	value any
}

// props lists the diffable properties in report order. Paths are keyed by the
// chunk the value came from, so both sides always produce the same paths.
func props(w *wavInfo) []prop {
	return []prop{
		{"fmt.sampleRate", "sample rate (Hz)", int(w.sampleRate)},
		{"fmt.channels", "channels", int(w.channels)},
		{"fmt.bitsPerSample", "bit depth", int(w.bitsPerSample)},
		{"fmt.audioFormat", "audio format", formatName(w.audioFormat)},
		{"data.duration", "duration", durationText(w)},
		{"data.size", "data size (bytes)", int(w.dataSize)},
	}
}

// Diff produces a property-level semantic diff of two WAV containers. An empty
// blob on either side is the added/deleted-file case: every property is
// reported as added / removed with its value.
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	// Non-nil so an empty diff marshals as [] (never null).
	changes := []DiffChange{}

	switch {
	case len(base) == 0 && len(head) == 0:
		// Nothing on either side — nothing to report.

	case len(base) == 0:
		w, err := parseWAV(head)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("head: %w", err)
		}
		for _, p := range props(w) {
			changes = append(changes, DiffChange{Path: p.path, Kind: Added, Label: p.label, After: p.value})
		}

	case len(head) == 0:
		w, err := parseWAV(base)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("base: %w", err)
		}
		for _, p := range props(w) {
			changes = append(changes, DiffChange{Path: p.path, Kind: Removed, Label: p.label, Before: p.value})
		}

	default:
		a, err := parseWAV(base)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("base: %w", err)
		}
		b, err := parseWAV(head)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("head: %w", err)
		}
		ap, bp := props(a), props(b)
		for i := range ap {
			if ap[i].value != bp[i].value {
				changes = append(changes, DiffChange{Path: ap[i].path, Kind: Modified, Label: ap[i].label, Before: ap[i].value, After: bp[i].value})
			}
		}
	}

	return StructuredDiff{Version: "1.0", Format: "wav", Changes: changes}, nil
}

// Merge is not yet supported for WAV (v0 is diff-only); Forge falls back to
// blob-pick. A waveform-aware merge is out of scope — see issue #30.
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for wav")
}
