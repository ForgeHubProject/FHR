package scene

import (
	"fmt"
	"math"
	"strings"
)

// X3D and VRML describe some shapes as recipes rather than triangles: a sphere
// of radius r, a cone, a cylinder (FHR#102). The file holds no triangles for
// them, so the reader makes them, with a fixed tessellation: the same file
// always yields the same GLB, so a diff of it is stable and an unchanged sphere
// is never reported as edited.

const (
	// x3dSegments is how many slices go around a sphere, cone or cylinder. A
	// multiple of four, so the four axis directions are vertices exactly.
	x3dSegments = 32
	// x3dBands is how many bands a sphere has from pole to pole.
	x3dBands = 16
)

// x3dCircle is the point i of n around the unit circle, in the X3D texture
// convention: starting at the back (−Z) and turning counter-clockwise seen from
// above, so u = i/n. Exact at the quarter turns.
func x3dCircle(i, n int) (x, z float64) {
	a := 2 * math.Pi * float64(i) / float64(n)
	x, z = -math.Sin(a), -math.Cos(a)
	if math.Abs(x) < 1e-15 {
		x = 0
	}
	if math.Abs(z) < 1e-15 {
		z = 0
	}
	return x, z
}

// x3dBool reads an SFBool attribute (X3D writes "true"/"false"; the VRML reader
// lowercases TRUE/FALSE), with its default when absent.
func x3dBool(n *XMLNode, attr string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(n.Attr[attr])) {
	case "true":
		return true
	case "false":
		return false
	}
	return def
}

// x3dPositive reads a single number that must be greater than zero.
func x3dPositive(n *XMLNode, attr string, def float64) (float64, error) {
	v, ok, err := x3dFloatsAttr(n, attr, 1)
	if err != nil {
		return 0, err
	}
	if !ok {
		return def, nil
	}
	if !(v[0] > 0) {
		return 0, fmt.Errorf("<%s %s=%q> must be greater than zero", n.Name, attr, n.Attr[attr])
	}
	return v[0], nil
}

// x3dSphere is X3D's Sphere: centred on the origin, with normals pointing out
// and u running around from the back, v from the bottom pole to the top.
func x3dSphere(r float64) *FlatPrim {
	fp := &FlatPrim{Kind: KindTriangles, MaterialIndex: -1}
	for b := 0; b <= x3dBands; b++ {
		polar := math.Pi * float64(b) / x3dBands // 0 at the top
		y, ring := math.Cos(polar), math.Sin(polar)
		if b == 0 || b == x3dBands {
			ring = 0
		}
		if math.Abs(y) < 1e-15 {
			y = 0
		}
		for s := 0; s <= x3dSegments; s++ {
			x, z := x3dCircle(s, x3dSegments)
			n := [3]float64{x * ring, y, z * ring}
			fp.Positions = append(fp.Positions, [3]float64{n[0] * r, n[1] * r, n[2] * r})
			fp.Normals = append(fp.Normals, n)
			fp.UVs = append(fp.UVs, [2]float64{float64(s) / x3dSegments, float64(b) / x3dBands}) // glTF v is down
		}
	}
	row := uint32(x3dSegments + 1)
	for b := uint32(0); b < x3dBands; b++ {
		for s := uint32(0); s < x3dSegments; s++ {
			a, c := b*row+s, (b+1)*row+s
			if b != 0 { // the top band's upper edge is the pole: one triangle per slice
				fp.Indices = append(fp.Indices, a, c, a+1)
			}
			if b != x3dBands-1 {
				fp.Indices = append(fp.Indices, a+1, c, c+1)
			}
		}
	}
	return fp
}

// x3dRevolved is a cylinder or cone: a side whose radius goes from bottom to
// top over the height, centred on the origin along Y, and optional flat caps.
// A cone is a top radius of zero.
func x3dRevolved(bottom, top, height float64, side, bottomCap, topCap bool) *FlatPrim {
	fp := &FlatPrim{Kind: KindTriangles, MaterialIndex: -1}
	h := height / 2
	if side {
		// The side's normal leans out by the slope: (h·dir, Δr) for a radius that
		// shrinks by Δr over the height, normalised.
		ny, nh := bottom-top, height
		l := math.Hypot(ny, nh)
		ny, nh = ny/l, nh/l
		base := uint32(len(fp.Positions))
		for s := 0; s <= x3dSegments; s++ {
			x, z := x3dCircle(s, x3dSegments)
			u := float64(s) / x3dSegments
			n := [3]float64{x * nh, ny, z * nh}
			fp.Positions = append(fp.Positions, [3]float64{x * bottom, -h, z * bottom}, [3]float64{x * top, h, z * top})
			fp.Normals = append(fp.Normals, n, n)
			fp.UVs = append(fp.UVs, [2]float64{u, 1}, [2]float64{u, 0})
		}
		for s := uint32(0); s < x3dSegments; s++ {
			b0, t0, b1, t1 := base+2*s, base+2*s+1, base+2*s+2, base+2*s+3
			fp.Indices = append(fp.Indices, b0, b1, t0)
			if top > 0 { // a cone's apex has no second triangle
				fp.Indices = append(fp.Indices, t0, b1, t1)
			}
		}
	}
	addCap := func(y, r, ny float64) {
		if r <= 0 {
			return
		}
		centre := uint32(len(fp.Positions))
		fp.Positions = append(fp.Positions, [3]float64{0, y, 0})
		fp.Normals = append(fp.Normals, [3]float64{0, ny, 0})
		fp.UVs = append(fp.UVs, [2]float64{0.5, 0.5})
		for s := 0; s <= x3dSegments; s++ {
			x, z := x3dCircle(s, x3dSegments)
			fp.Positions = append(fp.Positions, [3]float64{x * r, y, z * r})
			fp.Normals = append(fp.Normals, [3]float64{0, ny, 0})
			// To 1/65536: a binary fraction that short survives the v = 1 − v
			// flip between X3D and glTF exactly, so an export reads back the same.
			q := func(f float64) float64 { return math.Round(f*65536) / 65536 }
			fp.UVs = append(fp.UVs, [2]float64{q(0.5 + x/2), q(0.5 + ny*z/2)})
		}
		for s := uint32(1); s <= x3dSegments; s++ {
			if ny > 0 { // counter-clockwise seen from outside
				fp.Indices = append(fp.Indices, centre, centre+s, centre+s+1)
			} else {
				fp.Indices = append(fp.Indices, centre, centre+s+1, centre+s)
			}
		}
	}
	if bottomCap {
		addCap(-h, bottom, -1)
	}
	if topCap {
		addCap(h, top, 1)
	}
	if len(fp.Indices) == 0 {
		return nil
	}
	return fp
}

// x3dFirstUse renumbers a primitive's vertices in the order its triangles first
// use them, dropping any they never use. That is the order the reader gives an
// IndexedFaceSet, so a made shape exported and read back numbers its vertices
// the same way, and the export after that is byte for byte the same.
func x3dFirstUse(fp *FlatPrim) *FlatPrim {
	if fp == nil {
		return nil
	}
	remap := make(map[uint32]uint32, len(fp.Positions))
	out := &FlatPrim{Kind: fp.Kind, MaterialIndex: fp.MaterialIndex, Material: fp.Material, BaseColor: fp.BaseColor}
	for _, i := range fp.Indices {
		j, ok := remap[i]
		if !ok {
			j = uint32(len(out.Positions))
			remap[i] = j
			out.Positions = append(out.Positions, fp.Positions[i])
			if fp.Normals != nil {
				out.Normals = append(out.Normals, fp.Normals[i])
			}
			if fp.UVs != nil {
				out.UVs = append(out.UVs, fp.UVs[i])
			}
			if fp.Colors != nil {
				out.Colors = append(out.Colors, fp.Colors[i])
			}
		}
		out.Indices = append(out.Indices, j)
	}
	return out
}

// x3dRecipe makes the triangles for a Sphere, Cone or Cylinder.
func x3dRecipe(geo *XMLNode) (*FlatPrim, error) {
	switch geo.Name {
	case "Sphere":
		r, err := x3dPositive(geo, "radius", 1)
		if err != nil {
			return nil, err
		}
		return x3dFirstUse(x3dSphere(r)), nil
	case "Cone":
		r, err := x3dPositive(geo, "bottomRadius", 1)
		if err != nil {
			return nil, err
		}
		h, err := x3dPositive(geo, "height", 2)
		if err != nil {
			return nil, err
		}
		return x3dFirstUse(x3dRevolved(r, 0, h, x3dBool(geo, "side", true), x3dBool(geo, "bottom", true), false)), nil
	case "Cylinder":
		r, err := x3dPositive(geo, "radius", 1)
		if err != nil {
			return nil, err
		}
		h, err := x3dPositive(geo, "height", 2)
		if err != nil {
			return nil, err
		}
		return x3dFirstUse(x3dRevolved(r, r, h, x3dBool(geo, "side", true), x3dBool(geo, "bottom", true), x3dBool(geo, "top", true))), nil
	}
	return nil, fmt.Errorf("geometry <%s> is not a recipe", geo.Name)
}

// x3dStripIndices turns the strip, fan and quad sets into a triangle list over
// their Coordinate points. These are not recipes, only other orderings of
// triangles: every attribute is per vertex, indexed by the same number as the
// point, so the points stay the vertex stream.
func x3dStripIndices(geo *XMLNode, points int) ([]uint32, error) {
	// Runs of vertices: from a count list (TriangleStripSet stripCount,
	// TriangleFanSet fanCount), from an index list split at -1, or by fours.
	var runs [][]int
	seq := func(counts []int) error {
		next := 0
		for _, c := range counts {
			if c < 0 || next+c > points {
				return fmt.Errorf("%s counts more vertices than its %d points", geo.Name, points)
			}
			run := make([]int, c)
			for i := range run {
				run[i] = next + i
			}
			runs = append(runs, run)
			next += c
		}
		return nil
	}
	switch geo.Name {
	case "TriangleStripSet", "TriangleFanSet":
		attr := "stripCount"
		if geo.Name == "TriangleFanSet" {
			attr = "fanCount"
		}
		counts, err := x3dIndexList(geo, attr)
		if err != nil {
			return nil, err
		}
		if err := seq(counts); err != nil {
			return nil, err
		}
	case "IndexedTriangleStripSet", "IndexedTriangleFanSet":
		idx, err := x3dIndexList(geo, "index")
		if err != nil {
			return nil, err
		}
		var run []int
		for _, i := range append(idx, -1) {
			if i == -1 {
				if len(run) > 0 {
					runs = append(runs, run)
				}
				run = nil
				continue
			}
			run = append(run, i)
		}
	case "QuadSet":
		if points%4 != 0 {
			return nil, fmt.Errorf("QuadSet has %d points, not a whole number of quads", points)
		}
		for i := 0; i < points; i += 4 {
			runs = append(runs, []int{i, i + 1, i + 2, i + 3})
		}
	case "IndexedQuadSet":
		idx, err := x3dIndexList(geo, "index")
		if err != nil {
			return nil, err
		}
		if len(idx)%4 != 0 {
			return nil, fmt.Errorf("IndexedQuadSet has %d indices, not a whole number of quads", len(idx))
		}
		for i := 0; i < len(idx); i += 4 {
			runs = append(runs, idx[i:i+4])
		}
	}
	ccw := x3dBool(geo, "ccw", true)
	var out []uint32
	tri := func(a, b, c int) {
		if !ccw {
			b, c = c, b
		}
		out = append(out, uint32(a), uint32(b), uint32(c))
	}
	for _, run := range runs {
		for _, i := range run {
			if i < 0 || i >= points {
				return nil, fmt.Errorf("%s: index %d is out of range (%d points)", geo.Name, i, points)
			}
		}
		switch {
		case strings.Contains(geo.Name, "Strip"):
			// Every other triangle of a strip is wound the other way round;
			// swapping two corners keeps them all facing the same side.
			for k := 2; k < len(run); k++ {
				if k%2 == 0 {
					tri(run[k-2], run[k-1], run[k])
				} else {
					tri(run[k-1], run[k-2], run[k])
				}
			}
		case strings.Contains(geo.Name, "Fan"):
			for k := 2; k < len(run); k++ {
				tri(run[0], run[k-1], run[k])
			}
		default: // a quad
			tri(run[0], run[1], run[2])
			tri(run[0], run[2], run[3])
		}
	}
	return out, nil
}
