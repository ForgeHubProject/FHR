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

## Merge: two branches of a design

`merge` runs FigCraft's own three-way merge (`figcraft_collab::merge`, the one
its in-app branches use). Each side's changes since the common version are
kept, property by property; lists whose items stand on their own (fills,
strokes, effects…) merge item by item. A conflict is the same property changed
both ways, or a layer deleted on one side and edited on the other. The result
holds ours at every conflict, and each conflict is reported with its path in
the diff (`layers/18/kind`, `layers/23`, `document/styles/S:4`) and both
values. The merged file keeps ours' name, page to open on and in-file history,
and has no thumbnail until FigCraft next saves it.

Outside a live session FigCraft numbers a new layer one above the highest in
the file, so two branches made from the same commit number their new layers
alike: layer 144 is a screen on one branch and a button on the other. Before
merging, theirs' new layers and comments that clash with ours get numbers
nobody uses, and everything that points at them follows: the tree, instances
and the layers that mirror a component, component properties, prototype links
and flows, measurements, board connectors, pattern fills, comments, dev
snapshots, votes and library copies. Ours, the branch merged into, keeps its
numbers. The renumbering names every field of FigCraft's layer model, so a
FigCraft update that adds one doesn't compile until it is looked at.

Limits:

- A layer theirs added keeps its new number only in the merged file. A branch
  that keeps going after the merge still has the old one, so merge the target
  back into it before its next round of edits (or start a fresh branch).
- Styles, variables and variable collections added on both sides get the same
  id too, and are reported as a conflict rather than renumbered.
- `apply-choices` isn't answered yet: taking theirs at a conflict needs the
  renumbering, which only the merge knows.

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
