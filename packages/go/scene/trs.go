package scene

import (
	"math"

	"github.com/qmuntal/gltf"
)

// A node's placement can be written two ways in glTF: one matrix, or a
// translation, rotation and scale (TRS). glTF holds them equal. The diff, the
// merge and the renderer's change list speak TRS ("moved", "rotated", "scaled"),
// so a node placed by a matrix used to change without any of them noticing: a
// 3MF part placed by a component transform could move, and the diff said nothing.
//
// This file turns a matrix into TRS wherever that is exact, so the rest of the
// package sees one form. A matrix that is not TRS (sheared, or not affine)
// stays a matrix, and is compared as one.

// decomposeTRS splits an affine matrix (column-major, glTF's layout) into
// translation, rotation (a unit quaternion, x y z w) and scale. ok is false
// when the matrix is not one: it shears, is projective or degenerate, or its
// TRS does not rebuild it.
func decomposeTRS(m [16]float64) (t [3]float64, r [4]float64, s [3]float64, ok bool) {
	for _, v := range m {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return t, r, s, false
		}
	}
	if math.Abs(m[3]) > 1e-12 || math.Abs(m[7]) > 1e-12 || math.Abs(m[11]) > 1e-12 || math.Abs(m[15]-1) > 1e-12 {
		return t, r, s, false
	}
	t = [3]float64{m[12], m[13], m[14]}
	cols := [3][3]float64{{m[0], m[1], m[2]}, {m[4], m[5], m[6]}, {m[8], m[9], m[10]}}
	for i, c := range cols {
		s[i] = math.Sqrt(c[0]*c[0] + c[1]*c[1] + c[2]*c[2])
		if s[i] < 1e-12 {
			return t, r, s, false
		}
	}
	// A mirror is a negative scale; put it on x, as exporters do.
	c0, c1, c2 := cols[0], cols[1], cols[2]
	if c0[0]*(c1[1]*c2[2]-c1[2]*c2[1])-c0[1]*(c1[0]*c2[2]-c1[2]*c2[0])+c0[2]*(c1[0]*c2[1]-c1[1]*c2[0]) < 0 {
		s[0] = -s[0]
	}
	// R[row][col], from the unit columns.
	var R [3][3]float64
	for col := range 3 {
		for row := range 3 {
			R[row][col] = cols[col][row] / s[col]
		}
	}
	// Shepperd's method: divide by the largest of the four candidates.
	switch tr := R[0][0] + R[1][1] + R[2][2]; {
	case tr > 0:
		k := 2 * math.Sqrt(tr+1)
		r = [4]float64{(R[2][1] - R[1][2]) / k, (R[0][2] - R[2][0]) / k, (R[1][0] - R[0][1]) / k, k / 4}
	case R[0][0] > R[1][1] && R[0][0] > R[2][2]:
		k := 2 * math.Sqrt(1+R[0][0]-R[1][1]-R[2][2])
		r = [4]float64{k / 4, (R[0][1] + R[1][0]) / k, (R[0][2] + R[2][0]) / k, (R[2][1] - R[1][2]) / k}
	case R[1][1] > R[2][2]:
		k := 2 * math.Sqrt(1+R[1][1]-R[0][0]-R[2][2])
		r = [4]float64{(R[0][1] + R[1][0]) / k, k / 4, (R[1][2] + R[2][1]) / k, (R[0][2] - R[2][0]) / k}
	default:
		k := 2 * math.Sqrt(1+R[2][2]-R[0][0]-R[1][1])
		r = [4]float64{(R[0][2] + R[2][0]) / k, (R[1][2] + R[2][1]) / k, k / 4, (R[1][0] - R[0][1]) / k}
	}
	if r[3] < 0 { // one sign for the same rotation, so equal matrices give equal fields
		r = [4]float64{-r[0], -r[1], -r[2], -r[3]}
	}
	// The proof: TRS must rebuild the matrix. A shear fails here, because no
	// rotation and scale reproduce it.
	back := nodeMatrix(&gltf.Node{Translation: t, Rotation: r, Scale: s})
	scale := 1.0
	for _, v := range m {
		scale = math.Max(scale, math.Abs(v))
	}
	for i := range m {
		if math.Abs(back[i]-m[i]) > 1e-9*scale {
			return t, r, s, false
		}
	}
	return t, r, s, true
}

// setTransform places n by m: as TRS when m is one, else as the matrix itself.
func setTransform(n *gltf.Node, m [16]float64) {
	if m == [16]float64(identity4) {
		return
	}
	if t, r, s, ok := decomposeTRS(m); ok {
		n.Translation, n.Rotation, n.Scale = t, r, s
		return
	}
	n.Matrix = m
}

// normalizeTransforms rewrites, in place, each node of doc whose matrix is a
// TRS as that TRS, and leaves every other node as it was.
func normalizeTransforms(doc *gltf.Document) {
	if doc == nil {
		return
	}
	for _, n := range doc.Nodes {
		if n == nil {
			continue
		}
		m := n.MatrixOrDefault()
		if m == [16]float64(identity4) {
			continue
		}
		if t, r, s, ok := decomposeTRS(m); ok {
			n.Matrix = gltf.DefaultMatrix
			n.Translation, n.Rotation, n.Scale = t, r, s
		}
	}
}
