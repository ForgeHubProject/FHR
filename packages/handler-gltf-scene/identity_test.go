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
	roots := make([]int, len(nodes))
	for i := range nodes {
		roots[i] = i
	}
	primitives := []any{map[string]any{"attributes": map[string]any{}}}
	return doc(t, map[string]any{
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": roots}},
		"nodes":  nodes,
		"meshes": []any{
			map[string]any{"name": "Hull", "primitives": primitives},
			map[string]any{"name": "Trim", "primitives": primitives},
		},
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
			f[i] = sigField{v, 1}
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

	score := map[[2]int]float64{{0, 10}: 0.9, {0, 11}: 0.9}
	if _, _, ok := strictBest(0, []int{10, 11}, score, false); ok {
		t.Error("a tie must not produce a winner")
	}
	score[[2]int{0, 11}] = 0.6
	if best, _, ok := strictBest(0, []int{10, 11}, score, false); !ok || best != 10 {
		t.Errorf("strictBest = %d, ok = %v; want 10, true", best, ok)
	}
}
