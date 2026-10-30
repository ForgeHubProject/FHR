//! What a reviewer sees for each kind of edit, on files built in code and
//! saved the way FigCraft saves them.

use std::collections::BTreeMap;

use fhr::{ChangeKind, DiffChange, Handler, StructuredDiff};
use figcraft_doc::color::{Color, Paint};
use figcraft_doc::{AutoLayout, Document, Node, NodeId, NodeKind, Style, StyleKind, TextData};
use handler_figcraft::FigCraft;
use serde_json::{json, Value};

/// A file with one page, "Onboarding", holding a "Sign up" screen with a
/// title and a button.
struct Fixture {
    doc: Document,
    page: NodeId,
    screen: NodeId,
    title: NodeId,
    button: NodeId,
}

fn fixture() -> Fixture {
    let mut doc = Document::new("Ridgeline");
    let page = doc.pages[0];
    doc.get_mut(page).unwrap().name = "Onboarding".into();
    let screen = add(&mut doc, page, NodeKind::Frame, "Sign up", 360.0, 640.0);
    let title = add(
        &mut doc,
        screen,
        NodeKind::Text(TextData::new("Create account")),
        "Title",
        200.0,
        32.0,
    );
    let button = add(&mut doc, screen, NodeKind::Rectangle, "Button", 120.0, 40.0);
    Fixture {
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

fn set_text(doc: &mut Document, id: NodeId, text: &str) {
    match &mut doc.get_mut(id).unwrap().kind {
        NodeKind::Text(t) => t.characters = text.into(),
        other => panic!("not a text: {other:?}"),
    }
}

fn save(doc: &Document) -> Vec<u8> {
    figcraft_format::save_bytes(doc, None).unwrap()
}

fn diff(base: &Document, head: &Document) -> StructuredDiff {
    FigCraft.diff(&save(base), &save(head)).unwrap()
}

fn find<'a>(changes: &'a [DiffChange], path: &str) -> Option<&'a DiffChange> {
    changes.iter().find_map(|c| {
        if c.path == path {
            Some(c)
        } else {
            find(&c.children, path)
        }
    })
}

/// The change at `path`, anywhere in the tree.
fn at<'a>(d: &'a StructuredDiff, path: &str) -> &'a DiffChange {
    find(&d.changes, path).unwrap_or_else(|| {
        panic!(
            "no change at {path} in {}",
            serde_json::to_string_pretty(d).unwrap()
        )
    })
}

fn paths(changes: &[DiffChange]) -> Vec<&str> {
    changes.iter().map(|c| c.path.as_str()).collect()
}

#[test]
fn info_declares_figcraft_files_a_semantic_compare_and_merge() {
    let info = serde_json::to_value(FigCraft.info()).unwrap();
    assert_eq!(info["id"], "figcraft");
    assert_eq!(info["formats"], json!([".figcraft"]));
    assert_eq!(info["capabilities"]["semanticCompare"], true);
    assert_eq!(info["capabilities"]["semanticMerge"], true);
}

#[test]
fn the_same_design_saved_twice_has_no_changes() {
    let f = fixture();
    let d = diff(&f.doc, &f.doc.clone());
    assert_eq!(d.format, "figcraft");
    assert!(d.changes.is_empty(), "{:?}", d.changes);
}

#[test]
fn a_text_edit_reads_page_then_screen_then_layer_then_property() {
    let f = fixture();
    let mut head = f.doc.clone();
    set_text(&mut head, f.title, "Create your account");
    let d = diff(&f.doc, &head);

    assert_eq!(paths(&d.changes), [format!("pages/{}", f.page)]);
    let page = &d.changes[0];
    assert_eq!(page.label, "Page \"Onboarding\"");
    assert_eq!(paths(&page.children), [format!("screens/{}", f.screen)]);
    let screen = &page.children[0];
    assert_eq!(screen.label, "Screen \"Sign up\"");
    assert_eq!(paths(&screen.children), [format!("layers/{}", f.title)]);
    let layer = &screen.children[0];
    assert_eq!(
        (layer.kind, layer.label.as_str()),
        (ChangeKind::Modified, "Text \"Title\"")
    );

    let text = at(&d, &format!("layers/{}/text", f.title));
    assert_eq!(text.label, "Text");
    assert_eq!(text.before, Some(json!("Create account")));
    assert_eq!(text.after, Some(json!("Create your account")));
}

#[test]
fn a_solid_fill_is_a_colour_the_renderer_draws_as_a_chip() {
    let f = fixture();
    let mut head = f.doc.clone();
    head.get_mut(f.button).unwrap().fills = vec![Paint::solid(Color::from_hex("#FF8A3D").unwrap())];
    let base_fill = f
        .doc
        .node(f.button)
        .unwrap()
        .fills
        .first()
        .and_then(Paint::solid_color);
    let d = diff(&f.doc, &head);

    let fill = at(&d, &format!("layers/{}/fill-colour", f.button));
    assert_eq!(fill.label, "Fill colour");
    // [r, g, b, a] in 0..1, under a label naming a colour: renderer-sdk's
    // formatter turns exactly that into a swatch.
    assert_eq!(fill.after, Some(json!([1.0, 0.5412, 0.2392, 1.0])));
    assert!(base_fill.is_some(), "a new rectangle has a solid fill");
    assert!(fill
        .before
        .as_ref()
        .and_then(Value::as_array)
        .is_some_and(|a| a.len() == 4));
}

#[test]
fn a_rename_alone_is_a_renamed_layer() {
    let f = fixture();
    let mut head = f.doc.clone();
    head.get_mut(f.button).unwrap().name = "Primary button".into();
    let d = diff(&f.doc, &head);

    let layer = at(&d, &format!("layers/{}", f.button));
    assert_eq!(layer.kind, ChangeKind::Renamed);
    assert_eq!(layer.label, "Rectangle \"Primary button\"");
    let name = at(&d, &format!("layers/{}/name", f.button));
    assert_eq!(
        (name.before.clone(), name.after.clone()),
        (Some(json!("Button")), Some(json!("Primary button")))
    );
}

#[test]
fn a_move_is_a_position_delta_and_a_turn_an_angle() {
    let f = fixture();
    let mut head = f.doc.clone();
    let b = head.get_mut(f.button).unwrap();
    b.transform = figcraft_doc::geom::from_position_rotation(24.0, 560.5, 15.0);
    let d = diff(&f.doc, &head);

    let pos = at(&d, &format!("layers/{}/position", f.button));
    assert_eq!(pos.before, Some(json!([0, 0])));
    assert_eq!(pos.after, Some(json!([24, 560.5])));
    let rot = at(&d, &format!("layers/{}/rotation", f.button));
    assert_eq!(
        (rot.before.clone(), rot.after.clone()),
        (Some(json!("0°")), Some(json!("15°")))
    );
}

#[test]
fn what_auto_layout_moved_is_kept_apart_from_what_was_edited() {
    let mut f = fixture();
    f.doc.get_mut(f.screen).unwrap().layout = Some(AutoLayout::default());
    let mut head = f.doc.clone();
    set_text(&mut head, f.title, "Create your account");
    // What FigCraft's layout pass does next: the text hugs its words, and the
    // button after it in the row moves along.
    head.get_mut(f.title).unwrap().width = 236.0;
    head.get_mut(f.button).unwrap().transform =
        figcraft_doc::geom::from_position_rotation(236.0, 0.0, 0.0);
    let d = diff(&f.doc, &head);

    let screen = at(&d, &format!("screens/{}", f.screen));
    assert_eq!(
        paths(&screen.children),
        [
            format!("layers/{}", f.title),
            format!("screens/{}/layout", f.screen)
        ]
    );
    // The edit: only the words.
    assert_eq!(
        paths(&screen.children[0].children),
        [format!("layers/{}/text", f.title)]
    );
    let layout = &screen.children[1];
    assert_eq!(layout.label, "Moved or resized by layout");
    assert_eq!(
        paths(&layout.children),
        [
            format!("layout/{}", f.title),
            format!("layout/{}", f.button)
        ]
    );
    assert_eq!(
        at(&d, &format!("layout/{}/width", f.title)).after,
        Some(json!(236))
    );
    assert_eq!(
        at(&d, &format!("layout/{}/position", f.button)).after,
        Some(json!([236, 0]))
    );
}

#[test]
fn a_layer_moved_into_another_frame_is_reparented() {
    let f = fixture();
    let mut base = f.doc.clone();
    let footer = add(&mut base, f.screen, NodeKind::Frame, "Footer", 360.0, 80.0);
    let mut head = base.clone();
    head.reparent(f.button, footer, None).unwrap();
    let d = diff(&base, &head);

    let layer = at(&d, &format!("layers/{}", f.button));
    assert_eq!(layer.kind, ChangeKind::Reparented);
    let parent = at(&d, &format!("layers/{}/parent", f.button));
    assert_eq!(parent.before, Some(json!("Frame \"Sign up\"")));
    assert_eq!(parent.after, Some(json!("Frame \"Footer\"")));
    // The order within the new parent comes with the move.
    assert!(find(&d.changes, &format!("layers/{}/layer-order", f.button)).is_none());
}

#[test]
fn added_and_removed_layers_are_one_row_each_with_what_they_were() {
    let f = fixture();
    let mut head = f.doc.clone();
    let badge = add(
        &mut head,
        f.screen,
        NodeKind::Rectangle,
        "New badge",
        60.0,
        24.0,
    );
    let welcome = add(&mut head, f.page, NodeKind::Frame, "Welcome", 360.0, 640.0);
    let hello = add(
        &mut head,
        welcome,
        NodeKind::Text(TextData::new("Hello")),
        "Hello",
        50.0,
        20.0,
    );
    head.remove(f.title).unwrap();
    let d = diff(&f.doc, &head);

    let added = at(&d, &format!("layers/{badge}"));
    assert_eq!(
        (added.kind, added.label.as_str()),
        (ChangeKind::Added, "Rectangle \"New badge\"")
    );
    assert_eq!(added.after, Some(json!("60 × 24")));

    // A new screen is a row on its page, its layers inside it.
    let page = at(&d, &format!("pages/{}", f.page));
    let screen = page
        .children
        .iter()
        .find(|c| c.path == format!("layers/{welcome}"))
        .unwrap();
    assert_eq!(screen.kind, ChangeKind::Added);
    assert_eq!(screen.after, Some(json!("360 × 640, 1 layer inside")));
    assert!(find(&d.changes, &format!("layers/{hello}")).is_none());

    let removed = at(&d, &format!("layers/{}", f.title));
    assert_eq!(removed.kind, ChangeKind::Removed);
    assert_eq!(removed.before, Some(json!("Create account")));
}

#[test]
fn a_page_that_was_only_renamed_is_one_row() {
    let f = fixture();
    let mut head = f.doc.clone();
    head.get_mut(f.page).unwrap().name = "Sign-up flow".into();
    let d = diff(&f.doc, &head);

    assert_eq!(paths(&d.changes), [format!("pages/{}", f.page)]);
    assert_eq!(d.changes[0].kind, ChangeKind::Renamed);
    assert_eq!(d.changes[0].label, "Page \"Sign-up flow\"");
    assert_eq!(
        paths(&d.changes[0].children),
        [format!("pages/{}/name", f.page)]
    );
}

#[test]
fn an_added_or_deleted_file_lists_its_pages() {
    let f = fixture();
    let bytes = save(&f.doc);

    let added = FigCraft.diff(&[], &bytes).unwrap();
    assert_eq!(paths(&added.changes), [format!("pages/{}", f.page)]);
    assert_eq!(added.changes[0].kind, ChangeKind::Added);
    assert_eq!(added.changes[0].after, Some(json!("3 layers")));

    let removed = FigCraft.diff(&bytes, &[]).unwrap();
    assert_eq!(removed.changes[0].kind, ChangeKind::Removed);
    assert_eq!(removed.changes[0].before, Some(json!("3 layers")));

    assert!(FigCraft.diff(&[], &[]).unwrap().changes.is_empty());
}

#[test]
fn styles_are_reported_under_the_document() {
    let f = fixture();
    let mut head = f.doc.clone();
    head.styles.push(Style {
        id: "S:brand".into(),
        name: "Brand/Orange".into(),
        description: String::new(),
        kind: StyleKind::Paint {
            paints: vec![Paint::solid(Color::from_hex("#FF8A3D").unwrap())],
        },
        bound: BTreeMap::new(),
    });
    let d = diff(&f.doc, &head);

    assert_eq!(paths(&d.changes), ["document"]);
    let style = at(&d, "document/styles/S:brand");
    assert_eq!(
        (style.kind, style.label.as_str()),
        (ChangeKind::Added, "Style \"Brand/Orange\"")
    );
    assert_eq!(style.after, Some(json!("Paint style")));
}

#[test]
fn a_file_that_is_not_figcraft_is_an_error_naming_its_side() {
    let f = fixture();
    let err = FigCraft.diff(b"not a zip", &save(&f.doc)).unwrap_err();
    assert!(err.starts_with("base: "), "{err}");
    let err = FigCraft.diff(&save(&f.doc), b"not a zip").unwrap_err();
    assert!(err.starts_with("head: "), "{err}");
}

#[test]
fn the_fhr_wasm_1_call_answers_the_same_diff() {
    let f = fixture();
    let mut head = f.doc.clone();
    set_text(&mut head, f.title, "Create your account");
    let (base, head_bytes) = (save(&f.doc), save(&head));

    let resp = fhr::call(
        &FigCraft,
        &fhr::encode_request("diff", json!({}), &[&base, &head_bytes]),
    );
    let (result, _) = fhr::decode_response(&resp).unwrap();
    let direct = serde_json::to_value(FigCraft.diff(&base, &head_bytes).unwrap()).unwrap();
    assert_eq!(result, direct);
}

#[test]
fn an_override_note_shows_only_inside_an_instance() {
    let f = fixture();
    let mut head = f.doc.clone();
    set_text(&mut head, f.title, "Create your account");
    // What FigCraft's own text edit also writes.
    head.get_mut(f.title)
        .unwrap()
        .overrides
        .insert("characters".into());
    let d = diff(&f.doc, &head);

    let layer = at(&d, &format!("layers/{}", f.title));
    assert_eq!(paths(&layer.children), [format!("layers/{}/text", f.title)]);
}
