package main

// Regression tests for the diff defects catalogued in issue #41: silent
// data loss (duplicate names, invisible re-parents, undiffed textures and
// keyframes) and unusable change paths (relative, and ambiguous when a glTF
// name contains the separator).
//
// The whole file is toolchain-agnostic on purpose: the same tests run against
// the native (subprocess) build and against the wasm build, which is what keeps
// the two entry points honest — both call the same Handler.Diff.
//
//	go test ./...
//	GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./...

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// diffOf runs a diff and fails the test on error.
func diffOf(t *testing.T, base, head []byte) StructuredDiff {
	t.Helper()
	d, err := (&Handler{}).Diff(base, head)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	return d
}

// findChange returns the change at an exact path, searching the whole tree.
func findChange(d StructuredDiff, path string) *DiffChange {
	var hit *DiffChange
	walk(d.Changes, func(c *DiffChange, _ int) {
		if hit == nil && c.Path == path {
			hit = c
		}
	})
	return hit
}

// paths lists every path in the tree, in depth-first order.
func paths(d StructuredDiff) []string {
	var out []string
	walk(d.Changes, func(c *DiffChange, _ int) { out = append(out, c.Path) })
	return out
}

func walk(changes []DiffChange, fn func(c *DiffChange, depth int)) {
	var rec func([]DiffChange, int)
	rec = func(cs []DiffChange, depth int) {
		for i := range cs {
			fn(&cs[i], depth)
			rec(cs[i].Children, depth+1)
		}
	}
	rec(changes, 0)
}

// mustChange asserts a change exists at path and returns it.
func mustChange(t *testing.T, d StructuredDiff, path string) *DiffChange {
	t.Helper()
	c := findChange(d, path)
	if c == nil {
		got := paths(d)
		sort.Strings(got)
		t.Fatalf("no change at %q; diff contains:\n  %s", path, strings.Join(got, "\n  "))
	}
	return c
}

// doc marshals a glTF document written as a Go map, so tests can express one
// small scene without hand-escaping JSON.
func doc(t *testing.T, d map[string]any) []byte {
	t.Helper()
	if _, ok := d["asset"]; !ok {
		d["asset"] = map[string]any{"version": "2.0"}
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshalling test document: %v", err)
	}
	return b
}

func nodesDoc(t *testing.T, nodes ...map[string]any) []byte {
	t.Helper()
	roots := make([]int, len(nodes))
	for i := range nodes {
		roots[i] = i
	}
	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": roots}},
		"nodes":  nodes,
	})
}

// ── defect 1: duplicate names must not be silently dropped ────────────────────

func TestDiffDuplicateNodeNamesBothParticipate(t *testing.T) {
	base := nodesDoc(t,
		map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}},
		map[string]any{"name": "Wheel", "translation": []float64{-1, 0, 0}},
	)
	// Only the second Wheel moves. Before the fix the second element never made
	// it into the map, so this diffed as no change whatsoever.
	head := nodesDoc(t,
		map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}},
		map[string]any{"name": "Wheel", "translation": []float64{-1, 5, 0}},
	)

	d := diffOf(t, base, head)
	c := mustChange(t, d, "nodes/Wheel#1/translation")
	if c.Before == c.After {
		t.Fatalf("expected a translation change on the second Wheel, got %+v", c)
	}
	if findChange(d, "nodes/Wheel/translation") != nil {
		t.Error("the first Wheel did not move; it must not appear as changed")
	}
	// Label stays human-readable — the disambiguator is part of the identity, and
	// never a percent-escaped form.
	if lbl := mustChange(t, d, "nodes/Wheel#1").Label; lbl != "Wheel#1" {
		t.Errorf("label = %q, want %q", lbl, "Wheel#1")
	}
}

// Every collection keyed by name had the same drop; each must now report both
// elements. The removed side is the clearest probe: delete the collection
// wholesale and count what the diff reports.
func TestDiffDuplicateNamesAcrossCollections(t *testing.T) {
	tests := []struct {
		name     string
		document map[string]any
		want     []string
	}{
		{
			name: "nodes",
			document: map[string]any{
				"nodes": []any{
					map[string]any{"name": "Wheel"},
					map[string]any{"name": "Wheel"},
				},
			},
			want: []string{"nodes/Wheel", "nodes/Wheel#1"},
		},
		{
			name: "materials",
			document: map[string]any{
				"materials": []any{
					map[string]any{"name": "Paint"},
					map[string]any{"name": "Paint"},
				},
			},
			want: []string{"materials/Paint", "materials/Paint#1"},
		},
		{
			name: "meshes",
			document: map[string]any{
				"meshes": []any{
					map[string]any{"name": "Body", "primitives": []any{map[string]any{"attributes": map[string]any{}}}},
					map[string]any{"name": "Body", "primitives": []any{map[string]any{"attributes": map[string]any{}}}},
				},
			},
			want: []string{"meshes/Body", "meshes/Body#1"},
		},
		{
			name: "animations",
			document: map[string]any{
				"nodes":      []any{map[string]any{"name": "Cube"}},
				"animations": []any{spinAnimation("Spin"), spinAnimation("Spin")},
			},
			want: []string{"animations/Spin", "animations/Spin#1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := doc(t, tc.document)
			d := diffOf(t, base, nil) // deleted file: everything is removed
			for _, want := range tc.want {
				c := mustChange(t, d, want)
				if c.Kind != Removed {
					t.Errorf("%s: kind = %q, want %q", want, c.Kind, Removed)
				}
			}
		})
	}
}

// spinAnimation is a minimal animation with no samplers; enough to exist in the
// animations array for key-collision purposes.
func spinAnimation(name string) map[string]any {
	return map[string]any{
		"name":     name,
		"channels": []any{},
		"samplers": []any{},
	}
}

// A name that already ends in the disambiguator must not collide with a
// generated key.
func TestDiffDuplicateNameCollidingWithDisambiguator(t *testing.T) {
	base := nodesDoc(t,
		map[string]any{"name": "Wheel"},
		map[string]any{"name": "Wheel#1"},
		map[string]any{"name": "Wheel"},
	)
	d := diffOf(t, base, nil)
	for _, want := range []string{"nodes/Wheel", "nodes/Wheel#1", "nodes/Wheel#2"} {
		mustChange(t, d, want)
	}
}

// ── defect 2: re-parenting must be visible ────────────────────────────────────

func TestDiffReparentIsVisible(t *testing.T) {
	// Body(0) → Mirror_L(2); Door_L(1) is a sibling.
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body", "children": []int{2}},
			map[string]any{"name": "Door_L"},
			map[string]any{"name": "Mirror_L"},
		},
	})
	// The mirror moves onto the door. Nothing else changes: no transform, no
	// mesh, no name — the flat node walk saw nothing at all before this fix.
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body"},
			map[string]any{"name": "Door_L", "children": []int{2}},
			map[string]any{"name": "Mirror_L"},
		},
	})

	d := diffOf(t, base, head)
	c := mustChange(t, d, "nodes/Mirror_L/parent")
	if c.Before != "Body" || c.After != "Door_L" {
		t.Errorf("parent change = %v → %v, want Body → Door_L", c.Before, c.After)
	}
}

func TestDiffParentReportedForRootAndAddedNodes(t *testing.T) {
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes":  []any{map[string]any{"name": "Body", "children": []int{1}}, map[string]any{"name": "Mirror_L"}},
	})
	// Mirror_L is detached to the top level, and a new child appears under Body.
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body", "children": []int{2}},
			map[string]any{"name": "Mirror_L"},
			map[string]any{"name": "Badge"},
		},
	})

	d := diffOf(t, base, head)
	if c := mustChange(t, d, "nodes/Mirror_L/parent"); c.After != rootParentLabel {
		t.Errorf("detached node parent = %v, want %v", c.After, rootParentLabel)
	}
	if c := mustChange(t, d, "nodes/Badge/parent"); c.After != "Body" {
		t.Errorf("added node parent = %v, want Body", c.After)
	}
}

// A malformed hierarchy (dangling child index, self-parenting, two parents
// claiming one node) must not break the diff.
func TestDiffMalformedHierarchyDoesNotBreakDiff(t *testing.T) {
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			map[string]any{"name": "A", "children": []int{0, 2, 99}},
			map[string]any{"name": "B", "children": []int{2}},
			map[string]any{"name": "C"},
		},
	})
	head := nodesDoc(t, map[string]any{"name": "A"})
	d := diffOf(t, base, head) // must not panic
	if len(d.Changes) == 0 {
		t.Fatal("expected changes")
	}
}

// ── defect 3: unnamed elements no longer cascade on insertion (#42) ────────────

// TestDiffUnnamedInsertionPairsByContent is the flipped pin of what used to be
// TestDiffUnnamedInsertionCascadeIsAKnownLimitation. Unnamed nodes are keyed by
// array index (node[0], node[1], …), so inserting one node upstream re-indexes
// every node after it; matching on those keys reported a wall of false
// modifications plus a spurious add at the wrong index. The exact-content tier
// (identity.go, matchByExactContent) now pairs the survivors by their stated
// content, so the diff is the one thing that happened: a single added node,
// reported where it was inserted.
func TestDiffUnnamedInsertionPairsByContent(t *testing.T) {
	base := nodesDoc(t,
		map[string]any{"translation": []float64{1, 0, 0}},
		map[string]any{"translation": []float64{2, 0, 0}},
	)
	head := nodesDoc(t,
		map[string]any{"translation": []float64{9, 9, 9}}, // inserted at the front
		map[string]any{"translation": []float64{1, 0, 0}},
		map[string]any{"translation": []float64{2, 0, 0}},
	)

	d := diffOf(t, base, head)

	if c := mustChange(t, d, "nodes/node[0]"); c.Kind != Added {
		t.Errorf("node[0]: kind = %q, want %q", c.Kind, Added)
	}
	// And NOTHING else in the nodes collection: the two survivors paired with
	// their identical selves and vanished from the diff.
	nodes := mustChange(t, d, "nodes")
	if len(nodes.Children) != 1 {
		t.Errorf("the insertion is the only change; nodes reported %d: %v", len(nodes.Children), paths(d))
	}
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Removed || c.Kind == Renamed {
			t.Errorf("an insertion removed and renamed nothing: %+v", c)
		}
	})
}

// ── defects 4 & 5: fully-qualified, unambiguous paths ─────────────────────────

func TestDiffChildPathsAreFullyQualified(t *testing.T) {
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes":  []any{map[string]any{"name": "Cube", "translation": []float64{5, 0, -2}}},
		"materials": []any{map[string]any{
			"name":                 "Paint",
			"pbrMetallicRoughness": map[string]any{"baseColorFactor": []float64{1, 0, 0, 1}},
		}},
		"meshes": []any{map[string]any{"name": "Body", "primitives": []any{map[string]any{"attributes": map[string]any{}}}}},
	})
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes":  []any{map[string]any{"name": "Cube", "translation": []float64{9, -9, 9}}},
		"materials": []any{map[string]any{
			"name":                 "Paint",
			"pbrMetallicRoughness": map[string]any{"baseColorFactor": []float64{0, 0, 1, 1}},
		}},
		"meshes": []any{map[string]any{"name": "Body", "primitives": []any{
			map[string]any{"attributes": map[string]any{}},
			map[string]any{"attributes": map[string]any{}},
		}}},
	})

	d := diffOf(t, base, head)

	// Every child path must be qualified by its parent — the whole point is that
	// a consumer can use a row's path as a selection key verbatim.
	for _, want := range []string{
		"nodes/Cube/translation",
		"materials/Paint/baseColorFactor",
		"meshes/Body/primitives",
	} {
		mustChange(t, d, want)
	}

	// Animations need real keyframe data, so they get their own document.
	times := []float32{0, 1}
	anim := diffOf(t,
		animationDoc(t, times, [][3]float32{{0, 0, 0}, {1, 0, 0}}, 0, "LINEAR"),
		animationDoc(t, times, [][3]float32{{0, 0, 0}, {2, 0, 0}}, 0, "LINEAR"),
	)
	mustChange(t, anim, "animations/Spin/channels/0/output")

	// And no change anywhere may carry a bare, unqualified path.
	for _, sd := range []StructuredDiff{d, anim} {
		walk(sd.Changes, func(c *DiffChange, depth int) {
			if depth > 0 && !strings.Contains(c.Path, pathSep) {
				t.Errorf("nested change %q (label %q) has an unqualified path", c.Path, c.Label)
			}
		})
	}
}

func TestDiffPathsRemainUnambiguousForAwkwardNames(t *testing.T) {
	tests := []struct {
		name     string
		nodeName string
		wantPath string
	}{
		// The separator used to be ".", which made a Blender name unparseable.
		{"dot in name", "Cube.001", "nodes/Cube.001/translation"},
		{"slash in name", "rig/hand", "nodes/rig%2Fhand/translation"},
		{"percent in name", "50%", "nodes/50%25/translation"},
		{"separator soup", "a/b%c.d", "nodes/a%2Fb%25c.d/translation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := nodesDoc(t, map[string]any{"name": tc.nodeName, "translation": []float64{1, 0, 0}})
			head := nodesDoc(t, map[string]any{"name": tc.nodeName, "translation": []float64{2, 0, 0}})
			d := diffOf(t, base, head)

			mustChange(t, d, tc.wantPath)
			// The escaped form is a machine key only: labels stay raw for display.
			if lbl := mustChange(t, d, joinPath("nodes", tc.nodeName)).Label; lbl != tc.nodeName {
				t.Errorf("label = %q, want the raw name %q", lbl, tc.nodeName)
			}
			// A path is parseable back into its segments.
			got := splitPath(tc.wantPath)
			want := []string{"nodes", tc.nodeName, "translation"}
			if len(got) != len(want) {
				t.Fatalf("splitPath(%q) = %q, want %q", tc.wantPath, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("segment %d = %q, want %q", i, got[i], want[i])
				}
			}
		})
	}
}

func TestPathSegmentEscapeRoundTrip(t *testing.T) {
	for _, s := range []string{"", "Cube", "Cube.001", "a/b", "100%", "%2F", "%25", "a%2Fb", "/", "%", "%%//", "ünïcode"} {
		if got := unescapeSegment(escapeSegment(s)); got != s {
			t.Errorf("round trip of %q = %q", s, got)
		}
		if e := escapeSegment(s); strings.Contains(e, pathSep) {
			t.Errorf("escaped %q = %q still contains the separator", s, e)
		}
	}
}

// ── defect 6: texture reference changes ───────────────────────────────────────

// materialDoc builds a one-material document whose base colour texture points at
// the given image URI through a sampler with the given wrap mode.
func materialDoc(t *testing.T, imageURI string, wrapS int) []byte {
	t.Helper()
	return doc(t, map[string]any{
		"scene":    0,
		"scenes":   []any{map[string]any{"nodes": []int{0}}},
		"nodes":    []any{map[string]any{"name": "Cube"}},
		"images":   []any{map[string]any{"uri": imageURI}},
		"samplers": []any{map[string]any{"wrapS": wrapS, "wrapT": 10497}},
		"textures": []any{map[string]any{"source": 0, "sampler": 0}},
		"materials": []any{map[string]any{
			"name": "Paint",
			"pbrMetallicRoughness": map[string]any{
				"baseColorFactor":  []float64{1, 1, 1, 1},
				"baseColorTexture": map[string]any{"index": 0},
			},
		}},
	})
}

func TestDiffTextureReferenceChanges(t *testing.T) {
	const repeat, clamp = 10497, 33071

	t.Run("retexture with a different image", func(t *testing.T) {
		d := diffOf(t, materialDoc(t, "paint_red.png", repeat), materialDoc(t, "paint_blue.png", repeat))
		c := mustChange(t, d, "materials/Paint/baseColorTexture")
		if !strings.Contains(fmt.Sprint(c.Before), "paint_red.png") || !strings.Contains(fmt.Sprint(c.After), "paint_blue.png") {
			t.Errorf("texture change = %v → %v, want the image URIs", c.Before, c.After)
		}
	})

	t.Run("sampler change on the same image", func(t *testing.T) {
		d := diffOf(t, materialDoc(t, "paint_red.png", repeat), materialDoc(t, "paint_red.png", clamp))
		mustChange(t, d, "materials/Paint/baseColorTexture")
	})

	t.Run("identical materials produce no diff", func(t *testing.T) {
		d := diffOf(t, materialDoc(t, "paint_red.png", repeat), materialDoc(t, "paint_red.png", repeat))
		if len(d.Changes) != 0 {
			t.Errorf("expected no changes, got %v", paths(d))
		}
	})

	t.Run("texture added and removed", func(t *testing.T) {
		plain := doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes":  []any{map[string]any{"name": "Cube"}},
			"materials": []any{map[string]any{
				"name":                 "Paint",
				"pbrMetallicRoughness": map[string]any{"baseColorFactor": []float64{1, 1, 1, 1}},
			}},
		})
		textured := materialDoc(t, "paint_red.png", repeat)

		added := mustChange(t, diffOf(t, plain, textured), "materials/Paint/baseColorTexture")
		if added.Before != noTexture {
			t.Errorf("before = %v, want %q", added.Before, noTexture)
		}
		removed := mustChange(t, diffOf(t, textured, plain), "materials/Paint/baseColorTexture")
		if removed.After != noTexture {
			t.Errorf("after = %v, want %q", removed.After, noTexture)
		}
	})

	t.Run("embedded image bytes change", func(t *testing.T) {
		embedded := func(pixels string) []byte {
			return materialDoc(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte(pixels)), repeat)
		}
		d := diffOf(t, embedded("red-pixels"), embedded("blue-pixels"))
		c := mustChange(t, d, "materials/Paint/baseColorTexture")
		if !strings.Contains(fmt.Sprint(c.Before), "mime=image/png") {
			t.Errorf("expected the mime type in %v", c.Before)
		}
		if c.Before == c.After {
			t.Error("embedded image bytes changed but the descriptor did not")
		}
	})

	t.Run("every texture slot is covered", func(t *testing.T) {
		want := []string{"baseColorTexture", "metallicRoughnessTexture", "normalTexture", "occlusionTexture", "emissiveTexture"}
		got := make([]string, 0, len(textureSlots))
		for _, s := range textureSlots {
			got = append(got, s.label)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("texture slots = %v, want %v", got, want)
		}
	})

	t.Run("non-pbr slots diff too", func(t *testing.T) {
		withNormal := func(scale float64) []byte {
			return doc(t, map[string]any{
				"scene":    0,
				"scenes":   []any{map[string]any{"nodes": []int{0}}},
				"nodes":    []any{map[string]any{"name": "Cube"}},
				"images":   []any{map[string]any{"uri": "normal.png"}},
				"textures": []any{map[string]any{"source": 0}},
				"materials": []any{map[string]any{
					"name":          "Paint",
					"normalTexture": map[string]any{"index": 0, "scale": scale},
				}},
			})
		}
		c := mustChange(t, diffOf(t, withNormal(1), withNormal(2)), "materials/Paint/normalTexture")
		if c.Before == c.After {
			t.Errorf("normal scale change not reported: %+v", c)
		}
	})

	t.Run("dangling references degrade instead of panicking", func(t *testing.T) {
		dangling := doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes":  []any{map[string]any{"name": "Cube"}},
			"materials": []any{map[string]any{
				"name": "Paint",
				"pbrMetallicRoughness": map[string]any{
					"baseColorTexture": map[string]any{"index": 7},
				},
			}},
		})
		d := diffOf(t, dangling, materialDoc(t, "paint_red.png", repeat))
		if c := mustChange(t, d, "materials/Paint/baseColorTexture"); !strings.Contains(fmt.Sprint(c.Before), "dangling") {
			t.Errorf("before = %v, want a dangling-reference note", c.Before)
		}
	})
}

// ── defect 7: animation keyframes, not just channel counts ────────────────────

// animationDoc is a complete document with one animation over real keyframe
// data, so the accessor bytes are actually readable.
func animationDoc(t *testing.T, times []float32, values [][3]float32, targetNode int, interpolation string) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, v := range times {
		if err := binary.Write(&buf, binary.LittleEndian, math.Float32bits(v)); err != nil {
			t.Fatal(err)
		}
	}
	inputLen := buf.Len()
	for _, v := range values {
		for _, c := range v {
			if err := binary.Write(&buf, binary.LittleEndian, math.Float32bits(c)); err != nil {
				t.Fatal(err)
			}
		}
	}
	data := buf.Bytes()

	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Cube"},
			map[string]any{"name": "Lamp"},
		},
		"buffers": []any{map[string]any{
			"byteLength": len(data),
			"uri":        "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(data),
		}},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": inputLen},
			map[string]any{"buffer": 0, "byteOffset": inputLen, "byteLength": len(data) - inputLen},
		},
		"accessors": []any{
			map[string]any{"bufferView": 0, "componentType": 5126, "count": len(times), "type": "SCALAR"},
			map[string]any{"bufferView": 1, "componentType": 5126, "count": len(values), "type": "VEC3"},
		},
		"animations": []any{map[string]any{
			"name": "Spin",
			"channels": []any{map[string]any{
				"sampler": 0,
				"target":  map[string]any{"node": targetNode, "path": "translation"},
			}},
			"samplers": []any{map[string]any{"input": 0, "output": 1, "interpolation": interpolation}},
		}},
	})
}

func TestDiffAnimationKeyframeChanges(t *testing.T) {
	times := []float32{0, 0.5, 1}
	values := [][3]float32{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}

	t.Run("value edit with an unchanged channel count", func(t *testing.T) {
		edited := [][3]float32{{0, 0, 0}, {1, 0, 0}, {5, 0, 0}}
		d := diffOf(t, animationDoc(t, times, values, 0, "LINEAR"), animationDoc(t, times, edited, 0, "LINEAR"))
		c := mustChange(t, d, "animations/Spin/channels/0/output")
		if c.Before == c.After {
			t.Errorf("output stream change not reported: %+v", c)
		}
		if !strings.Contains(fmt.Sprint(c.Before), "hash=") {
			t.Errorf("expected a content hash in %v", c.Before)
		}
		// The channel wrapper carries a display label and a qualified path.
		if got := mustChange(t, d, "animations/Spin/channels/0").Label; got != "channel[0]" {
			t.Errorf("channel label = %q, want channel[0]", got)
		}
	})

	t.Run("retiming edits the input stream", func(t *testing.T) {
		retimed := []float32{0, 0.9, 1}
		d := diffOf(t, animationDoc(t, times, values, 0, "LINEAR"), animationDoc(t, retimed, values, 0, "LINEAR"))
		mustChange(t, d, "animations/Spin/channels/0/input")
	})

	t.Run("channel repointed at another node", func(t *testing.T) {
		d := diffOf(t, animationDoc(t, times, values, 0, "LINEAR"), animationDoc(t, times, values, 1, "LINEAR"))
		c := mustChange(t, d, "animations/Spin/channels/0/target")
		if c.Before != "Cube.translation" || c.After != "Lamp.translation" {
			t.Errorf("target change = %v → %v, want Cube.translation → Lamp.translation", c.Before, c.After)
		}
	})

	t.Run("interpolation change", func(t *testing.T) {
		d := diffOf(t, animationDoc(t, times, values, 0, "LINEAR"), animationDoc(t, times, values, 0, "STEP"))
		c := mustChange(t, d, "animations/Spin/channels/0/interpolation")
		if c.Before != "LINEAR" || c.After != "STEP" {
			t.Errorf("interpolation change = %v → %v, want LINEAR → STEP", c.Before, c.After)
		}
	})

	t.Run("identical animations produce no diff", func(t *testing.T) {
		d := diffOf(t, animationDoc(t, times, values, 0, "LINEAR"), animationDoc(t, times, values, 0, "LINEAR"))
		if len(d.Changes) != 0 {
			t.Errorf("expected no changes, got %v", paths(d))
		}
	})

	t.Run("channel count change is still reported", func(t *testing.T) {
		base := animationDoc(t, times, values, 0, "LINEAR")
		var m map[string]any
		if err := json.Unmarshal(base, &m); err != nil {
			t.Fatal(err)
		}
		anim := m["animations"].([]any)[0].(map[string]any)
		ch := anim["channels"].([]any)
		anim["channels"] = append(ch, map[string]any{
			"sampler": 0,
			"target":  map[string]any{"node": 1, "path": "scale"},
		})
		head, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		c := mustChange(t, diffOf(t, base, head), "animations/Spin/channels")
		if c.Before != "1" || c.After != "2" {
			t.Errorf("channel count = %v → %v, want 1 → 2", c.Before, c.After)
		}
	})

	t.Run("unreadable accessor data degrades honestly", func(t *testing.T) {
		// An external buffer URI: there is no filesystem to read it from, so the
		// bytes are unavailable and the label says so rather than claiming equality.
		external := doc(t, map[string]any{
			"scene":       0,
			"scenes":      []any{map[string]any{"nodes": []int{0}}},
			"nodes":       []any{map[string]any{"name": "Cube"}},
			"buffers":     []any{map[string]any{"byteLength": 48, "uri": "scene.bin"}},
			"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 48}},
			"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "SCALAR"}},
			"animations": []any{map[string]any{
				"name":     "Spin",
				"channels": []any{map[string]any{"sampler": 0, "target": map[string]any{"node": 0, "path": "translation"}}},
				"samplers": []any{map[string]any{"input": 0, "output": 0}},
			}},
		})
		d := diffOf(t, external, animationDoc(t, times, values, 0, "LINEAR"))
		c := mustChange(t, d, "animations/Spin/channels/0/input")
		if !strings.Contains(fmt.Sprint(c.Before), "unreadable") {
			t.Errorf("before = %v, want an unreadable-data note", c.Before)
		}
	})
}

// ── merge: the disambiguated keys have to survive the round trip ──────────────

func TestMergeConflictPathsUseTheSameScheme(t *testing.T) {
	base := nodesDoc(t, map[string]any{"name": "Cube.001", "translation": []float64{0, 0, 0}})
	ours := nodesDoc(t, map[string]any{"name": "Cube.001", "translation": []float64{1, 0, 0}})
	theirs := nodesDoc(t, map[string]any{"name": "Cube.001", "translation": []float64{2, 0, 0}})

	h := &Handler{}
	merged, ci, err := h.Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if ci == nil || len(ci.Conflicts) != 1 {
		t.Fatalf("expected one conflict, got %+v", ci)
	}
	want := "nodes/Cube.001/translation"
	if ci.Conflicts[0].Path != want {
		t.Fatalf("conflict path = %q, want %q", ci.Conflicts[0].Path, want)
	}

	// Taking theirs at that path must resolve to their value: the path the
	// handler emitted has to be the path the handler accepts back.
	resolved, err := h.ApplyChoices(merged, theirs, []string{want})
	if err != nil {
		t.Fatal(err)
	}
	d := diffOf(t, resolved, theirs)
	if len(d.Changes) != 0 {
		t.Errorf("resolved blob still differs from theirs: %v", paths(d))
	}
}

func TestApplyChoicesResolvesEscapedNames(t *testing.T) {
	name := "rig/hand"
	base := nodesDoc(t, map[string]any{"name": name, "translation": []float64{0, 0, 0}})
	ours := nodesDoc(t, map[string]any{"name": name, "translation": []float64{1, 0, 0}})
	theirs := nodesDoc(t, map[string]any{"name": name, "translation": []float64{2, 0, 0}})

	h := &Handler{}
	merged, ci, err := h.Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if ci == nil || len(ci.Conflicts) == 0 {
		t.Fatal("expected a conflict")
	}
	if got, want := ci.Conflicts[0].Path, "nodes/rig%2Fhand/translation"; got != want {
		t.Fatalf("conflict path = %q, want %q", got, want)
	}
	resolved, err := h.ApplyChoices(merged, theirs, []string{ci.Conflicts[0].Path})
	if err != nil {
		t.Fatal(err)
	}
	if d := diffOf(t, resolved, theirs); len(d.Changes) != 0 {
		t.Errorf("resolved blob still differs from theirs: %v", paths(d))
	}
}

// A second element with a duplicate name used to vanish from the merge as well:
// it was neither compared nor conflict-reported.
func TestMergeDuplicateNamesAreCompared(t *testing.T) {
	scene := func(second []float64) []byte {
		return nodesDoc(t,
			map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}},
			map[string]any{"name": "Wheel", "translation": second},
		)
	}
	_, ci, err := (&Handler{}).Merge(scene([]float64{0, 0, 0}), scene([]float64{5, 0, 0}), scene([]float64{9, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if ci == nil || len(ci.Conflicts) != 1 {
		t.Fatalf("expected one conflict on the second Wheel, got %+v", ci)
	}
	if got, want := ci.Conflicts[0].Path, "nodes/Wheel#1/translation"; got != want {
		t.Errorf("conflict path = %q, want %q", got, want)
	}
}

// ── wire format ───────────────────────────────────────────────────────────────

// Both entry points (the subprocess binary in main.go and the wasm export in
// wasm.go) serialise exactly what Handler.Diff returns, so the JSON has to
// survive a round trip with its paths and labels intact.
func TestDiffJSONRoundTripPreservesPathsAndLabels(t *testing.T) {
	base := nodesDoc(t, map[string]any{"name": "Cube.001", "translation": []float64{1, 0, 0}})
	head := nodesDoc(t, map[string]any{"name": "Cube.001", "translation": []float64{2, 0, 0}})
	d := diffOf(t, base, head)

	js, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var back StructuredDiff
	if err := json.Unmarshal(js, &back); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(paths(back), ","), strings.Join(paths(d), ","); got != want {
		t.Errorf("paths after round trip = %q, want %q", got, want)
	}
	mustChange(t, back, "nodes/Cube.001/translation")
	if lbl := mustChange(t, back, "nodes/Cube.001").Label; lbl != "Cube.001" {
		t.Errorf("label after round trip = %q, want Cube.001", lbl)
	}
}
