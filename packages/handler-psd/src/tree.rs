//! The diff, the way a Photoshop user reads a file: the document, then each
//! changed layer — inside its group, top of the stack first — then property.
//!
//! Paths are stable keys, not a tree to walk: `document/<property>`,
//! `groups/<id>` (a group with changes inside it), `layers/<id>` (a layer's
//! own changes, or the layer added or removed), `layers/<id>/<property>`. Ids
//! are Photoshop's layer ids.

use std::collections::{BTreeMap, BTreeSet, HashMap};

use fhr::{ChangeKind, DiffChange};

use crate::file::{Document, Kind, Layer};
use crate::values::{self, layer_label, summary};

/// Deeper than any group nesting Photoshop allows; a file nesting further is damaged.
const MAX_DEPTH: usize = 64;

/// The changes from `base` to `head`; `None` is a side where the file doesn't exist.
pub(crate) fn changes(base: Option<&Document>, head: Option<&Document>) -> Vec<DiffChange> {
    let mut out = Vec::new();
    if let (Some(b), Some(h)) = (base, head) {
        let leaves = values::document_leaves(b, h);
        if !leaves.is_empty() {
            out.push(
                DiffChange::new("document", ChangeKind::Modified, "Document")
                    .children(leaves.into_iter().map(|l| l.change("document")).collect()),
            );
        }
    }
    let empty = Document::empty();
    let (b, h) = (base.unwrap_or(&empty), head.unwrap_or(&empty));
    let bi: HashMap<u32, usize> = b
        .layers
        .iter()
        .enumerate()
        .map(|(i, l)| (l.id, i))
        .collect();
    let hi: HashMap<u32, usize> = h
        .layers
        .iter()
        .enumerate()
        .map(|(i, l)| (l.id, i))
        .collect();
    let moved = moved(h, &bi);

    // Each row, under the group it's in (on the side it's on), in stack order.
    // A group that is only on that side (a new group, say) has no row of its
    // own to hold it: the row goes to the nearest group on both sides.
    let shown_in = |d: &Document,
                    at: &HashMap<u32, usize>,
                    other: &HashMap<u32, usize>,
                    mut parent: Option<u32>| {
        for _ in 0..MAX_DEPTH {
            match parent {
                Some(p) if !other.contains_key(&p) => {
                    parent = at.get(&p).and_then(|i| d.layers[*i].parent)
                }
                _ => break,
            }
        }
        parent
    };
    let mut rows: BTreeMap<Option<u32>, Vec<(usize, DiffChange)>> = BTreeMap::new();
    let mut own: HashMap<u32, DiffChange> = HashMap::new();
    for (i, hl) in h.layers.iter().enumerate() {
        let path = format!("layers/{}", hl.id);
        match bi.get(&hl.id).map(|j| &b.layers[*j]) {
            Some(bl) => {
                let leaves = values::layer_leaves(b, bl, h, hl, moved.contains(&hl.id));
                if leaves.is_empty() {
                    continue;
                }
                let kind = if leaves.len() == 1 && leaves[0].label == "Name" {
                    ChangeKind::Renamed
                } else {
                    ChangeKind::Modified
                };
                let row = DiffChange::new(path.clone(), kind, layer_label(hl))
                    .children(leaves.into_iter().map(|l| l.change(&path)).collect());
                if hl.kind == Kind::Group {
                    own.insert(hl.id, row);
                } else {
                    rows.entry(shown_in(h, &hi, &bi, hl.parent))
                        .or_default()
                        .push((i, row));
                }
            }
            // Only the root of what was added: its layers come with it.
            None if hl.parent.is_some_and(|p| !bi.contains_key(&p)) => {}
            None => {
                let row =
                    DiffChange::new(path, ChangeKind::Added, layer_label(hl)).after(summary(hl, h));
                rows.entry(hl.parent).or_default().push((i, row));
            }
        }
    }
    for (j, bl) in b.layers.iter().enumerate() {
        if hi.contains_key(&bl.id) || bl.parent.is_some_and(|p| !hi.contains_key(&p)) {
            continue;
        }
        let row = DiffChange::new(
            format!("layers/{}", bl.id),
            ChangeKind::Removed,
            layer_label(bl),
        )
        .before(summary(bl, b));
        // Where it was: after the layers above it that are still there.
        let at = b.layers[..j]
            .iter()
            .rev()
            .find_map(|x| hi.get(&x.id).copied())
            .map_or(0, |k| k + 1);
        rows.entry(shown_in(b, &bi, &hi, bl.parent))
            .or_default()
            .push((at, row));
    }

    let groups: Vec<&Layer> = h
        .layers
        .iter()
        .filter(|l| l.kind == Kind::Group && bi.contains_key(&l.id))
        .collect();
    out.extend(nest(None, &groups, &hi, &mut rows, &mut own, 0));
    out
}

/// The rows in group `parent` (None: the top level), each group with changes
/// inside it as a row of its own around them.
fn nest(
    parent: Option<u32>,
    groups: &[&Layer],
    hi: &HashMap<u32, usize>,
    rows: &mut BTreeMap<Option<u32>, Vec<(usize, DiffChange)>>,
    own: &mut HashMap<u32, DiffChange>,
    depth: usize,
) -> Vec<DiffChange> {
    let mut items = rows.remove(&parent).unwrap_or_default();
    if depth < MAX_DEPTH {
        for g in groups.iter().filter(|g| g.parent == parent) {
            let inner = nest(Some(g.id), groups, hi, rows, own, depth + 1);
            let own_row = format!("layers/{}", g.id);
            let children: Vec<DiffChange> = own.remove(&g.id).into_iter().chain(inner).collect();
            let at = hi.get(&g.id).copied().unwrap_or(0);
            match children.as_slice() {
                [] => {}
                // Only the group's own properties changed: its row alone.
                [only] if only.path == own_row => {
                    items.extend(children.into_iter().map(|c| (at, c)))
                }
                _ => items.push((
                    at,
                    DiffChange::new(
                        format!("groups/{}", g.id),
                        ChangeKind::Modified,
                        layer_label(g),
                    )
                    .children(children),
                )),
            }
        }
    }
    items.sort_by_key(|(at, _)| *at);
    items.into_iter().map(|(_, c)| c).collect()
}

/// The layers whose place in the stack changed: those outside the longest run
/// that kept its order, so moving one layer up names that layer alone.
fn moved(h: &Document, bi: &HashMap<u32, usize>) -> BTreeSet<u32> {
    let both: Vec<(u32, usize)> = h
        .layers
        .iter()
        .filter_map(|l| bi.get(&l.id).map(|j| (l.id, *j)))
        .collect();
    // Longest increasing subsequence of base positions, in head order.
    let mut tails: Vec<usize> = Vec::new();
    let mut prev: Vec<Option<usize>> = vec![None; both.len()];
    let mut tail_at: Vec<usize> = Vec::new();
    for (i, (_, j)) in both.iter().enumerate() {
        let k = tails.partition_point(|t| *t < *j);
        if k > 0 {
            prev[i] = Some(tail_at[k - 1]);
        }
        if k == tails.len() {
            tails.push(*j);
            tail_at.push(i);
        } else {
            tails[k] = *j;
            tail_at[k] = i;
        }
    }
    let mut kept = BTreeSet::new();
    let mut at = tail_at.last().copied();
    while let Some(i) = at {
        kept.insert(both[i].0);
        at = prev[i];
    }
    both.iter()
        .map(|(id, _)| *id)
        .filter(|id| !kept.contains(id))
        .collect()
}
