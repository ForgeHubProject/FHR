//! The FHR handler SDK for Rust — the Rust twin of `packages/go/fhr`.
//!
//! A handler implements one [`Handler`] and gets both entry points every FHR
//! handler needs (SPEC.md §7):
//!
//! * the **subprocess protocol** forge runs natively — `match`, `diff`,
//!   `merge`, `preview`, `apply-choices`, `import`, `export` and `info`, JSON
//!   over stdin/stdout with base64 blobs — from [`run`] in its `main`;
//! * the **`fhr-wasm-1` module** ForgeHub runs on its server and in browsers —
//!   the `fhr_alloc` / `fhr_free` / `fhr_call` exports — from [`export_wasm!`].
//!
//! ```ignore
//! struct Kv;
//! impl fhr::Handler for Kv {
//!     fn info(&self) -> fhr::Info { fhr::Info::new("kv", [".kv"]) }
//!     fn diff(&self, base: &[u8], head: &[u8]) -> Result<fhr::StructuredDiff, String> { … }
//! }
//! fhr::export_wasm!(Kv, Kv);
//! fn main() { fhr::run(&Kv) }
//! ```
//!
//! Both entry points answer exactly as the Go SDK's do — same JSON, same exit
//! codes, same error texts — so forge and ForgeHub cannot tell the languages
//! apart. Unlike Go, Rust cannot ask a value which traits it implements, so a
//! handler *declares* its optional calls in [`Info`] (`preview`,
//! `capabilities`) and the SDK answers only the calls declared.

mod call;
mod cli;
mod wire;

// Only meaningful in a wasm32 module (its pointers are 32 bits), and only
// reached through export_wasm!, which is compiled for wasm32 alone.
#[doc(hidden)]
#[cfg(target_arch = "wasm32")]
pub mod wasm;

pub use call::{call, decode_response, encode_request};
pub use cli::{run, run_cli};
pub use wire::*;

/// The subprocess/wasm protocol revision these entry points speak.
pub const PROTOCOL_VERSION: &str = "1.0";

/// The wasm contract the module exports implement (SPEC.md §7).
pub const WASM_ABI: &str = "fhr-wasm-1";

/// The media type of a binary glTF — a 3D preview's, and `import`'s output.
pub const MEDIA_TYPE_GLB: &str = "model/gltf-binary";

/// A format's handler. Only `info` and `diff` are required; every other call
/// answers "not supported" until the handler implements it and declares it in
/// [`Info`]. Errors are plain messages, reported as `{"error": "…"}`.
pub trait Handler {
    /// The handler's id, formats and capabilities — the `info` call's answer.
    /// [`Info::preview`] and the capabilities flags decide which optional calls
    /// the SDK answers.
    fn info(&self) -> Info;

    /// Whether this handler claims `path`. Default: its extension is one of
    /// [`Info::formats`], compared case-insensitively.
    fn matches(&self, path: &str) -> bool {
        let lower = path.to_ascii_lowercase();
        self.info()
            .formats
            .iter()
            .any(|ext| lower.ends_with(&ext.to_ascii_lowercase()))
    }

    /// The semantic diff of two blobs. An empty blob is a side that doesn't
    /// exist: the file was added (empty base) or deleted (empty head).
    fn diff(&self, base: &[u8], head: &[u8]) -> Result<StructuredDiff, String>;

    /// Three-way merge. Declare `capabilities.semantic_merge` when implemented.
    /// The blob holds ours wherever the handler could not reconcile, and
    /// `conflicts` says where; no conflicts is a clean merge.
    fn merge(&self, _base: &[u8], _ours: &[u8], _theirs: &[u8]) -> Result<Merged, String> {
        Err(format!("{} cannot merge", self.info().id))
    }

    /// Something a host can draw instead of the raw bytes (OBJ → GLB, a design →
    /// PNG). Answered only when [`Info::preview`] names its media type.
    fn preview(&self, _blob: &[u8]) -> Result<Vec<u8>, String> {
        Err(no_preview(&self.info().id))
    }

    /// Resolve a merge's conflicts: `merged` is the merge's own result (ours at
    /// every conflict), `take` the conflict paths to take from theirs.
    /// Answered only when `capabilities.apply_choices` is declared.
    fn apply_choices(
        &self,
        _merged: &[u8],
        _theirs: &[u8],
        _take: &[String],
    ) -> Result<Vec<u8>, String> {
        Err(format!("{} cannot apply conflict choices", self.info().id))
    }

    /// The handler's own format → a GLB. Answered only when `capabilities.import`.
    fn import(&self, _blob: &[u8]) -> Result<Vec<u8>, String> {
        Err(format!("{} cannot import to glTF", self.info().id))
    }

    /// A GLB → one of [`Info::formats`]. Answered only when `capabilities.export`.
    fn export(&self, _glb: &[u8], _format: &str) -> Result<Vec<u8>, String> {
        Err(format!("{} cannot export from glTF", self.info().id))
    }
}

pub(crate) fn no_preview(id: &str) -> String {
    format!("{id} has no preview: the format renders from its own bytes")
}

/// The target of an `export`, resolved against the handler's formats: an empty
/// request is the only format, or an error when there are several — never a
/// silent pick. (The Go SDK's exportFormat, word for word.)
pub(crate) fn export_format(info: &Info, want: &str) -> Result<String, String> {
    if want.is_empty() {
        return match info.formats.as_slice() {
            [only] => Ok(only.clone()),
            all => Err(format!(
                "{} exports several formats ({}): say which",
                info.id,
                go_list(all)
            )),
        };
    }
    if info.formats.iter().any(|f| f == want) {
        return Ok(want.to_string());
    }
    Err(format!(
        "{} does not export {:?} (formats: {})",
        info.id,
        want,
        go_list(&info.formats)
    ))
}

/// A string list as Go's %v prints it: `[.a .b]`.
fn go_list(items: &[String]) -> String {
    format!("[{}]", items.join(" "))
}

/// Export a handler as an fhr-wasm-1 module: defines `fhr_alloc`, `fhr_free`
/// and `fhr_call` (SPEC.md §7) when compiled for wasm32, and nothing natively.
/// The handler is built on first use and kept for the module's life.
///
/// ```ignore
/// fhr::export_wasm!(MyHandler, MyHandler::new());
/// ```
#[macro_export]
macro_rules! export_wasm {
    ($handler:ty, $make:expr) => {
        #[cfg(target_arch = "wasm32")]
        mod __fhr_wasm_exports {
            #[allow(unused_imports)]
            use super::*;

            fn handler() -> &'static $handler {
                static HANDLER: ::std::sync::OnceLock<$handler> = ::std::sync::OnceLock::new();
                HANDLER.get_or_init(|| $make)
            }

            #[unsafe(no_mangle)]
            pub extern "C" fn fhr_alloc(size: u32) -> u32 {
                $crate::wasm::alloc(size)
            }

            /// # Safety
            /// Called by the host with a buffer it was given.
            #[unsafe(no_mangle)]
            pub unsafe extern "C" fn fhr_free(ptr: u32, size: u32) {
                unsafe { $crate::wasm::free(ptr, size) }
            }

            /// # Safety
            /// Called by the host with a buffer from fhr_alloc.
            #[unsafe(no_mangle)]
            pub unsafe extern "C" fn fhr_call(ptr: u32, size: u32) -> u64 {
                unsafe { $crate::wasm::call(handler(), ptr, size) }
            }
        }
    };
}
