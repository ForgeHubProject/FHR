// Package main is a format-aware handler for STL meshes (.stl), the lingua
// franca of 3D printing. STL is a raw triangle soup — no node names, no scene
// graph, no object identity of any kind — so a structural, entity-matching
// diff is impossible. Instead the diff is geometric/statistical: it parses
// both variants (ASCII and binary), computes per-side mesh statistics
// (triangle count, bounding box, surface area, approximate volume, ASCII
// solid name) and reports which of those properties changed. The output is a
// geometry-level comparison, not a per-triangle patch.
//
// v0 compares whole-mesh statistics. A spatial delta (which regions changed,
// via voxel/octree bucketing) is the planned follow-up — issue #14.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// Handler is the STL format handler.
type Handler struct{}

// Match returns true for .stl files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".stl")
}

// vec3 is a 3D point/vector; triangle is its three corner vertices.
type vec3 [3]float64
type triangle [3]vec3

// mesh is the parsed form of either STL variant. Facet normals are dropped on
// parse: they are redundant (recomputable from the vertices) and frequently
// wrong in files found in the wild.
type mesh struct {
	Name      string // ASCII `solid <name>`; always "" for binary
	Triangles []triangle
}

// meshStats is the statistical summary the diff actually compares — STL has
// no object identity, so per-side aggregate geometry is the semantic unit.
type meshStats struct {
	Triangles int
	Min, Max  vec3
	Area      float64
	Volume    float64
	Name      string
}

// isASCII sniffs the variant: ASCII STL starts with the "solid" keyword AND
// mentions "facet normal" within the first KB. The second condition matters
// because binary files may legally start their free-form 80-byte header with
// the word "solid" too.
func isASCII(blob Blob) bool {
	head := blob
	if len(head) > 1024 {
		head = head[:1024]
	}
	s := string(head)
	return strings.HasPrefix(strings.TrimLeft(s, " \t\r\n"), "solid") &&
		strings.Contains(s, "facet normal")
}

func parseSTL(blob Blob) (*mesh, error) {
	if isASCII(blob) {
		return parseASCII(blob)
	}
	return parseBinary(blob)
}

// parseASCII reads the `solid … facet normal … vertex …` text form. It is
// lenient about indentation, keyword case, and a missing final `endsolid`,
// but rejects malformed vertices and unknown tokens with a clean error.
func parseASCII(blob Blob) (*mesh, error) {
	m := &mesh{}
	var cur []vec3
	seenSolid := false
	sc := bufio.NewScanner(bytes.NewReader(blob))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "solid":
			if !seenSolid {
				seenSolid = true
				m.Name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "solid"))
			}
		case "vertex":
			if len(fields) != 4 {
				return nil, fmt.Errorf("line %d: vertex needs 3 coordinates, got %d", line, len(fields)-1)
			}
			var v vec3
			for i := 0; i < 3; i++ {
				f, err := strconv.ParseFloat(fields[i+1], 64)
				if err != nil {
					return nil, fmt.Errorf("line %d: bad vertex coordinate %q", line, fields[i+1])
				}
				v[i] = f
			}
			cur = append(cur, v)
			if len(cur) > 3 {
				return nil, fmt.Errorf("line %d: more than 3 vertices in a facet", line)
			}
		case "endfacet":
			if len(cur) != 3 {
				return nil, fmt.Errorf("line %d: facet has %d vertices, want 3", line, len(cur))
			}
			m.Triangles = append(m.Triangles, triangle{cur[0], cur[1], cur[2]})
			cur = cur[:0]
		case "facet", "outer", "endloop", "endsolid":
			// Facet normals are ignored (recomputable, often wrong in the
			// wild); loop delimiters carry no data.
		default:
			return nil, fmt.Errorf("line %d: unexpected token %q in ASCII STL", line, fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading ASCII STL: %w", err)
	}
	if !seenSolid {
		return nil, fmt.Errorf("ASCII STL is missing its 'solid' header")
	}
	if len(cur) != 0 {
		return nil, fmt.Errorf("unterminated facet at end of ASCII STL")
	}
	return m, nil
}

const (
	binHeaderLen   = 80 // free-form header, no meaning
	binTriangleLen = 50 // 12 float32 (normal + 3 vertices) + uint16 attribute
)

// parseBinary reads the binary form: an 80-byte header, a uint32 LE triangle
// count, then count fixed 50-byte records. The byte length must match the
// declared count exactly — a truncated or padded file is a clean error.
func parseBinary(blob Blob) (*mesh, error) {
	if len(blob) < binHeaderLen+4 {
		return nil, fmt.Errorf("binary STL too short: %d bytes, need at least %d for header + triangle count", len(blob), binHeaderLen+4)
	}
	count := binary.LittleEndian.Uint32(blob[binHeaderLen : binHeaderLen+4])
	want := uint64(binHeaderLen+4) + uint64(count)*binTriangleLen
	if uint64(len(blob)) != want {
		return nil, fmt.Errorf("binary STL length mismatch: header declares %d triangles (%d bytes), file has %d bytes", count, want, len(blob))
	}
	m := &mesh{Triangles: make([]triangle, 0, count)}
	off := binHeaderLen + 4
	for i := uint32(0); i < count; i++ {
		// rec[0:12] is the facet normal (ignored, see mesh doc);
		// rec[48:50] is the attribute byte count (unused by most software).
		rec := blob[off : off+binTriangleLen]
		var t triangle
		for v := 0; v < 3; v++ {
			for c := 0; c < 3; c++ {
				bits := binary.LittleEndian.Uint32(rec[12+v*12+c*4:])
				t[v][c] = float64(math.Float32frombits(bits))
			}
		}
		m.Triangles = append(m.Triangles, t)
		off += binTriangleLen
	}
	return m, nil
}

func sub(a, b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func cross(a, b vec3) vec3 {
	return vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func dot(a, b vec3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func norm(a vec3) float64 { return math.Sqrt(dot(a, a)) }

// computeStats derives the aggregate geometry of a mesh: triangle count,
// axis-aligned bounding box, total surface area, and approximate enclosed
// volume — abs of the summed scalar triple products / 6 (divergence theorem;
// exact for a closed, consistently-oriented mesh, approximate otherwise).
func computeStats(m *mesh) meshStats {
	s := meshStats{Triangles: len(m.Triangles), Name: m.Name}
	if len(m.Triangles) == 0 {
		return s
	}
	s.Min, s.Max = m.Triangles[0][0], m.Triangles[0][0]
	var signedVol float64
	for _, t := range m.Triangles {
		for _, v := range t {
			for c := 0; c < 3; c++ {
				s.Min[c] = math.Min(s.Min[c], v[c])
				s.Max[c] = math.Max(s.Max[c], v[c])
			}
		}
		s.Area += 0.5 * norm(cross(sub(t[1], t[0]), sub(t[2], t[0])))
		signedVol += dot(t[0], cross(t[1], t[2]))
	}
	s.Volume = math.Abs(signedVol) / 6
	return s
}

// relTol is the relative tolerance for surface area and volume comparisons:
// sides within 1e-6 of each other are unchanged, so floating-point jitter
// (re-exports, ASCII↔binary round-trips) does not read as a change.
const relTol = 1e-6

func approxEqual(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= relTol*math.Max(math.Abs(a), math.Abs(b))
}

// f4 prints a float rounded to 4 decimals — enough to describe print-scale
// geometry without leaking floating-point noise into the diff.
func f4(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

func (s meshStats) boundsString() string {
	if s.Triangles == 0 {
		return "(none)"
	}
	return fmt.Sprintf("[%s %s %s] – [%s %s %s]",
		f4(s.Min[0]), f4(s.Min[1]), f4(s.Min[2]),
		f4(s.Max[0]), f4(s.Max[1]), f4(s.Max[2]))
}

func (s meshStats) summary() string {
	return fmt.Sprintf("%d triangles, area %s, volume %s, bounds %s",
		s.Triangles, f4(s.Area), f4(s.Volume), s.boundsString())
}

func meshLabel(s meshStats) string {
	if s.Name != "" {
		return "mesh " + s.Name
	}
	return "mesh"
}

// Diff produces a geometric/statistical diff of two STL blobs. STL has no
// object identity to match entities by, so the comparison is between each
// side's aggregate statistics (see meshStats). An empty blob on either side
// is the added/deleted-file case: one added/removed entry carrying the other
// side's statistics.
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	changes := []DiffChange{} // non-nil so an empty diff marshals as [] not null

	switch {
	case len(base) == 0 && len(head) == 0:
		// Nothing on either side; nothing to compare.
	case len(base) == 0:
		m, err := parseSTL(head)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("head: %w", err)
		}
		s := computeStats(m)
		changes = append(changes, DiffChange{Path: "mesh", Kind: Added, Label: meshLabel(s), After: s.summary()})
	case len(head) == 0:
		m, err := parseSTL(base)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("base: %w", err)
		}
		s := computeStats(m)
		changes = append(changes, DiffChange{Path: "mesh", Kind: Removed, Label: meshLabel(s), Before: s.summary()})
	default:
		bm, err := parseSTL(base)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("base: %w", err)
		}
		hm, err := parseSTL(head)
		if err != nil {
			return StructuredDiff{}, fmt.Errorf("head: %w", err)
		}
		changes = append(changes, diffStats(computeStats(bm), computeStats(hm))...)
	}

	return StructuredDiff{Version: "1.0", Format: "stl", Changes: changes}, nil
}

// diffStats compares the two sides' statistics. Triangle counts compare
// exactly; bounds compare by their printed 4-decimal form; surface area and
// volume compare within relTol so FP jitter is not a change.
func diffStats(a, b meshStats) []DiffChange {
	var ch []DiffChange
	if a.Triangles != b.Triangles {
		ch = append(ch, DiffChange{Path: "triangles", Kind: Modified, Label: "triangle count", Before: a.Triangles, After: b.Triangles})
	}
	if ab, bb := a.boundsString(), b.boundsString(); ab != bb {
		ch = append(ch, DiffChange{Path: "bounds", Kind: Modified, Label: "bounding box", Before: ab, After: bb})
	}
	if !approxEqual(a.Area, b.Area) {
		ch = append(ch, DiffChange{Path: "surface_area", Kind: Modified, Label: "surface area", Before: f4(a.Area), After: f4(b.Area)})
	}
	if !approxEqual(a.Volume, b.Volume) {
		ch = append(ch, DiffChange{Path: "volume", Kind: Modified, Label: "volume", Before: f4(a.Volume), After: f4(b.Volume)})
	}
	if a.Name != b.Name {
		c := DiffChange{Path: "name", Kind: Modified, Label: "solid name", Before: a.Name, After: b.Name}
		switch {
		case a.Name == "":
			c.Kind, c.Before = Added, nil
		case b.Name == "":
			c.Kind, c.After = Removed, nil
		}
		ch = append(ch, c)
	}
	return ch
}

// Merge is not supported for STL: a triangle soup has no object identity, so
// there is no meaningful semantic 3-way merge. Forge falls back to blob-pick,
// same as plain git.
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not supported for stl")
}
