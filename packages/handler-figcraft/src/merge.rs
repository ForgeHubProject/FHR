//! `merge`: two branches of a design, merged with FigCraft's own three-way merge
//! (`figcraft_collab::merge`, the one its in-app branches use).
//!
//! What each side changed since the common version is diffed property by
//! property, and both sides' changes are kept. Lists whose items stand on
//! their own (fills, strokes, effects…) merge item by item. A conflict is the
//! same property changed both ways, or a layer deleted on one side and edited
//! on the other. At a conflict the result keeps ours, and the conflict is
//! reported, so a host never takes a design with a silent pick.
//!
//! Before merging, the new layers and comments of theirs that clash with
//! numbers in ours are renumbered (renumber.rs). Two styles, variables or
//! variable collections added on both sides under the same id are still a
//! conflict.
//!
//! The result keeps ours' file name, page to open on and in-file history
//! (FigCraft's own versions and branches); git holds the history of both.

use fhr::{Merged, SemanticConflict};
use figcraft_collab::merge::{plan, Conflict, Key, Plan, Side};
use figcraft_collab::Change;
use figcraft_format::Loaded;
use serde_json::{json, Value};

use crate::renumber;
use crate::tree::entry_path;

/// Three-way merge of a `.figcraft` file. Theirs is merged into ours: ours is
/// the branch being merged into, so its layers keep their numbers.
pub(crate) fn merge(base: &[u8], ours: &[u8], theirs: &[u8]) -> Result<Merged, String> {
    let take = |blob: &[u8]| Merged {
        blob: blob.to_vec(),
        conflicts: Vec::new(),
    };
    if ours == theirs || base == theirs {
        return Ok(take(ours));
    }
    if base == ours {
        return Ok(take(theirs));
    }
    if base.is_empty() {
        return Err("both sides added the file: there is no common version to merge from".into());
    }
    if ours.is_empty() || theirs.is_empty() {
        return Err("the file was deleted on one side and changed on the other".into());
    }
    let b = load(base, "base")?;
    let o = load(ours, "ours")?;
    let t = load(theirs, "theirs")?;

    let t_doc = renumber::apart(&b.doc, &o.doc, &t.doc)?;
    let plan = plan(&b.doc, &t_doc, &o.doc).map_err(|e| e.to_string())?;
    let conflicts = plan.conflicts.iter().map(|c| conflict(&plan, c)).collect();
    let (doc, _) = plan
        .merge(&o.doc, Side::Branch, &|_| Some(Side::Main))
        .map_err(|e| format!("the merged design is not valid: {e}"))?;
    let issues = doc.validate();
    if !issues.is_empty() {
        return Err(format!(
            "the merged design is not valid: {}",
            issues.join("; ")
        ));
    }
    // No thumbnail: ours' would show the design before theirs' changes, and
    // FigCraft draws a new one the next time the file is saved.
    let blob = figcraft_format::save_bytes_with_history(&doc, None, &o.versions, &o.branches)
        .map_err(|e| e.to_string())?;
    Ok(Merged { blob, conflicts })
}

fn load(blob: &[u8], side: &str) -> Result<Loaded, String> {
    figcraft_format::load_bytes_with_versions(blob).map_err(|e| format!("{side}: {e}"))
}

/// A conflict as FHR reports it: where (the path of the diff row it is
/// about), and each side's value there. Ours is `plan`'s main side, theirs
/// its branch.
fn conflict(plan: &Plan, c: &Conflict) -> SemanticConflict {
    let side = |s: Side| -> Value {
        if c.deleted_by == Some(s) {
            return Value::Null;
        }
        let changes: Vec<&Change> = c
            .changes(s)
            .iter()
            .filter_map(|i| plan.changes(s).get(*i))
            .collect();
        match changes.as_slice() {
            [one] if !matches!(c.key, Key::Layer(_)) => value(one),
            many => Value::Array(many.iter().map(|ch| described(ch)).collect()),
        }
    };
    SemanticConflict {
        path: path(&c.key),
        ours: side(Side::Main),
        theirs: side(Side::Branch),
    }
}

/// The diff tree's path for what a conflict is about: `layers/12/fills`,
/// `layers/12` (deleted on one side, edited on the other), `document/…`.
fn path(key: &Key) -> String {
    match key {
        Key::Node(id, prop) => format!("layers/{id}/{}", camel(prop)),
        Key::Layer(id) => format!("layers/{id}"),
        Key::Doc(prop) => format!("document/{}", camel(prop)),
        Key::Entry(entry) => entry_path(entry),
    }
}

/// What one change writes.
fn value(c: &Change) -> Value {
    match c {
        Change::Set { value, .. } | Change::SetDoc { value, .. } | Change::Put { value, .. } => {
            value.0.clone()
        }
        Change::Create { .. } | Change::Delete { .. } | Change::Remove { .. } => described(c),
    }
}

/// One change, said whole: one of a side's edits to a layer the other side
/// deleted, say.
fn described(c: &Change) -> Value {
    match c {
        Change::Set { id, prop, value } => json!({ "layer": id.0, camel(prop): value.0 }),
        Change::Create { id, .. } => json!({ "added": id.0 }),
        Change::Delete { id } => json!({ "deleted": id.0 }),
        Change::SetDoc { prop, value } => json!({ camel(prop): value.0 }),
        Change::Put { entry, value } => json!({ "put": entry_path(entry), "value": value.0 }),
        Change::Remove { entry } => json!({ "removed": entry_path(entry) }),
    }
}

/// A property's name as FigCraft writes it (`fills`, `cornerRadius`, `styleOrder`).
fn camel<T: serde::Serialize>(v: &T) -> String {
    match serde_json::to_value(v) {
        Ok(Value::String(s)) => s,
        _ => String::new(),
    }
}
