//! FHR handler for Photoshop documents (`.psd`, and `.psb` for large ones).
//!
//! A PSD is binary, so git's own diff has nothing a reviewer can read. This
//! handler reads both sides with a reader written from Adobe's published file
//! format (file.rs) and says what changed the way Photoshop's panels show it:
//! the document, then each layer — inside its group — then property. "Text:
//! Hello World → Today is your tomorrow", a colour as a chip, a move as a
//! position delta, a smart object's turn as an angle. Layers keep the id
//! Photoshop gives them across saves, so a renamed or moved layer is the same
//! layer on both sides.
//!
//! The preview is the image Photoshop flattened the layers into, with where
//! each layer is on it (preview.rs), so the renderer can show both versions and
//! outline what changed.
//!
//! Build:
//!   cargo build --release -p handler-psd                                  # forge-handler-psd
//!   cargo build --release -p handler-psd --lib --target wasm32-unknown-unknown

mod bytes;
mod descriptor;
mod engine;
mod file;
mod preview;
mod tree;
mod values;

use fhr::{Capabilities, Handler, Info, StructuredDiff};

pub use preview::CHUNK_KEYWORD;

/// The handler id, also the `format` of every diff it answers.
pub const ID: &str = "psd";

/// The `.psd` handler.
pub struct Psd;

fhr::export_wasm!(Psd, Psd);

impl Handler for Psd {
    fn info(&self) -> Info {
        Info::new(ID, [".psd", ".psb"])
            .capabilities(Capabilities {
                semantic_compare: true,
                ..Capabilities::default()
            })
            .preview("image/png")
    }

    /// The flattened image with every layer's box (see preview.rs): what the
    /// renderer compares.
    fn preview(&self, blob: &[u8]) -> Result<Vec<u8>, String> {
        preview::sheet(blob)
    }

    fn diff(&self, base: &[u8], head: &[u8]) -> Result<StructuredDiff, String> {
        let b = load(base).map_err(|e| format!("base: {e}"))?;
        let h = load(head).map_err(|e| format!("head: {e}"))?;
        let mut changes = tree::changes(b.as_ref(), h.as_ref());
        // Every edit shows: bytes that differ with no layer change between them
        // changed something outside the layers (the thumbnail, metadata).
        if changes.is_empty() && base != head {
            changes.push(values::file_data_changed());
        }
        Ok(StructuredDiff::new(ID, changes))
    }
}

/// One side. An empty blob is the added- or deleted-file case.
fn load(blob: &[u8]) -> Result<Option<file::Document>, String> {
    if blob.is_empty() {
        return Ok(None);
    }
    file::parse(blob).map(Some)
}
