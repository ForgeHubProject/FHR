//! What a reviewer sees for each kind of edit, on documents built in code and
//! laid out the way Photoshop saves them.

mod common;

use common::{Layer, Psd};
use fhr::{ChangeKind, DiffChange, Handler, StructuredDiff};
use handler_psd::Psd as Handler_;
use serde_json::{json, Value};

const W: u32 = 400;
const H: u32 = 300;

/// A poster: a title, a caption, a banner shape, a photo placed as a smart
/// object, and a pixel layer in a group.
fn poster() -> Vec<Layer> {
    vec![
        Layer::text(
            10,
            "Hello World",
            (40.0, 120.0),
            "NotoSans-Bold",
            48.0,
            [1.0, 1.0, 1.0],
            0.0,
        ),
        Layer::text(
            8,
            "What a beautiful day.",
            (40.0, 170.0),
            "DroidSans",
            20.0,
            [1.0, 1.0, 1.0],
            0.0,
        ),
        Layer::group(
            20,
            "Badges",
            vec![Layer::pixel(
                21,
                "Sticker",
                (300, 20, 360, 80),
                [255, 200, 0, 255],
            )],
        ),
        Layer::shape(
            6,
            "Strip",
            (0.0, 90.0, 400.0, 190.0),
            (W, H),
            [0.0, 0.0, 0.0],
        )
        .opacity(179),
        Layer::smart(
            5,
            "Background",
            [0.0, 0.0, 400.0, 0.0, 400.0, 300.0, 0.0, 300.0],
            "photo-1",
        ),
    ]
}

fn diff(base: Vec<Layer>, head: Vec<Layer>) -> StructuredDiff {
    Handler_
        .diff(&Psd::new(W, H, base).save(), &Psd::new(W, H, head).save())
        .unwrap()
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

/// Every leaf's label under `path`.
fn labels(d: &StructuredDiff, path: &str) -> Vec<String> {
    at(d, path)
        .children
        .iter()
        .map(|c| c.label.clone())
        .collect()
}

fn pair(c: &DiffChange) -> (Value, Value) {
    (
        c.before.clone().unwrap_or(Value::Null),
        c.after.clone().unwrap_or(Value::Null),
    )
}

/// `poster()` with layer `id` replaced.
fn with(id: u32, layer: Layer) -> Vec<Layer> {
    fn swap(list: Vec<Layer>, id: u32, layer: &Layer) -> Vec<Layer> {
        list.into_iter()
            .map(|l| {
                if l.id == id {
                    return layer.clone();
                }
                let mut l = l;
                if let Some(children) = l.children.take() {
                    l.children = Some(swap(children, id, layer));
                }
                l
            })
            .collect()
    }
    swap(poster(), id, &layer)
}

#[test]
fn info_declares_photoshop_files_a_compare_and_a_png_preview() {
    let info = serde_json::to_value(Handler_.info()).unwrap();
    assert_eq!(info["id"], "psd");
    assert_eq!(info["formats"], json!([".psd", ".psb"]));
    assert_eq!(info["capabilities"]["semanticCompare"], true);
    assert_eq!(info["capabilities"]["semanticMerge"], false);
    assert_eq!(info["preview"], "image/png");
}

#[test]
fn the_same_document_saved_twice_has_no_changes() {
    let d = diff(poster(), poster());
    assert!(d.changes.is_empty(), "{:?}", d.changes);
}

#[test]
fn new_text_reads_as_the_text_not_a_rename() {
    let head = with(
        10,
        Layer::text(
            10,
            "Today is your tomorrow",
            (40.0, 120.0),
            "NotoSans-Bold",
            48.0,
            [1.0, 1.0, 1.0],
            0.0,
        ),
    );
    let d = diff(poster(), head);
    assert_eq!(at(&d, "layers/10").label, "Text \"Today is your tomorrow\"");
    assert_eq!(labels(&d, "layers/10"), ["Text"]);
    assert_eq!(
        pair(at(&d, "layers/10/text")),
        (json!("Hello World"), json!("Today is your tomorrow"))
    );
}

#[test]
fn a_text_colour_is_a_chip() {
    let head = with(
        10,
        Layer::text(
            10,
            "Hello World",
            (40.0, 120.0),
            "NotoSans-Bold",
            48.0,
            [1.0, 0.5, 0.0],
            0.0,
        ),
    );
    let d = diff(poster(), head);
    assert_eq!(labels(&d, "layers/10"), ["Colour"]);
    assert_eq!(
        pair(at(&d, "layers/10/colour")),
        (json!([1.0, 1.0, 1.0, 1.0]), json!([1.0, 0.5, 0.0, 1.0]))
    );
}

#[test]
fn rotated_text_reads_as_an_angle() {
    let head = with(
        10,
        Layer::text(
            10,
            "Hello World",
            (40.0, 120.0),
            "NotoSans-Bold",
            48.0,
            [1.0, 1.0, 1.0],
            15.0,
        ),
    );
    let d = diff(poster(), head);
    assert_eq!(
        pair(at(&d, "layers/10/rotation")),
        (json!("0°"), json!("15°"))
    );
    assert!(find(&d.changes, "layers/10/position").is_none());
}

#[test]
fn font_and_size_changes_are_named() {
    let head = with(
        8,
        Layer::text(
            8,
            "What a beautiful day.",
            (60.0, 170.0),
            "Inter-Regular",
            24.0,
            [1.0, 1.0, 1.0],
            0.0,
        ),
    );
    let d = diff(poster(), head);
    assert_eq!(labels(&d, "layers/8"), ["Position", "Font", "Font size"]);
    assert_eq!(
        pair(at(&d, "layers/8/position")),
        (json!([40, 170]), json!([60, 170]))
    );
    assert_eq!(
        pair(at(&d, "layers/8/font")),
        (json!("DroidSans"), json!("Inter-Regular"))
    );
    assert_eq!(pair(at(&d, "layers/8/font-size")), (json!(20), json!(24)));
}

#[test]
fn a_smart_object_turned_about_its_centre_reads_as_a_rotation_only() {
    // The photo turned 90° about its centre (200, 150): its corners move,
    // its centre and its own size don't.
    let turned = [350.0, -50.0, 350.0, 350.0, 50.0, 350.0, 50.0, -50.0];
    let d = diff(
        poster(),
        with(5, Layer::smart(5, "Background", turned, "photo-1")),
    );
    assert_eq!(labels(&d, "layers/5"), ["Rotation"]);
    assert_eq!(
        pair(at(&d, "layers/5/rotation")),
        (json!("0°"), json!("90°"))
    );
}

#[test]
fn a_smart_object_scaled_and_moved_reads_as_its_size_and_centre() {
    let scaled = [100.0, 50.0, 400.0, 50.0, 400.0, 275.0, 100.0, 275.0];
    let d = diff(
        poster(),
        with(5, Layer::smart(5, "Background", scaled, "photo-1")),
    );
    assert_eq!(
        labels(&d, "layers/5"),
        ["Position (centre)", "Width", "Height"]
    );
    assert_eq!(
        pair(at(&d, "layers/5/position-centre")),
        (json!([200, 150]), json!([250, 162.5]))
    );
    assert_eq!(pair(at(&d, "layers/5/width")), (json!(400), json!(300)));
}

#[test]
fn a_smart_object_showing_another_file_has_new_contents() {
    let d = diff(
        poster(),
        with(
            5,
            Layer::smart(
                5,
                "Background",
                [0.0, 0.0, 400.0, 0.0, 400.0, 300.0, 0.0, 300.0],
                "photo-2",
            ),
        ),
    );
    assert_eq!(labels(&d, "layers/5"), ["Contents"]);
}

#[test]
fn a_shape_reads_as_its_fill_colour_opacity_and_bounds() {
    let head = with(
        6,
        Layer::shape(
            6,
            "Strip",
            (0.0, 80.0, 400.0, 200.0),
            (W, H),
            [0.0, 0.4, 0.8],
        )
        .opacity(128),
    );
    let d = diff(poster(), head);
    assert_eq!(
        labels(&d, "layers/6"),
        ["Opacity", "Position", "Height", "Fill colour"]
    );
    assert_eq!(
        pair(at(&d, "layers/6/opacity")),
        (json!("70%"), json!("50%"))
    );
    assert_eq!(
        pair(at(&d, "layers/6/position")),
        (json!([0, 90]), json!([0, 80]))
    );
    assert_eq!(pair(at(&d, "layers/6/height")), (json!(100), json!(120)));
    assert_eq!(
        pair(at(&d, "layers/6/fill-colour")),
        (json!([0.0, 0.0, 0.0, 1.0]), json!([0.0, 0.4, 0.8, 1.0]))
    );
}

#[test]
fn a_pixel_layer_moved_and_repainted_inside_its_group() {
    let head = with(
        21,
        Layer::pixel(21, "Sticker", (280, 30, 340, 90), [0, 200, 255, 255]),
    );
    let d = diff(poster(), head);
    // Under its group's row.
    assert_eq!(at(&d, "groups/20").label, "Group \"Badges\"");
    assert!(at(&d, "groups/20")
        .children
        .iter()
        .any(|c| c.path == "layers/21"));
    assert_eq!(labels(&d, "layers/21"), ["Position", "Pixels"]);
    assert_eq!(
        pair(at(&d, "layers/21/position")),
        (json!([300, 20]), json!([280, 30]))
    );
}

#[test]
fn a_new_effect_reads_with_its_settings() {
    let mut head = poster();
    head[3] = head[3].clone().drop_shadow([0.0, 0.0, 0.0], 75.0);
    let d = diff(poster(), head);
    assert_eq!(
        labels(&d, "layers/6"),
        [
            "Drop Shadow",
            "Drop Shadow colour",
            "Drop Shadow opacity",
            "Drop Shadow blend mode",
            "Drop Shadow size",
            "Drop Shadow distance",
            "Drop Shadow angle"
        ]
    );
    assert_eq!(
        pair(at(&d, "layers/6/drop-shadow")),
        (json!("none"), json!("on"))
    );
    assert_eq!(
        pair(at(&d, "layers/6/drop-shadow-colour")),
        (Value::Null, json!([0.0, 0.0, 0.0, 1.0]))
    );
    assert_eq!(
        pair(at(&d, "layers/6/drop-shadow-opacity")),
        (Value::Null, json!("75%"))
    );
    // Turned up later: just what changed.
    let mut later = poster();
    later[3] = later[3].clone().drop_shadow([0.0, 0.0, 0.0], 40.0);
    let mut before = poster();
    before[3] = before[3].clone().drop_shadow([0.0, 0.0, 0.0], 75.0);
    let d = diff(before, later);
    assert_eq!(labels(&d, "layers/6"), ["Drop Shadow opacity"]);
}

#[test]
fn hidden_blended_and_renamed() {
    let mut head = poster();
    head[3] = head[3].clone().hidden().blend(b"mul ");
    head[2] = Layer::group(
        20,
        "Stickers",
        vec![Layer::pixel(
            21,
            "Sticker",
            (300, 20, 360, 80),
            [255, 200, 0, 255],
        )],
    );
    let d = diff(poster(), head);
    assert_eq!(labels(&d, "layers/6"), ["Visibility", "Blend mode"]);
    assert_eq!(
        pair(at(&d, "layers/6/blend-mode")),
        (json!("Normal"), json!("Multiply"))
    );
    let renamed = at(&d, "layers/20");
    assert_eq!(renamed.kind, ChangeKind::Renamed);
    assert_eq!(
        pair(at(&d, "layers/20/name")),
        (json!("Badges"), json!("Stickers"))
    );
}

#[test]
fn added_and_removed_layers_and_a_group_added_whole() {
    let mut head = poster();
    head.remove(1); // the caption
    head.insert(
        0,
        Layer::group(
            30,
            "Logos",
            vec![Layer::pixel(31, "Logo", (10, 10, 50, 50), [0, 0, 0, 255])],
        ),
    );
    let d = diff(poster(), head);
    let added = at(&d, "layers/30");
    assert_eq!(added.kind, ChangeKind::Added);
    assert_eq!(added.after, Some(json!("1 layer")));
    assert!(
        find(&d.changes, "layers/31").is_none(),
        "comes with its group"
    );
    let removed = at(&d, "layers/8");
    assert_eq!(removed.kind, ChangeKind::Removed);
    assert_eq!(removed.before, Some(json!("What a beautiful day.")));
}

#[test]
fn a_layer_moved_into_a_group_says_which() {
    let mut head = poster();
    let caption = head.remove(1);
    head[1] = Layer::group(
        20,
        "Badges",
        vec![
            caption,
            Layer::pixel(21, "Sticker", (300, 20, 360, 80), [255, 200, 0, 255]),
        ],
    );
    let d = diff(poster(), head);
    assert_eq!(
        pair(at(&d, "layers/8/group")),
        (json!("—"), json!("Badges"))
    );
    assert!(at(&d, "groups/20")
        .children
        .iter()
        .any(|c| c.path == "layers/8"));
}

#[test]
fn moving_one_layer_up_names_that_layer_alone() {
    let mut head = poster();
    let photo = head.remove(4);
    head.insert(0, photo);
    let d = diff(poster(), head);
    assert_eq!(d.changes.len(), 1, "{:?}", d.changes);
    assert_eq!(
        pair(at(&d, "layers/5/stack-order")),
        (json!("6 of 6 from the top"), json!("1 of 6 from the top"))
    );
}

#[test]
fn a_new_file_lists_its_layers_as_added() {
    let d = Handler_
        .diff(&[], &Psd::new(W, H, poster()).save())
        .unwrap();
    let kinds: Vec<_> = d
        .changes
        .iter()
        .map(|c| (c.path.as_str(), c.kind))
        .collect();
    assert_eq!(
        kinds,
        [
            ("layers/10", ChangeKind::Added),
            ("layers/8", ChangeKind::Added),
            ("layers/20", ChangeKind::Added),
            ("layers/6", ChangeKind::Added),
            ("layers/5", ChangeKind::Added),
        ]
    );
}

#[test]
fn not_a_photoshop_file_is_an_error() {
    let err = Handler_
        .diff(b"nope", &Psd::new(W, H, poster()).save())
        .unwrap_err();
    assert!(err.starts_with("base:"), "{err}");
}
