package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/fhr"
)

// resolve merges, then applies "take incoming" for the given conflict paths —
// what forge mergetool does after the reviewer picks.
func resolve(t *testing.T, base, ours, theirs string, take ...string) (string, []fhr.SemanticConflict) {
	t.Helper()
	merged, conflicts := merge(t, base, ours, theirs)
	out, err := (&Handler{}).ApplyChoices([]byte(merged), []byte(theirs), take)
	if err != nil {
		t.Fatalf("ApplyChoices: %v", err)
	}
	if _, err := parseOBJ(out); err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, out)
	}
	return string(out), conflicts
}

func TestTakeIncomingForAModifiedGroup(t *testing.T) {
	ours := strings.Replace(abc, "v 1 1 0", "v 1 2 0", 1)
	theirs := strings.Replace(abc, "v 1 1 0", "v 1 3 0", 1)
	out, conflicts := resolve(t, abc, ours, theirs, "nodes/A")
	if len(conflicts) != 1 {
		t.Fatalf("want the one conflict at A, got %+v", conflicts)
	}
	sameContent(t, theirs, out)
}

// The desk case from the forge mergetool run: ours removed a group, theirs
// recoloured it. Taking incoming brings it back as theirs has it, and keeps
// every other change the merge already made.
func TestTakeIncomingRestoresARemovedGroup(t *testing.T) {
	withoutB := strings.Replace(abc, "o B\nv 5 0 0\nv 6 0 0\nv 6 1 0\nv 5 1 0\nusemtl Steel\nf 5 6 7 8\n", "", 1)
	withoutB = strings.Replace(withoutB, "f 9 10 11", "f 5 6 7", 1)
	ours := strings.Replace(withoutB, "v 1 1 0", "v 1 2 0", 1) // and moved A
	theirs := strings.Replace(abc, "usemtl Steel\nf 5 6 7 8", "usemtl Brass\nf 5 6 7 8", 1)
	out, conflicts := resolve(t, abc, ours, theirs, "nodes/B")
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/B" {
		t.Fatalf("want the conflict at nodes/B, got %+v", conflicts)
	}
	// B is back as theirs has it; A keeps ours' move.
	mustLack(t, diffOf(t, theirs, out), "nodes/B", "meshes/B")
	mustHave(t, diffOf(t, theirs, out), "modified meshes/A/primitives/0/geometry/POSITION")
}

func TestTakeIncomingRemovesWhatTheirsRemoved(t *testing.T) {
	withoutB := strings.Replace(abc, "o B\nv 5 0 0\nv 6 0 0\nv 6 1 0\nv 5 1 0\nusemtl Steel\nf 5 6 7 8\n", "", 1)
	withoutB = strings.Replace(withoutB, "f 9 10 11", "f 5 6 7", 1)
	ours := strings.Replace(abc, "v 6 1 0", "v 6 2 0", 1) // changed B
	out, _ := resolve(t, abc, ours, withoutB, "nodes/B")
	sameContent(t, withoutB, out)
}

// A group changed on one side inside an object the other side removed: the
// merge keeps ours (removed); taking incoming restores the group AND the
// object it lives in.
func TestTakeIncomingForAGroupBringsItsObject(t *testing.T) {
	base := "o Car\nv 0 0 0\nv 1 0 0\nv 0 1 0\ng Wheel\nf 1 2 3\no Road\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 4 5 6\n"
	noCar := "o Road\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 1 2 3\n"
	wheelChanged := strings.Replace(base, "f 1 2 3", "f 3 2 1", 1)
	out, conflicts := resolve(t, base, noCar, wheelChanged, "nodes/Wheel")
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/Wheel" {
		t.Fatalf("want the conflict at nodes/Wheel, got %+v", conflicts)
	}
	sameContent(t, wheelChanged, out)
}

func TestTakeIncomingMtlLib(t *testing.T) {
	a := strings.Replace(abc, "mtllib parts.mtl", "mtllib a.mtl", 1)
	b := strings.Replace(abc, "mtllib parts.mtl", "mtllib b.mtl", 1)
	out, _ := resolve(t, abc, a, b, "mtllib")
	if !strings.Contains(out, "mtllib b.mtl") {
		t.Fatalf("mtllib not taken:\n%s", out)
	}
}

func TestApplyChoicesRefusesWhatItDoesNotKnow(t *testing.T) {
	h := &Handler{}
	if out, err := h.ApplyChoices([]byte(abc), []byte(abc), nil); err != nil || !bytes.Equal(out, []byte(abc)) {
		t.Fatalf("no choices must return the merged file unchanged (err=%v)", err)
	}
	for _, p := range []string{"meshes/A", "nodes/Nope", "other"} {
		if _, err := h.ApplyChoices([]byte(abc), []byte(abc), []string{p}); err == nil {
			t.Errorf("%s: want an error, not a silent no-op", p)
		}
	}
}
