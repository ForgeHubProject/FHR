//! Example FHR handler written in Rust: `.kv` files, one `key = value` per line
//! (`#` starts a comment). It shows the whole shape of a handler on the `fhr`
//! SDK — a semantic diff per key, a three-way merge that reports the keys both
//! sides changed differently, and conflict resolution — and exports itself both
//! ways: `fhr::run` in main.rs is the subprocess protocol forge runs, and
//! `fhr::export_wasm!` below makes this library the fhr-wasm-1 module ForgeHub
//! runs.
//!
//! Build:
//!   cargo build --release -p example-handler-rust                                  # forge-handler-kv
//!   cargo build --release -p example-handler-rust --lib --target wasm32-unknown-unknown

use fhr::{
    Capabilities, ChangeKind, DiffChange, Handler, Info, Merged, SemanticConflict, StructuredDiff,
};

/// The `.kv` handler.
pub struct Kv;

fhr::export_wasm!(Kv, Kv);

/// A file's entries in their order. A key that appears twice keeps its last value.
fn parse(blob: &[u8]) -> Result<Vec<(String, String)>, String> {
    let text = std::str::from_utf8(blob).map_err(|e| format!("not UTF-8 text: {e}"))?;
    let mut entries: Vec<(String, String)> = Vec::new();
    for (n, line) in text.lines().enumerate() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let (k, v) = line
            .split_once('=')
            .ok_or_else(|| format!("line {}: expected key = value", n + 1))?;
        let (k, v) = (k.trim().to_string(), v.trim().to_string());
        match entries.iter_mut().find(|(key, _)| *key == k) {
            Some(e) => e.1 = v,
            None => entries.push((k, v)),
        }
    }
    Ok(entries)
}

fn get<'a>(entries: &'a [(String, String)], key: &str) -> Option<&'a str> {
    entries
        .iter()
        .find(|(k, _)| k == key)
        .map(|(_, v)| v.as_str())
}

fn write(entries: &[(String, String)]) -> Vec<u8> {
    entries
        .iter()
        .map(|(k, v)| format!("{k} = {v}\n"))
        .collect::<String>()
        .into_bytes()
}

/// Every key the files have, each once, in the order the files list them.
fn keys(files: &[&[(String, String)]]) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for (k, _) in files.iter().flat_map(|f| f.iter()) {
        if !out.contains(k) {
            out.push(k.clone());
        }
    }
    out
}

fn path(key: &str) -> String {
    format!("keys/{key}")
}

impl Handler for Kv {
    fn info(&self) -> Info {
        Info::new("kv", [".kv"]).capabilities(Capabilities {
            semantic_compare: true,
            semantic_merge: true,
            apply_choices: true,
            ..Capabilities::default()
        })
    }

    fn diff(&self, base: &[u8], head: &[u8]) -> Result<StructuredDiff, String> {
        let (a, b) = (
            parse(base).map_err(|e| format!("base: {e}"))?,
            parse(head).map_err(|e| format!("head: {e}"))?,
        );
        let changes = keys(&[&a, &b])
            .into_iter()
            .filter_map(|k| match (get(&a, &k), get(&b, &k)) {
                (None, Some(v)) => Some(DiffChange::new(path(&k), ChangeKind::Added, &k).after(v)),
                (Some(v), None) => {
                    Some(DiffChange::new(path(&k), ChangeKind::Removed, &k).before(v))
                }
                (Some(x), Some(y)) if x != y => Some(
                    DiffChange::new(path(&k), ChangeKind::Modified, &k)
                        .before(x)
                        .after(y),
                ),
                _ => None,
            })
            .collect();
        Ok(StructuredDiff::new("kv", changes))
    }

    /// Per key: a key one side changed takes that side's value; a key both
    /// changed the same way is fine; a key both changed differently is a
    /// conflict, and the merged file keeps ours there.
    fn merge(&self, base: &[u8], ours: &[u8], theirs: &[u8]) -> Result<Merged, String> {
        let (o, a, b) = (parse(base)?, parse(ours)?, parse(theirs)?);
        let mut merged: Vec<(String, String)> = Vec::new();
        let mut conflicts = Vec::new();
        for k in keys(&[&a, &b, &o]) {
            let (base_v, ours_v, theirs_v) = (get(&o, &k), get(&a, &k), get(&b, &k));
            let pick = if ours_v == theirs_v || theirs_v == base_v {
                ours_v
            } else if ours_v == base_v {
                theirs_v
            } else {
                conflicts.push(SemanticConflict {
                    path: path(&k),
                    ours: ours_v.into(),
                    theirs: theirs_v.into(),
                });
                ours_v
            };
            if let Some(v) = pick {
                merged.push((k, v.to_string()));
            }
        }
        Ok(Merged {
            blob: write(&merged),
            conflicts,
        })
    }

    fn apply_choices(
        &self,
        merged: &[u8],
        theirs: &[u8],
        take: &[String],
    ) -> Result<Vec<u8>, String> {
        let (mut m, t) = (parse(merged)?, parse(theirs)?);
        for p in take {
            let key = p
                .strip_prefix("keys/")
                .ok_or_else(|| format!("unknown conflict path {p}"))?;
            match get(&t, key) {
                Some(v) => match m.iter_mut().find(|(k, _)| k == key) {
                    Some(e) => e.1 = v.to_string(),
                    None => m.push((key.to_string(), v.to_string())),
                },
                None => m.retain(|(k, _)| k != key),
            }
        }
        Ok(write(&m))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn changes(base: &str, head: &str) -> serde_json::Value {
        serde_json::to_value(Kv.diff(base.as_bytes(), head.as_bytes()).unwrap().changes).unwrap()
    }

    #[test]
    fn diffs_per_key() {
        assert_eq!(
            changes("a = 1\nb = 2\n# note\nc = 3\n", "a = 1\nb = 20\nd = 4\n"),
            json!([
                {"path": "keys/b", "kind": "modified", "label": "b", "before": "2", "after": "20"},
                {"path": "keys/c", "kind": "removed", "label": "c", "before": "3"},
                {"path": "keys/d", "kind": "added", "label": "d", "after": "4"},
            ])
        );
        assert_eq!(changes("a = 1", "a=1"), json!([]));
    }

    #[test]
    fn refuses_what_it_cannot_read() {
        let e = Kv.diff(b"a = 1", b"just text").unwrap_err();
        assert_eq!(e, "head: line 1: expected key = value");
    }

    #[test]
    fn merges_per_key_and_reports_conflicts() {
        let m = Kv
            .merge(
                b"a = 1\nb = 2\nc = 3\n",
                b"a = 10\nb = 2\nc = 30\n",
                b"a = 1\nb = 20\nc = 31\nd = 4\n",
            )
            .unwrap();
        assert_eq!(
            String::from_utf8(m.blob.clone()).unwrap(),
            "a = 10\nb = 20\nc = 30\nd = 4\n"
        );
        assert_eq!(
            m.conflicts,
            vec![SemanticConflict {
                path: "keys/c".into(),
                ours: "30".into(),
                theirs: "31".into()
            }]
        );
        let resolved = Kv
            .apply_choices(
                &m.blob,
                b"a = 1\nb = 20\nc = 31\nd = 4\n",
                &["keys/c".into()],
            )
            .unwrap();
        assert_eq!(
            String::from_utf8(resolved).unwrap(),
            "a = 10\nb = 20\nc = 31\nd = 4\n"
        );
    }

    #[test]
    fn a_key_deleted_on_one_side_goes() {
        let m = Kv
            .merge(b"a = 1\nb = 2\n", b"a = 1\n", b"a = 1\nb = 2\n")
            .unwrap();
        assert_eq!(String::from_utf8(m.blob).unwrap(), "a = 1\n");
        assert!(m.conflicts.is_empty());
    }
}
