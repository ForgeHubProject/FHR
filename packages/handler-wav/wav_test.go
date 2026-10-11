package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

// makeWAV builds a minimal valid WAV blob in memory: RIFF/WAVE header, a
// 16-byte "fmt " chunk, and a "data" chunk of dataLen zero bytes (plus the
// RIFF pad byte when dataLen is odd). No fixtures needed.
func makeWAV(t *testing.T, audioFormat, channels uint16, sampleRate uint32, bits uint16, dataLen int) []byte {
	t.Helper()
	byteRate := sampleRate * uint32(channels) * uint32(bits) / 8
	blockAlign := channels * bits / 8
	pad := dataLen % 2

	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(4+(8+16)+(8+dataLen+pad)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, audioFormat)
	binary.Write(buf, binary.LittleEndian, channels)
	binary.Write(buf, binary.LittleEndian, sampleRate)
	binary.Write(buf, binary.LittleEndian, byteRate)
	binary.Write(buf, binary.LittleEndian, blockAlign)
	binary.Write(buf, binary.LittleEndian, bits)
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(make([]byte, dataLen+pad))
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

func findChange(d StructuredDiff, path string) (DiffChange, bool) {
	for _, c := range d.Changes {
		if c.Path == path {
			return c, true
		}
	}
	return DiffChange{}, false
}

func TestMatch(t *testing.T) {
	h := &Handler{}
	if !h.Match("audio/take-1.wav") || !h.Match("X.WAV") {
		t.Fatal("should match .wav case-insensitively")
	}
	if h.Match("notes.txt") || h.Match("song.flac") {
		t.Fatal("should only match .wav")
	}
}

func TestSampleRateChange(t *testing.T) {
	base := makeWAV(t, 1, 2, 44100, 16, 0)
	head := makeWAV(t, 1, 2, 48000, 16, 0)
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 1 {
		t.Fatalf("expected exactly one change, got %d: %s", len(d.Changes), js)
	}
	c := d.Changes[0]
	if c.Path != "fmt.sampleRate" || c.Kind != Modified {
		t.Fatalf("expected fmt.sampleRate modified, got %+v", c)
	}
	if c.Before != 44100 || c.After != 48000 {
		t.Fatalf("expected 44100 → 48000, got %v → %v", c.Before, c.After)
	}
}

func TestFormatChannelsBitDepthChange(t *testing.T) {
	base := makeWAV(t, 1, 1, 44100, 16, 0) // mono 16-bit PCM
	head := makeWAV(t, 3, 2, 44100, 32, 0) // stereo 32-bit float
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 3 {
		t.Fatalf("expected three changes (channels, bit depth, format), got %d: %s", len(d.Changes), js)
	}
	if c, ok := findChange(d, "fmt.channels"); !ok || c.Before != 1 || c.After != 2 {
		t.Fatalf("expected channels 1 → 2, got %+v", d.Changes)
	}
	if c, ok := findChange(d, "fmt.bitsPerSample"); !ok || c.Before != 16 || c.After != 32 {
		t.Fatalf("expected bit depth 16 → 32, got %+v", d.Changes)
	}
	if c, ok := findChange(d, "fmt.audioFormat"); !ok || c.Before != "PCM" || c.After != "IEEE float" {
		t.Fatalf("expected PCM → IEEE float, got %+v", d.Changes)
	}
}

func TestDurationAndDataSizeChange(t *testing.T) {
	// 44100 Hz stereo 16-bit → 176400 B/s; 176400 bytes = exactly 1 second.
	base := makeWAV(t, 1, 2, 44100, 16, 176400)
	head := makeWAV(t, 1, 2, 44100, 16, 352800)
	d, js := diffJSON(t, base, head)
	if len(d.Changes) != 2 {
		t.Fatalf("expected two changes (duration, data size), got %d: %s", len(d.Changes), js)
	}
	if c, ok := findChange(d, "data.duration"); !ok || c.Before != "1.00s" || c.After != "2.00s" {
		t.Fatalf("expected duration 1.00s → 2.00s, got %+v", d.Changes)
	}
	if c, ok := findChange(d, "data.size"); !ok || c.Before != 176400 || c.After != 352800 {
		t.Fatalf("expected data size 176400 → 352800, got %+v", d.Changes)
	}
}

func TestZeroByteRateDurationIsGuarded(t *testing.T) {
	head := makeWAV(t, 1, 2, 44100, 16, 4)
	// Zero out the byte-rate field (offset 28: RIFF header 12 + chunk header 8
	// + audioFormat 2 + channels 2 + sampleRate 4).
	copy(head[28:32], []byte{0, 0, 0, 0})
	d, _ := diffJSON(t, nil, head)
	c, ok := findChange(d, "data.duration")
	if !ok || c.After != "unknown" {
		t.Fatalf("zero byte rate should yield duration \"unknown\", got %+v", d.Changes)
	}
}

func TestIdenticalIsEmptyArray(t *testing.T) {
	same := makeWAV(t, 1, 2, 44100, 16, 64)
	d, js := diffJSON(t, same, same)
	if len(d.Changes) != 0 {
		t.Fatalf("identical files should yield no changes, got %+v", d.Changes)
	}
	if !strings.Contains(js, `"changes":[]`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestAddedFileAllPropsAdded(t *testing.T) {
	d, js := diffJSON(t, nil, makeWAV(t, 1, 2, 48000, 24, 96000))
	if len(d.Changes) != 6 {
		t.Fatalf("expected six added properties, got %d: %s", len(d.Changes), js)
	}
	for _, c := range d.Changes {
		if c.Kind != Added {
			t.Fatalf("new file should diff as additions, got %+v", c)
		}
	}
	if strings.Contains(js, `"changes":null`) {
		t.Fatalf("changes must marshal as [] not null: %s", js)
	}
}

func TestRemovedFileAllPropsRemoved(t *testing.T) {
	d, _ := diffJSON(t, makeWAV(t, 1, 1, 22050, 8, 100), nil)
	if len(d.Changes) != 6 {
		t.Fatalf("expected six removed properties, got %d", len(d.Changes))
	}
	for _, c := range d.Changes {
		if c.Kind != Removed {
			t.Fatalf("deleted file should diff as removals, got %+v", c)
		}
	}
}

func TestMalformedInputErrorsCleanly(t *testing.T) {
	h := &Handler{}
	valid := makeWAV(t, 1, 2, 44100, 16, 64)
	cases := map[string][]byte{
		"garbage text":      []byte("this is not audio at all"),
		"too short":         []byte("RIFF"),
		"wrong form type":   append([]byte("RIFF\x04\x00\x00\x00AVI "), make([]byte, 8)...),
		"truncated chunk":   valid[:20],
		"missing data":      valid[:36], // header + fmt chunk only
		"declared oversize": append([]byte("RIFF\x28\x00\x00\x00WAVEdata\xff\xff\xff\x0f"), make([]byte, 8)...),
	}
	for name, blob := range cases {
		if _, err := h.Diff(blob, valid); err == nil {
			t.Fatalf("%s as base should error", name)
		}
		if _, err := h.Diff(valid, blob); err == nil {
			t.Fatalf("%s as head should error", name)
		}
	}
}
