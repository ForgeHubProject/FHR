# The 3D family

Every 3D format FHR reads is turned into **one glTF scene**, and everything else —
the semantic diff, the 3D preview, and conversion to any other format — works on
that scene. A format therefore costs a parser and a writer, never a differ or a
per-pair converter.

```
file ──decode──▶ Model ──▶ glTF ──▶ scene.DiffDocuments  (semantic diff)
                              ├──▶ GLB preview            (gltf-scene viewport)
                              └──encode──▶ file           (any other format)
```

## Converting

A handler may declare two optional calls (SPEC.md §7): `import` (its format → a
GLB, faithfully) and `export` (a GLB → its format). `X → Y` is `X.import` then
`Y.export`, so each format needs one importer and one exporter.

```bash
forge convert bracket.obj --to glb          # needs the obj and gltf-scene handlers installed
forge convert part.3mf --to obj --rev v1.0  # the file as of a revision
```

ForgeHub serves the same thing at `GET /repos/:handle/:name/convert?path=&sha=&to=`
(`GET /convert/formats` lists what can be read and written) and the blob view has a
"Convert to…" menu.

Conversion is lossy exactly where the target is: OBJ has no PBR materials, STL no
names or colours, and so on. Nothing is silently different — a handler that cannot
write what the scene holds says so with an error.

## Formats

| Format | Extensions | Read | Write | Up / units | Checked against |
|---|---|---|---|---|---|
| glTF / GLB | `.glb` `.gltf` | ✓ | ✓ (`.gltf` self-contained) | Y-up, metres | — |
| Wavefront OBJ | `.obj` | ✓ | ✓ (no `.mtl`) | numbers pass through | Blender |
| STL | `.stl` | ✓ ASCII + binary | ✓ binary | numbers pass through | Blender |
| PLY | `.ply` | ✓ ASCII, binary LE/BE | ✓ binary LE | numbers pass through | Blender |
| 3MF | `.3mf` | ✓ incl. multi-part | ✓ | numbers pass through | hand-written |
| OFF | `.off` | ✓ | ✓ | numbers pass through | hand-written |
| Collada | `.dae` | ✓ | ✓ | Z-up handled; unit not applied | hand-written |
| X3D (XML) | `.x3d` | ✓ | ✓ | Y-up, metres | hand-written |
| VRML97 | `.wrl` | ✓ plain + gzip | ✓ | Y-up, metres | hand-written |
| FBX | `.fbx` | ✓ binary + ASCII | ✓ binary 7.4 | Z-up handled; **unit applied** | Blender |
| USD | `.usd` `.usda` `.usdc` `.usdz` | ✓ all four | ✓ text, usdz | up-axis and **units applied** | Blender |
| 3D Studio | `.3ds` | ✓ | ✓ | Z-up handled; numbers pass through | hand-written |
| Blender | `.blend` | ✓ 5.0+ | — | Z-up, scene **unit scale applied** | Blender |
| AMF | `.amf` | ✓ plain + zip | ✓ | **unit applied** | hand-written |
| DXF | `.dxf` | ✓ ASCII | ✓ R12 ASCII | Z-up handled; **`$INSUNITS` applied** | ezdxf |

"Units applied" means the file's numbers become glTF's metres on read and the writer
declares metres. "Pass through" means they are copied unchanged — the format has no
declared unit (STL, OBJ, PLY, OFF, 3DS) or the handler does not yet apply it (3MF,
Collada). That inconsistency is known and tracked; do not rely on it.

**"Checked against"** says what the handler was tested with beyond its own round
trips. Blender 5.x and `ezdxf` are real readers: their output was used as fixtures
and our exports were imported back into them. Blender 5.x has no Collada, X3D, 3MF,
3DS or AMF importer, and no VRML or DXF one, so those are tested against
hand-written files only.

### What is refused, and why

A handler refuses what it cannot read **by name**, rather than leaving it out: a
semantic diff that silently ignores a changed object would be wrong in the way that
matters most.

- **Composition and external files:** USD references/payloads/sublayers/variant
  sets, VRML `Inline`/`PROTO`, `.gltf` with an external `.bin`, 3MF parts that are
  missing from the package.
- **Geometry with no mesh in the file:** USD `Sphere`/`Cone`/…, X3D/VRML `Sphere`/
  `Extrusion`/…, DXF ACIS solids (`3DSOLID`, `BODY`, …), Blender curves/text/
  metaballs/grease pencil/volumes. STEP has no handler for this reason.
- **Versions it cannot read:** `.blend` before 5.0 (a different mesh layout), binary
  DXF, DWG.

Animation, skinning, morph targets, cameras, lights and textures are out of scope
across the family; lights and cameras are ignored, the rest is not read.

## Adding a format

1. **`packages/handler-<id>/`** — a Go module (copy `handler-ply` for the shape).
   Define a `scene.Codec`:

   ```go
   var Codec = &scene.Codec{
       ID:      "myformat",
       Formats: []string{".myfmt"},
       Decode:  decode,   // func(blob []byte) (*scene.Model, error)
       Encode:  encode,   // func(roots []*scene.FlatNode, format string) ([]byte, error); nil if read-only
   }
   func main() { fhr.Run(Codec.Handler(), Codec.Info()) }   // Codec.ReadOnly() when Encode is nil
   ```

   `Decode` builds a `scene.Model` (objects with transforms and `scene.FlatPrim`
   triangles/lines/points). `Encode` gets the scene already flattened — transforms
   baked in, strips and fans expanded, mirrored winding fixed (`scene.Flatten`).
   Diff, import, export and preview come from the codec.
2. **A renderer package** — copy `packages/renderer-ply` (a few lines: it points the
   gltf-scene review surface at the handler's preview).
3. **`.github/workflows/release-handler-<id>.yml`** (copy `release-handler-ply.yml`
   and replace the id; it updates the rolling release in place, never deletes it, #84)
   and **manifest entries** (a `[formats]` line, `[assets.handlers."<id>"]`,
   `[assets.renderers]`). Use build SHA `0000000` until the first release replaces it.

### What a handler must do

- **Never panic on input.** Handlers run in a server-side wasm worker on blobs anyone
  can push. Every handler has a test that truncates and corrupts its fixtures hundreds
  of ways and asserts errors, not panics (`TestHostileInputNeverPanics`); copy it.
  Bound everything an input controls: element counts, nesting depth, decompressed
  size, array lengths, expansion (instances, blocks).
- **Be deterministic.** The same scene always exports to the same bytes, and an
  import → export → import → export round trip is byte-stable. Round float64 to the
  float32 glTF holds before writing, and snap rotation noise (`scene.Flatten` does).
- **Prefer a real reader to the spec.** If a third-party tool reads the format
  (Blender, `ezdxf`), generate fixtures with it and import your exports back into it;
  compare world bounds. Say in the PR when no such reader exists.
- **State the up axis and unit** the format uses and what the handler does about them.
