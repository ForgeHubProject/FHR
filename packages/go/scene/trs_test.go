package scene

import (
	"math"
	"math/rand"
	"testing"

	"github.com/qmuntal/gltf"
)

func translate(x, y, z float64) [16]float64 {
	return [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, x, y, z, 1}
}

// TRS that rebuild the matrix they came from, for every kind of placement a
// file writes: none, a move, a turn, a stretch, a mirror and all of them at once.
func TestDecomposeTRSRebuildsTheMatrix(t *testing.T) {
	r := rand.New(rand.NewSource(86))
	for i := 0; i < 2000; i++ {
		q := [4]float64{r.NormFloat64(), r.NormFloat64(), r.NormFloat64(), r.NormFloat64()}
		l := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
		for k := range q {
			q[k] /= l
		}
		s := [3]float64{0.001 + r.Float64()*10, 0.001 + r.Float64()*10, 0.001 + r.Float64()*10}
		if i%4 == 0 {
			s[0] = -s[0] // a mirror
		}
		tr := [3]float64{r.NormFloat64() * 100, r.NormFloat64() * 100, r.NormFloat64() * 100}
		m := [16]float64(nodeMatrix(&gltf.Node{Translation: tr, Rotation: q, Scale: s}))
		gt, gr, gs, ok := decomposeTRS(m)
		if !ok {
			t.Fatalf("case %d: %v did not decompose", i, m)
		}
		back := nodeMatrix(&gltf.Node{Translation: gt, Rotation: gr, Scale: gs})
		for k := range m {
			if math.Abs(back[k]-m[k]) > 1e-9*100 {
				t.Fatalf("case %d: rebuilt %v, want %v", i, back, m)
			}
		}
		if gr[3] < 0 {
			t.Fatalf("case %d: rotation %v should have w ≥ 0", i, gr)
		}
	}
	// A plain move is a translation and nothing else.
	gt, gr, gs, ok := decomposeTRS(translate(0.01, 0.02, 0.005))
	if !ok || gt != [3]float64{0.01, 0.02, 0.005} || gr != [4]float64{0, 0, 0, 1} || gs != [3]float64{1, 1, 1} {
		t.Fatalf("a translation: %v %v %v %v", gt, gr, gs, ok)
	}
}

// What has no TRS stays a matrix.
func TestDecomposeTRSRefusesWhatIsNotOne(t *testing.T) {
	shear := translate(0, 0, 0)
	shear[4] = 0.5 // x += 0.5·y
	projective := translate(1, 2, 3)
	projective[3] = 0.1
	flat := translate(0, 0, 0)
	flat[10] = 0
	nan := translate(math.NaN(), 0, 0)
	for name, m := range map[string][16]float64{"shear": shear, "projective": projective, "flat": flat, "nan": nan} {
		if _, _, _, ok := decomposeTRS(m); ok {
			t.Errorf("%s decomposed", name)
		}
	}
}

// The bug (found testing FHR#103): a part placed by a matrix moved, and the
// diff reported nothing. Placement now diffs the same either way it is written.
func TestDiffSeesAMovedMatrixNode(t *testing.T) {
	base := nodesDoc(t, map[string]any{"name": "Pin", "matrix": translate(0.01, 0.01, 0.005)})
	head := nodesDoc(t, map[string]any{"name": "Pin", "matrix": translate(0.02, 0.01, 0.005)})
	c := mustChange(t, diffOf(t, base, head), "nodes/Pin/translation")
	if c.Before == nil || c.After == nil || c.Before == c.After {
		t.Fatalf("translation change: %+v", c)
	}
	// Written as a matrix on one side and TRS on the other, the same placement is
	// no change at all.
	trs := nodesDoc(t, map[string]any{"name": "Pin", "translation": []float64{0.01, 0.01, 0.005}})
	if d := diffOf(t, base, trs); len(d.Changes) != 0 {
		t.Fatalf("matrix vs the same TRS should be no change: %v", paths(d))
	}
}

// A sheared matrix has no TRS to compare, so it is compared whole.
func TestDiffComparesAShearAsAMatrix(t *testing.T) {
	a, b := translate(0, 0, 0), translate(0, 0, 0)
	a[4], b[4] = 0.5, 0.25
	d := diffOf(t, nodesDoc(t, map[string]any{"name": "Skew", "matrix": a}), nodesDoc(t, map[string]any{"name": "Skew", "matrix": b}))
	mustChange(t, d, "nodes/Skew/matrix")
}

// Merging: theirs moved a matrix-placed node, ours did not touch it. The merge
// used to keep ours' placement and drop theirs without a word.
func TestMergeKeepsTheirMoveOfAMatrixNode(t *testing.T) {
	base := nodesDoc(t, map[string]any{"name": "Pin", "matrix": translate(0.01, 0, 0)})
	ours := nodesDoc(t, map[string]any{"name": "Pin", "matrix": translate(0.01, 0, 0)}, map[string]any{"name": "Extra"})
	theirs := nodesDoc(t, map[string]any{"name": "Pin", "matrix": translate(0.03, 0, 0)})
	merged, ci, err := (&Handler{}).Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if ci != nil && len(ci.Conflicts) > 0 {
		t.Fatalf("no conflict expected: %+v", ci.Conflicts)
	}
	doc, err := parseDoc(merged)
	if err != nil {
		t.Fatal(err)
	}
	pin := nodeByName(doc.Nodes, "Pin")
	if got := [16]float64(nodeMatrix(pin)); math.Abs(got[12]-0.03) > 1e-12 {
		t.Fatalf("merged Pin sits at x=%v, want theirs' 0.03", got[12])
	}
}

// Converters write placement as TRS when it is one, which is what the diff,
// the merge and the renderer's change list read; a shear stays a matrix.
func TestCodecWritesTRS(t *testing.T) {
	move := translate(0.02, 0, 0.008)
	shear := translate(0, 0, 0)
	shear[4] = 0.5
	tri := []*FlatPrim{{Kind: KindTriangles, MaterialIndex: -1, Positions: [][3]float64{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, Indices: []uint32{0, 1, 2}}}
	doc, err := (&Model{Roots: []*Object{
		{Name: "Moved", Matrix: &move, Prims: tri},
		{Name: "Sheared", Matrix: &shear, Prims: tri},
	}}).Document()
	if err != nil {
		t.Fatal(err)
	}
	moved, sheared := nodeByName(doc.Nodes, "Moved"), nodeByName(doc.Nodes, "Sheared")
	if moved.MatrixOrDefault() != gltf.DefaultMatrix || moved.TranslationOrDefault() != [3]float64{0.02, 0, 0.008} {
		t.Fatalf("Moved: matrix %v translation %v", moved.Matrix, moved.Translation)
	}
	if sheared.MatrixOrDefault() != shear {
		t.Fatalf("Sheared lost its matrix: %v", sheared.Matrix)
	}
}
