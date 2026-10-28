//! The diff, grouped the way a designer looks at a file: page, then screen (a
//! top-level frame), then layer, then property.
//!
//! FigCraft's collaboration core says what changed: one `Change` per changed
//! property, one per created or deleted layer, one per document entry (styles,
//! variables, comments). The same diff run the other way gives each property's
//! value before. This module only arranges and words them.
//!
//! Paths are stable keys, not a tree to walk: `pages/<id>`, `screens/<id>`,
//! `layers/<id>/<property>`, `layout/<id>/<property>`, `document/...`,
//! `history/...` (ids are FigCraft's persistent layer ids).

use std::collections::{BTreeMap, HashMap, HashSet};

use fhr::{ChangeKind, DiffChange};
use figcraft_collab::{diff, Change, DocProp, Entry, Prop, Replica};
use figcraft_doc::{Document, NodeId};
use figcraft_format::Loaded;
use serde_json::Value;

use crate::values::{self, Leaf};

/// One screen's rows: the screen layer's own changes, its layers' changes and
/// added or removed layers (by document order), and what layout moved.
#[derive(Default)]
struct Screen {
    own: Option<DiffChange>,
    items: Vec<(usize, DiffChange)>,
    layout: Vec<(usize, DiffChange)>,
}

/// One page's rows: its own properties, rows placed straight on it, and its
/// screens.
#[derive(Default)]
struct Page {
    own: Vec<Leaf>,
    items: Vec<(usize, DiffChange)>,
    screens: BTreeMap<NodeId, Screen>,
}

/// Where a row goes.
enum Place {
    Top,
    Page(NodeId),
    Screen(NodeId, NodeId),
}

/// The changes from `base` to `head`, grouped.
pub(crate) fn changes(base: &Loaded, head: &Loaded) -> Result<Vec<DiffChange>, String> {
    let (b, h) = (&base.doc, &head.doc);
    let fwd = diff(&Replica::new(b.clone()), h)
        .map_err(|e| e.to_string())?
        .changes;
    let rev = diff(&Replica::new(h.clone()), b)
        .map_err(|e| e.to_string())?
        .changes;

    // What the forward diff overwrote, from the diff run the other way.
    let mut was: HashMap<(NodeId, Prop), Value> = HashMap::new();
    let mut was_doc: HashMap<DocProp, Value> = HashMap::new();
    let mut was_entry: HashMap<Entry, Value> = HashMap::new();
    for c in rev {
        match c {
            Change::Set { id, prop, value } => {
                was.insert((id, prop), value.0);
            }
            Change::SetDoc { prop, value } => {
                was_doc.insert(prop, value.0);
            }
            Change::Put { entry, value } => {
                was_entry.insert(entry, value.0);
            }
            _ => {}
        }
    }

    let created: HashSet<NodeId> = fwd
        .iter()
        .filter_map(|c| match c {
            Change::Create { id, .. } => Some(*id),
            _ => None,
        })
        .collect();
    let deleted: HashSet<NodeId> = fwd
        .iter()
        .filter_map(|c| match c {
            Change::Delete { id } => Some(*id),
            _ => None,
        })
        .collect();

    let order = Order::new(b, h);
    let mut tree = Tree {
        b,
        h,
        created: &created,
        deleted: &deleted,
        top: Vec::new(),
        pages: BTreeMap::new(),
    };

    let mut sets: BTreeMap<NodeId, Vec<(Prop, Value)>> = BTreeMap::new();
    let mut document: Vec<DiffChange> = Vec::new();
    let mut doc_leaves: Vec<Leaf> = Vec::new();
    for c in fwd {
        match c {
            Change::Set { id, prop, value } => sets.entry(id).or_default().push((prop, value.0)),
            Change::Create { id, parent, .. } => {
                // Only the root of what was added: its layers come with it.
                if parent.is_none_or(|p| !created.contains(&p)) {
                    let row = DiffChange::new(
                        format!("layers/{id}"),
                        ChangeKind::Added,
                        values::node_label(h, id),
                    )
                    .after(values::summary(h, id));
                    tree.place_row(h, id, order.head(id), row);
                }
            }
            Change::Delete { id } => {
                let row = DiffChange::new(
                    format!("layers/{id}"),
                    ChangeKind::Removed,
                    values::node_label(b, id),
                )
                .before(values::summary(b, id));
                tree.place_row(b, id, order.base(id), row);
            }
            Change::SetDoc { prop, value } => {
                doc_leaves.extend(doc_prop_leaves(prop, was_doc.get(&prop), &value.0));
            }
            Change::Put { entry, value } => {
                document.extend(entry_row(&entry, was_entry.get(&entry), Some(&value.0)));
            }
            Change::Remove { entry } => {
                document.extend(entry_row(&entry, was_entry.get(&entry), None));
            }
        }
    }

    for (id, props) in sets {
        let (Some(bn), Some(hn)) = (b.node(id), h.node(id)) else {
            continue;
        };
        // A layer that changed parent changed order too; the parent says it.
        let reparented = props.iter().any(|(p, _)| *p == Prop::Parent);
        let mut leaves = Vec::new();
        for (prop, after) in &props {
            if *prop == Prop::Position && reparented && !hn.is_page() {
                continue;
            }
            leaves.extend(values::node_leaves(
                *prop,
                (b, bn),
                (h, hn),
                was.get(&(id, *prop)),
                after,
            ));
        }
        if hn.is_page() {
            tree.pages.entry(id).or_default().own.extend(leaves);
            continue;
        }
        let (by_layout, edits): (Vec<Leaf>, Vec<Leaf>) =
            leaves.into_iter().partition(|l| l.by_layout);
        let label = values::node_label(h, id);
        if !edits.is_empty() {
            let path = format!("layers/{id}");
            let row = DiffChange::new(path.clone(), kind_of(&edits), label.clone())
                .children(edits.into_iter().map(|l| l.change(&path)).collect());
            match tree.place(h, id) {
                Place::Screen(page, screen) if screen == id => {
                    tree.pages
                        .entry(page)
                        .or_default()
                        .screens
                        .entry(screen)
                        .or_default()
                        .own = Some(row);
                }
                place => tree.push(place, order.head(id), row),
            }
        }
        if !by_layout.is_empty() {
            let path = format!("layout/{id}");
            let row = DiffChange::new(path.clone(), ChangeKind::Modified, label)
                .children(by_layout.into_iter().map(|l| l.change(&path)).collect());
            match tree.place(h, id) {
                Place::Screen(page, screen) => tree
                    .pages
                    .entry(page)
                    .or_default()
                    .screens
                    .entry(screen)
                    .or_default()
                    .layout
                    .push((order.head(id), row)),
                place => tree.push(place, order.head(id), row),
            }
        }
    }

    let mut out = tree.into_rows(&order);
    out.extend(document_row(doc_leaves, document));
    out.extend(history_row(base, head));
    Ok(out)
}

/// The screen layer `id` is on: the outermost frame holding it (past any
/// sections), else the page's own layer that holds it. A page is on no screen.
/// The preview draws the same screens the diff groups by.
pub(crate) fn screen_of(doc: &Document, id: NodeId) -> Option<NodeId> {
    let page = doc.page_of(id)?;
    if page == id {
        return None;
    }
    doc.top_level_frame(id).or_else(|| {
        std::iter::once(id)
            .chain(doc.ancestors(id))
            .find(|c| doc.node(*c).and_then(|n| n.parent) == Some(page))
    })
}

/// Rows being placed.
struct Tree<'a> {
    b: &'a Document,
    h: &'a Document,
    created: &'a HashSet<NodeId>,
    deleted: &'a HashSet<NodeId>,
    top: Vec<(usize, DiffChange)>,
    pages: BTreeMap<NodeId, Page>,
}

impl Tree<'_> {
    /// Where layer `id` of `doc` goes: on the page, or in the screen it is on
    /// (the outermost frame holding it, or the page's layer holding it).
    /// Pages, and anything in a page or screen that is itself added or
    /// removed, go one level up: that row already holds them.
    fn place(&self, doc: &Document, id: NodeId) -> Place {
        let Some(page) = doc.page_of(id) else {
            return Place::Top;
        };
        if page == id || self.created.contains(&page) || self.deleted.contains(&page) {
            return Place::Top;
        }
        match screen_of(doc, id) {
            Some(s) if !self.created.contains(&s) && !self.deleted.contains(&s) && s != id => {
                Place::Screen(page, s)
            }
            Some(s) if s == id => Place::Screen(page, s),
            _ => Place::Page(page),
        }
    }

    /// An Added or Removed row for layer `id` of `doc`.
    fn place_row(&mut self, doc: &Document, id: NodeId, at: usize, row: DiffChange) {
        let place = match self.place(doc, id) {
            // A whole screen added or removed is a row on its page.
            Place::Screen(page, screen) if screen == id => Place::Page(page),
            place => place,
        };
        let row = match place {
            Place::Top if doc.node(id).is_some_and(|n| n.is_page()) => {
                let mut row = row;
                row.path = format!("pages/{id}");
                row
            }
            _ => row,
        };
        self.push(place, at, row);
    }

    fn push(&mut self, place: Place, at: usize, row: DiffChange) {
        match place {
            Place::Top => self.top.push((at, row)),
            Place::Page(page) => self.pages.entry(page).or_default().items.push((at, row)),
            Place::Screen(page, screen) => self
                .pages
                .entry(page)
                .or_default()
                .screens
                .entry(screen)
                .or_default()
                .items
                .push((at, row)),
        }
    }

    fn into_rows(self, order: &Order) -> Vec<DiffChange> {
        let mut out = self.top;
        for (pid, page) in self.pages {
            let doc = if self.h.node(pid).is_some() {
                self.h
            } else {
                self.b
            };
            let mut kids = page.items;
            for (sid, screen) in page.screens {
                let doc = if self.h.node(sid).is_some() {
                    self.h
                } else {
                    self.b
                };
                kids.push((order.head(sid), screen_row(doc, sid, screen)));
            }
            kids.sort_by_key(|(at, _)| *at);
            let path = format!("pages/{pid}");
            let label = values::node_label(doc, pid);
            let own_kind = kind_of(&page.own);
            let own: Vec<DiffChange> = page.own.into_iter().map(|l| l.change(&path)).collect();
            let row = if kids.is_empty() {
                // Only the page itself changed.
                DiffChange::new(path, own_kind, label).children(own)
            } else {
                let mut children = Vec::new();
                if !own.is_empty() {
                    let props = format!("{path}/page");
                    let own = own
                        .into_iter()
                        .map(|mut c| {
                            c.path = c.path.replacen(&path, &props, 1);
                            c
                        })
                        .collect();
                    children
                        .push(DiffChange::new(props, own_kind, "Page properties").children(own));
                }
                children.extend(kids.into_iter().map(|(_, c)| c));
                DiffChange::new(path, ChangeKind::Modified, label).children(children)
            };
            out.push((order.head(pid), row));
        }
        out.sort_by_key(|(at, _)| *at);
        out.into_iter().map(|(_, c)| c).collect()
    }
}

/// A screen's row: the screen layer's own changes first, then its layers',
/// then what layout moved. A screen where only the screen layer changed is
/// just that layer's row.
fn screen_row(doc: &Document, sid: NodeId, mut s: Screen) -> DiffChange {
    if s.items.is_empty() && s.layout.is_empty() {
        if let Some(own) = s.own {
            return own;
        }
    }
    s.items.sort_by_key(|(at, _)| *at);
    s.layout.sort_by_key(|(at, _)| *at);
    let mut children: Vec<DiffChange> = s.own.into_iter().collect();
    children.extend(s.items.into_iter().map(|(_, c)| c));
    if !s.layout.is_empty() {
        children.push(
            DiffChange::new(
                format!("screens/{sid}/layout"),
                ChangeKind::Modified,
                "Moved or resized by layout",
            )
            .children(s.layout.into_iter().map(|(_, c)| c).collect()),
        );
    }
    let label = match doc.node(sid) {
        Some(n) if n.kind.is_frame_like() => format!("Screen \"{}\"", n.name),
        _ => values::node_label(doc, sid),
    };
    DiffChange::new(format!("screens/{sid}"), ChangeKind::Modified, label).children(children)
}

/// A layer's row kind: renamed or moved when that is all that changed.
fn kind_of(leaves: &[Leaf]) -> ChangeKind {
    if !leaves.is_empty() && leaves.iter().all(|l| l.label == "Name") {
        ChangeKind::Renamed
    } else if leaves.iter().any(|l| l.label == "Parent")
        && leaves.iter().all(|l| l.label == "Parent")
    {
        ChangeKind::Reparented
    } else {
        ChangeKind::Modified
    }
}

/// Document order: every layer's place in a depth-first walk of the pages,
/// head's first, then base's for what head no longer has.
struct Order {
    head: HashMap<NodeId, usize>,
    base: HashMap<NodeId, usize>,
}

impl Order {
    fn new(b: &Document, h: &Document) -> Order {
        let walk = |doc: &Document, from: usize| {
            let mut at = HashMap::new();
            for p in &doc.pages {
                for id in doc.subtree(*p) {
                    let i = from + at.len();
                    at.insert(id, i);
                }
            }
            at
        };
        let head = walk(h, 0);
        let base = walk(b, head.len());
        Order { head, base }
    }

    fn head(&self, id: NodeId) -> usize {
        self.head
            .get(&id)
            .or_else(|| self.base.get(&id))
            .copied()
            .unwrap_or(usize::MAX)
    }

    fn base(&self, id: NodeId) -> usize {
        self.base.get(&id).copied().unwrap_or(usize::MAX)
    }
}

/// Rows for a document-level property. Which page the file opens on and the
/// order of the styles and comment lists are not changes anyone reviews.
fn doc_prop_leaves(prop: DocProp, before: Option<&Value>, after: &Value) -> Vec<Leaf> {
    match prop {
        DocProp::CurrentPage | DocProp::StyleOrder | DocProp::CommentOrder => Vec::new(),
        DocProp::Name => values::generic("File name", before, Some(after)),
        other => values::generic(
            &values::humanize(&format!("{other:?}")),
            before,
            Some(after),
        ),
    }
}

/// A style, variable, collection, comment or image added, changed or removed.
fn entry_row(entry: &Entry, before: Option<&Value>, after: Option<&Value>) -> Option<DiffChange> {
    let either = after.or(before);
    let name = either
        .and_then(|v| v.get("name"))
        .and_then(Value::as_str)
        .map(|n| format!("\"{n}\""));
    let (path, label) = match entry {
        Entry::Style(id) => (
            format!("document/styles/{id}"),
            format!("Style {}", name.unwrap_or_default()),
        ),
        Entry::Collection(id) => (
            format!("document/collections/{id}"),
            format!("Variable collection {}", name.unwrap_or_default()),
        ),
        Entry::Variable(id) => (
            format!("document/variables/{id}"),
            format!("Variable {}", name.unwrap_or_default()),
        ),
        Entry::Comment(n) => (format!("document/comments/{n}"), format!("Comment #{n}")),
        Entry::Image(hash) => (
            format!("document/images/{hash}"),
            format!("Image {}", values::clip(hash, 8).trim_end_matches('…')),
        ),
        // What a ready-for-dev layer looked like, and a board's vote: kept
        // for FigCraft's own views, not design changes.
        Entry::DevSnapshot(_) | Entry::Ballot(_) => return None,
    };
    let label = label.trim_end().to_string();
    Some(match (before, after) {
        (None, Some(v)) => {
            DiffChange::new(path, ChangeKind::Added, label).after(entry_summary(entry, v))
        }
        (Some(v), None) => {
            DiffChange::new(path, ChangeKind::Removed, label).before(entry_summary(entry, v))
        }
        (b, a) => {
            let leaves = match entry {
                Entry::Image(_) => Vec::new(),
                Entry::Style(_) => style_leaves(b, a),
                _ => entry_leaves(b, a),
            };
            DiffChange::new(path.clone(), ChangeKind::Modified, label)
                .children(leaves.into_iter().map(|l| l.change(&path)).collect())
        }
    })
}

/// What an added or removed entry is.
fn entry_summary(entry: &Entry, v: &Value) -> String {
    let s = |k: &str| {
        v.get(k)
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string()
    };
    match entry {
        Entry::Comment(_) => format!("{}: {}", s("author"), values::clip(&s("text"), 200)),
        Entry::Image(_) => {
            let n = |k: &str| v.get(k).and_then(Value::as_u64).unwrap_or(0);
            format!("{} × {}", n("width"), n("height"))
        }
        Entry::Style(_) => format!("{} style", values::humanize(&s("type"))),
        Entry::Variable(_) => format!(
            "{} variable",
            values::humanize(&s("resolvedType")).to_lowercase()
        ),
        _ => String::new(),
    }
}

fn style_leaves(b: Option<&Value>, a: Option<&Value>) -> Vec<Leaf> {
    let (Some(Value::Object(bo)), Some(Value::Object(ao))) = (b, a) else {
        return values::generic("Style", b, a);
    };
    let mut out = Vec::new();
    for key in values::keys(bo, ao) {
        let (bv, av) = (bo.get(key), ao.get(key));
        if bv == av || key == "id" {
            continue;
        }
        match key {
            "paints" => out.extend(values::paints_json_leaves("Paint", bv, av)),
            "style" => out.extend(values::generic("Text", bv, av)),
            other => out.extend(values::generic(&values::humanize(other), bv, av)),
        }
    }
    out
}

fn entry_leaves(b: Option<&Value>, a: Option<&Value>) -> Vec<Leaf> {
    let (Some(Value::Object(bo)), Some(Value::Object(ao))) = (b, a) else {
        return values::generic("Value", b, a);
    };
    let mut out = Vec::new();
    for key in values::keys(bo, ao) {
        let (bv, av) = (bo.get(key), ao.get(key));
        if bv != av && key != "id" {
            out.extend(values::generic(&values::humanize(key), bv, av));
        }
    }
    out
}

/// The document group: file-level properties, then entries.
fn document_row(leaves: Vec<Leaf>, entries: Vec<DiffChange>) -> Option<DiffChange> {
    if leaves.is_empty() && entries.is_empty() {
        return None;
    }
    let mut children: Vec<DiffChange> = leaves.into_iter().map(|l| l.change("document")).collect();
    children.extend(entries);
    Some(DiffChange::new("document", ChangeKind::Modified, "Document").children(children))
}

/// Versions saved and branches made inside the file (FigCraft keeps its own
/// version history in the file). Autosaved checkpoints are left out.
fn history_row(base: &Loaded, head: &Loaded) -> Option<DiffChange> {
    let mut children = Vec::new();
    let had: HashSet<&str> = base.versions.iter().map(|v| v.id.as_str()).collect();
    let has: HashSet<&str> = head.versions.iter().map(|v| v.id.as_str()).collect();
    for v in head
        .versions
        .iter()
        .filter(|v| !v.auto && !had.contains(v.id.as_str()))
    {
        children.push(
            DiffChange::new(
                format!("history/versions/{}", v.id),
                ChangeKind::Added,
                format!("Version \"{}\"", v.name),
            )
            .after(v.description.clone()),
        );
    }
    for v in base
        .versions
        .iter()
        .filter(|v| !v.auto && !has.contains(v.id.as_str()))
    {
        children.push(
            DiffChange::new(
                format!("history/versions/{}", v.id),
                ChangeKind::Removed,
                format!("Version \"{}\"", v.name),
            )
            .before(v.description.clone()),
        );
    }
    let had: HashSet<&str> = base.branches.iter().map(|v| v.id.as_str()).collect();
    let has: HashSet<&str> = head.branches.iter().map(|v| v.id.as_str()).collect();
    for br in head
        .branches
        .iter()
        .filter(|br| !had.contains(br.id.as_str()))
    {
        children.push(DiffChange::new(
            format!("history/branches/{}", br.id),
            ChangeKind::Added,
            format!("Branch \"{}\"", br.name),
        ));
    }
    for br in base
        .branches
        .iter()
        .filter(|br| !has.contains(br.id.as_str()))
    {
        children.push(DiffChange::new(
            format!("history/branches/{}", br.id),
            ChangeKind::Removed,
            format!("Branch \"{}\"", br.name),
        ));
    }
    if children.is_empty() {
        return None;
    }
    Some(DiffChange::new("history", ChangeKind::Modified, "Version history").children(children))
}
