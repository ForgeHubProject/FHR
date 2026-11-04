# handler-psd

The FHR handler for Photoshop documents (`.psd`, and `.psb` for large ones),
written in Rust on the [`fhr` SDK](../rust/fhr).

A PSD is binary, so git's own diff has nothing a reviewer can read. This
handler reads both sides with a reader written from Adobe's published
[file format specification](https://www.adobe.com/devnet-apps/photoshop/fileformatashtml/)
and says what changed the way Photoshop's panels show it. Layers keep the id
Photoshop gives them (`lyid`) across saves, so a renamed, restyled or moved
layer is the same layer on both sides. Files without ids (some other apps'
exports) match layers by name.

## What a diff reads like

The document, then each changed layer, inside its group, top of the stack first:

```
Document                       Resolution   72 ppi → 300 ppi
Text "Today is your tomorrow"  Text         Hello World → Today is your tomorrow
                               Colour       ■ #FFFFFF → ■ #FF8000
                               Rotation     0° → 15°
Group "Badges"
  Layer "Sticker"              Position     (300, 20) → (280, 30)   Δ(-20, 10)
                               Pixels       edited
Smart object "Background"      Rotation     0° → 90°
Shape "Strip"                  Opacity      70% → 50%
                               Fill colour  ■ #000000 → ■ #0066CC
+ Layer "Logo"                 40 × 40
− Text "Caption"               What a beautiful day.
```

- **Every layer:** name, visibility, opacity, fill opacity, blend mode, clipping
  mask, locks, colour label, group, place in the stack (only the layers that
  moved, not every layer they passed), layer and vector masks, and layer
  effects (drop shadow, stroke, colour overlay… on or off, colour, opacity,
  size, distance, angle).
- **Text layers:** the text, position, rotation, font, font size (as the
  Character panel shows it, with the text's scale), colour, tracking, leading,
  faux bold or italic, underline, alignment, warp. Text styled in parts says
  which part changed. A text layer still named after its text isn't reported
  as renamed as well.
- **Smart objects:** as Free Transform reads them: the centre, their own width
  and height, rotation, flip, and whether the embedded file changed.
- **Shapes and fill layers:** the path's bounds, the fill colour (other fills as
  edited), and the stroke: on or off, width, colour, opacity, position.
- **Pixel layers:** bounds and whether their pixels changed (channels are
  fingerprinted as saved, not decoded). Adjustment layers say their settings
  changed.
- **Document:** canvas size, colour mode, bit depth, resolution, guides.

Colours are `[r, g, b, a]` under a label naming the colour, so the renderer
draws chips; positions are `[x, y]` in canvas pixels; rotations carry `°`.

## Preview

`preview` is the image Photoshop flattens the layers into and saves after them
(with "Maximize Compatibility", Photoshop's default), as a PNG: RGB, CMYK,
grayscale and indexed, 8, 16 or 32 bits, drawn smaller past 16 megapixels. A
`tEXt` chunk named `fhr-psd` holds each layer's box on it, by layer id (a
group's holds its layers'). The renderer (`renderer-psd`) shows the two images
in the image compare (the image, a slider, side by side) and with "Show
differences" outlines what changed: yellow changed, green added, red removed.
The changes are listed beside it, and pointing at a row or an outline lights
up the other.

## Not yet

- **merge**: a PSD is one binary file to git, so a conflict is a whole-file pick.
- Gradients, patterns, adjustment settings and 3D layers say they changed, not
  how. Text on a path and warped text report their text and styling, not the
  path or the warp's shape.
- A file saved without its flattened image has no preview, and the renderer
  shows the change list alone.

## Building

```bash
cargo build --release -p handler-psd                                         # forge-handler-psd
cargo build --release -p handler-psd --lib --target wasm32-unknown-unknown   # the fhr-wasm-1 module
cargo test -p handler-psd
```

The tests build their documents in code (`tests/common`), laid out the way
Photoshop saves them.
