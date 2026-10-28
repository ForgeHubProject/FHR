//! The preview: one PNG sheet of every screen, and the layout chunk that says
//! where each screen and each of its layers is.

use fhr::Handler;
use figcraft_doc::{Document, Node, NodeId, NodeKind, TextData};
use handler_figcraft::{FigCraft, CHUNK_KEYWORD};
use serde_json::Value;

fn add(doc: &mut Document, parent: NodeId, kind: NodeKind, name: &str, w: f64, h: f64) -> NodeId {
    let id = doc.alloc_id();
    doc.insert(Node::new(id, kind, name, w, h), parent, None)
        .unwrap()
}

/// The PNG's size and its layout chunk.
fn read(png: &[u8]) -> ((u32, u32), Value) {
    assert_eq!(&png[..8], b"\x89PNG\r\n\x1a\n");
    let (mut i, mut size, mut layout) = (8, (0, 0), Value::Null);
    while i + 8 <= png.len() {
        let len = u32::from_be_bytes(png[i..i + 4].try_into().unwrap()) as usize;
        let (kind, data) = (&png[i + 4..i + 8], &png[i + 8..i + 8 + len]);
        if kind == b"IHDR" {
            size = (
                u32::from_be_bytes(data[..4].try_into().unwrap()),
                u32::from_be_bytes(data[4..8].try_into().unwrap()),
            );
        }
        if kind == b"tEXt" {
            let nul = data.iter().position(|b| *b == 0).unwrap();
            if &data[..nul] == CHUNK_KEYWORD.as_bytes() {
                assert!(
                    data[nul + 1..].is_ascii(),
                    "the chunk is Latin-1: non-ASCII is escaped"
                );
                layout = serde_json::from_slice(&data[nul + 1..]).unwrap();
            }
        }
        i += 12 + len;
    }
    (size, layout)
}

#[test]
fn info_declares_a_png_preview() {
    assert_eq!(FigCraft.info().preview.as_deref(), Some("image/png"));
}

#[test]
fn every_screen_is_drawn_side_by_side_with_its_layers_boxes() {
    let mut doc = Document::new("Ridgeline");
    let page = doc.pages[0];
    let welcome = add(&mut doc, page, NodeKind::Frame, "1 Welcome", 393.0, 852.0);
    let title = add(
        &mut doc,
        welcome,
        NodeKind::Text(TextData::new("Hello")),
        "Title",
        200.0,
        32.0,
    );
    let button = add(
        &mut doc,
        welcome,
        NodeKind::Rectangle,
        "Button",
        120.0,
        40.0,
    );
    doc.get_mut(button).unwrap().transform =
        figcraft_doc::geom::from_position_rotation(24.0, 700.0, 0.0);
    let grüße = add(&mut doc, page, NodeKind::Frame, "2 Grüße", 393.0, 852.0);
    doc.get_mut(grüße).unwrap().transform =
        figcraft_doc::geom::from_position_rotation(500.0, 0.0, 0.0);
    let bytes = figcraft_format::save_bytes(&doc, None).unwrap();

    let png = FigCraft.preview(&bytes).unwrap();
    let ((w, h), layout) = read(&png);
    assert_eq!(
        (w, h),
        (786, 852),
        "two screens at their true size, in a row"
    );
    assert_eq!(layout["v"], 1);
    assert_eq!(layout["scale"], 1.0);
    let screens = layout["screens"].as_array().unwrap();
    let names: Vec<_> = screens
        .iter()
        .map(|s| s["name"].as_str().unwrap())
        .collect();
    assert_eq!(names, ["1 Welcome", "2 Grüße"]);
    let first = &screens[0];
    assert_eq!(
        (
            first["x"].as_u64(),
            first["w"].as_u64(),
            first["h"].as_u64()
        ),
        (Some(0), Some(393), Some(852))
    );
    assert_eq!(screens[1]["x"], 393);
    assert_eq!(first["pageName"], "Page 1");
    // Boxes on the screen's own image, by layer id: the screen, its title, its button.
    let boxes = &first["layers"];
    assert_eq!(
        boxes[welcome.to_string()],
        serde_json::json!([0.0, 0.0, 393.0, 852.0])
    );
    assert_eq!(
        boxes[title.to_string()],
        serde_json::json!([0.0, 0.0, 200.0, 32.0])
    );
    assert_eq!(
        boxes[button.to_string()],
        serde_json::json!([24.0, 700.0, 120.0, 40.0])
    );
    assert!(screens[1]["layers"].get(title.to_string()).is_none());
}

#[test]
fn a_file_without_screens_previews_as_an_empty_sheet() {
    let bytes = figcraft_format::save_bytes(&Document::new("Empty"), None).unwrap();
    let ((w, h), layout) = read(&FigCraft.preview(&bytes).unwrap());
    assert_eq!((w, h), (1, 1));
    assert_eq!(layout["screens"], serde_json::json!([]));
}

#[test]
fn a_file_that_is_not_figcraft_is_an_error() {
    assert!(FigCraft.preview(b"not a zip").is_err());
}
