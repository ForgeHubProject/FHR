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
	"strings"
	"testing"
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

	score := map[[2]int]float64{{0, 10}: 0.9, {0, 11}: 0.9}
	if _, _, ok := strictBest(0, []int{10, 11}, score, false); ok {
		t.Error("a tie must not produce a winner")
	}
	score[[2]int{0, 11}] = 0.6
	if best, _, ok := strictBest(0, []int{10, 11}, score, false); !ok || best != 10 {
		t.Errorf("strictBest = %d, ok = %v; want 10, true", best, ok)
	}
}
