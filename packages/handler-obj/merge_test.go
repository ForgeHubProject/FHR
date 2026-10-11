package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/fhr"
)

// three objects, written the way exporters do: each object's vertices, then
// its faces, with absolute indices.
const abc = `# exported by a test
mtllib parts.mtl
o A
v 0 0 0
v 1 0 0
v 1 1 0
v 0 1 0
usemtl Steel
f 1 2 3 4
o B
v 5 0 0
v 6 0 0
v 6 1 0
v 5 1 0
usemtl Steel
f 5 6 7 8
o C
v 9 0 0
v 10 0 0
v 10 1 0
f 9 10 11
`

func merge(t *testing.T, base, ours, theirs string) (string, []fhr.SemanticConflict) {
	t.Helper()
	out, ci, err := (&Handler{}).Merge([]byte(base), []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := parseOBJ(out); err != nil {
		t.Fatalf("merged file does not parse: %v\n%s", err, out)
	}
	if ci == nil {
		return string(out), nil
	}
	return string(out), ci.Conflicts
}

// sameContent: the two files draw the same thing, per the handler's own diff.
func sameContent(t *testing.T, want, got string) {
	t.Helper()
	if d := flat(diffOf(t, want, got)); len(d) != 0 {
		t.Fatalf("content differs: %v\n--- got\n%s", d, got)
	}
}

// Writing a parsed file back must not change what it draws — whatever its
// layout: shared vertices, negative references, every reference form, lines,
// points, nesting, smoothing, colours.
func TestWriteRoundTripsContent(t *testing.T) {
	for name, src := range map[string]string{
		"exporter layout": abc,
		"quad":            quad,
		"all vertices first, shared across groups": "v 0 0 0\nv 1 0 0\nv 1 1 0\nv 0 1 0\ng Left\nf 1 2 3\ng Right\nf 1 3 4\n",
		"reference forms":                          "v 0 0 0\nv 1 0 0\nv 0 1 0\nvt 0 0\nvt 1 0\nvt 0 1\nvn 0 0 1\ng UV\nf 1/1 2/2 3/3\ng N\nf 1//1 2//1 3//1\ng Both\nf -3/-3/-1 -2/-2/-1 -1/-1/-1\n",
		"lines and points":                         "v 0 0 0\nv 1 0 0\nv 1 1 0\no Wire\nl 1 2 3\no Dots\np 1 3\n",
		"nested, smoothed":                         "o Car\nv 0 0 0\nv 1 0 0\nv 0 1 0\ns 1\nf 1 2 3\ng Wheel\nusemtl Rubber\ns off\nf 3 2 1\ng Door\nf 1 3 2\n",
		"colours":                                  "v 0 0 0 1 0 0\nv 1 0 0 0 1 0\nv 0 1 0 0 0 1\nf 1 2 3\n",
		"repeated o":                               "v 0 0 0\nv 1 0 0\nv 0 1 0\no S\nf 1 2 3\no S\nf 3 2 1\n",
		"bare g":                                   "v 0 0 0\nv 1 0 0\nv 0 1 0\ng\nf 1 2 3\n",
		"material reset":                           "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\nusemtl M\nf 3 2 1\nusemtl\nf 1 3 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := mustParse(t, src)
			out := writeOBJ(f.header, f.mtlLibs, asWriteNodes(f))
			sameContent(t, src, string(out))
			if indexSides(f).digest != indexSides(mustParse(t, string(out))).digest {
				t.Fatalf("merge identity changed on round trip:\n%s", out)
			}
		})
	}
}

func TestMergeTakesEachSidesChanges(t *testing.T) {
	ours := strings.Replace(abc, "v 1 1 0", "v 1 2 0", 1) // move A's corner
	theirs := strings.Replace(abc, "usemtl Steel\nf 5 6 7 8", "usemtl Brass\nf 5 6 7 8", 1) +
		"o D\nv 20 0 0\nv 21 0 0\nv 21 1 0\nf 12 13 14\n" // B recoloured, D added
	out, conflicts := merge(t, abc, ours, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	// Against ours, only theirs' changes remain — and the other way round.
	mustHave(t, diffOf(t, ours, out), "modified meshes/B/primitives/0/material", "added nodes/D")
	mustLack(t, diffOf(t, ours, out), "meshes/A")
	mustHave(t, diffOf(t, theirs, out), "modified meshes/A/primitives/0/geometry/POSITION")
	mustLack(t, diffOf(t, theirs, out), "meshes/B", "nodes/D")
	if !strings.HasPrefix(out, "# exported by a test\nmtllib parts.mtl\n") {
		t.Fatalf("header and mtllib not carried:\n%s", out)
	}
}

// The case a text merge gets wrong, reported clean by git merge-file: ours
// adds a vertex to A, which renumbers everything after it; theirs appends an
// object numbered against the base. Here C must still draw its own vertices.
func TestMergeRenumbersWhatATextMergeCannot(t *testing.T) {
	base := "o A\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\no B\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 4 5 6\n# end of file\n"
	ours := "o A\nv 0 0 0\nv 1 0 0\nv 0 1 0\nv 1 1 0\nf 1 2 3\nf 2 4 3\no B\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 5 6 7\n# end of file\n"
	theirs := base + "o C\nv 9 0 0\nv 9 1 0\nv 8 1 0\nf 7 8 9\n"
	out, conflicts := merge(t, base, ours, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	f := mustParse(t, out)
	for _, r := range f.roots {
		if r.name != "C" {
			continue
		}
		var got []string
		for _, ref := range r.elems[0].refs {
			got = append(got, f.posText[ref.v])
		}
		if strings.Join(got, "|") != "9 0 0|9 1 0|8 1 0" {
			t.Fatalf("C's face points at %v, want its own vertices\n%s", got, out)
		}
		return
	}
	t.Fatalf("C is missing:\n%s", out)
}

func TestMergeConflictsKeepOursAndSaySo(t *testing.T) {
	ours := strings.Replace(abc, "v 1 1 0", "v 1 2 0", 1)
	theirs := strings.Replace(abc, "v 1 1 0", "v 1 3 0", 1)
	out, conflicts := merge(t, abc, ours, theirs)
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/A" {
		t.Fatalf("want one conflict at nodes/A, got %+v", conflicts)
	}
	sameContent(t, ours, out)
}

func TestMergeRemovals(t *testing.T) {
	withoutB := strings.Replace(abc, "o B\nv 5 0 0\nv 6 0 0\nv 6 1 0\nv 5 1 0\nusemtl Steel\nf 5 6 7 8\n", "", 1)
	withoutB = strings.Replace(withoutB, "f 9 10 11", "f 5 6 7", 1)
	movedA := strings.Replace(abc, "v 1 1 0", "v 1 2 0", 1)
	movedB := strings.Replace(abc, "v 6 1 0", "v 6 2 0", 1)

	// Removed on one side, untouched on the other: removed.
	out, conflicts := merge(t, abc, movedA, withoutB)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	mustHave(t, diffOf(t, movedA, out), "removed nodes/B")

	// Removed on one side, changed on the other: a conflict, and ours decides.
	out, conflicts = merge(t, abc, movedB, withoutB)
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/B" || conflicts[0].Theirs != "removed" {
		t.Fatalf("want a keep/remove conflict at nodes/B, got %+v", conflicts)
	}
	sameContent(t, movedB, out)

	out, conflicts = merge(t, abc, withoutB, movedB)
	if len(conflicts) != 1 || conflicts[0].Ours != "removed" {
		t.Fatalf("want a remove/keep conflict, got %+v", conflicts)
	}
	sameContent(t, withoutB, out)
}

func TestMergeAdditions(t *testing.T) {
	d := "o D\nv 20 0 0\nv 21 0 0\nv 21 1 0\nf 12 13 14\n"
	// Both added the same object: once, no conflict.
	out, conflicts := merge(t, abc, abc+d, abc+d)
	if len(conflicts) != 0 || strings.Count(out, "o D") != 1 {
		t.Fatalf("want one D and no conflict, got %+v\n%s", conflicts, out)
	}
	// Both added it differently: a conflict.
	_, conflicts = merge(t, abc, abc+d, abc+strings.Replace(d, "v 21 1 0", "v 22 1 0", 1))
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/D" {
		t.Fatalf("want a conflict at nodes/D, got %+v", conflicts)
	}
}

func TestMergeGroupsInsideRemovedObjects(t *testing.T) {
	base := "o Car\nv 0 0 0\nv 1 0 0\nv 0 1 0\ng Wheel\nf 1 2 3\no Road\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 4 5 6\n"
	noCar := "o Road\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 1 2 3\n"
	wheelChanged := strings.Replace(base, "f 1 2 3", "f 3 2 1", 1)

	out, conflicts := merge(t, base, noCar, wheelChanged)
	if len(conflicts) != 1 || conflicts[0].Ours != "removed with its object" {
		t.Fatalf("want the wheel's change to conflict with the car's removal, got %+v", conflicts)
	}
	sameContent(t, noCar, out)

	out, conflicts = merge(t, base, wheelChanged, noCar)
	if len(conflicts) == 0 {
		t.Fatal("want a conflict")
	}
	sameContent(t, wheelChanged, out)
}

func TestMergeMtlLib(t *testing.T) {
	a := strings.Replace(abc, "mtllib parts.mtl", "mtllib a.mtl", 1)
	b := strings.Replace(abc, "mtllib parts.mtl", "mtllib b.mtl", 1)
	moved := strings.Replace(a, "v 1 1 0", "v 1 2 0", 1)
	out, conflicts := merge(t, abc, moved, strings.Replace(abc, "v 6 1 0", "v 6 2 0", 1))
	if len(conflicts) != 0 || !strings.Contains(out, "mtllib a.mtl") {
		t.Fatalf("one-sided mtllib change not taken: %+v\n%s", conflicts, out)
	}
	_, conflicts = merge(t, abc, moved, strings.Replace(b, "v 6 1 0", "v 6 2 0", 1))
	if len(conflicts) != 1 || conflicts[0].Path != "mtllib" {
		t.Fatalf("want a conflict at mtllib, got %+v", conflicts)
	}
}

// When one side did not change — or changed only formatting — the other side
// is the merge, byte for byte: no rewrite, no noise in the text history.
func TestMergeKeepsBytesWhenOnlyOneSideChanged(t *testing.T) {
	changed := strings.Replace(abc, "v 1 1 0", "v 1 2 0", 1)
	reformatted := strings.ReplaceAll(abc, " ", "  ") + "# trailing comment\n"
	for name, c := range map[string]struct{ ours, theirs, want string }{
		"theirs unchanged":        {changed, abc, changed},
		"ours unchanged":          {abc, changed, changed},
		"both identical":          {changed, changed, changed},
		"theirs only reformatted": {changed, reformatted, changed},
		"ours only reformatted":   {reformatted, changed, changed},
	} {
		out, ci, err := (&Handler{}).Merge([]byte(abc), []byte(c.ours), []byte(c.theirs))
		if err != nil || ci != nil || !bytes.Equal(out, []byte(c.want)) {
			t.Errorf("%s: want the changed side verbatim (err=%v, conflicts=%v)", name, err, ci)
		}
	}
}

func TestMergeKeepsSmoothingAndDuplicateObjects(t *testing.T) {
	base := "v 0 0 0\nv 1 0 0\nv 0 1 0\nv 0 0 1\no Sensor\nf 1 2 3\no Sensor\nf 1 2 4\n"
	// Smooth the first only: `s` stays in effect for every face after it, so
	// the second Sensor has to switch it off again to stay unchanged.
	ours := strings.Replace(base, "o Sensor\nf 1 2 3\no Sensor\n", "o Sensor\ns 1\nf 1 2 3\no Sensor\ns off\n", 1)
	theirs := strings.Replace(base, "f 1 2 4", "f 1 3 4", 1) // reshape the second
	out, conflicts := merge(t, base, ours, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("the two Sensors are different objects, got conflicts %+v", conflicts)
	}
	mustHave(t, diffOf(t, ours, out), "modified meshes/Sensor#1")
	mustLack(t, diffOf(t, ours, out), "meshes/Sensor", "nodes/Sensor/smoothing")
	mustHave(t, diffOf(t, theirs, out), "added nodes/Sensor/smoothing")
}

func TestMergeRefusesWhatItCannotPlace(t *testing.T) {
	freeForm := strings.Replace(abc, "o C", "cstype bspline\no C", 1)
	moved := strings.Replace(freeForm, "v 1 1 0", "v 1 2 0", 1)
	_, _, err := (&Handler{}).Merge([]byte(freeForm), []byte(moved), []byte(strings.Replace(freeForm, "v 6 1 0", "v 6 2 0", 1)))
	if err == nil || !strings.Contains(err.Error(), "cstype") {
		t.Fatalf("want a refusal naming cstype, got %v", err)
	}
	if _, _, err := (&Handler{}).Merge([]byte(abc), []byte("v 0 x 0\n"), []byte(abc+"#")); err == nil {
		t.Fatal("malformed input must be an error")
	}
}

// Both sides adding the file (no base) is a merge like any other.
func TestMergeWithNoBase(t *testing.T) {
	a := "o A\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"
	b := "o B\nv 5 0 0\nv 6 0 0\nv 5 1 0\nf 1 2 3\n"
	out, conflicts := merge(t, "", a, b)
	if len(conflicts) != 0 || !strings.Contains(out, "o A") || !strings.Contains(out, "o B") {
		t.Fatalf("want both objects, got %+v\n%s", conflicts, out)
	}
}

// Both sides changed the same group, but different aspects of it: one
// reshaped it, the other recoloured it. Faces still line up, so each aspect
// merges on its own — the group is taller *and* brass, with no conflict.
func TestMergeCombinesDifferentAspectsOfOneGroup(t *testing.T) {
	reshaped := strings.Replace(abc, "v 6 1 0", "v 6 2 0", 1)
	recoloured := strings.Replace(abc, "usemtl Steel\nf 5 6 7 8", "usemtl Brass\nf 5 6 7 8", 1)
	out, conflicts := merge(t, abc, reshaped, recoloured)
	if len(conflicts) != 0 {
		t.Fatalf("reshape + recolour is not a conflict, got %+v", conflicts)
	}
	mustHave(t, diffOf(t, reshaped, out), "modified meshes/B/primitives/0/material")
	mustLack(t, diffOf(t, reshaped, out), "meshes/B/primitives/0/geometry/POSITION")
	mustHave(t, diffOf(t, recoloured, out), "modified meshes/B/primitives/0/geometry/POSITION")
	mustLack(t, diffOf(t, recoloured, out), "meshes/B/primitives/0/material")

	// The same aspect changed two ways is still a conflict.
	_, conflicts = merge(t, abc, reshaped, strings.Replace(abc, "v 6 1 0", "v 6 3 0", 1))
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/B" {
		t.Fatalf("want a conflict at nodes/B, got %+v", conflicts)
	}
	// And so is a group whose faces no longer line up (one side added a face).
	grown := strings.Replace(abc, "f 5 6 7 8\n", "f 5 6 7 8\nf 5 7 8\n", 1)
	_, conflicts = merge(t, abc, grown, recoloured)
	if len(conflicts) != 1 || conflicts[0].Path != "nodes/B" {
		t.Fatalf("want a conflict at nodes/B, got %+v", conflicts)
	}
}
