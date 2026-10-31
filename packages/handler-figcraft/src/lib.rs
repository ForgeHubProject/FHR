//! FHR handler for FigCraft design files (`.figcraft`).
//!
//! A `.figcraft` file is a zip of the document's JSON and its images, so git's
//! own diff shows nothing a reviewer can read. This handler opens both sides
//! with FigCraft's own libraries and asks FigCraft's collaboration core what
//! changed, property by property (the same diff its real-time editing syncs).
//! Layers keep persistent ids across saves, so a renamed, moved or restyled
//! layer is the same layer on both sides.
//!
//! Two branches of a design merge with FigCraft's own three-way merge
//! (merge.rs): both sides' edits are kept, and the same property changed both
//! ways is reported as a conflict.
//!
//! The answer reads the way a designer looks at a file: page, then screen (the
//! top-level frame), then layer, then property — "Text: Sign up → Create
//! account", a fill as a colour chip, a move as a position delta. What auto
//! layout moved or resized on its own is kept apart from what someone edited.
//!
//! Build:
//!   cargo build --release -p handler-figcraft                                  # forge-handler-figcraft
//!   cargo build --release -p handler-figcraft --lib --target wasm32-unknown-unknown

mod merge;
mod preview;
mod renumber;
mod tree;
mod values;

use fhr::{Capabilities, Handler, Info, Merged, StructuredDiff};

use figcraft_format::Loaded;
pub use preview::CHUNK_KEYWORD;

/// The handler id, also the `format` of every diff it answers.
pub const ID: &str = "figcraft";

/// The `.figcraft` handler.
pub struct FigCraft;

fhr::export_wasm!(FigCraft, FigCraft);

impl Handler for FigCraft {
    fn info(&self) -> Info {
        Info::new(ID, [".figcraft"])
            .capabilities(Capabilities {
                semantic_compare: true,
                semantic_merge: true,
                ..Capabilities::default()
            })
            .preview("image/png")
    }

    /// Every screen drawn on one PNG sheet, with where each screen and each of
    /// its layers is (see preview.rs): what the renderer compares.
    fn preview(&self, blob: &[u8]) -> Result<Vec<u8>, String> {
        let file = load(blob)?;
        preview::sheet(&file.doc)
    }

    fn merge(&self, base: &[u8], ours: &[u8], theirs: &[u8]) -> Result<Merged, String> {
        merge::merge(base, ours, theirs)
    }

    fn diff(&self, base: &[u8], head: &[u8]) -> Result<StructuredDiff, String> {
        let mut b = load(base).map_err(|e| format!("base: {e}"))?;
        let mut h = load(head).map_err(|e| format!("head: {e}"))?;
        // A missing side is the other file without its pages, not a file
        // with another name.
        if base.is_empty() {
            b.doc.name.clone_from(&h.doc.name);
        }
        if head.is_empty() {
            h.doc.name.clone_from(&b.doc.name);
        }
        let mut changes = tree::changes(&b, &h)?;
        // Every edit shows: bytes that differ with no design change between
        // them changed something outside the design (the thumbnail, say).
        if changes.is_empty() && base != head {
            changes.push(values::file_data_changed());
        }
        Ok(StructuredDiff::new(ID, changes))
    }
}

/// One side of the diff. An empty blob is the added- or deleted-file case: a
/// file with no pages, so everything on the other side is added or removed.
fn load(blob: &[u8]) -> Result<Loaded, String> {
    if blob.is_empty() {
        return Ok(Loaded::from(figcraft_doc::Document::without_pages("")));
    }
    figcraft_format::load_bytes_with_versions(blob).map_err(|e| e.to_string())
}
