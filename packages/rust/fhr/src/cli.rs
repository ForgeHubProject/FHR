//! The subprocess protocol (SPEC.md §7): one call per process, the subcommand
//! in argv[1], the payload as JSON on stdin with base64 blobs, the answer as
//! JSON on stdout, `{"error": …}` on stderr with exit code 1. Byte for byte
//! what the Go SDK's RunCLI does, so forge cannot tell the languages apart.

use std::io::{Read, Write};
use std::panic::{catch_unwind, AssertUnwindSafe};

use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine as _;
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::{export_format, no_preview, Handler, Info, SemanticConflict, MEDIA_TYPE_GLB};

/// A handler binary's whole `main`: the subprocess protocol over the process's
/// own arguments and streams, then exit with its status.
pub fn run<H: Handler + ?Sized>(handler: &H) -> ! {
    // A panic is answered as {"error": …} on stderr; the default hook would
    // print its own report there first, which is not protocol.
    std::panic::set_hook(Box::new(|_| {}));
    let args: Vec<String> = std::env::args().skip(1).collect();
    let code = run_cli(
        handler,
        &args,
        &mut std::io::stdin().lock(),
        &mut std::io::stdout().lock(),
        &mut std::io::stderr().lock(),
    );
    std::process::exit(code)
}

#[derive(Deserialize)]
struct DiffIn {
    #[serde(default)]
    base: String,
    #[serde(default)]
    head: String,
}

#[derive(Deserialize)]
struct MergeIn {
    #[serde(default)]
    base: String,
    #[serde(default)]
    ours: String,
    #[serde(default)]
    theirs: String,
}

#[derive(Deserialize)]
struct BlobIn {
    #[serde(default)]
    blob: String,
    #[serde(default)]
    format: String,
}

#[derive(Deserialize)]
struct ApplyIn {
    #[serde(default)]
    merged: String,
    #[serde(default)]
    theirs: String,
    #[serde(default)]
    take: Vec<String>,
}

#[derive(Serialize)]
struct MergeOut {
    blob: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    conflicts: Vec<SemanticConflict>,
}

#[derive(Serialize)]
struct MediaOut {
    #[serde(rename = "mediaType")]
    media_type: String,
    blob: String,
}

#[derive(Serialize)]
struct BlobOut {
    blob: String,
}

/// The subprocess protocol with explicit streams; returns the exit code. [`run`]
/// calls it with the real ones; tests call it in-process.
pub fn run_cli<H: Handler + ?Sized>(
    h: &H,
    args: &[String],
    stdin: &mut dyn Read,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let info = h.info().with_defaults();
    let Some(sub) = args.first() else {
        let _ = writeln!(
            stderr,
            "usage: forge-handler-{} <match|diff|merge|{}info> [filepath]",
            info.id,
            optional_usage(&info)
        );
        return 1;
    };
    if sub == "match" {
        // A missing path matches nothing — answered, not an error.
        let _ = writeln!(stdout, "{}", args.get(1).is_some_and(|p| h.matches(p)));
        return 0;
    }
    let known = [
        "diff",
        "merge",
        "preview",
        "apply-choices",
        "import",
        "export",
        "info",
    ];
    if !known.contains(&sub.as_str()) {
        let _ = writeln!(stderr, "unknown subcommand: {sub}");
        return 1;
    }
    let answer = catch_unwind(AssertUnwindSafe(|| subcommand(h, &info, sub, stdin)))
        .unwrap_or_else(|panic| Err(format!("handler panicked: {}", panic_text(&panic))));
    match answer {
        Ok(out) => {
            let _ = serde_json::to_writer(&mut *stdout, &out);
            let _ = writeln!(stdout);
            0
        }
        Err(e) => {
            let _ = serde_json::to_writer(&mut *stderr, &serde_json::json!({ "error": e }));
            let _ = writeln!(stderr);
            1
        }
    }
}

fn subcommand<H: Handler + ?Sized>(
    h: &H,
    info: &Info,
    sub: &str,
    stdin: &mut dyn Read,
) -> Result<Value, String> {
    match sub {
        "info" => json(info),
        "diff" => {
            let inp: DiffIn = read(stdin)?;
            json(&h.diff(&blob("base", &inp.base)?, &blob("head", &inp.head)?)?)
        }
        "merge" => {
            let inp: MergeIn = read(stdin)?;
            let m = h.merge(
                &blob("base", &inp.base)?,
                &blob("ours", &inp.ours)?,
                &blob("theirs", &inp.theirs)?,
            )?;
            json(&MergeOut {
                blob: B64.encode(&m.blob),
                conflicts: m.conflicts,
            })
        }
        "preview" => {
            let Some(media) = info.preview.clone() else {
                return Err(no_preview(&info.id));
            };
            let inp: BlobIn = read(stdin)?;
            json(&MediaOut {
                media_type: media,
                blob: B64.encode(h.preview(&blob("input", &inp.blob)?)?),
            })
        }
        "apply-choices" => {
            if !info.can(|c| c.apply_choices) {
                return Err(format!("{} cannot apply conflict choices", info.id));
            }
            let inp: ApplyIn = read(stdin)?;
            let out = h.apply_choices(
                &blob("merged", &inp.merged)?,
                &blob("theirs", &inp.theirs)?,
                &inp.take,
            )?;
            json(&BlobOut {
                blob: B64.encode(out),
            })
        }
        "import" => {
            if !info.can(|c| c.import) {
                return Err(format!("{} cannot import to glTF", info.id));
            }
            let inp: BlobIn = read(stdin)?;
            json(&MediaOut {
                media_type: MEDIA_TYPE_GLB.into(),
                blob: B64.encode(h.import(&blob("input", &inp.blob)?)?),
            })
        }
        "export" => {
            if !info.can(|c| c.export) {
                return Err(format!("{} cannot export from glTF", info.id));
            }
            let inp: BlobIn = read(stdin)?;
            let glb = blob("input", &inp.blob)?;
            let format = export_format(info, &inp.format)?;
            json(&BlobOut {
                blob: B64.encode(h.export(&glb, &format)?),
            })
        }
        _ => unreachable!("checked by run_cli"),
    }
}

/// The optional subcommands a handler declares, for the usage line — in the Go
/// SDK's order: apply-choices, preview, import, export.
fn optional_usage(info: &Info) -> String {
    let mut s = String::new();
    if info.can(|c| c.apply_choices) {
        s += "apply-choices|";
    }
    if info.preview.is_some() {
        s += "preview|";
    }
    if info.can(|c| c.import) {
        s += "import|";
    }
    if info.can(|c| c.export) {
        s += "export|";
    }
    s
}

fn read<T: DeserializeOwned>(stdin: &mut dyn Read) -> Result<T, String> {
    serde_json::from_reader(stdin).map_err(|e| e.to_string())
}

fn blob(side: &str, s: &str) -> Result<Vec<u8>, String> {
    B64.decode(s)
        .map_err(|e| format!("decoding {side} blob: {e}"))
}

fn json<T: Serialize>(v: &T) -> Result<Value, String> {
    serde_json::to_value(v).map_err(|e| e.to_string())
}

fn panic_text(panic: &Box<dyn std::any::Any + Send>) -> String {
    panic
        .downcast_ref::<&str>()
        .map(|s| (*s).to_string())
        .or_else(|| panic.downcast_ref::<String>().cloned())
        .unwrap_or_else(|| "unknown panic".into())
}
