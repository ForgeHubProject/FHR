# handler-figcraft

The FHR handler for [FigCraft](https://github.com/storytold/figcraft) design
files (`.figcraft`), written in Rust on the [`fhr` SDK](../rust/fhr).

A `.figcraft` file is a zip of the document's JSON and its images, so git's own
diff has nothing a reviewer can read. This handler opens both sides with
FigCraft's own crates (`figcraft-format`, `figcraft-doc`) and asks FigCraft's
collaboration core (`figcraft-collab`) what changed, property by property: the
same diff its real-time editing syncs. Layers keep their ids across saves, so a
renamed, moved or restyled layer is the same layer on both sides.

## What a diff reads like

Page, then screen (the top-level frame), then layer, then property:

```
Page "Onboarding"
  Screen "1 Welcome"
    Text "Title"              Text   Find your next summit → Find your next peak
    Ellipse "Sun"             Fill   Radial → #FF8A3D
    Frame "Get started"       Corner radius   16 → 24   Δ +8
    + Rectangle "New badge"   60 × 24
    − Text "Sign in link"     I already have an account
    Moved or resized by layout
      Text "Title"            Width  314 → 287
```

- Solid colours are `[r, g, b, a]` under a label naming the colour, so the
  renderer draws chips; positions are `[x, y]` in canvas pixels; rotations
  carry `°`.
- What auto layout, hugging text or a group fitting its layers moved or
  resized is kept in its own group, apart from what someone edited.
- An added or removed layer is one row with what it was (a text's words, or
  its size and how many layers it holds), not a row per layer inside it.
- Styles, variables, collections and comments are under "Document"; versions
  and branches saved inside the file under "Version history".

Paths are stable keys: `pages/<id>`, `screens/<id>`, `layers/<id>/<property>`,
`layout/<id>/<property>`, `document/…`, `history/…`, with FigCraft's own layer
ids.

## Preview: the screens themselves

`preview` draws every screen with FigCraft's own renderer onto one PNG sheet,
at true size (smaller only past 16 megapixels). A `tEXt` chunk named
`fhr-figcraft` says where each screen is on the sheet and where each of its
layers is on the screen, by layer id. The renderer cuts a changed screen out of
the before and after sheets, shows it in the image compare (the image, a
slider, side by side), and with "Show differences" outlines what changed:
yellow changed, green added, red removed, and grey dashed for what auto layout
moved (behind its own toggle). The screen's changes are listed beside it, and
pointing at a row or an outline lights up the other.

Text is drawn with the fonts compiled in, never the machine's own, so a file
looks the same previewed on the server, in a browser or by forge: Inter
(FigCraft's default font), from [craft-fonts](https://github.com/storytold/craft-fonts)
(OFL-1.1). `fonts.sh` makes that set from a craft-fonts checkout; builds take it
as `CRAFT_FONTS_DIR`, and without it text isn't drawn. The release workflow
fetches the fonts at a pinned commit.

## Not yet

- **merge**: FigCraft's core can three-way merge (`figcraft_collab::merge`),
  but layers added on both branches get the same new id, so the merge refuses
  them. Giving each branch its own id space first is the open question.

## Building

```bash
sh packages/handler-figcraft/fonts.sh ../craft-fonts "$PWD/.figcraft-fonts"         # once, from a craft-fonts checkout
export CRAFT_FONTS_DIR="$PWD/.figcraft-fonts" CRAFT_FONTS_REQUIRED=1
cargo build --release -p handler-figcraft                                         # forge-handler-figcraft
cargo build --release -p handler-figcraft --lib --target wasm32-unknown-unknown   # the fhr-wasm-1 module
cargo test -p handler-figcraft
```

FigCraft's crates come from its repository at one pinned commit
(`Cargo.toml`); bump them together. They need Rust 1.95 or newer.
