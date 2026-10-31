//! Layer and comment numbers for a merge.
//!
//! Outside a live session FigCraft numbers a new layer one above the highest in
//! the file, and a new comment one above the highest comment. Two branches made
//! from the same commit therefore number their new layers alike: layer 144 is a
//! badge on one branch and a button on the other. FigCraft's merge assumes a
//! number is only ever made once (inside the app every branch gets its own
//! range, see `Document::set_client_id`), so before a merge the other side's new
//! layers and comments that clash get numbers no side uses.
//!
//! Every place a layer number is kept follows: the tree, instances and the
//! layers that mirror their component, component properties, prototype links
//! and flows, measurements, board connectors, pattern fills, comments, dev
//! snapshots, votes and library copies. The matches below name every field and
//! variant they leave alone, so a FigCraft update that adds one stops this from
//! compiling until it is looked at.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use figcraft_doc::color::{Paint, PaintKind};
use figcraft_doc::proto::{self, Action, Flow};
use figcraft_doc::{
    Comment, DevSnapshot, Document, ImportedComponent, Measurement, Node, NodeId, NodeKind,
    PropDef, VoteScope,
};

/// `theirs` with its new layers and comments renumbered wherever their numbers
/// are also in `ours` (or anywhere else in the three files). Nothing clashes:
/// `theirs` itself.
pub(crate) fn apart(
    base: &Document,
    ours: &Document,
    theirs: &Document,
) -> Result<Document, String> {
    let layers = fresh(
        new_in(theirs, base, ours, layer_ids),
        [base, ours, theirs]
            .iter()
            .flat_map(|d| layer_ids(d))
            .collect(),
    )?;
    let comments = fresh(
        new_in(theirs, base, ours, comment_ids),
        [base, ours, theirs]
            .iter()
            .flat_map(|d| comment_ids(d))
            .collect(),
    )?;
    if layers.is_empty() && comments.is_empty() {
        return Ok(theirs.clone());
    }
    renumber(theirs, &layers, &comments)
}

/// Every layer number in `doc`, including the layers its dev snapshots keep
/// (a layer deleted since its snapshot keeps its number there).
fn layer_ids(doc: &Document) -> BTreeSet<u64> {
    doc.nodes()
        .map(|n| n.id.0)
        .chain(
            doc.dev_snapshots
                .values()
                .flat_map(|s| s.nodes.iter().map(|n| n.id.0)),
        )
        .collect()
}

fn comment_ids(doc: &Document) -> BTreeSet<u64> {
    doc.comments.iter().map(|c| c.id).collect()
}

/// The numbers `theirs` made since `base` that `ours` uses too.
fn new_in(
    theirs: &Document,
    base: &Document,
    ours: &Document,
    ids: fn(&Document) -> BTreeSet<u64>,
) -> Vec<u64> {
    let (b, o) = (ids(base), ids(ours));
    ids(theirs)
        .into_iter()
        .filter(|id| !b.contains(id) && o.contains(id))
        .collect()
}

/// A new number for each of `clash`, above every `taken` number in its id
/// space (the high 32 bits: 0 outside a live session, else the client's), in
/// ascending order.
fn fresh(clash: Vec<u64>, mut taken: BTreeSet<u64>) -> Result<BTreeMap<u64, u64>, String> {
    let mut out = BTreeMap::new();
    for id in clash {
        let space = id >> 32;
        let top = taken
            .range(space << 32..=(space << 32 | u64::from(u32::MAX)))
            .next_back()
            .copied()
            .unwrap_or(space << 32);
        let next = top + 1;
        if next >> 32 != space {
            return Err(format!("no free layer or comment number left after {top}"));
        }
        taken.insert(next);
        out.insert(id, next);
    }
    Ok(out)
}

/// `doc` with its layers numbered by `layers` and its comments by `comments`.
fn renumber(
    doc: &Document,
    layers: &BTreeMap<u64, u64>,
    comments: &BTreeMap<u64, u64>,
) -> Result<Document, String> {
    let map: BTreeMap<NodeId, NodeId> = layers
        .iter()
        .map(|(a, b)| (NodeId(*a), NodeId(*b)))
        .collect();
    let id = |n: &mut NodeId| {
        if let Some(m) = map.get(n) {
            *n = *m;
        }
    };

    let mut out = doc.clone();
    out.pages.iter_mut().for_each(id);
    out.current_page.iter_mut().for_each(id);
    for c in &mut out.comments {
        let Comment {
            id: number,
            page,
            anchor,
            x: _,
            y: _,
            first: _,
            resolved: _,
            replies: _,
            read_by: _,
        } = c;
        if let Some(m) = comments.get(number) {
            *number = *m;
        }
        id(page);
        anchor.iter_mut().for_each(id);
    }
    out.dev_snapshots = std::mem::take(&mut out.dev_snapshots)
        .into_iter()
        .map(|(mut key, snap)| {
            id(&mut key);
            let nodes = snap.nodes.iter().map(|n| Arc::new(node(n, &map))).collect();
            (key, Arc::new(DevSnapshot { nodes }))
        })
        .collect();
    if let Some(v) = &mut out.vote {
        match &mut v.scope {
            VoteScope::Only { ids } => ids.iter_mut().for_each(id),
            VoteScope::All | VoteScope::Stickies => {}
        }
    }
    out.votes.values_mut().flatten().for_each(id);
    let libs = Arc::make_mut(&mut out.libraries);
    libs.store.iter_mut().for_each(id);
    for c in &mut libs.components {
        // `key` and the `ids` keys are the library file's own numbers.
        let ImportedComponent {
            library: _,
            key: _,
            root,
            version: _,
            ids,
        } = c;
        id(root);
        ids.values_mut().for_each(id);
    }

    // The layers themselves are private to the document: rebuild its layer
    // table through FigCraft's own JSON form of it.
    let mut json = serde_json::to_value(&out).map_err(|e| e.to_string())?;
    let mut table = serde_json::Map::new();
    for n in out.nodes() {
        let n = node(n, &map);
        let v = serde_json::to_value(&n).map_err(|e| e.to_string())?;
        table.insert(n.id.0.to_string(), v);
    }
    *json
        .get_mut("nodes")
        .ok_or("a FigCraft document without a layer table")? = serde_json::Value::Object(table);
    let mut renumbered: Document =
        serde_json::from_value(json).map_err(|e| format!("renumbering layers: {e}"))?;
    // Kept apart from the JSON form; neither holds this document's layer numbers.
    renumbered.images = out.images;
    renumbered.published = out.published;
    Ok(renumbered)
}

/// A copy of layer `n` with every layer number in it mapped by `map`.
fn node(n: &Node, map: &BTreeMap<NodeId, NodeId>) -> Node {
    let id = |n: &mut NodeId| {
        if let Some(m) = map.get(n) {
            *n = *m;
        }
    };
    let mut n = n.clone();
    let Node {
        id: own,
        parent,
        children,
        kind,
        interactions,
        source,
        measurements,
        board,
        fills,
        strokes,
        name: _,
        visible: _,
        locked: _,
        opacity: _,
        blend: _,
        transform: _,
        width: _,
        height: _,
        stroke: _,
        effects: _,
        corners: _,
        constraints: _,
        layout: _,
        child_layout: _,
        clip: _,
        is_mask: _,
        mask_type: _,
        grids: _,
        guides: _,
        exports: _,
        overflow: _,
        scroll_position: _,
        styles: _,
        bound: _,
        var_modes: _,
        bool_op: _,
        constrain_proportions: _,
        exposed: _,
        dev_status: _,
        dev_changed: _,
        dev_note: _,
        overrides: _,
        prop_refs: _,
        plugin_data: _,
        annotations: _,
        code_links: _,
        dev_resources: _,
    } = &mut n;
    id(own);
    parent.iter_mut().for_each(id);
    children.iter_mut().for_each(id);
    match kind {
        NodeKind::Instance {
            component,
            props: _,
        } => id(component),
        NodeKind::Component {
            props,
            description: _,
        } => {
            for def in props.values_mut() {
                match def {
                    PropDef::InstanceSwap { default, preferred } => {
                        id(default);
                        preferred.iter_mut().for_each(id);
                    }
                    PropDef::Slot { preferred } => preferred.iter_mut().for_each(id),
                    PropDef::Boolean { .. } | PropDef::Text { .. } | PropDef::Variant { .. } => {}
                }
            }
        }
        NodeKind::Page {
            flows,
            background: _,
            prototype: _,
            guides: _,
        } => {
            for Flow {
                start,
                name: _,
                description: _,
            } in flows
            {
                id(start);
            }
        }
        NodeKind::Frame
        | NodeKind::Group
        | NodeKind::Section { .. }
        | NodeKind::Rectangle
        | NodeKind::Ellipse { .. }
        | NodeKind::Polygon { .. }
        | NodeKind::Star { .. }
        | NodeKind::Line
        | NodeKind::Vector { .. }
        | NodeKind::Text(_)
        | NodeKind::BooleanOp { .. }
        | NodeKind::ComponentSet { .. }
        | NodeKind::Slice => {}
    }
    for it in interactions {
        proto::walk_mut(&mut it.actions, &mut |a| match a {
            Action::Navigate { dest, .. }
            | Action::ScrollTo { dest, .. }
            | Action::OpenOverlay { dest, .. }
            | Action::SwapOverlay { dest }
            | Action::ChangeTo { dest } => id(dest),
            // Conditional blocks are walked into by walk_mut itself.
            Action::Back
            | Action::CloseOverlay
            | Action::OpenUrl { .. }
            | Action::SetVariable { .. }
            | Action::SetVariableMode { .. }
            | Action::Conditional { .. } => {}
        });
    }
    source.iter_mut().for_each(id);
    for Measurement { to, side: _ } in measurements {
        to.iter_mut().for_each(id);
    }
    if let Some(b) = board {
        b.remap(map);
    }
    for p in fills.iter_mut().chain(strokes.iter_mut()) {
        let Paint {
            kind,
            opacity: _,
            visible: _,
            blend: _,
            color_alpha: _,
        } = p;
        match kind {
            // A pattern keeps the layer it tiles as a plain number.
            PaintKind::Pattern(pattern) => {
                if let Some(m) = map.get(&NodeId(pattern.source)) {
                    pattern.source = m.0;
                }
            }
            PaintKind::Solid { .. } | PaintKind::Gradient(_) | PaintKind::Image(_) => {}
        }
    }
    n
}
