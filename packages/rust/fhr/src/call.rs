//! fhr-wasm-1 (SPEC.md §7): one request in, one response out, both shaped
//! `[u32 header length, little-endian][JSON header][blobs, back to back]`.
//! The calls and their JSON are the subprocess protocol's; blobs travel raw.
//! [`call`] is the whole contract — it has no wasm in it, so it is tested
//! natively; `wasm.rs` only moves bytes in and out of the module's memory.

use std::panic::{catch_unwind, AssertUnwindSafe};

use serde::Deserialize;
use serde_json::{json, Map, Value};

use crate::{export_format, no_preview, Handler, MEDIA_TYPE_GLB};

#[derive(Deserialize, Default)]
#[serde(default)]
struct Request {
    op: String,
    blobs: Vec<i64>,
    path: String,
    format: String,
    take: Vec<String>,
}

/// Answer one fhr-wasm-1 request. Never panics: a malformed request, a call the
/// handler doesn't declare and a panic inside the handler all come back as
/// `{"error": …}`, so the module stays usable (in a wasm module a panic traps
/// instead — the host then starts a fresh instance).
pub fn call<H: Handler + ?Sized>(handler: &H, request: &[u8]) -> Vec<u8> {
    match catch_unwind(AssertUnwindSafe(|| answer(handler, request))) {
        Ok(Ok((result, blobs))) => encode_response(Some(result), None, &blobs),
        Ok(Err(e)) => encode_response(None, Some(e), &[]),
        Err(panic) => encode_response(
            None,
            Some(format!("handler panicked: {}", panic_message(&panic))),
            &[],
        ),
    }
}

fn panic_message(panic: &Box<dyn std::any::Any + Send>) -> String {
    if let Some(s) = panic.downcast_ref::<&str>() {
        (*s).to_string()
    } else if let Some(s) = panic.downcast_ref::<String>() {
        s.clone()
    } else {
        "unknown panic".to_string()
    }
}

fn answer<H: Handler + ?Sized>(h: &H, request: &[u8]) -> Result<(Value, Vec<Vec<u8>>), String> {
    let (r, blobs) = decode_request(request)?;
    let info = h.info().with_defaults();
    let take = |names: &[&str]| -> Result<(), String> {
        if blobs.len() == names.len() {
            Ok(())
        } else {
            Err(format!(
                "{} takes {} blob(s) ({}), got {}",
                r.op,
                names.len(),
                go_names(names),
                blobs.len()
            ))
        }
    };
    match r.op.as_str() {
        "info" => Ok((to_value(&info)?, vec![])),
        "match" => Ok((Value::Bool(h.matches(&r.path)), vec![])),
        "diff" => {
            take(&["base", "head"])?;
            Ok((to_value(&h.diff(blobs[0], blobs[1])?)?, vec![]))
        }
        "merge" => {
            take(&["base", "ours", "theirs"])?;
            let m = h.merge(blobs[0], blobs[1], blobs[2])?;
            let mut result = Map::new();
            if !m.conflicts.is_empty() {
                result.insert("conflicts".into(), to_value(&m.conflicts)?);
            }
            Ok((Value::Object(result), vec![m.blob]))
        }
        "preview" => {
            let Some(media) = info.preview.clone() else {
                return Err(no_preview(&info.id));
            };
            take(&["blob"])?;
            Ok((json!({ "mediaType": media }), vec![h.preview(blobs[0])?]))
        }
        "applyChoices" => {
            if !info.can(|c| c.apply_choices) {
                return Err(format!("{} cannot apply conflict choices", info.id));
            }
            take(&["merged", "theirs"])?;
            Ok((
                json!({}),
                vec![h.apply_choices(blobs[0], blobs[1], &r.take)?],
            ))
        }
        "import" => {
            if !info.can(|c| c.import) {
                return Err(format!("{} cannot import to glTF", info.id));
            }
            take(&["blob"])?;
            Ok((
                json!({ "mediaType": MEDIA_TYPE_GLB }),
                vec![h.import(blobs[0])?],
            ))
        }
        "export" => {
            if !info.can(|c| c.export) {
                return Err(format!("{} cannot export from glTF", info.id));
            }
            take(&["glb"])?;
            let format = export_format(&info, &r.format)?;
            Ok((json!({}), vec![h.export(blobs[0], &format)?]))
        }
        op => Err(format!("unknown call {op:?}")),
    }
}

fn to_value<T: serde::Serialize>(v: &T) -> Result<Value, String> {
    serde_json::to_value(v).map_err(|e| format!("encoding the result: {e}"))
}

/// Blob names as Go's %v prints a list: `[base head]`.
fn go_names(names: &[&str]) -> String {
    format!("[{}]", names.join(" "))
}

/// Split a request into its header and blobs, checking every length against
/// what is actually there.
fn decode_request(req: &[u8]) -> Result<(Request, Vec<&[u8]>), String> {
    if req.len() < 4 {
        return Err(format!("request too short: {} bytes", req.len()));
    }
    let n = u32::from_le_bytes([req[0], req[1], req[2], req[3]]) as usize;
    if n > req.len() - 4 {
        return Err(format!("request header of {n} bytes overruns the request"));
    }
    let r: Request =
        serde_json::from_slice(&req[4..4 + n]).map_err(|e| format!("request header: {e}"))?;
    let mut at = 4 + n;
    let mut blobs = Vec::with_capacity(r.blobs.len());
    for (i, &len) in r.blobs.iter().enumerate() {
        if len < 0 || len as u64 > (req.len() - at) as u64 {
            return Err(format!("blob {i} ({len} bytes) overruns the request"));
        }
        blobs.push(&req[at..at + len as usize]);
        at += len as usize;
    }
    if at != req.len() {
        return Err(format!("{} bytes after the last blob", req.len() - at));
    }
    Ok((r, blobs))
}

fn encode_response(result: Option<Value>, error: Option<String>, blobs: &[Vec<u8>]) -> Vec<u8> {
    let mut header = Map::new();
    if let Some(e) = error {
        header.insert("error".into(), Value::String(e));
    } else {
        header.insert("result".into(), result.unwrap_or(Value::Null));
        if !blobs.is_empty() {
            header.insert(
                "blobs".into(),
                Value::Array(blobs.iter().map(|b| Value::from(b.len())).collect()),
            );
        }
    }
    let header = serde_json::to_vec(&Value::Object(header))
        .unwrap_or_else(|_| br#"{"error":"encoding the response"}"#.to_vec());
    let mut out = Vec::with_capacity(4 + header.len() + blobs.iter().map(Vec::len).sum::<usize>());
    out.extend_from_slice(&(header.len() as u32).to_le_bytes());
    out.extend_from_slice(&header);
    for b in blobs {
        out.extend_from_slice(b);
    }
    out
}

/// Build an fhr-wasm-1 request: a header with the call and its parameters,
/// then the blobs. For hosts written in Rust, and the tests.
pub fn encode_request(op: &str, params: Value, blobs: &[&[u8]]) -> Vec<u8> {
    let mut header = match params {
        Value::Object(m) => m,
        _ => Map::new(),
    };
    header.insert("op".into(), Value::String(op.into()));
    header.insert(
        "blobs".into(),
        Value::Array(blobs.iter().map(|b| Value::from(b.len())).collect()),
    );
    let header = serde_json::to_vec(&Value::Object(header)).unwrap_or_default();
    let mut out =
        Vec::with_capacity(4 + header.len() + blobs.iter().map(|b| b.len()).sum::<usize>());
    out.extend_from_slice(&(header.len() as u32).to_le_bytes());
    out.extend_from_slice(&header);
    for b in blobs {
        out.extend_from_slice(b);
    }
    out
}

/// Split a response into its result and blobs; its `{"error": …}` becomes `Err`.
/// For hosts written in Rust, and the tests.
pub fn decode_response(resp: &[u8]) -> Result<(Value, Vec<Vec<u8>>), String> {
    if resp.len() < 4 {
        return Err(format!("response too short: {} bytes", resp.len()));
    }
    let n = u32::from_le_bytes([resp[0], resp[1], resp[2], resp[3]]) as usize;
    if n > resp.len() - 4 {
        return Err("response header overruns the response".into());
    }
    let header: Value =
        serde_json::from_slice(&resp[4..4 + n]).map_err(|e| format!("response header: {e}"))?;
    if let Some(e) = header.get("error").and_then(Value::as_str) {
        return Err(e.to_string());
    }
    let mut at = 4 + n;
    let mut blobs = Vec::new();
    for len in header
        .get("blobs")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
    {
        let len = len.as_u64().ok_or("response blob length is not a number")? as usize;
        if len > resp.len() - at {
            return Err("response blob overruns the response".into());
        }
        blobs.push(resp[at..at + len].to_vec());
        at += len;
    }
    Ok((header.get("result").cloned().unwrap_or(Value::Null), blobs))
}
