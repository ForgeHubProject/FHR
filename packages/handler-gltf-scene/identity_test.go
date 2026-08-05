package main

// Tests for issue #47: stable identity across revisions — the authored
// extras.fhr_uid convention and the conservative content-signature fallback that
// works on files nobody has stamped.
//
// The property under test throughout is negative as often as positive: a rename
// that isn't detected costs a reviewer a delete and an add, both of which are
// true; a rename that is invented asserts a relationship between two unrelated
// objects and hides a deletion. Every "must not pair" case here is as load-bearing
// as the ones that must.
//
// Like the rest of the suite these run unchanged against the native and the wasm
// build, and build their documents in memory.

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/qmuntal/gltf"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// withUID decorates a test node/material/mesh map with an authored stable id.
func withUID(uid string, element map[string]any) map[string]any {
	element["extras"] = map[string]any{uidExtrasKey: uid}
	return element
}

// mustRename asserts that path is a rename of oldKey and returns the change.
func mustRename(t *testing.T, d StructuredDiff, path, oldKey string) *DiffChange {
	t.Helper()
	c := mustChange(t, d, path)
	if c.Kind != Renamed {
		t.Fatalf("%s: kind = %q, want %q", path, c.Kind, Renamed)
	}
	if c.Before != oldKey {
		t.Errorf("%s: before = %v, want %q", path, c.Before, oldKey)
	}
	return c
}

// kindAt reports the kind reported at a path, or "" when nothing is.
func kindAt(d StructuredDiff, path string) ChangeKind {
	if c := findChange(d, path); c != nil {
		return c.Kind
	}
	return ""
}

// afterText is a change's after value as a string, for the evidence assertions.
func afterText(c *DiffChange) string {
	s, _ := c.After.(string)
	return s
}

// meshedDoc is nodesDoc with two meshes in the document, so a node's mesh
// reference — the heaviest component of its content descriptor — is something a
// fixture can vary.
func meshedDoc(t *testing.T, nodes ...map[string]any) []byte {
	t.Helper()
	return meshListDoc(t, []string{"Hull", "Trim"}, nodes...)
}

// meshListDoc is meshedDoc with the mesh array spelled out, so a fixture can
// insert a mesh *upstream* of the ones its nodes draw and renumber every index
// after it — the edit that separates an index from a cross-revision key. An
// empty name leaves the mesh unnamed, which is the file that has no such key at
// all.
func meshListDoc(t *testing.T, meshNames []string, nodes ...map[string]any) []byte {
	t.Helper()
	roots := make([]int, len(nodes))
	for i := range nodes {
		roots[i] = i
	}
	primitives := []any{map[string]any{"attributes": map[string]any{}}}
	meshes := make([]any, len(meshNames))
	for i, name := range meshNames {
		mesh := map[string]any{"primitives": primitives}
		if name != "" {
			mesh["name"] = name
		}
		meshes[i] = mesh
	}
	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": roots}},
		"nodes":  nodes,
		"meshes": meshes,
	})
}

// ── the wire shape of a rename ────────────────────────────────────────────────

// A rename is reported under the name the *head* file uses, because that is the
// file a consumer is looking at and the key it will select with. The old name
// lives in `before`, never in the path.
func TestRenameIsReportedUnderTheNewName(t *testing.T) {
	base := nodesDoc(t, withUID("u-fender", map[string]any{"name": "Cube.003"}))
	head := nodesDoc(t, withUID("u-fender", map[string]any{"name": "Fender"}))

	d := diffOf(t, base, head)
	c := mustRename(t, d, "nodes/Fender", "Cube.003")
	if c.Label != "Fender" {
		t.Errorf("label = %q, want %q", c.Label, "Fender")
	}
	if len(c.Children) != 0 {
		t.Errorf("a rename with nothing else changed carries no children, got %+v", c.Children)
	}
	if findChange(d, "nodes/Cube.003") != nil {
		t.Error("the old name must not appear as a separate change")
	}
}

// ── tier 1: authored ids ──────────────────────────────────────────────────────

// The id is stronger evidence than the name, so a head node that merely inherited
// the old name is a new node — not the old one modified.
func TestStableIDBeatsName(t *testing.T) {
	base := nodesDoc(t,
		withUID("u-1", map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}}),
	)
	head := nodesDoc(t,
		withUID("u-1", map[string]any{"name": "Hub", "translation": []float64{1, 0, 0}}),
		withUID("u-2", map[string]any{"name": "Wheel", "translation": []float64{5, 0, 0}}),
	)

	d := diffOf(t, base, head)
	c := mustRename(t, d, "nodes/Hub", "Wheel")
	if !strings.Contains(afterText(c), uidExtrasKey) {
		t.Errorf("after = %q, want the %s evidence", afterText(c), uidExtrasKey)
	}
	if got := kindAt(d, "nodes/Wheel"); got != Added {
		t.Errorf("the node that took the old name: kind = %q, want %q", got, Added)
	}
}

// A rename and an edit in the same commit are one change with the edit under it,
// not two unrelated changes.
func TestRenameWithSimultaneousPropertyChange(t *testing.T) {
	base := nodesDoc(t, withUID("u-1", map[string]any{
		"name": "Cube.003", "translation": []float64{0, 0, 0},
	}))
	head := nodesDoc(t, withUID("u-1", map[string]any{
		"name": "Fender", "translation": []float64{0, 0, 1},
	}))

	d := diffOf(t, base, head)
	mustRename(t, d, "nodes/Fender", "Cube.003")
	if c := mustChange(t, d, "nodes/Fender/translation"); c.Kind != Modified {
		t.Errorf("translation: kind = %q, want %q", c.Kind, Modified)
	}
}

// An id on one side only is no evidence at all — matching falls through to the
// name, and then to content.
func TestStableIDOnOneSideOnly(t *testing.T) {
	tests := []struct {
		name       string
		base, head map[string]any
		want       ChangeKind
		wantPath   string
	}{
		{
			name:     "same name, id added",
			base:     map[string]any{"name": "Fender", "mesh": 0},
			head:     withUID("u-1", map[string]any{"name": "Fender", "mesh": 0}),
			want:     "",
			wantPath: "nodes/Fender",
		},
		{
			name:     "renamed, id on the base side only",
			base:     withUID("u-1", map[string]any{"name": "Cube.003", "mesh": 0}),
			head:     map[string]any{"name": "Fender", "mesh": 0},
			want:     Renamed,
			wantPath: "nodes/Fender",
		},
		{
			name:     "renamed, id on the head side only",
			base:     map[string]any{"name": "Cube.003", "mesh": 0},
			head:     withUID("u-1", map[string]any{"name": "Fender", "mesh": 0}),
			want:     Renamed,
			wantPath: "nodes/Fender",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := diffOf(t, meshedDoc(t, tc.base), meshedDoc(t, tc.head))
			if got := kindAt(d, tc.wantPath); got != tc.want {
				t.Errorf("%s: kind = %q, want %q", tc.wantPath, got, tc.want)
			}
		})
	}
}

// A duplicated id is untrusted past its first occurrence — uniqueKeys' rule for
// duplicated names, applied to identity — and any rename it produced says so, so
// the guess is visible rather than silent.
func TestDuplicateStableIDDegradesWithANote(t *testing.T) {
	base := nodesDoc(t,
		withUID("u-dup", map[string]any{"name": "Wheel_A", "translation": []float64{1, 0, 0}}),
		withUID("u-dup", map[string]any{"name": "Wheel_B", "translation": []float64{-1, 0, 0}}),
	)
	head := nodesDoc(t,
		withUID("u-dup", map[string]any{"name": "Hub_A", "translation": []float64{1, 0, 0}}),
	)

	d := diffOf(t, base, head)
	c := mustRename(t, d, "nodes/Hub_A", "Wheel_A")
	if !strings.Contains(afterText(c), "duplicated") {
		t.Errorf("after = %q, want it to say the id was duplicated", afterText(c))
	}
	// The second claimant keeps no identity of its own and is reported honestly.
	if got := kindAt(d, "nodes/Wheel_B"); got != Removed {
		t.Errorf("the second claimant: kind = %q, want %q", got, Removed)
	}
}

// ── tier 3: content signatures ────────────────────────────────────────────────

// The works-today half: no ids anywhere, one node renamed, everything about what
// it draws and where unchanged.
func TestRenameByContentSignature(t *testing.T) {
	base := meshedDoc(t, map[string]any{"name": "Cube.003", "mesh": 0, "translation": []float64{1, 2, 3}})
	head := meshedDoc(t, map[string]any{"name": "Fender", "mesh": 0, "translation": []float64{1, 2, 3}})

	d := diffOf(t, base, head)
	c := mustRename(t, d, "nodes/Fender", "Cube.003")
	if got := afterText(c); !strings.Contains(got, "matched by content") || strings.Contains(got, "%") {
		t.Errorf("after = %q, want an exact content match with no similarity figure", got)
	}
}

// A rename plus a move is still one node: the transform is one descriptor of
// several, so the pair clears the threshold and the move is reported under it.
func TestRenameByContentSignatureReportsApproximateSimilarity(t *testing.T) {
	base := meshedDoc(t, map[string]any{"name": "Cube.003", "mesh": 0, "translation": []float64{1, 2, 3}})
	head := meshedDoc(t, map[string]any{"name": "Fender", "mesh": 0, "translation": []float64{1, 2, 9}})

	d := diffOf(t, base, head)
	c := mustRename(t, d, "nodes/Fender", "Cube.003")
	if got := afterText(c); !strings.Contains(got, "% similar") {
		t.Errorf("after = %q, want an approximate similarity figure", got)
	}
	mustChange(t, d, "nodes/Fender/translation")
}

// Several renames in one commit each find their own counterpart: every candidate
// is scored against every other, and only a strict mutual best pairs.
func TestSeveralContentRenamesInOneCommit(t *testing.T) {
	base := meshedDoc(t,
		map[string]any{"name": "Cube.001", "mesh": 0, "translation": []float64{1, 0, 0}},
		map[string]any{"name": "Cube.002", "mesh": 1, "translation": []float64{0, 5, 0}},
	)
	head := meshedDoc(t,
		map[string]any{"name": "Fender", "mesh": 0, "translation": []float64{1, 0, 0}},
		map[string]any{"name": "Bonnet", "mesh": 1, "translation": []float64{0, 5, 0}},
	)

	d := diffOf(t, base, head)
	mustRename(t, d, "nodes/Fender", "Cube.001")
	mustRename(t, d, "nodes/Bonnet", "Cube.002")
}

// Two equally plausible candidates are not a rename. Guessing one of them would
// be a coin flip presented to a reviewer as a fact.
func TestAmbiguousCandidatesStayAddAndRemove(t *testing.T) {
	base := meshedDoc(t,
		map[string]any{"name": "Cube.001", "mesh": 0, "translation": []float64{1, 2, 3}},
		map[string]any{"name": "Cube.002", "mesh": 0, "translation": []float64{1, 2, 3}},
	)
	head := meshedDoc(t,
		map[string]any{"name": "Fender", "mesh": 0, "translation": []float64{1, 2, 3}},
		map[string]any{"name": "Bonnet", "mesh": 0, "translation": []float64{1, 2, 3}},
	)

	d := diffOf(t, base, head)
	for path, want := range map[string]ChangeKind{
		"nodes/Cube.001": Removed,
		"nodes/Cube.002": Removed,
		"nodes/Fender":   Added,
		"nodes/Bonnet":   Added,
	} {
		if got := kindAt(d, path); got != want {
			t.Errorf("%s: kind = %q, want %q", path, got, want)
		}
	}
}

// A node drawing different geometry is a different node, however exactly its
// placement happens to match.
func TestDifferentMeshIsNotARename(t *testing.T) {
	base := meshedDoc(t, map[string]any{"name": "Cube.003", "mesh": 0, "translation": []float64{1, 2, 3}})
	head := meshedDoc(t, map[string]any{"name": "Fender", "mesh": 1, "translation": []float64{1, 2, 3}})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/Cube.003"); got != Removed {
		t.Errorf("nodes/Cube.003: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Fender"); got != Added {
		t.Errorf("nodes/Fender: kind = %q, want %q", got, Added)
	}
}

// Two empty nodes at the origin describe nothing, so they never pair on content
// however few candidates there are.
func TestContentlessNodesNeverPair(t *testing.T) {
	base := nodesDoc(t, map[string]any{"name": "Empty.001"})
	head := nodesDoc(t, map[string]any{"name": "Locator"})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/Empty.001"); got != Removed {
		t.Errorf("nodes/Empty.001: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Locator"); got != Added {
		t.Errorf("nodes/Locator: kind = %q, want %q", got, Added)
	}
}

// Two nodes that draw nothing have nothing in common. Agreeing that the mesh
// reference is absent used to score the whole of meshWeight — 6 of the 11 the
// descriptor is worth — so *any* deleted meshless node cleared the 50% threshold
// against *any* added one, at 55% even when their parent, transform and child
// count all differed. Meshless is not exotic: Blender empties, armature joints, a
// skinned character's whole skeleton, camera and light nodes, pivots and groups.
func TestMeshlessNodesDoNotPairOnBothDrawingNothing(t *testing.T) {
	tests := []struct {
		name       string
		base, head map[string]any
	}{
		{
			name: "empties a few metres apart",
			base: map[string]any{"name": "Ctrl_Old", "translation": []float64{2, 0, 0}},
			head: map[string]any{"name": "Lamp_Pivot", "translation": []float64{0, 3, -1}},
		},
		{
			name: "armature joints",
			base: map[string]any{"name": "Bone_Tail", "translation": []float64{0, 2, 0}},
			head: map[string]any{"name": "Bone_Head", "translation": []float64{0, 3, 1}},
		},
		{
			name: "rotation and scale differ as well",
			base: map[string]any{
				"name": "Ctrl_Old", "translation": []float64{2, 0, 0},
				"scale": []float64{2, 2, 2},
			},
			head: map[string]any{
				"name": "Lamp_Pivot", "translation": []float64{0, 3, -1},
				"rotation": []float64{0, 0.3826834, 0, 0.9238795}, "scale": []float64{0.5, 0.5, 0.5},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := diffOf(t, meshedDoc(t, tc.base), meshedDoc(t, tc.head))
			if got := kindAt(d, "nodes/"+tc.base["name"].(string)); got != Removed {
				t.Errorf("the deleted node: kind = %q, want %q", got, Removed)
			}
			if got := kindAt(d, "nodes/"+tc.head["name"].(string)); got != Added {
				t.Errorf("the added node: kind = %q, want %q", got, Added)
			}
		})
	}
}

// The floor of the same defect, with a hierarchy under it: the two nodes share
// nothing whatsoever — different parent, different children, different
// translation, rotation and scale — and the mesh field alone still carried them
// over the threshold. A rename here hides the deletion of a control that had two
// children hanging off it.
func TestMeshlessNodesSharingNothingAreARemovalAndAnAddition(t *testing.T) {
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{3}}},
		"nodes": []any{
			map[string]any{"name": "Kid1"},
			map[string]any{"name": "Kid2"},
			map[string]any{
				"name": "Ctrl_Old", "children": []int{0, 1},
				"translation": []float64{2, 0, 0}, "scale": []float64{2, 2, 2},
				"rotation": []float64{0, 0.3826834, 0, 0.9238795},
			},
			map[string]any{"name": "Rig", "children": []int{2}, "translation": []float64{9, 9, 9}},
		},
	})
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{2, 3}}},
		"nodes": []any{
			map[string]any{"name": "Kid1"},
			map[string]any{"name": "Kid2"},
			map[string]any{"name": "Rig", "children": []int{0, 1}, "translation": []float64{9, 9, 9}},
			map[string]any{"name": "Lamp_Pivot", "translation": []float64{-4, 6, 1}, "scale": []float64{0.5, 0.5, 0.5}},
		},
	})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/Ctrl_Old"); got != Removed {
		t.Errorf("nodes/Ctrl_Old: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Lamp_Pivot"); got != Added {
		t.Errorf("nodes/Lamp_Pivot: kind = %q, want %q", got, Added)
	}
}

// riggedDoc parents one meshless node under a named `Armature`, optionally with
// grandchildren of its own. Two nodes tested through it share the one thing every
// sibling of a rig shares and nothing else, which is the configuration the whole
// feature is aimed at: a node's parent is unstated only at the scene root, and
// joints, lights and cameras are parented by definition.
func riggedDoc(t *testing.T, subject map[string]any, grandkids ...string) []byte {
	t.Helper()
	nodes := make([]any, 0, len(grandkids)+2)
	kids := make([]int, len(grandkids))
	for i, name := range grandkids {
		kids[i] = i
		nodes = append(nodes, map[string]any{
			"name": name, "translation": []float64{float64(i) + 1, 0, 0},
		})
	}
	if len(kids) > 0 {
		subject["children"] = kids
	}
	nodes = append(nodes, subject)
	nodes = append(nodes, map[string]any{"name": "Armature", "children": []int{len(grandkids)}})
	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{len(grandkids) + 1}}},
		"nodes":  nodes,
	})
}

// Two meshless nodes under one *named* parent. Dropping the mesh field left the
// placement fields the two nodes happened to write carrying the whole score, and
// `parent=Armature` — a value every joint under that armature holds, so evidence
// of nothing — was half of it: exactly renameThreshold, so an ordinary skeleton's
// deleted joint was renamed into an unrelated added one. With a child count as
// well it reached a perfect 1.0, reported at full confidence with no similarity
// hedge to mark it as a guess. Scored over the whole placement (signature.floor)
// they are 1 of 5 and 2 of 5.
func TestMeshlessSiblingsUnderOneParentDoNotPair(t *testing.T) {
	tests := []struct {
		name             string
		base, head       map[string]any
		baseKids, kids   []string
		removed, addedTo string
	}{
		{
			name: "joints of an ordinary skeleton",
			base: map[string]any{"name": "Bone_Tail", "translation": []float64{0, -0.4, 0.05}},
			head: map[string]any{"name": "Bone_Horn", "translation": []float64{0, 0.9, 1.9}},
		},
		{
			name:     "mid-chain joints, one child each",
			base:     map[string]any{"name": "Bone_Tail", "translation": []float64{0, 2, 0}},
			baseKids: []string{"Tip_Tail"},
			head:     map[string]any{"name": "Bone_Neck", "translation": []float64{0, 3, -1}},
			kids:     []string{"Tip_Neck"},
		},
		{
			name:     "groups at the parent's origin, two children each",
			base:     map[string]any{"name": "Ctrl_Old"},
			baseKids: []string{"Ctl_1", "Ctl_2"},
			head:     map[string]any{"name": "Camera_Main"},
			kids:     []string{"Lamp_Key", "Lamp_Fill"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := riggedDoc(t, tc.base, tc.baseKids...)
			head := riggedDoc(t, tc.head, tc.kids...)

			d := diffOf(t, base, head)
			// The grandchildren share a translation apiece and would pair on the
			// same too-thin evidence, so no rename anywhere in the tree.
			walk(d.Changes, func(c *DiffChange, _ int) {
				if c.Kind == Renamed {
					t.Errorf("a shared parent is not evidence of identity: %+v", c)
				}
			})
			if got := kindAt(d, "nodes/"+tc.base["name"].(string)); got != Removed {
				t.Errorf("the deleted node: kind = %q, want %q", got, Removed)
			}
			if got := kindAt(d, "nodes/"+tc.head["name"].(string)); got != Added {
				t.Errorf("the added node: kind = %q, want %q", got, Added)
			}
		})
	}
}

// The same defect with no parent at all to lean on: at the scene root the parent
// is unstated, so a child *count* was the entire surviving descriptor and two
// unrelated groups agreed on "two children" for a perfect 1.0.
func TestGroupNodesDoNotPairOnAChildCountAlone(t *testing.T) {
	group := func(name string, kids ...string) []byte {
		nodes := make([]any, 0, len(kids)+1)
		idx := make([]int, len(kids))
		for i, kid := range kids {
			idx[i] = i
			nodes = append(nodes, map[string]any{
				"name": kid, "translation": []float64{float64(i) + 1, 0, 0},
			})
		}
		nodes = append(nodes, map[string]any{"name": name, "children": idx})
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{len(kids)}}},
			"nodes":  nodes,
		})
	}
	base := group("Empty_Ctrl", "Ctl_1", "Ctl_2")
	head := group("Light_Rig", "Lamp_Key", "Lamp_Fill")

	d := diffOf(t, base, head)
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("a child count is not evidence of identity: %+v", c)
		}
	})
	if got := kindAt(d, "nodes/Empty_Ctrl"); got != Removed {
		t.Errorf("nodes/Empty_Ctrl: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Light_Rig"); got != Added {
		t.Errorf("nodes/Light_Rig: kind = %q, want %q", got, Added)
	}
}

// The other half of the floor, and the reason it covers the placement rather
// than the whole descriptor: a meshless node that really was renamed still pairs
// on what it states. This one agrees on four of its five placement fields — same
// spot, same scale, same parent, same two children — which is as much as a node
// with no geometry can say about itself, and comfortably over the threshold.
func TestMeshlessRenameStillPairsOnAFullPlacementMatch(t *testing.T) {
	ctrl := func(name string) []byte {
		return riggedDoc(t, map[string]any{
			"name": name, "translation": []float64{2, 0, 0}, "scale": []float64{2, 2, 2},
		}, "Kid1", "Kid2")
	}

	d := diffOf(t, ctrl("Ctrl_Old"), ctrl("Pivot_Hand"))
	c := mustRename(t, d, "nodes/Pivot_Hand", "Ctrl_Old")
	if got := afterText(c); !strings.Contains(got, "matched by content") {
		t.Errorf("after = %q, want the content evidence", got)
	}
	if findChange(d, "nodes/Ctrl_Old") != nil {
		t.Error("the old name must not appear as a separate change")
	}
}

// A rename says the *name* changed, and the diff key is not the name: uniqueKeys
// disambiguates duplicates into Wheel, Wheel#1, Wheel#2, so two elements that are
// both called "Wheel" can be paired correctly across a reorder and still hold
// different keys. Reporting one as renamed to the other puts `Wheel#1` in
// `before`, which SPEC.md §7 defines as the bare old *name* — the base file has
// nothing under that string, it has two elements named "Wheel".
//
// Reachable exactly where this feature is aimed. Duplicate names are the ordinary
// case uniqueKeys exists for, an authored id is what pairs the two across the
// swap, and an exporter reordering the array is the whole edit.
func TestDuplicateNamedElementsPairedByIDAreNotRenamed(t *testing.T) {
	wheel := func(uid string, z float64) map[string]any {
		return withUID(uid, map[string]any{
			"name": "Wheel", "mesh": 0, "translation": []float64{1.3, 0.45, z},
		})
	}

	t.Run("reordered", func(t *testing.T) {
		base := meshedDoc(t, wheel("u-a", 0.75), wheel("u-b", -0.75))
		head := meshedDoc(t, wheel("u-b", -0.75), wheel("u-a", 0.75))

		d := diffOf(t, base, head)
		if len(d.Changes) != 0 {
			t.Errorf("the same two elements in a different order is not a change; got %+v", d.Changes)
		}
	})

	// The pairing itself is the feature working, so an edit under it must still
	// land on the element the id says it belongs to.
	t.Run("reordered and edited", func(t *testing.T) {
		base := meshedDoc(t, wheel("u-a", 0.75), wheel("u-b", -0.75))
		head := meshedDoc(t, wheel("u-b", -0.75), wheel("u-a", 9))

		d := diffOf(t, base, head)
		walk(d.Changes, func(c *DiffChange, _ int) {
			if c.Kind == Renamed {
				t.Errorf("both elements are named Wheel in both files: %+v", c)
			}
		})
		if got := kindAt(d, "nodes/Wheel#1"); got != Modified {
			t.Errorf("nodes/Wheel#1: kind = %q, want %q", got, Modified)
		}
		c := mustChange(t, d, "nodes/Wheel#1/translation")
		if !strings.Contains(fmt.Sprint(c.Before), "0.75") {
			t.Errorf("before = %v, want the id's own previous translation", c.Before)
		}
	})
}

// A node's mesh is the heaviest thing its content descriptor knows about it
// (meshWeight), and it is compared as the mesh's *diff key*. Never as the array
// index, which means "whatever is second in this file's mesh array" — a claim any
// insertion upstream silently redefines.
//
// The fixture is the smallest edit that separates the two readings: one mesh
// inserted at the head of the array, so WheelMesh moves 0 → 1 and BodyMesh 1 → 2.
// Read as indices the surviving node draws "mesh[1]", which is what the *deleted*
// node drew in the previous revision — an 11-of-11 score, so the rename is
// asserted at the handler's full confidence, against the wrong element, and the
// real deletion disappears with it.
func TestNodeContentMatchUsesTheMeshKeyNotItsArrayIndex(t *testing.T) {
	pose := []float64{1.3, 0.45, 0.75}
	base := meshListDoc(t, []string{"WheelMesh", "BodyMesh"},
		map[string]any{"name": "Alpha", "mesh": 0, "translation": pose},
		map[string]any{"name": "Beta", "mesh": 1, "translation": pose},
	)
	head := meshListDoc(t, []string{"NewMesh", "WheelMesh", "BodyMesh"},
		map[string]any{"name": "Gamma", "mesh": 1, "translation": pose},
	)

	d := diffOf(t, base, head)
	// Gamma draws what Alpha drew, so Alpha is where it came from.
	mustRename(t, d, "nodes/Gamma", "Alpha")
	if got := kindAt(d, "nodes/Beta"); got != Removed {
		t.Errorf("nodes/Beta: kind = %q, want %q", got, Removed)
	}
	if findChange(d, "nodes/Alpha") != nil {
		t.Error("the renamed node must not also be reported as removed")
	}
	// Its geometry is untouched; only the number in front of it moved.
	if c := findChange(d, "nodes/Gamma/mesh"); c != nil {
		t.Errorf("the mesh reference did not change; got %v → %v", c.Before, c.After)
	}
}

// The same fixture with the mesh names taken away, which is the file this whole
// feature exists for — SPEC.md §7's pipeline tool that stripped them. meshName
// then falls back to `mesh[1]`, so the "key" IS the array index and the previous
// test's guarantee evaporates: Gamma drew mesh[1], which is the number Beta drew
// in the previous revision, and the pair scored a perfect 11 of 11.
//
// There is no key to compare here, so the field is worth nothing rather than
// worth everything (identity.go, fieldKind opaque). The cost is Alpha's rename,
// which goes unreported; the alternative is asserting Beta's, which is false, and
// losing Beta's deletion with it.
func TestUnnamedMeshIsNotContentEvidence(t *testing.T) {
	pose := []float64{1.3, 0.45, 0.75}
	base := meshListDoc(t, []string{"", ""},
		map[string]any{"name": "Alpha", "mesh": 0, "translation": pose},
		map[string]any{"name": "Beta", "mesh": 1, "translation": pose},
	)
	head := meshListDoc(t, []string{"", "", ""},
		map[string]any{"name": "Gamma", "mesh": 1, "translation": pose},
	)

	d := diffOf(t, base, head)
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("an unnamed mesh's key is its array index, not evidence: %+v", c)
		}
	})
	for path, want := range map[string]ChangeKind{
		"nodes/Alpha": Removed,
		"nodes/Beta":  Removed,
		"nodes/Gamma": Added,
	} {
		if got := kindAt(d, path); got != want {
			t.Errorf("%s: kind = %q, want %q", path, got, want)
		}
	}
}

// The other key a node's descriptor resolves is its parent's, and an unnamed
// parent has the same non-key: `node[0]`. Here it is the only thing the two
// nodes have in common — they are metres apart — and one field of two is exactly
// the threshold, so counting it paired them.
func TestUnnamedParentIsNotContentEvidence(t *testing.T) {
	group := func(child map[string]any) []byte {
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes":  []any{map[string]any{"children": []int{1}}, child},
		})
	}
	base := group(map[string]any{"name": "Ctrl_Old", "translation": []float64{2, 0, 0}})
	head := group(map[string]any{"name": "Pivot_New", "translation": []float64{0, 3, -1}})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/Ctrl_Old"); got != Removed {
		t.Errorf("nodes/Ctrl_Old: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Pivot_New"); got != Added {
		t.Errorf("nodes/Pivot_New: kind = %q, want %q", got, Added)
	}
}

// The same insertion with nothing renamed at all. Read as raw indices this is a
// re-mesh of every node below it — "mesh[0] → mesh[1]" on a node whose geometry
// nobody touched — which is the property-level half of the same defect.
func TestUpstreamMeshInsertionIsNotAReMesh(t *testing.T) {
	pose := []float64{1.3, 0.45, 0.75}
	base := meshListDoc(t, []string{"WheelMesh", "BodyMesh"},
		map[string]any{"name": "Alpha", "mesh": 0, "translation": pose},
	)
	head := meshListDoc(t, []string{"NewMesh", "WheelMesh", "BodyMesh"},
		map[string]any{"name": "Alpha", "mesh": 1, "translation": pose},
	)

	d := diffOf(t, base, head)
	if c := findChange(d, "nodes/Alpha"); c != nil {
		t.Errorf("nothing about the node changed; got %+v", c)
	}
	// The insertion is still reported once, where it happened.
	if got := kindAt(d, "meshes/NewMesh"); got != Added {
		t.Errorf("meshes/NewMesh: kind = %q, want %q", got, Added)
	}
}

// A node pointed at a genuinely different mesh is still a change — named by the
// two meshes rather than numbered.
func TestNodeReMeshIsReportedByMeshKey(t *testing.T) {
	base := meshedDoc(t, map[string]any{"name": "Body", "mesh": 0})
	head := meshedDoc(t, map[string]any{"name": "Body", "mesh": 1})

	c := mustChange(t, diffOf(t, base, head), "nodes/Body/mesh")
	if c.Before != "Hull" || c.After != "Trim" {
		t.Errorf("mesh: %v → %v, want Hull → Trim", c.Before, c.After)
	}
}

// Unnamed elements are keyed by array index, so there is no name for them to have
// changed. Pairing two synthetic keys would report a rename between node[0] and
// node[1], and the index cascade that produces those is issue #42's to fix.
func TestUnnamedNodesAreNotRenameCandidates(t *testing.T) {
	base := meshedDoc(t, map[string]any{"mesh": 0, "translation": []float64{1, 2, 3}})
	head := meshedDoc(t,
		map[string]any{"mesh": 1, "translation": []float64{7, 7, 7}},
		map[string]any{"mesh": 0, "translation": []float64{1, 2, 3}},
	)

	d := diffOf(t, base, head)
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("unnamed nodes must not be paired by content: %+v", c)
		}
	})
}

// The same invariant two tiers up, which is where it used to leak. An authored id
// does pair two unnamed elements — that is the whole point of stamping a file
// whose names a pipeline tool dropped — but their keys are array indices, so a
// shift between them is not a rename and must not be reported as one. The pairing
// stands and only the label is withheld: the edits still land against the right
// element instead of reading as a removal plus an addition.
func TestUnnamedElementsWithAStableIDAreNotRenamed(t *testing.T) {
	uidNode := func(translation []float64) map[string]any {
		return withUID("u-1", map[string]any{"mesh": 0, "translation": translation})
	}
	namedNode := map[string]any{"name": "Body", "mesh": 1}
	uidMaterial := withUID("m-1", map[string]any{"emissiveFactor": []float64{0.4, 0.1, 0}})
	materialsDoc := func(mats ...map[string]any) []byte {
		list := make([]any, len(mats))
		for i, m := range mats {
			list[i] = m
		}
		return doc(t, map[string]any{"materials": list})
	}

	tests := []struct {
		name       string
		base, head []byte
		// wantPath is the head key the element's surviving edit reports under, or
		// "" when the array shift is the only difference and the diff is empty.
		wantPath string
	}{
		{
			name: "node moved down the array, nothing else changed",
			base: meshedDoc(t, uidNode([]float64{1, 2, 3}), namedNode),
			head: meshedDoc(t, namedNode, uidNode([]float64{1, 2, 3})),
		},
		{
			name:     "node moved down the array and edited",
			base:     meshedDoc(t, uidNode([]float64{1, 2, 3}), namedNode),
			head:     meshedDoc(t, namedNode, uidNode([]float64{1, 2, 9})),
			wantPath: "nodes/node[1]",
		},
		{
			name: "material moved down the array",
			base: materialsDoc(uidMaterial, map[string]any{"name": "Rubber"}),
			head: materialsDoc(map[string]any{"name": "Rubber"}, uidMaterial),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := diffOf(t, tc.base, tc.head)
			walk(d.Changes, func(c *DiffChange, _ int) {
				if c.Kind == Renamed {
					t.Errorf("an unnamed element has no name to have changed: %+v", c)
				}
			})
			if tc.wantPath == "" {
				if len(d.Changes) != 0 {
					t.Errorf("an array shift on its own is not a change; got %+v", d.Changes)
				}
				return
			}
			if got := kindAt(d, tc.wantPath); got != Modified {
				t.Errorf("%s: kind = %q, want %q", tc.wantPath, got, Modified)
			}
			mustChange(t, d, tc.wantPath+"/translation")
		})
	}
}

// A name on one side only is a different matter: a name was added or removed,
// which is a real edit and the one thing `renamed` exists to report. The unnamed
// side's index key is the only thing that element is called anywhere else in the
// diff, so it is what the pair is reported against.
func TestNameAddedOrRemovedIsStillARename(t *testing.T) {
	unnamed := withUID("u-1", map[string]any{"mesh": 0, "translation": []float64{1, 2, 3}})
	named := withUID("u-1", map[string]any{"name": "Fender", "mesh": 0, "translation": []float64{1, 2, 3}})

	t.Run("name removed", func(t *testing.T) {
		d := diffOf(t, meshedDoc(t, named), meshedDoc(t, unnamed))
		mustRename(t, d, "nodes/node[0]", "Fender")
	})
	t.Run("name added", func(t *testing.T) {
		d := diffOf(t, meshedDoc(t, unnamed), meshedDoc(t, named))
		mustRename(t, d, "nodes/Fender", "node[0]")
	})
}

// ── identity and the hierarchy ────────────────────────────────────────────────

// Re-parenting is compared by identity, not by parent name. Renaming a parent
// used to be indistinguishable from moving every one of its children onto a
// different parent — a one-line edit reported as a structural change to the whole
// subtree.
func TestRenamedParentDoesNotReparentItsChildren(t *testing.T) {
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			withUID("u-body", map[string]any{"name": "Body", "children": []int{1}}),
			map[string]any{"name": "Door_L"},
		},
	})
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			withUID("u-body", map[string]any{"name": "Chassis", "children": []int{1}}),
			map[string]any{"name": "Door_L"},
		},
	})

	d := diffOf(t, base, head)
	mustRename(t, d, "nodes/Chassis", "Body")
	if c := findChange(d, "nodes/Door_L/parent"); c != nil {
		t.Errorf("the child was not moved; got a parent change %v → %v", c.Before, c.After)
	}
	// A genuine re-parent still reports, and names the parent as the head file
	// spells it.
	if c := findChange(d, "nodes/Door_L"); c != nil {
		t.Errorf("nothing about the child changed; got %+v", c)
	}
	// And nothing here is `reparented` either: the child sits under the same
	// (renamed) parent, and identity — not the parent's key — decides the kind.
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Reparented {
			t.Errorf("renaming a parent moves none of its children: %+v", c)
		}
	})
}

// `before` and `after` are the elements' bare *names* (SPEC.md §7), never
// uniqueKeys' disambiguated key. Duplicate names are the ordinary case uniqueKeys
// exists for and an authored id is what pairs them across a rename, so this is
// the feature's own target file — and `Wheel#1` in `before` sends a consumer
// looking for a node the previous revision does not have: it has two called
// `Wheel` and none called `Wheel#1`. The renderer's base-file lookup is keyed on
// raw names and misses outright, taking the removed ghost and the motion vector
// for "renamed and moved" with it.
func TestRenameOfADuplicateNamedElementReportsBareNames(t *testing.T) {
	t.Run("the duplicate is on the base side", func(t *testing.T) {
		base := nodesDoc(t,
			withUID("u1", map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}}),
			withUID("u2", map[string]any{"name": "Wheel", "translation": []float64{-1, 0, 0}}),
		)
		head := nodesDoc(t,
			withUID("u1", map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}}),
			withUID("u2", map[string]any{"name": "Tire", "translation": []float64{-1, 0, 0}}),
		)

		c := mustRename(t, diffOf(t, base, head), "nodes/Tire", "Wheel")
		if got := afterText(c); strings.Contains(got, "#") {
			t.Errorf("after = %q, want no disambiguated key in it", got)
		}
	})

	// The mirror: the head file is the one with two `Wheel`s, so the *new* name is
	// the one whose key was suffixed. The path still carries the key — it has to
	// address one element — but `after` is the name.
	t.Run("the duplicate is on the head side", func(t *testing.T) {
		base := nodesDoc(t,
			withUID("u1", map[string]any{"name": "Spoke", "translation": []float64{1, 0, 0}}),
			withUID("u2", map[string]any{"name": "Wheel", "translation": []float64{-1, 0, 0}}),
		)
		head := nodesDoc(t,
			withUID("u2", map[string]any{"name": "Wheel", "translation": []float64{-1, 0, 0}}),
			withUID("u1", map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}}),
		)

		c := mustRename(t, diffOf(t, base, head), "nodes/Wheel#1", "Spoke")
		if got, want := afterText(c), "Wheel (matched by "+uidExtrasKey+")"; got != want {
			t.Errorf("after = %q, want %q", got, want)
		}
	})

	// An element with no name at all has no bare name to report, and its key is
	// the only thing it is called anywhere in the diff — so that one stays.
	t.Run("an unnamed side keeps its key", func(t *testing.T) {
		base := nodesDoc(t, withUID("u1", map[string]any{"translation": []float64{1, 0, 0}}))
		head := nodesDoc(t, withUID("u1", map[string]any{"name": "Wheel", "translation": []float64{1, 0, 0}}))

		mustRename(t, diffOf(t, base, head), "nodes/Wheel", "node[0]")
	})
}

// ── one path per change ───────────────────────────────────────────────────────

// A matched element is reported under the *head* key and an unmatched base
// element under the *base* key, and the two namespaces are independent — which
// they were not before this layer, when both sides were walked in one merged key
// order. Base [A(id=1), B] against head [B(id=1)] therefore put the rename A→B
// and the removal of the old B at the same path.
//
// Two different elements at one path is not a cosmetic collision. Every consumer
// keys on it — the change tree's rows, the review callout's headline map, the
// renderer's per-node facts — so one of the two rows is silently dropped, and the
// one that loses is the deletion.
func TestSiblingChangesNeverShareAPath(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		base := meshedDoc(t,
			withUID("u1", map[string]any{"name": "A", "mesh": 0, "translation": []float64{1, 0, 0}}),
			withUID("u2", map[string]any{"name": "B", "mesh": 1, "translation": []float64{2, 0, 0}}),
		)
		head := meshedDoc(t,
			withUID("u1", map[string]any{"name": "B", "mesh": 0, "translation": []float64{1, 0, 0}}),
		)

		d := diffOf(t, base, head)
		mustRename(t, d, "nodes/B", "A")
		assertUniquePaths(t, d)

		removed := mustChange(t, d, "nodes/B#1")
		if removed.Kind != Removed {
			t.Errorf("nodes/B#1: kind = %q, want %q", removed.Kind, Removed)
		}
		// The label is what the element is called in the file the change is about,
		// so a consumer resolving it against the previous revision still finds it.
		// Only the path — the addressing key, which has to be unique — is suffixed.
		if removed.Label != "B" {
			t.Errorf("nodes/B#1: label = %q, want %q", removed.Label, "B")
		}
		if findChange(d, "nodes/B#1/mesh") == nil {
			t.Error("the removed node's own properties must hang off its own path")
		}
	})

	t.Run("materials", func(t *testing.T) {
		mats := func(list ...map[string]any) []byte {
			items := make([]any, len(list))
			for i, m := range list {
				items[i] = m
			}
			return doc(t, map[string]any{"materials": items})
		}
		base := mats(
			withUID("u1", map[string]any{"name": "A", "emissiveFactor": []float64{0.4, 0.1, 0}}),
			withUID("u2", map[string]any{"name": "B", "doubleSided": true}),
		)
		head := mats(
			withUID("u1", map[string]any{"name": "B", "emissiveFactor": []float64{0.4, 0.1, 0}}),
		)

		d := diffOf(t, base, head)
		mustRename(t, d, "materials/B", "A")
		assertUniquePaths(t, d)
		if got := kindAt(d, "materials/B#1"); got != Removed {
			t.Errorf("materials/B#1: kind = %q, want %q", got, Removed)
		}
	})
}

// assertUniquePaths is the property the collision above breaks, checked over the
// whole tree rather than at the two paths one fixture happens to produce.
func assertUniquePaths(t *testing.T, d StructuredDiff) {
	t.Helper()
	seen := make(map[string]bool)
	for _, p := range paths(d) {
		if seen[p] {
			t.Errorf("path %q is emitted twice; a path addresses one change", p)
		}
		seen[p] = true
	}
}

// ── the cost of tier 3 ────────────────────────────────────────────────────────

// Content matching is quadratic in the leftovers, and the case that makes every
// element a leftover is ordinary: any pipeline step that rewrites all the names
// at once — an FBX round trip, a namespace prefix on import — clears tiers 1 and
// 2 outright. Unbounded, 8k nodes took four minutes and a gigabyte, in a package
// that also runs single-threaded in a browser tab.
//
// So above git's renameLimit the tier is skipped and its leftovers are reported
// as the removals and additions they are. Both halves are pinned here: the limit
// is a cap on the work, not a change to the answer below it.
func TestContentMatchingIsSkippedOverTheRenameLimit(t *testing.T) {
	// Every node draws one shared mesh and sits at its own translation, so each
	// base node has exactly one head node it matches perfectly and tier 3 pairs
	// all of them — until the cap says otherwise.
	side := func(prefix string, n int) []byte {
		nodes := make([]map[string]any, n)
		for i := range nodes {
			nodes[i] = map[string]any{
				"name": fmt.Sprintf("%s%d", prefix, i), "mesh": 0,
				"translation": []float64{float64(i + 1), 0, 0},
			}
		}
		return meshListDoc(t, []string{"Hull"}, nodes...)
	}

	at := diffOf(t, side("Old_", renameLimit), side("New_", renameLimit))
	mustRename(t, at, "nodes/New_0", "Old_0")

	over := diffOf(t, side("Old_", renameLimit+1), side("New_", renameLimit+1))
	if got := kindAt(over, "nodes/Old_0"); got != Removed {
		t.Errorf("over the limit, nodes/Old_0: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(over, "nodes/New_0"); got != Added {
		t.Errorf("over the limit, nodes/New_0: kind = %q, want %q", got, Added)
	}
	assertUniquePaths(t, over)
}

// The cap bounds the score matrix — leftBase × leftHead — and nothing else, so
// every pass over the leftovers has to stay inside it.
//
// The one that didn't was the mutual-best back-check, which re-derived each head
// candidate's best base row by rescanning that column per row: O(leftBase²), and
// the cap permits leftBase to reach renameLimit² when leftHead is small. The
// trigger is an ordinary revision, not a crafted one — a lot of named parts
// sharing one mesh deleted, one part drawing that mesh added — and with a single
// head candidate every row has a forward winner, so the rescan ran unconditionally.
// Measured on the branch that shipped the cap: 100 000 nodes took 45 s natively
// and, compiled to wasm and driven single-threaded as a browser tab runs it,
// 50 000 took 46 s. That is the hung page the cap exists to prevent.
//
// The shape is built here rather than diffed from a document so the test measures
// the pass and not glTF parsing. The budget is deliberately loose: the pass this
// pins is milliseconds, and the quadratic it replaces is minutes.
func TestContentMatchingDoesNotRescanThePerCandidateColumn(t *testing.T) {
	// One head candidate, and every base element clears the threshold against it
	// — they all draw the shared mesh and differ only in where they sit. Exactly
	// one also matches its placement, so the column has a strict winner and the
	// other 99 999 rows are the back-check's cost and nothing else.
	const n = 100_000
	const winner = n / 2
	part := func(place string) func() signature {
		return func() signature {
			return signature{
				fields: []sigField{
					{"mesh:Hull", meshWeight, stated},
					{place, 5, stated},
				},
				specific: true,
			}
		}
	}
	base := make([]entity, n)
	for i := range base {
		key := fmt.Sprintf("Old_%d", i)
		place := "elsewhere"
		if i == winner {
			place = "here"
		}
		base[i] = entity{key: key, name: key, sig: part(place)}
	}
	head := []entity{{key: "New_0", name: "New_0", sig: part("here")}}

	p := pairing{headOf: map[int]int{}, baseOf: map[int]int{}, how: map[int]matchEvidence{}}
	start := time.Now()
	matchByContent(p, base, head)
	elapsed := time.Since(start)

	// The answer first: the cheaper pass must be the same pass.
	if got, ok := p.headOf[winner]; !ok || got != 0 {
		t.Errorf("base[%d] paired with head %d (ok = %v); want head 0", winner, got, ok)
	}
	if len(p.headOf) != 1 {
		t.Errorf("pairs = %d, want 1: only the strict column winner may pair", len(p.headOf))
	}
	if budget := 10 * time.Second; elapsed > budget {
		t.Errorf("matchByContent over %d base leftovers and one head candidate took %v, over the %v "+
			"budget — the per-row column rescan is back", n, elapsed, budget)
	}
}

// ── materials and meshes ──────────────────────────────────────────────────────

func TestMaterialRename(t *testing.T) {
	tests := []struct {
		name       string
		base, head map[string]any
		wantPath   string
		wantBefore string
	}{
		{
			name:       "by stable id",
			base:       withUID("u-paint", map[string]any{"name": "Paint"}),
			head:       withUID("u-paint", map[string]any{"name": "BodyPaint"}),
			wantPath:   "materials/BodyPaint",
			wantBefore: "Paint",
		},
		{
			name:       "by content",
			base:       map[string]any{"name": "Paint", "emissiveFactor": []float64{0.4, 0.1, 0}, "doubleSided": true},
			head:       map[string]any{"name": "BodyPaint", "emissiveFactor": []float64{0.4, 0.1, 0}, "doubleSided": true},
			wantPath:   "materials/BodyPaint",
			wantBefore: "Paint",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := doc(t, map[string]any{"materials": []any{tc.base}})
			head := doc(t, map[string]any{"materials": []any{tc.head}})
			mustRename(t, diffOf(t, base, head), tc.wantPath, tc.wantBefore)
		})
	}
}

// Two untextured materials used to agree on all five texture slots for having no
// textures, and on emissiveFactor, alphaMode and doubleSided for leaving them
// alone: 8 of 11 equal fields, comfortably over the threshold, however far apart
// the three properties they actually state were. Untextured is the ordinary case
// — this repo's own car fixtures are — so in an untextured scene every removed
// material paired with every added one and the deletion vanished.
func TestMaterialsDoNotPairOnSharedDefaults(t *testing.T) {
	pbr := func(r, g, b, metallic, roughness float64) map[string]any {
		return map[string]any{
			"baseColorFactor": []float64{r, g, b, 1},
			"metallicFactor":  metallic, "roughnessFactor": roughness,
		}
	}
	base := doc(t, map[string]any{"materials": []any{
		map[string]any{"name": "Rubber", "pbrMetallicRoughness": pbr(0.02, 0.02, 0.03, 0, 0.95)},
	}})
	head := doc(t, map[string]any{"materials": []any{
		map[string]any{"name": "Chrome", "pbrMetallicRoughness": pbr(0.95, 0.96, 0.98, 1, 0.05)},
	}})

	d := diffOf(t, base, head)
	if got := kindAt(d, "materials/Rubber"); got != Removed {
		t.Errorf("materials/Rubber: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "materials/Chrome"); got != Added {
		t.Errorf("materials/Chrome: kind = %q, want %q", got, Added)
	}
}

// The other half of the same rule: dropping the shared defaults must not cost a
// material that really was renamed its match. These two state two properties
// between them and agree on both, so they are content-identical and pair at full
// confidence — no similarity hedge, even though 9 of the 11 fields the descriptor
// has are defaults neither side wrote.
func TestUntexturedMaterialRenameStillPairsOnWhatItStates(t *testing.T) {
	mat := func(name string) map[string]any {
		return map[string]any{
			"name": name, "emissiveFactor": []float64{0.4, 0.1, 0}, "doubleSided": true,
		}
	}
	base := doc(t, map[string]any{"materials": []any{mat("Paint")}})
	head := doc(t, map[string]any{"materials": []any{mat("BodyPaint")}})

	c := mustRename(t, diffOf(t, base, head), "materials/BodyPaint", "Paint")
	if got := afterText(c); !strings.Contains(got, "matched by content") || strings.Contains(got, "%") {
		t.Errorf("after = %q, want an exact content match with no similarity figure", got)
	}
}

// Dropping the fields nobody wrote shrinks the denominator, and with equal
// weights that promoted whatever survived: two materials whose only common ground
// was `doubleSided: true` landed on exactly the threshold however far apart their
// colours were, and two that stated nothing else at all scored a perfect 1.0. The
// weights say what a field can distinguish (a colour is four numbers; a flag is
// one of two states) and the floor says a material is at least its colour.
func TestMaterialsDoNotPairOnOneLowInformationField(t *testing.T) {
	rgba := func(r, g, b float64) map[string]any {
		return map[string]any{"baseColorFactor": []float64{r, g, b, 1}}
	}
	tests := []struct {
		name       string
		base, head map[string]any
	}{
		{
			name: "opposite colours, both double-sided",
			base: map[string]any{"name": "Leaf", "pbrMetallicRoughness": rgba(0, 1, 0), "doubleSided": true},
			head: map[string]any{"name": "Wire", "pbrMetallicRoughness": rgba(1, 0, 0), "doubleSided": true},
		},
		{
			name: "opposite emissive, both double-sided",
			base: map[string]any{"name": "Glow", "emissiveFactor": []float64{1, 0, 0}, "doubleSided": true},
			head: map[string]any{"name": "Spark", "emissiveFactor": []float64{0, 0, 1}, "doubleSided": true},
		},
		{
			// The realistic one: artists set metallic and roughness on everything, so
			// two unrelated plastics routinely agree on both and differ only in colour.
			name: "different colour, same metallic and roughness",
			base: map[string]any{"name": "RedPlastic", "pbrMetallicRoughness": map[string]any{
				"baseColorFactor": []float64{0.9, 0.1, 0.1, 1}, "metallicFactor": 0.0, "roughnessFactor": 0.4,
			}},
			head: map[string]any{"name": "BluePlastic", "pbrMetallicRoughness": map[string]any{
				"baseColorFactor": []float64{0.1, 0.1, 0.9, 1}, "metallicFactor": 0.0, "roughnessFactor": 0.4,
			}},
		},
		{
			name: "nothing stated but the flag, and they agree on it",
			base: map[string]any{"name": "Leaf", "doubleSided": true},
			head: map[string]any{"name": "Wire", "doubleSided": true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := doc(t, map[string]any{"materials": []any{tc.base}})
			head := doc(t, map[string]any{"materials": []any{tc.head}})
			d := diffOf(t, base, head)
			if got := kindAt(d, "materials/"+tc.base["name"].(string)); got != Removed {
				t.Errorf("%s: kind = %q, want %q", tc.base["name"], got, Removed)
			}
			if got := kindAt(d, "materials/"+tc.head["name"].(string)); got != Added {
				t.Errorf("%s: kind = %q, want %q", tc.head["name"], got, Added)
			}
		})
	}
}

// The other side of those weights. A texture slot resolves to image content — a
// URI, or a mime type plus a hash of the pixels — which is the one material
// property two unrelated materials cannot agree on by coincidence, so a shared
// one carries a rename on its own even though every factor around it is a default
// the floor keeps in the denominator.
func TestTexturedMaterialRenamePairsOnTheSharedImage(t *testing.T) {
	textured := func(name string) []byte {
		return doc(t, map[string]any{
			"images":   []any{map[string]any{"uri": "body-albedo.png"}},
			"textures": []any{map[string]any{"source": 0}},
			"materials": []any{map[string]any{"name": name, "pbrMetallicRoughness": map[string]any{
				"baseColorTexture": map[string]any{"index": 0},
			}}},
		})
	}

	c := mustRename(t, diffOf(t, textured("Paint"), textured("BodyPaint")), "materials/BodyPaint", "Paint")
	if got := afterText(c); !strings.Contains(got, "matched by content") || strings.Contains(got, "%") {
		t.Errorf("after = %q, want an exact content match with no similarity figure", got)
	}
}

// A default material describes nothing, so it never pairs on content — the same
// rule that keeps two empty nodes apart.
func TestDefaultMaterialsNeverPairOnContent(t *testing.T) {
	base := doc(t, map[string]any{"materials": []any{map[string]any{"name": "Paint"}}})
	head := doc(t, map[string]any{"materials": []any{map[string]any{"name": "BodyPaint"}}})

	d := diffOf(t, base, head)
	if got := kindAt(d, "materials/Paint"); got != Removed {
		t.Errorf("materials/Paint: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "materials/BodyPaint"); got != Added {
		t.Errorf("materials/BodyPaint: kind = %q, want %q", got, Added)
	}
}

// A reference is compared by identity, not by the key it renders to. Renaming the
// referent changes that string for everyone pointing at it, so a string compare
// asserts a structural edit nobody made — the same mistake the parent compare
// avoids, and the one that makes a mesh rename read as "every node drawing it was
// pointed at different geometry" and a material rename as "every primitive using
// it was reassigned".
func TestRenamingAReferentDoesNotEditWhatPointsAtIt(t *testing.T) {
	t.Run("a renamed mesh", func(t *testing.T) {
		side := func(mesh string) []byte {
			return meshListDoc(t, []string{mesh},
				map[string]any{"name": "Body", "mesh": 0, "translation": []float64{0, 1, 0}},
				map[string]any{"name": "Shadow", "mesh": 0},
			)
		}

		d := diffOf(t, side("BodyMesh"), side("HullMesh"))
		mustRename(t, d, "meshes/HullMesh", "BodyMesh")
		for _, path := range []string{"nodes", "nodes/Body", "nodes/Body/mesh", "nodes/Shadow"} {
			if c := findChange(d, path); c != nil {
				t.Errorf("no node's mesh reference changed; got %s = %+v", path, c)
			}
		}
	})

	t.Run("a renamed material", func(t *testing.T) {
		side := func(material string) []byte {
			return doc(t, map[string]any{
				"scene":  0,
				"scenes": []any{map[string]any{"nodes": []int{0}}},
				"nodes":  []any{map[string]any{"name": "Body", "mesh": 0}},
				"meshes": []any{map[string]any{"name": "BodyMesh", "primitives": []any{
					map[string]any{"attributes": map[string]any{}, "material": 0},
				}}},
				"materials": []any{map[string]any{
					"name": material, "emissiveFactor": []float64{0.4, 0.1, 0}, "doubleSided": true,
				}},
			})
		}

		d := diffOf(t, side("Paint"), side("BodyPaint"))
		mustRename(t, d, "materials/BodyPaint", "Paint")
		if c := findChange(d, "meshes/BodyMesh/primitives/0/material"); c != nil {
			t.Errorf("no primitive was reassigned; got %+v", c)
		}
	})
}

// A renamed mesh matches on the same primitive descriptors the geometry compare
// reports — vertex digests included, on a real binary chunk.
func TestMeshRenameByContentSignature(t *testing.T) {
	positions := [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}
	base := geometryGLB(t, geometrySpec{mesh: "Hull", positions: positions})
	head := geometryGLB(t, geometrySpec{mesh: "Shell", positions: positions})

	d := diffOf(t, base, head)
	mustRename(t, d, "meshes/Shell", "Hull")
	if findChange(d, "meshes/Hull") != nil {
		t.Error("the old mesh name must not appear as a separate change")
	}
}

// Different vertices under a new name is a different mesh: the descriptor that
// separates them is the one the geometry compare already trusts.
func TestMeshWithDifferentGeometryIsNotARename(t *testing.T) {
	base := geometryGLB(t, geometrySpec{mesh: "Hull", positions: [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}})
	head := geometryGLB(t, geometrySpec{mesh: "Shell", positions: [][3]float32{{0, 0, 0}, {9, 0, 0}, {0, 9, 0}}})

	d := diffOf(t, base, head)
	if got := kindAt(d, "meshes/Hull"); got != Removed {
		t.Errorf("meshes/Hull: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "meshes/Shell"); got != Added {
		t.Errorf("meshes/Shell: kind = %q, want %q", got, Added)
	}
}

// ── regression: nothing renamed, nothing changed ──────────────────────────────

// The identity layer runs on every diff, so the case it must be invisible in is
// the one where no name changed at all.
func TestIdentityLayerLeavesOrdinaryDiffsAlone(t *testing.T) {
	base := meshedDoc(t,
		map[string]any{"name": "Body", "mesh": 0, "translation": []float64{0, 0, 0}},
		map[string]any{"name": "Mirror_L", "mesh": 1},
	)
	head := meshedDoc(t,
		map[string]any{"name": "Body", "mesh": 0, "translation": []float64{0, 0, 1}},
		map[string]any{"name": "Mirror_L", "mesh": 1},
	)

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/Body"); got != Modified {
		t.Errorf("nodes/Body: kind = %q, want %q", got, Modified)
	}
	mustChange(t, d, "nodes/Body/translation")
	if findChange(d, "nodes/Mirror_L") != nil {
		t.Error("the untouched node must not appear in the diff")
	}
}

// An identical file is still an empty diff: an authored id is not itself a change.
func TestStampedFileWithNoEditsHasAnEmptyDiff(t *testing.T) {
	same := nodesDoc(t, withUID("u-1", map[string]any{"name": "Body", "translation": []float64{1, 2, 3}}))
	if d := diffOf(t, same, same); len(d.Changes) != 0 {
		t.Errorf("expected no changes, got %+v", d.Changes)
	}
}

// ── unit: the matcher's own conservatism ──────────────────────────────────────

func TestSimilarityAndStrictBest(t *testing.T) {
	field := func(values ...string) signature {
		f := make([]sigField, len(values))
		for i, v := range values {
			f[i] = sigField{v, 1, stated}
		}
		return signature{fields: f, specific: true}
	}

	if got := similarity(field("a", "b"), field("a", "b")); got != 1 {
		t.Errorf("identical signatures: similarity = %v, want 1", got)
	}
	if got := similarity(field("a", "b"), field("a", "c")); got != 0.5 {
		t.Errorf("one field of two: similarity = %v, want 0.5", got)
	}
	// Extra fields count against the score rather than being ignored, so a mesh
	// that gained a primitive does not read as unchanged content.
	if got := similarity(field("a"), field("a", "b")); got != 0.5 {
		t.Errorf("one field of two present: similarity = %v, want 0.5", got)
	}

	// A field neither side stated is not agreement, and not a denominator either:
	// the score is measured over what the two elements say about themselves.
	kinded := func(kinds ...fieldKind) signature {
		f := make([]sigField, len(kinds))
		for i, k := range kinds {
			f[i] = sigField{"same", 1, k}
		}
		return signature{fields: f, specific: true}
	}
	if got := similarity(kinded(stated, unstated), kinded(stated, unstated)); got != 1 {
		t.Errorf("a field neither side stated must drop out: similarity = %v, want 1", got)
	}
	if got := similarity(kinded(unstated, unstated), kinded(unstated, unstated)); got != 0 {
		t.Errorf("nothing stated at all: similarity = %v, want 0", got)
	}
	// Written against not-written is a real difference, so it counts and disagrees.
	if got := similarity(kinded(stated, stated), kinded(stated, unstated)); got != 0.5 {
		t.Errorf("stated against unstated: similarity = %v, want 0.5", got)
	}
	// An opaque value is the same string on both sides and still means different
	// elements, so it never scores — but its weight holds the denominator up.
	if got := similarity(kinded(stated, opaque), kinded(stated, opaque)); got != 0.5 {
		t.Errorf("identical opaque values must not agree: similarity = %v, want 0.5", got)
	}
	// A floor holds it up the other way: the fields an element has whether or not
	// anyone wrote them stay in the denominator, so dropping four of five cannot
	// promote the one that survived from a fifth of the score to all of it.
	floored := func(floor int, kinds ...fieldKind) signature {
		s := kinded(kinds...)
		s.floor = floor
		return s
	}
	one := floored(5, stated, unstated, unstated, unstated, unstated)
	if got := similarity(one, one); got != 0.2 {
		t.Errorf("one stated field of five: similarity = %v, want 0.2", got)
	}
	// It is a floor and not a denominator: a descriptor with more weight in play
	// than the floor is scored over what is in play.
	if got := similarity(floored(1, stated, stated), floored(1, stated, unstated)); got != 0.5 {
		t.Errorf("a floor below the live weight must not bind: similarity = %v, want 0.5", got)
	}

	row := []float64{0.9, 0.9}
	at := func(k int) float64 { return row[k] }
	if _, _, ok := strictBest(len(row), at); ok {
		t.Error("a tie must not produce a winner")
	}
	row[1] = 0.6
	if best, _, ok := strictBest(len(row), at); !ok || best != 0 {
		t.Errorf("strictBest = %d, ok = %v; want 0, true", best, ok)
	}
	// A score below the threshold is not a candidate: the matrix stores zero for
	// every pair the build loop turned away, and a zero must not win by default.
	row[0], row[1] = 0, 0.4
	if _, _, ok := strictBest(len(row), at); ok {
		t.Error("nothing at or above the threshold must not produce a winner")
	}
}

// ── quantized canonical geometry signatures (#42, matching only) ──────────────

// perturb3 returns a copy of v with component c of vertex i nudged by delta,
// through float32 arithmetic so the change is exactly what an exporter's
// rounding produces.
func perturb3(v [][3]float32, i, c int, delta float32) [][3]float32 {
	out := make([][3]float32, len(v))
	copy(out, v)
	out[i][c] += delta
	return out
}

// The headline case and its scope fence in one test. A mesh renamed AND
// re-exported with quantization-level float jitter still pairs — the quantized
// digest treats the jitter as the noise it is — while the very same jitter on a
// name-matched mesh still REPORTS its POSITION stream as changed, with the two
// exact hashes differing: matching is quantized, the wire is not.
func TestQuantizedSignatureIgnoresReexportJitter(t *testing.T) {
	pos := ramp(16)
	// 1e-9 flips real float32 bits on the small components (vertex 0 sits at the
	// origin, where an ulp is far smaller than 1e-9) but rounds away at POSITION's
	// 8-decimal quantum.
	jittered := perturb3(pos, 0, 0, 1e-9)

	renamed := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: pos, indices: seq(16)}),
		geometryGLB(t, geometrySpec{mesh: "Shell", positions: jittered, indices: seq(16)}),
	)
	c := mustRename(t, renamed, "meshes/Shell", "Hull")
	if got := afterText(c); !strings.Contains(got, "matched by content") || strings.Contains(got, "%") {
		t.Errorf("after = %q, want a full-confidence content match", got)
	}

	// The fence: same jitter, same name — the geometry row still reports, and its
	// descriptors carry differing *exact* hashes, not the quantized digest.
	control := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: pos, indices: seq(16)}),
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: jittered, indices: seq(16)}),
	)
	row := mustChange(t, control, "meshes/Hull/primitives/0/geometry/POSITION")
	if row.Before == row.After {
		t.Fatalf("sub-quantum jitter must still be reported on the wire: %s", valueOf(row))
	}
	for _, v := range []any{row.Before, row.After} {
		s := fmt.Sprint(v)
		if !strings.Contains(s, " hash=") || strings.Contains(s, "qhash=") {
			t.Errorf("wire descriptor %q must carry the exact hash, never the quantized digest", s)
		}
	}
}

// A vertex permutation with its index buffer remapped is the same mesh: the
// canonical order sorts the records, so the shuffle cancels.
func TestCanonicalOrderingSurvivesVertexReorder(t *testing.T) {
	pos := ramp(6)
	indices := []uint32{0, 1, 2, 3, 4, 5}
	// Reverse the vertex array and remap the indices through the permutation.
	rev := make([][3]float32, len(pos))
	remapped := make([]uint32, len(indices))
	for i, p := range pos {
		rev[len(pos)-1-i] = p
	}
	for j, ix := range indices {
		remapped[j] = uint32(len(pos) - 1 - int(ix))
	}

	d := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: pos, indices: indices}),
		geometryGLB(t, geometrySpec{mesh: "Shell", positions: rev, indices: remapped}),
	)
	c := mustRename(t, d, "meshes/Shell", "Hull")
	if got := afterText(c); strings.Contains(got, "%") {
		t.Errorf("after = %q, want full similarity for a pure reorder", got)
	}
}

// Rotating a triangle keeps its winding, and the triangle list is a set: both
// rewrites are what an optimiser does to an untouched mesh.
func TestCanonicalOrderingSurvivesTriangleRotationAndOrder(t *testing.T) {
	pos := ramp(4)
	d := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: pos, indices: []uint32{0, 1, 2, 2, 3, 0}}),
		// The same two triangles: each cyclically rotated, and the list reordered.
		geometryGLB(t, geometrySpec{mesh: "Shell", positions: pos, indices: []uint32{3, 0, 2, 1, 2, 0}}),
	)
	mustRename(t, d, "meshes/Shell", "Hull")
}

// The reflected triangle list is NOT the same mesh: rotation preserves winding,
// so a mirrored surface keeps a different digest and stays a removal plus an
// addition.
func TestMirroredGeometryDoesNotMatch(t *testing.T) {
	pos := ramp(3)
	d := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: pos, indices: []uint32{0, 1, 2}}),
		geometryGLB(t, geometrySpec{mesh: "Shell", positions: pos, indices: []uint32{0, 2, 1}}),
	)
	if got := kindAt(d, "meshes/Hull"); got != Removed {
		t.Errorf("meshes/Hull: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "meshes/Shell"); got != Added {
		t.Errorf("meshes/Shell: kind = %q, want %q", got, Added)
	}
}

// A quantum that overflows int64 is refused like a NaN: Go's out-of-range
// float→int conversion is implementation-dependent (amd64 saturates to
// minInt64), so without the guard every |coordinate| beyond ~9.2e10 collapsed
// to ONE quantum and genuinely different out-of-range geometries
// canonicalized equal.
func TestQuantizeRefusesOverflowingQuanta(t *testing.T) {
	for _, v := range []float64{1e12, 2e12, -1e12, -2e12, 3.4e38, -3.4e38} {
		if q, ok := quantize(v, 1e8); ok {
			t.Errorf("quantize(%g, 1e8) = %d, ok — want refusal for an overflowing quantum", v, q)
		}
	}
	if q, ok := quantize(12345.6789, 1e8); !ok || q != 1234567890000 {
		t.Errorf("quantize(12345.6789, 1e8) = %d, %v — want 1234567890000, true", q, ok)
	}
	if q, ok := quantize(-0.0, 1e8); !ok || q != 0 {
		t.Errorf("quantize(-0, 1e8) = %d, %v — want 0, true", q, ok)
	}
}

// Two genuinely different out-of-range geometries fall back to the exact
// descriptors and stay a removal plus an addition — while byte-identical
// out-of-range geometry still pairs through the very same fallback. The
// overflow refusal loses nothing but the guess.
func TestOutOfRangeGeometryNeverPairsAcrossARename(t *testing.T) {
	far := func(c float32) [][3]float32 {
		return [][3]float32{{c, 0, 0}, {c + 1e6, 0, 0}, {c, 1e6, 0}}
	}
	d := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "MeshOld", positions: far(1e12), indices: seq(3)}),
		geometryGLB(t, geometrySpec{mesh: "MeshNew", positions: far(2e12), indices: seq(3)}),
	)
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("two different out-of-range geometries are not one mesh: %+v", c)
		}
	})
	if got := kindAt(d, "meshes/MeshOld"); got != Removed {
		t.Errorf("meshes/MeshOld: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "meshes/MeshNew"); got != Added {
		t.Errorf("meshes/MeshNew: kind = %q, want %q", got, Added)
	}

	// The control: identical out-of-range bytes still pair — the exact-hash
	// fallback carries the rename without any canonical digest.
	control := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "MeshOld", positions: far(1e12), indices: seq(3)}),
		geometryGLB(t, geometrySpec{mesh: "MeshNew", positions: far(1e12), indices: seq(3)}),
	)
	mustRename(t, control, "meshes/MeshNew", "MeshOld")
}

// skinnedGLB builds a single-mesh GLB whose primitive carries POSITION,
// JOINTS_0 (ubyte VEC4) and WEIGHTS_0 (float VEC4) — the shape of every
// skinned character asset — with the vertex array permuted by perm and the
// index buffer remapped through it.
func skinnedGLB(t *testing.T, name string, perm []int) []byte {
	t.Helper()
	pos := ramp(6)
	n := len(pos)
	joints := make([][4]byte, n)
	weights := make([][4]float32, n)
	for i := range n {
		joints[i] = [4]byte{byte(i % 4), byte((i + 1) % 4), 0, 0}
		weights[i] = [4]float32{0.75, 0.25, 0, 0}
	}
	indices := []uint32{0, 1, 2, 3, 4, 5}

	permPos := make([][3]float32, n)
	permJoints := make([][4]byte, n)
	permWeights := make([][4]float32, n)
	for i, at := range perm {
		permPos[at], permJoints[at], permWeights[at] = pos[i], joints[i], weights[i]
	}
	remapped := make([]uint32, len(indices))
	for k, ix := range indices {
		remapped[k] = uint32(perm[ix])
	}

	b := &binWriter{doc: &gltf.Document{Asset: gltf.Asset{Version: "2.0"}}}
	attrs := gltf.PrimitiveAttributes{}
	attrs[gltf.POSITION] = b.vec3(t, permPos, true)
	jointBytes := make([]byte, 0, 4*n)
	for _, j := range permJoints {
		jointBytes = append(jointBytes, j[0], j[1], j[2], j[3])
	}
	jview := b.view(jointBytes, 0)
	attrs[gltf.JOINTS_0] = b.accessor(&gltf.Accessor{
		BufferView: &jview, ComponentType: gltf.ComponentUbyte,
		Type: gltf.AccessorVec4, Count: n,
	})
	weightBytes := make([]byte, 0, 16*n)
	for _, w := range permWeights {
		for _, c := range w {
			bits := math.Float32bits(c)
			weightBytes = append(weightBytes, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
		}
	}
	wview := b.view(weightBytes, 0)
	attrs[gltf.WEIGHTS_0] = b.accessor(&gltf.Accessor{
		BufferView: &wview, ComponentType: gltf.ComponentFloat,
		Type: gltf.AccessorVec4, Count: n,
	})
	idx := b.scalarU32(remapped)
	b.doc.Meshes = []*gltf.Mesh{{Name: name, Primitives: []*gltf.Primitive{
		{Attributes: attrs, Indices: &idx},
	}}}
	mesh := 0
	b.doc.Nodes = []*gltf.Node{{Name: "Body", Mesh: &mesh}}
	b.doc.Scenes = []*gltf.Scene{{Name: "Scene", Nodes: []int{0}}}
	scene := 0
	b.doc.Scene = &scene
	b.doc.Buffers = []*gltf.Buffer{{ByteLength: len(b.bin), Data: b.bin}}
	blob, err := encodeBlob(b.doc, true)
	if err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	return blob
}

// A skinned mesh canonicalizes like any other: JOINTS_0 (ubyte) passes through
// exactly, unquantized, instead of disqualifying the whole primitive — the
// header's "integer streams are never quantized" read as pass-through, not as
// refusal. Renamed AND vertex-reordered, it still pairs at full confidence.
func TestSkinnedMeshSurvivesVertexReorderAcrossARename(t *testing.T) {
	identityPerm := []int{0, 1, 2, 3, 4, 5}
	shuffled := []int{5, 3, 1, 4, 0, 2}
	d := diffOf(t,
		skinnedGLB(t, "MeshOld", identityPerm),
		skinnedGLB(t, "MeshNew", shuffled),
	)
	c := mustRename(t, d, "meshes/MeshNew", "MeshOld")
	if got := afterText(c); !strings.Contains(got, "matched by content") || strings.Contains(got, "%") {
		t.Errorf("after = %q, want a full-confidence content match", got)
	}
}

// Each semantic has its own tolerance — trimesh's constants — so noise is
// forgiven at the precision that semantic is authored at, and a real edit one
// order of magnitude above it still separates the meshes.
func TestQuantizationPrecisionPerSemantic(t *testing.T) {
	pos := ramp(8)
	tests := []struct {
		name       string
		head       geometrySpec
		wantPaired bool
	}{
		{"POSITION jitter at 1e-9 pairs", geometrySpec{positions: perturb3(pos, 0, 0, 1e-9)}, true},
		{"POSITION at 1e-7 does not", geometrySpec{positions: perturb3(pos, 0, 0, 1e-7)}, false},
		{"NORMAL jitter at 1e-3 pairs", geometrySpec{positions: pos, normals: perturb3(ramp(8), 0, 0, 1e-3)}, true},
		{"NORMAL at 1e-1 does not", geometrySpec{positions: pos, normals: perturb3(ramp(8), 0, 0, 1e-1)}, false},
		{"TEXCOORD jitter at 1e-5 pairs", geometrySpec{positions: pos, uvs: [][2]float32{{1e-5, 0}, {0, 0}, {0, 1}, {1, 0}, {1, 1}, {0.5, 0}, {0, 0.5}, {0.5, 0.5}}}, true},
		{"TEXCOORD at 1e-3 does not", geometrySpec{positions: pos, uvs: [][2]float32{{1e-3, 0}, {0, 0}, {0, 1}, {1, 0}, {1, 1}, {0.5, 0}, {0, 0.5}, {0.5, 0.5}}}, false},
	}
	baseUVs := [][2]float32{{0, 0}, {0, 0}, {0, 1}, {1, 0}, {1, 1}, {0.5, 0}, {0, 0.5}, {0.5, 0.5}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := geometrySpec{mesh: "Hull", positions: pos}
			head := tc.head
			head.mesh = "Shell"
			if head.normals != nil {
				base.normals = ramp(8)
			}
			if head.uvs != nil {
				base.uvs = baseUVs
			}
			d := diffOf(t, geometryGLB(t, base), geometryGLB(t, head))
			if tc.wantPaired {
				mustRename(t, d, "meshes/Shell", "Hull")
			} else if got := kindAt(d, "meshes/Hull"); got != Removed {
				t.Errorf("meshes/Hull: kind = %q, want %q (an edit above the quantum is a different mesh)", got, Removed)
			}
		})
	}
}

// Duplicate vertex records collapse to one canonical rank, so shuffling which
// array slot holds which duplicate — with the indices following — cancels.
func TestDuplicateVertexPermutationCancels(t *testing.T) {
	a, b, c := [3]float32{0, 0, 0}, [3]float32{1, 0, 0}, [3]float32{0, 1, 0}
	d := diffOf(t,
		geometryGLB(t, geometrySpec{mesh: "Hull", positions: [][3]float32{a, b, a, c}, indices: []uint32{0, 1, 3, 2, 3, 1}}),
		// The duplicate of `a` swaps slots; the triangles are the same by rank.
		geometryGLB(t, geometrySpec{mesh: "Shell", positions: [][3]float32{a, a, b, c}, indices: []uint32{0, 2, 3, 1, 3, 2}}),
	)
	cch := mustRename(t, d, "meshes/Shell", "Hull")
	if got := afterText(cch); strings.Contains(got, "%") {
		t.Errorf("after = %q, want full similarity", got)
	}
}

// Data that cannot be decoded keeps the exact descriptor — the same
// hash-or-<unreadable> string the wire uses — rather than a guessed canonical
// digest. Mirrors primitiveCentroid's refuse-don't-guess rule.
func TestSparseOrUnreadableAccessorFallsBackToExactDescriptor(t *testing.T) {
	t.Run("external buffer", func(t *testing.T) {
		doc, err := parseDoc(externalBufferDoc(t, "scene.bin", 12))
		if err != nil {
			t.Fatal(err)
		}
		side := newMeshSide(doc)
		got := side.streamDescriptor(doc.Meshes[0].Primitives[0], "POSITION")
		if !strings.Contains(got, "hash=<unreadable>") || strings.Contains(got, "qhash") {
			t.Errorf("descriptor = %q, want the exact unreadable fallback", got)
		}
	})

	t.Run("sparse accessor", func(t *testing.T) {
		blob := doc(t, map[string]any{
			"buffers": []any{map[string]any{"byteLength": 48, "uri": "data:application/octet-stream;base64,AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}},
			"bufferViews": []any{
				map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 24},
				map[string]any{"buffer": 0, "byteOffset": 24, "byteLength": 24},
			},
			"accessors": []any{map[string]any{
				"bufferView": 0, "componentType": 5126, "count": 2, "type": "VEC3",
				"sparse": map[string]any{
					"count":   1,
					"indices": map[string]any{"bufferView": 1, "componentType": 5123},
					"values":  map[string]any{"bufferView": 1},
				},
			}},
			"meshes": []any{map[string]any{"name": "Hull", "primitives": []any{
				map[string]any{"attributes": map[string]any{"POSITION": 0}},
			}}},
		})
		docP, err := parseDoc(blob)
		if err != nil {
			t.Fatal(err)
		}
		side := newMeshSide(docP)
		got := side.streamDescriptor(docP.Meshes[0].Primitives[0], "POSITION")
		if !strings.Contains(got, "hash=<unreadable>") || strings.Contains(got, "qhash") {
			t.Errorf("descriptor = %q, want the exact unreadable fallback", got)
		}
	})
}

// The canonical digest is computed once per primitive per diff: the second
// lookup must come from the meshSide cache, not from the bytes.
func TestQuantizedDigestIsMemoized(t *testing.T) {
	blob := geometryGLB(t, geometrySpec{positions: ramp(8), indices: seq(8)})
	docP, err := parseDoc(blob)
	if err != nil {
		t.Fatal(err)
	}
	side := newMeshSide(docP)
	p := docP.Meshes[0].Primitives[0]

	first := side.streamDescriptor(p, "POSITION")
	// Rewrite the first POSITION component to 1.0 — far beyond any quantum — and
	// look again through the same side: a memo hit returns the old digest.
	posAcc := docP.Accessors[p.Attributes["POSITION"]]
	span, _, ok := accessorSpan(docP, posAcc)
	if !ok {
		t.Fatal("fixture POSITION must be readable")
	}
	span[0], span[1], span[2], span[3] = 0x00, 0x00, 0x80, 0x3f // float32(1.0), little-endian
	if got := side.streamDescriptor(p, "POSITION"); got != first {
		t.Errorf("second lookup = %q, want the memoized %q", got, first)
	}
	// A fresh side sees the mutation, which proves the memo (and not an
	// insensitive digest) is what held the value steady.
	if got := newMeshSide(docP).streamDescriptor(p, "POSITION"); got == first {
		t.Error("a fresh meshSide must digest the mutated bytes differently")
	}
}

// ── unnamed-element pairing (#42) ─────────────────────────────────────────────

// testPairing is an empty pairing for driving a single tier directly.
func testPairing() pairing {
	return pairing{headOf: map[int]int{}, baseOf: map[int]int{}, how: map[int]matchEvidence{}}
}

// An unchanged file with no names at all must diff empty — including
// byte-identical duplicates, which the exact tier refuses as ambiguous and the
// positional fallback still pairs slot for slot.
func TestUnnamedUnchangedFileHasEmptyDiff(t *testing.T) {
	same := nodesDoc(t,
		map[string]any{"translation": []float64{1, 1, 1}},
		map[string]any{"translation": []float64{1, 1, 1}}, // byte-identical duplicate
		map[string]any{"translation": []float64{2, 0, 0}},
	)
	if d := diffOf(t, same, same); len(d.Changes) != 0 {
		t.Errorf("expected no changes, got %v", paths(d))
	}
}

// No insertion, one edit: the positional fallback pairs node[0] with node[0],
// exactly as the pre-#42 key match did, so an unnamed node edited in place
// still reports under its own index instead of as a removal plus an addition.
func TestUnnamedEditedNodeReportsUnderItsIndex(t *testing.T) {
	base := nodesDoc(t, map[string]any{"translation": []float64{1, 0, 0}})
	head := nodesDoc(t, map[string]any{"translation": []float64{1, 0, 5}})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/node[0]"); got != Modified {
		t.Errorf("nodes/node[0]: kind = %q, want %q", got, Modified)
	}
	mustChange(t, d, "nodes/node[0]/translation")
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("nothing has a name here: %+v", c)
		}
	})
}

// An insertion plus an edit of a survivor is the honest limit of the cascade
// fix: the edited survivor's content no longer matches exactly, its sparse
// stated content (a translation, 1/5) is below the scored threshold, and the
// insertion shifted its index out from under the positional fallback — so it is
// reported as the removal and addition it might genuinely be. Conservative by
// construction: a guessed pairing here could hide a real deletion.
func TestUnnamedInsertionPlusEditFallsBackToPosition(t *testing.T) {
	base := nodesDoc(t,
		map[string]any{"translation": []float64{1, 0, 0}},
		map[string]any{"translation": []float64{2, 0, 0}},
	)
	head := nodesDoc(t,
		map[string]any{"translation": []float64{9, 9, 9}}, // inserted at the front
		map[string]any{"translation": []float64{1, 0, 0}}, // untouched survivor
		map[string]any{"translation": []float64{2, 0, 5}}, // edited survivor
	)

	d := diffOf(t, base, head)
	// The untouched survivor paired exactly and vanished from the diff.
	if c := findChange(d, "nodes/node[1]"); c != nil {
		t.Errorf("the untouched survivor must pair silently, got %+v", c)
	}
	// The rest is reported without a guess. The removed base node's path takes
	// the #1 suffix because the head-side namespace owns `node[1]` (pathKeys).
	for path, want := range map[string]ChangeKind{
		"nodes/node[0]":   Added,
		"nodes/node[2]":   Added,
		"nodes/node[1]#1": Removed,
	} {
		if got := kindAt(d, path); got != want {
			t.Errorf("%s: kind = %q, want %q", path, got, want)
		}
	}
}

// Two identical unnamed candidates on one side are interchangeable, and the
// exact tier must refuse the coin flip: ambiguity never pairs.
func TestExactContentTierRefusesAmbiguity(t *testing.T) {
	sig := func() signature {
		return signature{fields: []sigField{{"translation=[1 1 1]", 1, stated}}, specific: true}
	}
	ent := func(key string) entity { return entity{key: key, sig: sig} }

	p := testPairing()
	matchByExactContent(p, []entity{ent("node[0]"), ent("node[1]")}, []entity{ent("node[0]")}, nil)
	if len(p.headOf) != 0 {
		t.Errorf("two identical candidates paired anyway: %v", p.headOf)
	}

	// Unique against unique is the whole tier: full-confidence content evidence.
	p = testPairing()
	matchByExactContent(p, []entity{ent("node[0]")}, []entity{ent("node[3]")}, nil)
	if got, ok := p.headOf[0]; !ok || got != 0 {
		t.Fatalf("a unique identical pair must match, got %v (ok=%v)", got, ok)
	}
	if ev := p.how[0]; ev.by != byContent || ev.similarity != 1 {
		t.Errorf("evidence = %+v, want a full-similarity content match", ev)
	}
}

// A signature carrying an opaque field — an index-derived referent key that
// cannot be verified cross-file — disqualifies the element from exact matching
// outright, even when the opaque strings happen to be equal.
func TestExactContentTierRefusesOpaqueFields(t *testing.T) {
	t.Run("unit", func(t *testing.T) {
		sig := func() signature {
			return signature{fields: []sigField{
				{"mesh=mesh[0]", meshWeight, opaque},
				{"translation=[1 1 1]", 1, stated},
			}, specific: true}
		}
		p := testPairing()
		matchByExactContent(p, []entity{{key: "node[0]", sig: sig}}, []entity{{key: "node[7]", sig: sig}}, nil)
		if len(p.headOf) != 0 {
			t.Errorf("an unverifiable referent must not assert identity: %v", p.headOf)
		}
	})

	// End to end: a node drawing an unpaired unnamed mesh never exact-pairs, so
	// its content twin at a shifted index is reported as the addition it may be.
	t.Run("through a document", func(t *testing.T) {
		// Two identical unnamed meshes per side keep the mesh collection itself
		// unpairable by content (ambiguous), so the node's mesh reference stays an
		// index — opaque.
		base := meshListDoc(t, []string{"", ""},
			map[string]any{"mesh": 0, "translation": []float64{1, 0, 0}},
		)
		head := meshListDoc(t, []string{"", ""},
			map[string]any{"translation": []float64{9, 9, 9}},
			map[string]any{"mesh": 0, "translation": []float64{1, 0, 0}},
		)

		d := diffOf(t, base, head)
		if got := kindAt(d, "nodes/node[1]"); got != Added {
			t.Errorf("the content twin must stay an addition, got %q", got)
		}
		walk(d.Changes, func(c *DiffChange, _ int) {
			if c.Kind == Renamed {
				t.Errorf("nothing here is nameable: %+v", c)
			}
		})
	})
}

// The issue's optimizer scenario, end to end: a pipeline tool strips every name
// from an otherwise identical file. Materials pair by content, meshes pair on
// their streams plus the material pair-token, nodes pair on the mesh pair-token
// plus placement — the cross-collection threading in Diff — and every pair is
// reported as the one-sided rename it is, with the content evidence stated.
// Zero removals: nothing was deleted, and the diff must not say otherwise.
func TestOptimizerStrippedNamesMatchByContent(t *testing.T) {
	scene := func(named bool) []byte {
		name := func(s string) map[string]any {
			if named {
				return map[string]any{"name": s}
			}
			return map[string]any{}
		}
		with := func(m map[string]any, kv map[string]any) map[string]any {
			for k, v := range kv {
				m[k] = v
			}
			return m
		}
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
			"nodes": []any{
				with(name("Body"), map[string]any{"mesh": 0, "translation": []float64{0, 1, 0}}),
				with(name("Wheel"), map[string]any{"mesh": 1, "translation": []float64{1.3, 0.45, 0.75}}),
			},
			"meshes": []any{
				with(name("BodyMesh"), map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{}, "material": 0}}}),
				with(name("WheelMesh"), map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{}, "material": 1}}}),
			},
			"materials": []any{
				with(name("Paint"), map[string]any{"pbrMetallicRoughness": map[string]any{"baseColorFactor": []float64{0.8, 0.1, 0.1, 1}}}),
				with(name("Rubber"), map[string]any{"pbrMetallicRoughness": map[string]any{
					"baseColorFactor": []float64{0.05, 0.05, 0.05, 1}, "metallicFactor": 0, "roughnessFactor": 0.9,
				}}),
			},
		})
	}

	d := diffOf(t, scene(true), scene(false))
	for path, oldName := range map[string]string{
		"nodes/node[0]":         "Body",
		"nodes/node[1]":         "Wheel",
		"meshes/mesh[0]":        "BodyMesh",
		"meshes/mesh[1]":        "WheelMesh",
		"materials/material[0]": "Paint",
		"materials/material[1]": "Rubber",
	} {
		c := mustRename(t, d, path, oldName)
		if got := afterText(c); !strings.Contains(got, "matched by content") {
			t.Errorf("%s: after = %q, want the content evidence", path, got)
		}
	}
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Removed || c.Kind == Added {
			t.Errorf("nothing was deleted or created: %+v", c)
		}
	})
}

// Two meshes whose only common ground is `material[0]` on both sides — an
// index-derived value pointing at unpaired unnamed materials — have agreed on
// nothing: the material field is opaque, the streams disagree, and the pair
// stays a removal plus an addition.
func TestMeshSignatureUnnamedMaterialIsNotAgreement(t *testing.T) {
	base := geometryGLB(t, geometrySpec{
		mesh: "Hull", positions: ramp(6), materials: []string{""}, material: intPtr(0),
	})
	head := geometryGLB(t, geometrySpec{
		mesh: "Shell", positions: sculptAll(ramp(6), 5), materials: []string{""}, material: intPtr(0),
	})

	d := diffOf(t, base, head)
	if got := kindAt(d, "meshes/Hull"); got != Removed {
		t.Errorf("meshes/Hull: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "meshes/Shell"); got != Added {
		t.Errorf("meshes/Shell: kind = %q, want %q", got, Added)
	}
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("a shared array index is not evidence: %+v", c)
		}
	})
}

// ── structural matching: the node tree (#42) ──────────────────────────────────

// unnamedSubtreeDoc builds [G(unnamed) → P(unnamed, t=(5,5,5)) → k1, k2], with
// `extra` unnamed root nodes inserted at the FRONT of the array so every index
// below shifts — the edit that used to cascade. The kids' parent is unnamed, so
// their parent field is opaque and no per-element content tier can pair them:
// only the tree can.
func unnamedSubtreeDoc(t *testing.T, extra ...[]float64) []byte {
	t.Helper()
	n := len(extra)
	nodes := make([]any, 0, n+4)
	roots := make([]int, 0, n+1)
	for i, tr := range extra {
		nodes = append(nodes, map[string]any{"translation": tr})
		roots = append(roots, i)
	}
	nodes = append(nodes,
		map[string]any{"children": []int{n + 1}},                                           // G
		map[string]any{"translation": []float64{5, 5, 5}, "children": []int{n + 2, n + 3}}, // P
		map[string]any{"translation": []float64{1, 0, 0}},                                  // k1
		map[string]any{"translation": []float64{2, 0, 0}},                                  // k2
	)
	roots = append(roots, n)
	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": roots}},
		"nodes":  nodes,
	})
}

// One unrelated node inserted upstream of a fully unnamed subtree: the subtree
// anchors on its content hash and survives the re-index whole, so the diff is
// the one thing that happened — a single added node.
func TestUnnamedSubtreeSurvivesInsertionByAnchoring(t *testing.T) {
	base := unnamedSubtreeDoc(t)
	head := unnamedSubtreeDoc(t, []float64{9, 9, 9})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/node[0]"); got != Added {
		t.Errorf("nodes/node[0]: kind = %q, want %q", got, Added)
	}
	nodes := mustChange(t, d, "nodes")
	if len(nodes.Children) != 1 {
		t.Errorf("the insertion is the only change; nodes reported %d: %v", len(nodes.Children), paths(d))
	}
}

// The same subtree moved under a new named parent: the anchor pairs the whole
// subtree (the hash excludes the parent field — a moved subtree's root differs
// on it by definition), the root reports its re-parent, and the children —
// whose parent identity is unchanged — emit nothing.
func TestMovedUnnamedSubtreeReportsReparented(t *testing.T) {
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			map[string]any{"translation": []float64{5, 5, 5}, "children": []int{1, 2}}, // P at the root
			map[string]any{"translation": []float64{1, 0, 0}},
			map[string]any{"translation": []float64{2, 0, 0}},
		},
	})
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			map[string]any{"name": "Rack", "children": []int{1}},
			map[string]any{"translation": []float64{5, 5, 5}, "children": []int{2, 3}}, // P moved under Rack
			map[string]any{"translation": []float64{1, 0, 0}},
			map[string]any{"translation": []float64{2, 0, 0}},
		},
	})

	d := diffOf(t, base, head)
	if got := kindAt(d, "nodes/Rack"); got != Added {
		t.Errorf("nodes/Rack: kind = %q, want %q", got, Added)
	}
	// The subtree's root is REPARENTED — an unnamed pair may carry that label
	// (the never-label invariant is about `renamed` only; parent keys resolve
	// within each file) — and the structural evidence is stated on it.
	root := mustChange(t, d, "nodes/node[1]")
	if root.Kind != Reparented {
		t.Errorf("nodes/node[1]: kind = %q, want %q", root.Kind, Reparented)
	}
	if got, want := afterText(root), "Rack (matched by structure)"; got != want {
		t.Errorf("after = %q, want %q", got, want)
	}
	c := mustChange(t, d, "nodes/node[1]/parent")
	if c.Before != rootParentLabel || c.After != "Rack" {
		t.Errorf("parent = %v → %v, want %s → Rack", c.Before, c.After, rootParentLabel)
	}
	// The children moved with their parent — that is one change, not three.
	for _, quiet := range []string{"nodes/node[2]", "nodes/node[3]"} {
		if c := findChange(d, quiet); c != nil {
			t.Errorf("the child moved with its subtree, nothing about it changed: %+v", c)
		}
	}
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Removed || c.Kind == Renamed {
			t.Errorf("nothing was deleted or renamed: %+v", c)
		}
	})
}

// The bottom-up half: a renamed container with no uid and sparse own content —
// too little for the scored tier's floors — still pairs when at least two of
// its matched children say where it went, and the evidence states exactly what
// was measured.
func TestBottomUpMatchesRenamedContainer(t *testing.T) {
	group := func(name string) []byte {
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes": []any{
				map[string]any{"name": name, "translation": []float64{1, 2, 3}, "children": []int{1, 2}},
				map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
				map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
			},
		})
	}

	d := diffOf(t, group("Group_Old"), group("Group_New"))
	c := mustRename(t, d, "nodes/Group_New", "Group_Old")
	if got, want := afterText(c), "Group_New (matched by structure, ~100% of descendants shared)"; got != want {
		t.Errorf("after = %q, want %q", got, want)
	}
	// The children stayed under the same (paired) parent: no re-parent rows.
	for _, quiet := range []string{"nodes/Ka", "nodes/Kb"} {
		if c := findChange(d, quiet); c != nil {
			t.Errorf("the child did not change: %+v", c)
		}
	}
}

// One shared child is the child-count-as-evidence trap the signature floors
// exist for: "renamed parent of a single child, no uid" stays an honest miss.
func TestBottomUpRequiresTwoMatchedDescendants(t *testing.T) {
	group := func(name string) []byte {
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes": []any{
				map[string]any{"name": name, "translation": []float64{1, 2, 3}, "children": []int{1}},
				map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
			},
		})
	}

	d := diffOf(t, group("Group_Old"), group("Group_New"))
	if got := kindAt(d, "nodes/Group_Old"); got != Removed {
		t.Errorf("nodes/Group_Old: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Group_New"); got != Added {
		t.Errorf("nodes/Group_New: kind = %q, want %q", got, Added)
	}
}

// Names are inside the subtree hash, so content-identical subtrees whose node
// names differ do not anchor — with names excluded, the pinned "groups at
// origin" conservatism cases would anchor and hide a deletion.
func TestAnchorRequiresIdenticalNames(t *testing.T) {
	group := func(parent, k1, k2 string) []byte {
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes": []any{
				map[string]any{"name": parent, "children": []int{1, 2}},
				map[string]any{"name": k1, "translation": []float64{1, 0, 0}},
				map[string]any{"name": k2, "translation": []float64{2, 0, 0}},
			},
		})
	}

	d := diffOf(t, group("Ctrl_Old", "C1", "C2"), group("Light_Rig", "L1", "L2"))
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("identical shape under different names is not identity: %+v", c)
		}
	})
	if got := kindAt(d, "nodes/Ctrl_Old"); got != Removed {
		t.Errorf("nodes/Ctrl_Old: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/Light_Rig"); got != Added {
		t.Errorf("nodes/Light_Rig: kind = %q, want %q", got, Added)
	}
}

// subtreeForest builds n unnamed [P → k1, k2] subtrees per document, each with
// its own kid translations so every subtree hash is unique. Used to drive
// matchByStructure directly.
func subtreeForest(t *testing.T, n int) *nodeIndex {
	t.Helper()
	nodes := make([]any, 0, 3*n)
	roots := make([]int, 0, n)
	for s := range n {
		at := 3 * s
		roots = append(roots, at)
		nodes = append(nodes,
			map[string]any{"children": []int{at + 1, at + 2}},
			map[string]any{"translation": []float64{float64(s), 1, 0}},
			map[string]any{"translation": []float64{float64(s), 2, 0}},
		)
	}
	parsed, err := parseDoc(doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": roots}},
		"nodes":  nodes,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return indexNodes(parsed)
}

// Two identical unnamed subtrees on one side against one on the other: the
// anchor bucket is ambiguous, and ambiguity never pairs.
func TestAnchorRefusesAmbiguousDuplicates(t *testing.T) {
	one := subtreeForest(t, 1)
	// Two copies of the SAME subtree: duplicate the forest's only spec.
	two := subtreeForest(t, 1)
	dupDoc, err := parseDoc(doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 3}}},
		"nodes": []any{
			map[string]any{"children": []int{1, 2}},
			map[string]any{"translation": []float64{0, 1, 0}},
			map[string]any{"translation": []float64{0, 2, 0}},
			map[string]any{"children": []int{4, 5}},
			map[string]any{"translation": []float64{0, 1, 0}},
			map[string]any{"translation": []float64{0, 2, 0}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	two = indexNodes(dupDoc)

	p := testPairing()
	matchByStructure(p, two, one)
	if len(p.headOf) != 0 {
		t.Errorf("an ambiguous anchor bucket paired anyway: %v", p.headOf)
	}

	// The unambiguous control: one identical subtree per side anchors whole.
	p = testPairing()
	matchByStructure(p, subtreeForest(t, 1), one)
	if len(p.headOf) != 3 {
		t.Errorf("paired %d nodes, want the whole 3-node subtree", len(p.headOf))
	}
	for i := range 3 {
		if ev := p.how[i]; ev.by != byStructure {
			t.Errorf("node %d evidence = %+v, want %s", i, ev, byStructure)
		}
	}
}

// Over renameLimit² leftovers the tier is skipped outright — the same safe
// failure direction as matchByContent, mirrored from
// TestContentMatchingIsSkippedOverTheRenameLimit. The budget must bound the
// whole algorithm, not just a matrix (#59's commit-7 lesson).
func TestStructuralTierIsBoundedByRenameLimit(t *testing.T) {
	// 334 three-node subtrees per side: 1002 leftovers each, and 1002 × 1002
	// crosses renameLimit². Every subtree would anchor 1:1 below the cap.
	big := 334
	p := testPairing()
	matchByStructure(p, subtreeForest(t, big), subtreeForest(t, big))
	if len(p.headOf) != 0 {
		t.Errorf("over the cap the tier must be skipped; paired %d", len(p.headOf))
	}

	p = testPairing()
	matchByStructure(p, subtreeForest(t, 2), subtreeForest(t, 2))
	if len(p.headOf) != 6 {
		t.Errorf("under the cap, paired %d nodes, want all 6", len(p.headOf))
	}
}

// An element pairable by both content and structure carries the CONTENT
// evidence: the scored tier runs first, and the structural tier never touches
// an existing pair.
func TestStructuralTierNeverStealsContentPairs(t *testing.T) {
	side := func(withRack bool) []byte {
		g := map[string]any{"mesh": 0, "translation": []float64{5, 5, 5}}
		nodes := []any{}
		rootIdx := []int{}
		shift := 0
		if withRack {
			nodes = append(nodes, map[string]any{"name": "Rack", "children": []int{1}})
			rootIdx = append(rootIdx, 0)
			shift = 1
		} else {
			rootIdx = append(rootIdx, 0)
		}
		g["children"] = []int{shift + 1, shift + 2}
		nodes = append(nodes, g,
			map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
			map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
		)
		return doc(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": rootIdx}},
			"nodes":  nodes,
			"meshes": []any{map[string]any{"name": "Hull", "primitives": []any{map[string]any{"attributes": map[string]any{}}}}},
		})
	}
	docA, err := parseDoc(side(false))
	if err != nil {
		t.Fatal(err)
	}
	docB, err := parseDoc(side(true))
	if err != nil {
		t.Fatal(err)
	}
	mats := matchMaterials(docA, docB)
	meshes := matchMeshes(docA, docB, mats)
	aIx, bIx := indexNodes(docA), indexNodes(docB)
	aIx.adoptMeshPairs(meshes, true)
	bIx.adoptMeshPairs(meshes, false)
	m := matchEntities(nodeEntities(aIx), nodeEntities(bIx), nil)
	matchByStructure(m, aIx, bIx)

	// The unnamed mesh-drawing container G: base index 0, head index 1.
	if got, ok := m.headOf[0]; !ok || got != 1 {
		t.Fatalf("G paired with %d (ok=%v), want head 1", got, ok)
	}
	if ev := m.how[0]; ev.by != byContent {
		t.Errorf("evidence = %+v; content paired it first and structure must not steal it", ev)
	}
}

// nodeIndexOf parses a document spec and indexes its nodes, for driving the
// matching tiers directly.
func nodeIndexOf(t *testing.T, spec map[string]any) *nodeIndex {
	t.Helper()
	parsed, err := parseDoc(doc(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	return indexNodes(parsed)
}

// A dice TIE refuses to pair: two base containers each sharing the same two
// children with one head container score identically, and neither is the
// strict mutual best. Pairing either would be a coin flip that hides which
// container was deleted — the same rule every content tier enforces, pinned
// here for the structural tier's phase 2.
func TestStructuralTierRefusesDiceTies(t *testing.T) {
	aIx := nodeIndexOf(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 3}}},
		"nodes": []any{
			map[string]any{"children": []int{1, 2}},
			map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
			map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
			map[string]any{"children": []int{4, 5}},
			map[string]any{"name": "Kc", "translation": []float64{3, 0, 0}},
			map[string]any{"name": "Kd", "translation": []float64{4, 0, 0}},
		},
	})
	kids := []any{
		map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
		map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
		map[string]any{"name": "Kc", "translation": []float64{3, 0, 0}},
		map[string]any{"name": "Kd", "translation": []float64{4, 0, 0}},
	}
	bIx := nodeIndexOf(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{4}}},
		"nodes":  append(append([]any{}, kids...), map[string]any{"children": []int{0, 1, 2, 3}}),
	})
	m := matchEntities(nodeEntities(aIx), nodeEntities(bIx), nil)
	matchByStructure(m, aIx, bIx)
	if bi, ok := m.baseOf[4]; ok {
		t.Errorf("the head container paired with base %d on a dice tie; ambiguity never pairs", bi)
	}

	// The control: drop the second base container and the same score is the
	// unambiguous strict mutual best — the refusal above is about the tie, not
	// the score.
	aIx = nodeIndexOf(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			map[string]any{"children": []int{1, 2}},
			map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
			map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
		},
	})
	m = matchEntities(nodeEntities(aIx), nodeEntities(bIx), nil)
	matchByStructure(m, aIx, bIx)
	if bi, ok := m.baseOf[4]; !ok || bi != 0 {
		t.Errorf("the unambiguous container pair = %d (ok=%v), want base 0", bi, ok)
	}
}

// A container keeping too small a share of its descendants stays a removal
// plus an addition: dice 0.4 (two of eight children kept) is below the 0.5
// threshold even with no competing candidate on either side.
func TestStructuralTierEnforcesDiceThreshold(t *testing.T) {
	base := func(kids int) *nodeIndex {
		nodes := []any{map[string]any{"children": seqInts(1, kids)}}
		for i := range kids {
			nodes = append(nodes, map[string]any{
				"name": fmt.Sprintf("K%d", i+1), "translation": []float64{float64(i + 1), 0, 0},
			})
		}
		return nodeIndexOf(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes":  nodes,
		})
	}
	head := func(kept int) *nodeIndex {
		nodes := []any{}
		for i := range kept {
			nodes = append(nodes, map[string]any{
				"name": fmt.Sprintf("K%d", i+1), "translation": []float64{float64(i + 1), 0, 0},
			})
		}
		nodes = append(nodes, map[string]any{"children": seqInts(0, kept)})
		return nodeIndexOf(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{kept}}},
			"nodes":  nodes,
		})
	}

	aIx, bIx := base(8), head(2) // dice = 2·2/(8+2) = 0.4
	m := matchEntities(nodeEntities(aIx), nodeEntities(bIx), nil)
	matchByStructure(m, aIx, bIx)
	if bi, ok := m.baseOf[2]; ok {
		t.Errorf("the head container paired with base %d at dice 0.4; the threshold is 0.5", bi)
	}

	// The control: six of eight kept is dice 0.86 and pairs, with the dice
	// evidence stated.
	aIx, bIx = base(8), head(6)
	m = matchEntities(nodeEntities(aIx), nodeEntities(bIx), nil)
	matchByStructure(m, aIx, bIx)
	if bi, ok := m.baseOf[6]; !ok || bi != 0 {
		t.Fatalf("the container pair above threshold = %d (ok=%v), want base 0", bi, ok)
	}
	if ev := m.how[0]; ev.by != byStructure || !ev.dice {
		t.Errorf("evidence = %+v, want a dice byStructure pair", ev)
	}
}

// seqInts returns [from, from+n) — children lists for the container fixtures.
func seqInts(from, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = from + i
	}
	return out
}

// Two different stated meshes are the content tier's hard NO (a swapped mesh
// is a removal plus an addition, never a rename), and sharing descendants must
// not override it: a table and a lamp that swapped custody of two chairs are
// not one renamed object, whatever their dice score says.
func TestStructuralTierRefusesSwappedMesh(t *testing.T) {
	meshes := []any{
		map[string]any{"name": "ChairMesh", "primitives": []any{map[string]any{"attributes": map[string]any{}}}},
		map[string]any{"name": "TableMesh", "primitives": []any{map[string]any{"attributes": map[string]any{}}}},
		map[string]any{"name": "LampMesh", "primitives": []any{map[string]any{"attributes": map[string]any{}}}},
	}
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{2}}},
		"meshes": meshes,
		"nodes": []any{
			map[string]any{"name": "Chair1", "mesh": 0, "translation": []float64{1, 0, 0}},
			map[string]any{"name": "Chair2", "mesh": 0, "translation": []float64{2, 0, 0}},
			map[string]any{"name": "TableOld", "mesh": 1, "children": []int{0, 1}},
		},
	})
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{2}}},
		"meshes": meshes,
		"nodes": []any{
			map[string]any{"name": "Chair1", "mesh": 0, "translation": []float64{1, 0, 0}},
			map[string]any{"name": "Chair2", "mesh": 0, "translation": []float64{2, 0, 0}},
			map[string]any{"name": "LampNew", "mesh": 2, "translation": []float64{5, 5, 5}, "children": []int{0, 1}},
		},
	})
	d := diffOf(t, base, head)
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Renamed {
			t.Errorf("a table did not become a lamp: %+v", c)
		}
	})
	if got := kindAt(d, "nodes/TableOld"); got != Removed {
		t.Errorf("nodes/TableOld: kind = %q, want %q", got, Removed)
	}
	if got := kindAt(d, "nodes/LampNew"); got != Added {
		t.Errorf("nodes/LampNew: kind = %q, want %q", got, Added)
	}
	// The chairs' change of custody is still told, honestly, as the re-parents
	// it is.
	if got := kindAt(d, "nodes/Chair1"); got != Reparented {
		t.Errorf("nodes/Chair1: kind = %q, want %q", got, Reparented)
	}
}

// Identical twin unnamed subtrees, one deleted: the paired parent
// disambiguates which twin survived, so the diff tells ONE story — the other
// twin removed whole — instead of pairing the surviving twin's root through
// its parent key while the positional tier pairs its children ACROSS the
// twins, yielding self-contradictory reparent rows whose Before and After
// print as the same string.
func TestTwinSubtreeDeletionTellsOneStory(t *testing.T) {
	mesh := []any{map[string]any{"name": "M", "primitives": []any{map[string]any{"attributes": map[string]any{}}}}}
	twin := func(at int) []any {
		return []any{
			map[string]any{"mesh": 0, "translation": []float64{1, 0, 0}},
			map[string]any{"mesh": 0, "translation": []float64{2, 0, 0}},
			map[string]any{"translation": []float64{1, 1, 1}, "children": []int{at, at + 1}},
		}
	}
	base := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{6, 7}}},
		"meshes": mesh,
		"nodes": append(append(twin(0), twin(3)...),
			map[string]any{"name": "A", "translation": []float64{0, 5, 0}, "children": []int{2}},
			map[string]any{"name": "B", "translation": []float64{0, 9, 0}, "children": []int{5}},
		),
	})
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{3, 4}}},
		"meshes": mesh,
		"nodes": append(twin(0),
			map[string]any{"name": "A", "translation": []float64{0, 5, 0}},
			map[string]any{"name": "B", "translation": []float64{0, 9, 0}, "children": []int{2}},
		),
	})
	d := diffOf(t, base, head)
	removed := 0
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Reparented {
			t.Errorf("nothing moved; the narrative must not fragment across the twins: %+v", c)
		}
		if c.Kind == Added {
			t.Errorf("nothing was added: %+v", c)
		}
		if c.Kind == Removed && c.Before == "node" {
			removed++
		}
	})
	// Exactly the deleted twin: its root (under A) and its two mesh children.
	if removed != 3 {
		t.Errorf("removed %d nodes, want the deleted twin's 3", removed)
	}
	if got := kindAt(d, "nodes/node[2]#1"); got != Removed {
		t.Errorf("nodes/node[2]#1 (the deleted twin's root): kind = %q, want %q", got, Removed)
	}
}

// Over structureWorkBudget the bottom-up pass aborts UNAPPLIED — the abort
// must be reachable and must fail toward removals and additions, never toward
// a partial application. (The budget is a var precisely so this path is
// testable without a pathological fixture.)
func TestStructuralWorkBudgetAbortsUnapplied(t *testing.T) {
	// A container losing one of three children: different content on the two
	// sides (children counts 3 vs 2), so only phase 2 — dice 2·2/(3+2) = 0.8 —
	// can pair it.
	build := func() (*nodeIndex, *nodeIndex, pairing) {
		aIx := nodeIndexOf(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{0}}},
			"nodes": []any{
				map[string]any{"children": []int{1, 2, 3}},
				map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
				map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
				map[string]any{"name": "Kc", "translation": []float64{3, 0, 0}},
			},
		})
		bIx := nodeIndexOf(t, map[string]any{
			"scene":  0,
			"scenes": []any{map[string]any{"nodes": []int{2}}},
			"nodes": []any{
				map[string]any{"name": "Ka", "translation": []float64{1, 0, 0}},
				map[string]any{"name": "Kb", "translation": []float64{2, 0, 0}},
				map[string]any{"children": []int{0, 1}},
			},
		})
		m := matchEntities(nodeEntities(aIx), nodeEntities(bIx), nil)
		if _, ok := m.baseOf[2]; ok {
			t.Fatal("the containers must reach matchByStructure unpaired for this test to mean anything")
		}
		return aIx, bIx, m
	}

	aIx, bIx, m := build()
	matchByStructure(m, aIx, bIx)
	if bi, ok := m.baseOf[2]; !ok || bi != 0 {
		t.Fatalf("under budget phase 2 pairs the containers (got %d, ok=%v)", bi, ok)
	}
	if ev := m.how[0]; ev.by != byStructure || !ev.dice {
		t.Fatalf("evidence = %+v, want a dice byStructure pair", ev)
	}

	defer func(old int) { structureWorkBudget = old }(structureWorkBudget)
	structureWorkBudget = 1
	aIx, bIx, m = build()
	matchByStructure(m, aIx, bIx)
	if bi, ok := m.baseOf[2]; ok {
		t.Errorf("over budget the pass must abort unapplied; paired base %d", bi)
	}
}

// ── reparented: its own ChangeKind (#42) ──────────────────────────────────────

// reparentDoc hangs `child` under the named parent, with `other` as a second
// top-level node the child can move to.
func reparentDoc(t *testing.T, parent string, child map[string]any, other string) []byte {
	t.Helper()
	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": parent, "children": []int{2}},
			map[string]any{"name": other},
			child,
		},
	})
}

// A pure re-parent is its own kind, not a bag of modified fields: the node
// kept its identity and moved. The existing `parent` child row is KEPT under
// it — wraps, not replaces — and a name-matched pair carries no evidence note.
func TestReparentIsItsOwnKind(t *testing.T) {
	child := map[string]any{"name": "Mirror_L"}
	base := reparentDoc(t, "Body", child, "Door_L")
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body"},
			map[string]any{"name": "Door_L", "children": []int{2}},
			child,
		},
	})

	d := diffOf(t, base, head)
	c := mustChange(t, d, "nodes/Mirror_L")
	if c.Kind != Reparented {
		t.Fatalf("nodes/Mirror_L: kind = %q, want %q", c.Kind, Reparented)
	}
	if c.Before != "Body" || c.After != "Door_L" {
		t.Errorf("reparented = %v → %v, want Body → Door_L with no evidence note for a name match", c.Before, c.After)
	}
	// The carry-through row for consumers that predate the kind.
	row := mustChange(t, d, "nodes/Mirror_L/parent")
	if row.Kind != Modified || row.Before != "Body" || row.After != "Door_L" {
		t.Errorf("parent child row = %q %v → %v, want modified Body → Door_L", row.Kind, row.Before, row.After)
	}
}

// The root is a parent like any other, spelled <root> on both ends.
func TestReparentToAndFromRoot(t *testing.T) {
	attached := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes": []any{
			map[string]any{"name": "Body", "children": []int{1}},
			map[string]any{"name": "Mirror_L"},
		},
	})
	detached := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body"},
			map[string]any{"name": "Mirror_L"},
		},
	})

	out := mustChange(t, diffOf(t, attached, detached), "nodes/Mirror_L")
	if out.Kind != Reparented || out.Before != "Body" || out.After != rootParentLabel {
		t.Errorf("detach = %q %v → %v, want reparented Body → %s", out.Kind, out.Before, out.After, rootParentLabel)
	}
	in := mustChange(t, diffOf(t, detached, attached), "nodes/Mirror_L")
	if in.Kind != Reparented || in.Before != rootParentLabel || in.After != "Body" {
		t.Errorf("attach = %q %v → %v, want reparented %s → Body", in.Kind, in.Before, in.After, rootParentLabel)
	}
}

// #59's precedence, pinned: a rename plus a move is ONE change — the rename —
// with the parent row hanging under it. `reparented` never competes.
func TestReparentPlusRenameIsOneRenamedChange(t *testing.T) {
	base := reparentDoc(t, "Body", withUID("u-m", map[string]any{"name": "Mirror_L"}), "Door_L")
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body"},
			map[string]any{"name": "Door_L", "children": []int{2}},
			withUID("u-m", map[string]any{"name": "Mirror_Left"}),
		},
	})

	d := diffOf(t, base, head)
	mustRename(t, d, "nodes/Mirror_Left", "Mirror_L")
	mustChange(t, d, "nodes/Mirror_Left/parent")
	walk(d.Changes, func(c *DiffChange, _ int) {
		if c.Kind == Reparented {
			t.Errorf("a rename plus a move is one renamed change: %+v", c)
		}
	})
}

// A re-parent that also moved in space is still the re-parent, with the
// transform under it — NOT a transform-only modification.
func TestReparentWithTransformChange(t *testing.T) {
	base := reparentDoc(t, "Body",
		map[string]any{"name": "Mirror_L", "translation": []float64{1, 0, 0}}, "Door_L")
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body"},
			map[string]any{"name": "Door_L", "children": []int{2}},
			map[string]any{"name": "Mirror_L", "translation": []float64{1, 0, 5}},
		},
	})

	d := diffOf(t, base, head)
	if c := mustChange(t, d, "nodes/Mirror_L"); c.Kind != Reparented {
		t.Errorf("nodes/Mirror_L: kind = %q, want %q", c.Kind, Reparented)
	}
	mustChange(t, d, "nodes/Mirror_L/parent")
	mustChange(t, d, "nodes/Mirror_L/translation")
}

// A non-trivial pairing states its evidence on the reparented row, the same
// parenthetical convention renames use — here the uid that paired an unnamed
// node across the move.
func TestReparentEvidenceStatesThePairing(t *testing.T) {
	base := reparentDoc(t, "Body", withUID("u-m", map[string]any{"translation": []float64{1, 0, 0}}), "Door_L")
	head := doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0, 1}}},
		"nodes": []any{
			map[string]any{"name": "Body"},
			map[string]any{"name": "Door_L", "children": []int{2}},
			withUID("u-m", map[string]any{"translation": []float64{1, 0, 0}}),
		},
	})

	d := diffOf(t, base, head)
	c := mustChange(t, d, "nodes/node[2]")
	if c.Kind != Reparented {
		t.Fatalf("nodes/node[2]: kind = %q, want %q", c.Kind, Reparented)
	}
	if got, want := afterText(c), "Door_L (matched by "+uidExtrasKey+")"; got != want {
		t.Errorf("after = %q, want %q", got, want)
	}
}
