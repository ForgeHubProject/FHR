//! Two branches of a design merged: what each side did is kept, layers both
//! sides numbered alike get apart, and a real clash is reported, never picked.

use std::collections::BTreeMap;

use fhr::{Handler, Merged};
use figcraft_doc::color::{Color, Paint};
use figcraft_doc::proto::{Action, Interaction};
use figcraft_doc::{Comment, Document, Node, NodeId, NodeKind, TextData};
use handler_figcraft::FigCraft;
use serde_json::{json, Value};

/// The common version: an "Onboarding" page with a "Welcome" screen holding a
/// title and a button.
struct Base {
    doc: Document,
    page: NodeId,
    screen: NodeId,
    title: NodeId,
    button: NodeId,
}

fn base() -> Base {
    let mut doc = Document::new("Ridgeline");
    let page = doc.pages[0];
    doc.get_mut(page).unwrap().name = "Onboarding".into();
    let screen = add(&mut doc, page, NodeKind::Frame, "Welcome", 390.0, 844.0);
    let title = add(
        &mut doc,
        screen,
        NodeKind::Text(TextData::new("Find your next summit")),
        "Title",
        300.0,
        40.0,
    );
    let button = add(
        &mut doc,
        screen,
        NodeKind::Rectangle,
        "Get started",
        300.0,
        48.0,
    );
    Base {
        doc,
        page,
        screen,
        title,
        button,
    }
}

fn add(doc: &mut Document, parent: NodeId, kind: NodeKind, name: &str, w: f64, h: f64) -> NodeId {
    let id = doc.alloc_id();
    doc.insert(Node::new(id, kind, name, w, h), parent, None)
        .unwrap()
}

fn text(doc: &Document, id: NodeId) -> String {
    match &doc.node(id).unwrap().kind {
        NodeKind::Text(t) => t.characters.clone(),
        other => panic!("not a text: {other:?}"),
    }
}

fn set_text(doc: &mut Document, id: NodeId, s: &str) {
    match &mut doc.get_mut(id).unwrap().kind {
        NodeKind::Text(t) => t.characters = s.into(),
        other => panic!("not a text: {other:?}"),
    }
}

fn fill(doc: &mut Document, id: NodeId, hex: &str) {
    doc.get_mut(id).unwrap().fills = vec![Paint::solid(Color::from_hex(hex).unwrap())];
}

fn comment(doc: &mut Document, page: NodeId, text: &str) -> u64 {
    let id = doc.comments.iter().map(|c| c.id).max().unwrap_or(0) + 1;
    let c: Comment = serde_json::from_value(json!({
        "id": id, "page": page.0, "x": 10.0, "y": 20.0, "author": "dana", "text": text,
    }))
    .unwrap();
    doc.comments.push(c);
    id
}

fn save(doc: &Document) -> Vec<u8> {
    figcraft_format::save_bytes(doc, None).unwrap()
}

fn merge(base: &Document, ours: &Document, theirs: &Document) -> (Merged, Document) {
    let merged = FigCraft
        .merge(&save(base), &save(ours), &save(theirs))
        .unwrap_or_else(|e| panic!("merge failed: {e}"));
    let (doc, issues) = figcraft_format::load_bytes(&merged.blob).unwrap();
    assert!(
        issues.is_empty(),
        "the merged file needed repairs: {issues:?}"
    );
    (merged, doc)
}

/// The layer named `name`, wherever it is.
fn named(doc: &Document, name: &str) -> Vec<NodeId> {
    doc.nodes()
        .filter(|n| n.name == name)
        .map(|n| n.id)
        .collect()
}

#[test]
fn declares_a_semantic_merge() {
    let caps = FigCraft.info().capabilities.unwrap();
    assert!(caps.semantic_merge);
    assert!(!caps.apply_choices, "apply-choices is not answered yet");
}

#[test]
fn keeps_both_sides_edits() {
    let b = base();
    let mut ours = b.doc.clone();
    set_text(&mut ours, b.title, "Find your next peak");
    let mut theirs = b.doc.clone();
    fill(&mut theirs, b.button, "#2EB67D");

    let (m, doc) = merge(&b.doc, &ours, &theirs);
    assert!(m.conflicts.is_empty(), "{:?}", m.conflicts);
    assert_eq!(text(&doc, b.title), "Find your next peak");
    assert_eq!(
        doc.node(b.button).unwrap().fills,
        theirs.node(b.button).unwrap().fills
    );
}

#[test]
fn layers_both_sides_numbered_alike_are_kept_apart() {
    let b = base();
    let mut ours = b.doc.clone();
    let badge = add(
        &mut ours,
        b.screen,
        NodeKind::Rectangle,
        "New badge",
        60.0,
        24.0,
    );
    let mut theirs = b.doc.clone();
    let skip = add(
        &mut theirs,
        b.screen,
        NodeKind::Rectangle,
        "Skip",
        80.0,
        32.0,
    );
    // FigCraft numbers both one above the highest in the common version.
    assert_eq!(badge, skip);

    let (m, doc) = merge(&b.doc, &ours, &theirs);
    assert!(m.conflicts.is_empty(), "{:?}", m.conflicts);
    // Ours keeps its numbers; theirs' layer gets one nobody used.
    assert_eq!(doc.node(badge).unwrap().name, "New badge");
    let skips = named(&doc, "Skip");
    assert_eq!(skips.len(), 1);
    assert!(skips[0].0 > badge.0);
    assert_eq!(doc.node(skips[0]).unwrap().parent, Some(b.screen));
    let children = &doc.node(b.screen).unwrap().children;
    assert!(children.contains(&badge) && children.contains(&skips[0]));
}

#[test]
fn what_points_at_a_renumbered_layer_follows_it() {
    let b = base();
    let mut ours = b.doc.clone();
    let sign_in = add(&mut ours, b.page, NodeKind::Frame, "Sign in", 390.0, 844.0);
    add(
        &mut ours,
        sign_in,
        NodeKind::Rectangle,
        "Email",
        300.0,
        48.0,
    );
    add(
        &mut ours,
        sign_in,
        NodeKind::Rectangle,
        "Password",
        300.0,
        48.0,
    );
    let mut theirs = b.doc.clone();
    // Theirs adds a screen, a component on it and an instance of that
    // component (numbered like ours' three new layers), and links the button
    // to the new screen.
    let screen = add(
        &mut theirs,
        b.page,
        NodeKind::Frame,
        "Interests",
        390.0,
        844.0,
    );
    let chip = add(
        &mut theirs,
        screen,
        NodeKind::Component {
            description: String::new(),
            props: BTreeMap::new(),
        },
        "Chip",
        80.0,
        32.0,
    );
    let instance = add(
        &mut theirs,
        screen,
        NodeKind::Instance {
            component: chip,
            props: BTreeMap::new(),
        },
        "Chip copy",
        80.0,
        32.0,
    );
    let link: Interaction =
        serde_json::from_value(json!({ "actions": [{ "type": "navigate", "dest": screen.0 }] }))
            .unwrap();
    theirs.get_mut(b.button).unwrap().interactions = vec![link];
    let note = comment(&mut theirs, b.page, "Is this the right order?");
    theirs.comments.last_mut().unwrap().anchor = Some(chip);

    let (m, doc) = merge(&b.doc, &ours, &theirs);
    assert!(m.conflicts.is_empty(), "{:?}", m.conflicts);
    assert_eq!(
        doc.node(screen).unwrap().name,
        "Sign in",
        "ours keeps its number"
    );
    assert_eq!(doc.node(instance).unwrap().name, "Password");
    let [screen2] = named(&doc, "Interests")[..] else {
        panic!("one Interests screen")
    };
    let [chip2] = named(&doc, "Chip")[..] else {
        panic!("one Chip")
    };
    let [instance2] = named(&doc, "Chip copy")[..] else {
        panic!("one Chip copy")
    };
    assert!([screen2, chip2, instance2]
        .iter()
        .all(|id| id.0 > instance.0));
    assert_eq!(doc.node(chip2).unwrap().parent, Some(screen2));
    match &doc.node(instance2).unwrap().kind {
        NodeKind::Instance { component, .. } => assert_eq!(*component, chip2),
        other => panic!("not an instance: {other:?}"),
    }
    match doc.node(b.button).unwrap().interactions[0].actions[0] {
        Action::Navigate { dest, .. } => assert_eq!(dest, screen2),
        ref other => panic!("not a navigation: {other:?}"),
    }
    let c = doc.comments.iter().find(|c| c.id == note).unwrap();
    assert_eq!(c.anchor, Some(chip2));
}

#[test]
fn comments_from_both_sides_are_kept() {
    let b = base();
    let mut ours = b.doc.clone();
    let a = comment(&mut ours, b.page, "Title feels long");
    let mut theirs = b.doc.clone();
    let t = comment(&mut theirs, b.page, "Button needs more contrast");
    assert_eq!(a, t);

    let (m, doc) = merge(&b.doc, &ours, &theirs);
    assert!(m.conflicts.is_empty(), "{:?}", m.conflicts);
    let texts: Vec<&str> = doc.comments.iter().map(|c| c.first.text.as_str()).collect();
    assert_eq!(texts, ["Title feels long", "Button needs more contrast"]);
    assert_ne!(doc.comments[0].id, doc.comments[1].id);
}

#[test]
fn the_same_property_changed_both_ways_is_a_conflict_holding_ours() {
    let b = base();
    let mut ours = b.doc.clone();
    set_text(&mut ours, b.title, "Find your next peak");
    fill(&mut ours, b.button, "#0E7C66");
    let mut theirs = b.doc.clone();
    set_text(&mut theirs, b.title, "Your next adventure");
    theirs.get_mut(b.title).unwrap().opacity = 0.5;

    let (m, doc) = merge(&b.doc, &ours, &theirs);
    assert_eq!(m.conflicts.len(), 1, "{:?}", m.conflicts);
    let c = &m.conflicts[0];
    assert_eq!(c.path, format!("layers/{}/kind", b.title));
    assert_eq!(c.ours["characters"], json!("Find your next peak"));
    assert_eq!(c.theirs["characters"], json!("Your next adventure"));
    // Ours at the conflict, everything else from both.
    assert_eq!(text(&doc, b.title), "Find your next peak");
    assert_eq!(doc.node(b.title).unwrap().opacity, 0.5);
    assert_eq!(
        doc.node(b.button).unwrap().fills,
        ours.node(b.button).unwrap().fills
    );
}

#[test]
fn a_layer_deleted_on_one_side_and_edited_on_the_other_is_a_conflict() {
    let b = base();
    let mut ours = b.doc.clone();
    ours.remove(b.button).unwrap();
    let mut theirs = b.doc.clone();
    fill(&mut theirs, b.button, "#2EB67D");

    let (m, doc) = merge(&b.doc, &ours, &theirs);
    assert_eq!(m.conflicts.len(), 1, "{:?}", m.conflicts);
    let c = &m.conflicts[0];
    assert_eq!(c.path, format!("layers/{}", b.button));
    assert_eq!(c.ours, Value::Null, "ours deleted it");
    assert_eq!(c.theirs[0]["layer"], json!(b.button.0));
    assert!(c.theirs[0].get("fills").is_some(), "{}", c.theirs);
    assert!(doc.node(b.button).is_none(), "ours at the conflict");
}

#[test]
fn trivial_merges_take_a_side_as_is() {
    let b = base();
    let mut changed = b.doc.clone();
    set_text(&mut changed, b.title, "Find your next peak");
    let (base_bytes, changed_bytes) = (save(&b.doc), save(&changed));
    let theirs = FigCraft
        .merge(&base_bytes, &base_bytes, &changed_bytes)
        .unwrap();
    assert_eq!(theirs.blob, changed_bytes);
    let ours = FigCraft
        .merge(&base_bytes, &changed_bytes, &base_bytes)
        .unwrap();
    assert_eq!(ours.blob, changed_bytes);
}

#[test]
fn a_file_both_sides_added_has_no_common_version_to_merge_from() {
    let b = base();
    let mut other = b.doc.clone();
    set_text(&mut other, b.title, "Hi");
    let err = FigCraft
        .merge(&[], &save(&b.doc), &save(&other))
        .unwrap_err();
    assert!(err.contains("no common version"), "{err}");
}

#[test]
fn not_a_figcraft_file_is_an_error() {
    let b = base();
    let mut theirs = b.doc.clone();
    set_text(&mut theirs, b.title, "Hi");
    let err = FigCraft
        .merge(&save(&b.doc), b"nope", &save(&theirs))
        .unwrap_err();
    assert!(err.starts_with("ours:"), "{err}");
}
