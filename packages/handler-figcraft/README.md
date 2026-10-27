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

## Not yet

- **preview**: rendering the changed screens to images for a visual compare.
- **merge**: FigCraft's core can three-way merge (`figcraft_collab::merge`),
  but layers added on both branches get the same new id, so the merge refuses
  them. Giving each branch its own id space first is the open question.

## Building

```bash
cargo build --release -p handler-figcraft                                         # forge-handler-figcraft
cargo build --release -p handler-figcraft --lib --target wasm32-unknown-unknown   # the fhr-wasm-1 module
cargo test -p handler-figcraft
```

FigCraft's crates come from its repository at one pinned commit
(`Cargo.toml`); bump the three together. They need Rust 1.95 or newer.
