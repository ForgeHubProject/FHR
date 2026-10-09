package main

import (
	"bytes"
	"compress/gzip"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/forgehubproject/fhr/packages/go/scene"
	"github.com/klauspost/compress/zstd"
)

func h() fhrHandler { return Codec.Handler() }

type fhrHandler = *scene.CodecHandler

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bounds(t *testing.T, glb []byte) (lo, hi [3]float64, prims int) {
	t.Helper()
	roots, err := scene.Flatten(glb)
	if err != nil {
		t.Fatal(err)
	}
	lo = [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi = [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		for _, p := range n.Prims {
			prims++
			for _, v := range p.Positions {
				for i := 0; i < 3; i++ {
					lo[i], hi[i] = math.Min(lo[i], v[i]), math.Max(hi[i], v[i])
				}
			}
		}
	})
	return
}

func near(a, b [3]float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-3 {
			return false
		}
	}
	return true
}

// The fixtures are real Blender 5.2 saves (zstd-compressed, as Blender writes
// them by default) and the expected numbers are what Blender itself reports for
// the same files, mapped from its Z-up space to glTF's Y-up: (x, y, z) → (x, z, -y).

func TestSceneMatchesBlender(t *testing.T) {
	// Blender: world bounds (-3.5, -2.232, -1) .. (3.866, 2.232, 3.5).
	glb, err := h().Import(fixture(t, "blender-scene.blend"))
	if err != nil {
		t.Fatal(err)
	}
	lo, hi, prims := bounds(t, glb)
	if prims != 3 || !near(lo, [3]float64{-3.5, -1, -2.232}) || !near(hi, [3]float64{3.866, 3.5, 2.232}) {
		t.Fatalf("%d prims, bounds %v..%v", prims, lo, hi)
	}
}

func TestFeaturesMatchBlender(t *testing.T) {
	// Blender (Z-up, Blender units): (-5, -1, -1.85083) .. (8.86602, 9, 1.85083), and the
	// scene's unit scale is 0.5 m per unit.
	glb, err := h().Import(fixture(t, "blender-features.blend"))
	if err != nil {
		t.Fatal(err)
	}
	lo, hi, prims := bounds(t, glb)
	wantLo := [3]float64{-5 * 0.5, -1.85083 * 0.5, -9 * 0.5}
	wantHi := [3]float64{8.86602 * 0.5, 1.85083 * 0.5, 1 * 0.5}
	if !near(lo, wantLo) || !near(hi, wantHi) {
		t.Fatalf("bounds %v..%v, want %v..%v", lo, hi, wantLo, wantHi)
	}
	// FlatCube (1 primitive), SmoothBall (1), TwoMat (2 materials), SubCone (1);
	// the lamp and camera leave nothing.
	if prims != 5 {
		t.Fatalf("%d primitives, want 5", prims)
	}
}

func objects(m *scene.Model) map[string]*scene.Object {
	out := map[string]*scene.Object{}
	var walk func(o *scene.Object)
	walk = func(o *scene.Object) {
		out[o.Name] = o
		for _, c := range o.Children {
			walk(c)
		}
	}
	for _, r := range m.Roots {
		walk(r)
	}
	return out
}

func TestHierarchyMaterialsAndShading(t *testing.T) {
	m, err := decode(fixture(t, "blender-features.blend"))
	if err != nil {
		t.Fatal(err)
	}
	o := objects(m)
	for _, name := range []string{"Pivot", "FlatCube", "SmoothBall", "TwoMat", "SubCone"} {
		if o[name] == nil {
			t.Fatalf("missing %q: %v", name, o)
		}
	}
	if o["Point"] != nil || o["Camera"] != nil {
		t.Fatal("lights and cameras leave no trace")
	}
	if len(o["Pivot"].Children) != 1 || o["Pivot"].Children[0] != o["FlatCube"] {
		t.Fatal("FlatCube is parented to Pivot")
	}

	// Materials: the Principled BSDF's Base Color. Blender 5 keeps a node tree on every
	// material (use_nodes is deprecated), so the second material — whose viewport colour
	// was set to blue — renders with the node tree's default grey, and that is what is read.
	cube := o["FlatCube"].Prims[0]
	if cube.Material != "RedNodes" || cube.BaseColor == nil || cube.BaseColor[0] != 1 || cube.BaseColor[1] != 0 {
		t.Fatalf("cube material %q %v", cube.Material, cube.BaseColor)
	}
	two := o["TwoMat"].Prims
	if len(two) != 2 || two[0].Material != "RedNodes" || two[1].Material != "BlueViewport" ||
		math.Abs(two[1].BaseColor[0]-0.8) > 1e-6 || math.Abs(two[1].BaseColor[2]-0.8) > 1e-6 {
		t.Fatalf("two-material plane: %d prims", len(two))
	}
	// Shading: a flat-shaded cube has 24 vertices (per-face normals); each normal is axis-aligned.
	if len(cube.Positions) != 24 {
		t.Fatalf("flat cube has %d vertices, want 24", len(cube.Positions))
	}
	for _, n := range cube.Normals {
		if math.Abs(math.Max(math.Abs(n[0]), math.Max(math.Abs(n[1]), math.Abs(n[2])))-1) > 1e-6 {
			t.Fatalf("flat normal %v is not along an axis", n)
		}
	}
	// A smooth sphere shares vertices: far fewer than 3 per triangle, and unit normals pointing outward.
	ball := o["SmoothBall"].Prims[0]
	if len(ball.Positions) >= len(ball.Indices) {
		t.Fatalf("smooth sphere: %d vertices for %d indices", len(ball.Positions), len(ball.Indices))
	}
	for i, n := range ball.Normals {
		p := ball.Positions[i]
		if n[0]*p[0]+n[1]*p[1]+n[2]*p[2] <= 0 {
			t.Fatalf("smooth normal %v points into the sphere at %v", n, p)
		}
	}
	if ball.UVs == nil {
		t.Fatal("the sphere has UVs")
	}
}

func TestTransformsMatchBlender(t *testing.T) {
	// Blender reports FlatCube's world translation as (5, 0, 0), Pivot at (1, 2, 4) (the delta
	// location adds 1 to z) — rotated through a parent whose quaternion turns 90° about Z.
	glb, err := h().Import(fixture(t, "blender-features.blend"))
	if err != nil {
		t.Fatal(err)
	}
	roots, _ := scene.Flatten(glb)
	var cube *scene.FlatNode
	scene.Walk(roots, func(n *scene.FlatNode, _ int) {
		if n.Name == "FlatCube" {
			cube = n
		}
	})
	if cube == nil {
		t.Fatal("no FlatCube")
	}
	var c [3]float64
	for _, v := range cube.Prims[0].Positions {
		for i := range c {
			c[i] += v[i] / float64(len(cube.Prims[0].Positions))
		}
	}
	// The cube's centre: Blender (5, 0, 0) × 0.5 m, in Y-up (x, z, -y) = (2.5, 0, 0).
	if !near(c, [3]float64{2.5, 0, 0}) {
		t.Fatalf("cube centre %v, want (2.5, 0, 0)", c)
	}
}

func TestCompressionVariantsDecodeAlike(t *testing.T) {
	zst := fixture(t, "blender-scene.blend")
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := dec.DecodeAll(zst, nil)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(raw)
	_ = w.Close()
	want, err := h().Import(zst)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"uncompressed": raw, "gzip": gz.Bytes()} {
		got, err := h().Import(b)
		if err != nil || !bytes.Equal(want, got) {
			t.Errorf("%s decodes differently from zstd (err=%v)", name, err)
		}
	}
}

func TestOldVersionsAreRefusedByName(t *testing.T) {
	dec, _ := zstd.NewReader(nil)
	raw, _ := dec.DecodeAll(fixture(t, "blender-scene.blend"), nil)
	old := append([]byte(nil), raw...)
	copy(old[13:17], "0402") // BLENDER17-01v0402: saved by Blender 4.2
	_, err := decode(old)
	if err == nil || !strings.Contains(err.Error(), "4.2") {
		t.Fatalf("want an error naming 4.2, got %v", err)
	}
}

func TestUnsupportedObjectsAreRefusedByName(t *testing.T) {
	_, err := decode(fixture(t, "blender-curve.blend"))
	if err == nil || !strings.Contains(err.Error(), `"Path"`) || !strings.Contains(err.Error(), "curve") {
		t.Fatalf("a curve object must be refused by name, got %v", err)
	}
}

func TestMalformed(t *testing.T) {
	cases := map[string][]byte{
		"empty":     {},
		"not blend": []byte("hello world, this is not a blend file"),
		"bad head":  []byte("BLENDER17-02v0502xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
		"32-bit":    []byte("BLENDER_v500xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
		"bad zstd":  {0x28, 0xb5, 0x2f, 0xfd, 1, 2, 3},
		"bad gzip":  {0x1f, 0x8b, 1, 2, 3},
	}
	for name, b := range cases {
		if _, err := decode(b); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := h().Export([]byte("x"), ".blend"); err == nil {
		t.Fatal("writing .blend is not supported")
	}
}

// ReadOnly must not advertise an export the handler cannot perform.
func TestHandlerDeclaresNoExport(t *testing.T) {
	var hd interface{} = Codec.ReadOnly()
	if _, ok := hd.(interface {
		Export([]byte, string) ([]byte, error)
	}); ok {
		t.Fatal("a read-only handler must not implement Exporter")
	}
	if _, ok := hd.(interface{ Import([]byte) ([]byte, error) }); !ok {
		t.Fatal("it still imports")
	}
}

func TestDiffAndMatch(t *testing.T) {
	a := fixture(t, "blender-scene.blend")
	d, err := h().Diff(a, a)
	if err != nil || len(d.Changes) != 0 || d.Format != "blend" {
		t.Fatalf("identical files: %+v err=%v", d.Changes, err)
	}
	d, err = h().Diff(nil, a)
	if err != nil || len(d.Changes) == 0 {
		t.Fatalf("an added file: %+v err=%v", d.Changes, err)
	}
	if !h().Match("a/Rig.BLEND") || h().Match("a.blend1x") {
		t.Fatal("Match is by extension")
	}
}

func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	dec, _ := zstd.NewReader(nil)
	raw, _ := dec.DecodeAll(fixture(t, "blender-scene.blend"), nil)
	try := func(b []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic %v", r)
			}
		}()
		_, _ = decode(b)
	}
	for n := 0; n < len(raw); n += max(1, len(raw)/300) { // truncations
		try(raw[:n])
	}
	for i := 0; i < 600; i++ { // corruptions
		b := append([]byte(nil), raw...)
		for k := 0; k < 1+rng.Intn(8); k++ {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		try(b)
	}
	for i := 0; i < 50; i++ { // corrupted compressed form
		b := append([]byte(nil), fixture(t, "blender-scene.blend")...)
		b[rng.Intn(len(b))] = byte(rng.Intn(256))
		try(b)
	}
}
